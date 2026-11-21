package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// 扩池 = 对接渠道（design.md §2.6，v3.0 语义）。
//
// 不是「把上游账号搬进本系统」，而是在本系统创建一个指向该上游的转发 Channel：
//
//	名称      = ups-{站点}-{上游分组 ID}（可覆盖）
//	分组      = 本地目标分组（channel_groups）
//	上游 Key  = 用户填写的上游普通 Key（加密存 channels.ops_mgmt_upstream_key_enc）
//	标签      = ops_mgmt_site_id / ops_mgmt_remote_group_id
//
// 去重键：(site_id, remote_group_id, api_key_hash)，重复导入直接跳过（design §2.6）。
const (
	// OpsTaskKindSync 同步上游站点。
	OpsTaskKindSync = "sync"
	// OpsTaskKindExpand 批量扩池（对接渠道）。
	OpsTaskKindExpand = "expand"
	// OpsTaskKindEvaluate 手动利润评估。
	OpsTaskKindEvaluate = "evaluate"
)

const (
	// OpsTaskStatusPending 排队中。
	OpsTaskStatusPending = "pending"
	// OpsTaskStatusRunning 执行中。
	OpsTaskStatusRunning = "running"
	// OpsTaskStatusSucceeded 成功。
	OpsTaskStatusSucceeded = "succeeded"
	// OpsTaskStatusFailed 失败。
	OpsTaskStatusFailed = "failed"
)

const (
	opsTaskRegistryCapacity = 200
	opsTaskRegistryTTL      = 24 * time.Hour
)

// OpsTaskItem 批量任务的单条结果。
type OpsTaskItem struct {
	Ref       string `json:"ref"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	ChannelID int64  `json:"channel_id,omitempty"`
}

// OpsTask 运营异步任务（内存态，进程内进度反馈）。
type OpsTask struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	SiteID     int64          `json:"site_id"`
	Status     string         `json:"status"`
	Progress   int            `json:"progress"`
	Total      int            `json:"total"`
	Message    string         `json:"message,omitempty"`
	Error      string         `json:"error,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	Items      []OpsTaskItem  `json:"items"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

// clone 深拷贝，避免把内部指针暴露给调用方（并发安全）。
func (t *OpsTask) clone() *OpsTask {
	if t == nil {
		return nil
	}
	copied := *t
	if t.Items != nil {
		copied.Items = append([]OpsTaskItem(nil), t.Items...)
	}
	if t.Result != nil {
		result := make(map[string]any, len(t.Result))
		for key, value := range t.Result {
			result[key] = value
		}
		copied.Result = result
	}
	if t.FinishedAt != nil {
		finished := *t.FinishedAt
		copied.FinishedAt = &finished
	}
	return &copied
}

// opsTaskRegistry 有界内存任务表（进程内，重启后清空；用于 UI 进度反馈）。
type opsTaskRegistry struct {
	mu      sync.Mutex
	tasks   map[string]*OpsTask
	order   []string
	now     func() time.Time
	maxSize int
	ttl     time.Duration
}

func newOpsTaskRegistry() *opsTaskRegistry {
	return &opsTaskRegistry{
		tasks:   make(map[string]*OpsTask),
		now:     time.Now,
		maxSize: opsTaskRegistryCapacity,
		ttl:     opsTaskRegistryTTL,
	}
}

func (r *opsTaskRegistry) create(kind string, siteID int64, total int) *OpsTask {
	if r == nil {
		return &OpsTask{ID: "noop", Kind: kind, SiteID: siteID, Total: total, Status: OpsTaskStatusPending, Items: []OpsTaskItem{}}
	}
	now := r.now()
	task := &OpsTask{
		ID:        newOpsTaskID(),
		Kind:      kind,
		SiteID:    siteID,
		Status:    OpsTaskStatusPending,
		Total:     total,
		Items:     []OpsTaskItem{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	r.tasks[task.ID] = task
	r.order = append(r.order, task.ID)
	if len(r.order) > r.maxSize {
		drop := len(r.order) - r.maxSize
		for _, id := range r.order[:drop] {
			delete(r.tasks, id)
		}
		r.order = r.order[drop:]
	}
	return task.clone()
}

func (r *opsTaskRegistry) markRunning(id string) {
	r.mutate(id, func(task *OpsTask, now time.Time) {
		task.Status = OpsTaskStatusRunning
		task.UpdatedAt = now
	})
}

func (r *opsTaskRegistry) updateProgress(id string, progress int, message string) {
	r.mutate(id, func(task *OpsTask, now time.Time) {
		if task.Status == OpsTaskStatusPending {
			task.Status = OpsTaskStatusRunning
		}
		task.Progress = progress
		if message != "" {
			task.Message = message
		}
		task.UpdatedAt = now
	})
}

func (r *opsTaskRegistry) appendItem(id string, item OpsTaskItem) {
	r.mutate(id, func(task *OpsTask, now time.Time) {
		task.Items = append(task.Items, item)
		task.Progress = len(task.Items)
		task.UpdatedAt = now
	})
}

func (r *opsTaskRegistry) complete(id string, message string, result map[string]any) {
	r.mutate(id, func(task *OpsTask, now time.Time) {
		task.Status = OpsTaskStatusSucceeded
		task.Message = message
		task.Result = result
		task.Progress = task.Total
		task.UpdatedAt = now
		finished := now
		task.FinishedAt = &finished
	})
}

func (r *opsTaskRegistry) fail(id string, message string) {
	r.mutate(id, func(task *OpsTask, now time.Time) {
		task.Status = OpsTaskStatusFailed
		task.Error = message
		task.UpdatedAt = now
		finished := now
		task.FinishedAt = &finished
	})
}

func (r *opsTaskRegistry) mutate(id string, apply func(*OpsTask, time.Time)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		return
	}
	apply(task, r.now())
}

// get 读取任务快照。
func (r *opsTaskRegistry) get(id string) *OpsTask {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		return nil
	}
	return task.clone()
}

// list 按创建时间倒序返回最近的 limit 个任务。
func (r *opsTaskRegistry) list(limit int) []OpsTask {
	if r == nil {
		return []OpsTask{}
	}
	if limit <= 0 || limit > r.maxSize {
		limit = 50
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OpsTask, 0, limit)
	for i := len(r.order) - 1; i >= 0 && len(out) < limit; i-- {
		if task, ok := r.tasks[r.order[i]]; ok {
			out = append(out, *task.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (r *opsTaskRegistry) pruneLocked(now time.Time) {
	if r.ttl <= 0 || len(r.order) == 0 {
		return
	}
	kept := r.order[:0]
	for _, id := range r.order {
		task, ok := r.tasks[id]
		if !ok {
			continue
		}
		if task.FinishedAt != nil && now.Sub(*task.FinishedAt) > r.ttl {
			delete(r.tasks, id)
			continue
		}
		kept = append(kept, id)
	}
	r.order = kept
}

func newOpsTaskID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// ListTasks 任务列表（倒序）。
func (s *OpsManagementService) ListTasks(limit int) []OpsTask {
	if s == nil || s.tasks == nil {
		return []OpsTask{}
	}
	return s.tasks.list(limit)
}

// GetTask 任务详情。
func (s *OpsManagementService) GetTask(id string) (*OpsTask, error) {
	if s == nil || s.tasks == nil {
		return nil, ErrOpsTaskNotFound
	}
	task := s.tasks.get(id)
	if task == nil {
		return nil, ErrOpsTaskNotFound
	}
	return task, nil
}

// OpsExpandInput 单个上游分组的对接入参。
type OpsExpandInput struct {
	SiteID         int64  `json:"site_id"`
	RemoteGroupID  int64  `json:"remote_group_id"`
	UpstreamAPIKey string `json:"upstream_api_key"`
	TargetGroupID  int64  `json:"target_group_id"`
	ChannelName    string `json:"channel_name,omitempty"`
	Description    string `json:"description,omitempty"`
	// ModelPricing 可选：直接把模型定价带进新 Channel（留空则不带定价）。
	ModelPricing []ChannelModelPricing `json:"model_pricing,omitempty"`
}

// OpsExpandResult 单个对接结果。
type OpsExpandResult struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	SiteID      int64  `json:"site_id"`
	RemoteGroup string `json:"remote_group_id"`
	Created     bool   `json:"created"`
	Skipped     bool   `json:"skipped"`
	Message     string `json:"message,omitempty"`
}

// Expand 对接单个上游分组：在本系统创建（或复用）指向该上游的 Channel。
func (s *OpsManagementService) Expand(ctx context.Context, input OpsExpandInput) (*OpsExpandResult, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if input.SiteID <= 0 || input.RemoteGroupID <= 0 || input.TargetGroupID <= 0 {
		return nil, ErrOpsExpandInvalidInput
	}
	site, err := s.repo.GetSite(ctx, input.SiteID)
	if err != nil {
		return nil, err
	}
	upstream, err := s.repo.GetUpstreamGroup(ctx, input.SiteID, input.RemoteGroupID)
	if err != nil {
		return nil, err
	}
	if upstream == nil {
		return nil, ErrOpsUpstreamGroupNotFound
	}
	groups, err := s.repo.ListLocalGroups(ctx, []int64{input.TargetGroupID})
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("%w: target group %d not found", ErrOpsExpandInvalidInput, input.TargetGroupID)
	}
	targetGroup := groups[0]

	apiKey := strings.TrimSpace(input.UpstreamAPIKey)
	if apiKey == "" {
		siteKey, keyErr := s.siteUpstreamAPIKey(site)
		if keyErr != nil {
			return nil, keyErr
		}
		apiKey = strings.TrimSpace(siteKey)
	}
	if apiKey == "" {
		return nil, ErrOpsExpandInvalidInput
	}
	keyHash := OpsUpstreamAPIKeyHash(apiKey)

	existing, err := s.repo.FindChannelLinkByUpstreamKey(ctx, site.ID, upstream.RemoteGroupID, keyHash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return &OpsExpandResult{
			ChannelID:   existing.ChannelID,
			ChannelName: existing.ChannelName,
			SiteID:      site.ID,
			RemoteGroup: opsInt64ToString(upstream.RemoteGroupID),
			Skipped:     true,
			Message:     "already connected (same site, upstream group and upstream key)",
		}, nil
	}

	keyEnc, err := s.encryptOpsSecret(apiKey)
	if err != nil {
		return nil, err
	}
	if s.channelSvc == nil {
		return nil, fmt.Errorf("channel service is not configured")
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = fmt.Sprintf("对接上游 %s · 分组 %s（ops_mgmt site=%d group=%d target=%d）",
			site.Name, upstream.Name, site.ID, upstream.RemoteGroupID, targetGroup.ID)
	}
	created, err := s.createChannelWithUniqueName(ctx, input.ChannelName, site, upstream, description, input.ModelPricing, targetGroup.ID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetChannelOpsLink(ctx, created.ID, site.ID, upstream.RemoteGroupID, keyEnc, keyHash); err != nil {
		// 打标签失败则回滚 Channel，避免留下「无来源」的渠道。
		if deleteErr := s.channelSvc.Delete(ctx, created.ID); deleteErr != nil {
			return nil, fmt.Errorf("tag channel ops link: %w (rollback failed: %v)", err, deleteErr)
		}
		return nil, fmt.Errorf("tag channel ops link: %w", err)
	}
	return &OpsExpandResult{
		ChannelID:   created.ID,
		ChannelName: created.Name,
		SiteID:      site.ID,
		RemoteGroup: opsInt64ToString(upstream.RemoteGroupID),
		Created:     true,
		Message:     "channel created and tagged",
	}, nil
}

// createChannelWithUniqueName 创建 Channel；名字冲突时按 -2、-3… 递增重试。
func (s *OpsManagementService) createChannelWithUniqueName(
	ctx context.Context,
	requested string,
	site *OpsUpstreamSite,
	upstream *OpsUpstreamGroup,
	description string,
	modelPricing []ChannelModelPricing,
	targetGroupID int64,
) (*Channel, error) {
	base := sanitizeOpsChannelName(requested)
	if base == "" {
		base = sanitizeOpsChannelName(fmt.Sprintf("ups-%s-%d", site.Name, upstream.RemoteGroupID))
	}
	if base == "" {
		base = "ups-" + opsInt64ToString(upstream.RemoteGroupID)
	}
	var lastErr error
	for attempt := 1; attempt <= 50; attempt++ {
		name := base
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", base, attempt)
		}
		created, err := s.channelSvc.Create(ctx, &CreateChannelInput{
			Name:         name,
			Description:  description,
			GroupIDs:     []int64{targetGroupID},
			ModelPricing: modelPricing,
		})
		if err == nil {
			return created, nil
		}
		lastErr = err
		if !errors.Is(err, ErrChannelExists) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("cannot allocate unique channel name for %q: %w", base, lastErr)
}

// sanitizeOpsChannelName 只保留可安全出现在 Channel 名里的字符，长度上限 100。
func sanitizeOpsChannelName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	dashPending := false
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			if dashPending && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			dashPending = false
			builder.WriteRune(r)
		default:
			dashPending = true
		}
	}
	out := builder.String()
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

// ExpandBatchAsync 批量对接（异步任务，带进度）。
func (s *OpsManagementService) ExpandBatchAsync(ctx context.Context, inputs []OpsExpandInput) (*OpsTask, error) {
	if s == nil || s.tasks == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if len(inputs) == 0 {
		return nil, ErrOpsExpandInvalidInput
	}
	if len(inputs) > 50 {
		return nil, fmt.Errorf("%w: at most 50 upstream groups per batch", ErrOpsExpandInvalidInput)
	}
	siteID := inputs[0].SiteID
	task := s.tasks.create(OpsTaskKindExpand, siteID, len(inputs))
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		s.tasks.markRunning(task.ID)
		created, skipped, failed := 0, 0, 0
		for _, input := range inputs {
			ref := fmt.Sprintf("%d/%d", input.SiteID, input.RemoteGroupID)
			result, err := s.Expand(bg, input)
			item := OpsTaskItem{Ref: ref}
			switch {
			case err != nil:
				failed++
				item.Status = OpsTaskStatusFailed
				item.Message = err.Error()
			case result != nil && result.Skipped:
				skipped++
				item.Status = "skipped"
				item.Message = result.Message
				item.ChannelID = result.ChannelID
			case result != nil:
				created++
				item.Status = OpsTaskStatusSucceeded
				item.ChannelID = result.ChannelID
				item.Message = result.ChannelName
			}
			s.tasks.appendItem(task.ID, item)
		}
		message := fmt.Sprintf("created=%d skipped=%d failed=%d", created, skipped, failed)
		if failed > 0 {
			s.tasks.fail(task.ID, message)
			return
		}
		s.tasks.complete(task.ID, message, map[string]any{
			"created": created,
			"skipped": skipped,
			"failed":  failed,
		})
	}()
	return s.tasks.get(task.ID), nil
}

// EvaluateAsync 手动触发评估（异步任务）。
func (s *OpsManagementService) EvaluateAsync(ctx context.Context, siteID int64) (*OpsTask, error) {
	if s == nil || s.tasks == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if siteID > 0 && s.repo != nil {
		if _, err := s.repo.GetSite(ctx, siteID); err != nil {
			return nil, err
		}
	}
	total := 1
	task := s.tasks.create(OpsTaskKindEvaluate, siteID, total)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		s.tasks.markRunning(task.ID)
		result, err := s.Evaluate(bg, siteID, "manual")
		if err != nil {
			s.tasks.fail(task.ID, err.Error())
			return
		}
		s.tasks.complete(task.ID, fmt.Sprintf("checks=%d risk=%d", len(result.Checks), result.Risk), map[string]any{
			"checks":        len(result.Checks),
			"healthy":       result.Healthy,
			"risk":          result.Risk,
			"unsupported":   result.Unsupported,
			"unknown":       result.Unknown,
			"would_disable": len(result.WouldDisable),
			"applied":       len(result.ProtectionApplied),
			"recovered":     len(result.Recovered),
		})
	}()
	return s.tasks.get(task.ID), nil
}
