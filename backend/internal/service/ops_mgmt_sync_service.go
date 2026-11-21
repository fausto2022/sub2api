package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 上游同步服务（design.md §2.2 / §2.3）。
//
// 两种取数路径：
//   - password 模式（v3.0 默认）：登录 → GET /api/v1/groups/available（分组全量字段）
//   - GET /api/v1/groups/rates（用户专属倍率，覆盖展示倍率）。
//   - api_key 模式（可选）：x-api-key → GET /api/v1/admin/groups + /admin/accounts。
//
// 失败语义：**保留旧快照**（不删除、不写半成品），只把 last_error/status 记到站点上，
// 这样看板与利润评估还能用上一次的倍率（并因 SyncedAt 过期被判为 unreliable → 不动作）。
type OpsSyncService struct {
	repo     OpsMgmtRepository
	client   *OpsUpstreamClient
	service  *OpsManagementService
	settings *opsSettingsCache
	now      func() time.Time

	mu      sync.Mutex
	running map[int64]struct{}

	schedulerOnce sync.Once
}

// opsSyncSchedulerTick 调度器检查间隔（站点粒度仍由 sync_interval_minutes 控制）。
const opsSyncSchedulerTick = time.Minute

// OpsSyncResult 一次站点同步的结果。
type OpsSyncResult struct {
	SiteID       int64                 `json:"site_id"`
	SiteName     string                `json:"site_name"`
	AuthMode     string                `json:"auth_mode"`
	GroupCount   int                   `json:"group_count"`
	AccountCount int                   `json:"account_count"`
	RateChanges  []OpsUpstreamGroup    `json:"rate_changes"`
	Warnings     []string              `json:"warnings,omitempty"`
	SyncedAt     time.Time             `json:"synced_at"`
	DurationMS   int64                 `json:"duration_ms"`
	Evaluation   *OpsEvaluationSummary `json:"evaluation,omitempty"`
}

func newOpsSyncService(repo OpsMgmtRepository, client *OpsUpstreamClient, service *OpsManagementService, settings *opsSettingsCache) *OpsSyncService {
	return &OpsSyncService{
		repo:     repo,
		client:   client,
		service:  service,
		settings: settings,
		now:      time.Now,
		running:  make(map[int64]struct{}),
	}
}

// SyncSite 同步单个站点（保留旧快照；失败记录 last_error）。
func (s *OpsSyncService) SyncSite(ctx context.Context, siteID int64) (*OpsSyncResult, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops sync service is not configured")
	}
	if !s.acquire(siteID) {
		return nil, fmt.Errorf("site %d is already syncing", siteID)
	}
	defer s.release(siteID)

	started := s.now()
	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		return nil, err
	}
	result := &OpsSyncResult{
		SiteID:   site.ID,
		SiteName: site.Name,
		AuthMode: site.AuthMode,
		SyncedAt: started,
	}

	groups, accounts, rates, warnings, fetchErr := s.fetchSnapshot(ctx, site)
	if fetchErr != nil {
		// 失败：保留旧快照，只更新错误状态（synced_at 不变，避免给过期快照「续命」）。
		s.markSyncFailure(ctx, site, fetchErr)
		return nil, fetchErr
	}

	merged := mergeUpstreamGroupRates(groups, rates, s.now())
	oldGroups, upsertErr := s.repo.UpsertUpstreamGroups(ctx, site.ID, merged)
	if upsertErr != nil {
		s.markSyncFailure(ctx, site, upsertErr)
		return nil, fmt.Errorf("persist upstream groups: %w", upsertErr)
	}
	for i := range merged {
		if old, ok := oldGroups[merged[i].RemoteGroupID]; ok {
			if !opsNearlyEqual(old.RateMultiplier, merged[i].RateMultiplier) {
				previous := old.RateMultiplier
				merged[i].RateChanged = true
				merged[i].PreviousRateMultiplier = &previous
				result.RateChanges = append(result.RateChanges, merged[i])
			}
		}
	}

	if site.AuthMode == OpsAuthModeAPIKey {
		persistAccounts := opsAccountsFromRemote(site.ID, accounts, s.now())
		if replaceErr := s.repo.ReplaceUpstreamAccounts(ctx, site.ID, persistAccounts); replaceErr != nil {
			s.markSyncFailure(ctx, site, replaceErr)
			return nil, fmt.Errorf("persist upstream accounts: %w", replaceErr)
		}
	}

	syncedAt := s.now()
	if updateErr := s.repo.UpdateSiteSyncState(ctx, site.ID, &syncedAt, "", OpsSiteStatusActive); updateErr != nil {
		return nil, fmt.Errorf("update site sync state: %w", updateErr)
	}
	result.GroupCount = len(merged)
	result.AccountCount = len(accounts)
	result.Warnings = warnings
	result.SyncedAt = syncedAt
	result.DurationMS = syncedAt.Sub(started).Milliseconds()
	if result.RateChanges == nil {
		result.RateChanges = []OpsUpstreamGroup{}
	}

	// 同步后立即评估（灰度模式下只留痕，不停用）。
	if s.service != nil {
		if evaluation, evalErr := s.service.EvaluateSite(ctx, site.ID, "sync"); evalErr == nil && evaluation != nil {
			summary := evaluation.Summary()
			result.Evaluation = &summary
		} else if evalErr != nil {
			logger.LegacyPrintf("service.ops_mgmt", "Warning: evaluate site %d after sync failed: %v", site.ID, evalErr)
		}
	}
	return result, nil
}

// fetchSnapshot 拉取上游分组（password 模式还拉用户专属倍率；api_key 模式拉账号）。
func (s *OpsSyncService) fetchSnapshot(
	ctx context.Context,
	site *OpsUpstreamSite,
) ([]OpsUpstreamRemoteGroup, []OpsUpstreamRemoteAccount, map[int64]float64, []string, error) {
	warnings := make([]string, 0, 2)
	if site.AuthMode == OpsAuthModeAPIKey {
		apiKey, err := s.service.siteUpstreamAPIKey(site)
		if err != nil {
			return nil, nil, nil, warnings, fmt.Errorf("%w: decrypt upstream api key: %v", ErrOpsSiteAuthFailed, err)
		}
		if apiKey == "" {
			return nil, nil, nil, warnings, fmt.Errorf("%w: upstream api key is not configured", ErrOpsSiteAuthFailed)
		}
		groups, groupsErr := s.client.ListAdminGroups(ctx, site.BaseURL, apiKey, 200)
		if groupsErr != nil {
			return nil, nil, nil, warnings, fmt.Errorf("%w: %v", ErrOpsSiteUnreachable, groupsErr)
		}
		accounts, accountsErr := s.client.ListAdminAccounts(ctx, site.BaseURL, apiKey, 200)
		if accountsErr != nil {
			// 账号是附加信息：拉不到只告警，不影响分组快照。
			warnings = append(warnings, "list admin accounts failed: "+accountsErr.Error())
			accounts = nil
		}
		return groups, accounts, nil, warnings, nil
	}

	token, err := s.service.siteAccessToken(ctx, site)
	if err != nil {
		return nil, nil, nil, warnings, fmt.Errorf("%w: %v", ErrOpsSiteAuthFailed, err)
	}
	groups, groupsErr := s.client.ListAvailableGroups(ctx, site.BaseURL, token)
	if groupsErr != nil {
		return nil, nil, nil, warnings, fmt.Errorf("%w: %v", ErrOpsSiteUnreachable, groupsErr)
	}
	rates, ratesErr := s.client.ListGroupRates(ctx, site.BaseURL, token)
	if ratesErr != nil {
		// 用户专属倍率缺失时回落分组标价倍率（不阻断同步）。
		warnings = append(warnings, "list group rates failed: "+ratesErr.Error())
	}
	return groups, nil, rates, warnings, nil
}

// mergeUpstreamGroupRates 把用户专属倍率合并进分组快照。
//
// ratio 保留「上游分组标价倍率」，rate_multiplier 用用户专属倍率（没有时回落标价），
// 后续利润评估的成本口径一律用 rate_multiplier（与 relaydeck 一致）。
func mergeUpstreamGroupRates(groups []OpsUpstreamRemoteGroup, rates map[int64]float64, syncedAt time.Time) []OpsUpstreamGroup {
	if len(groups) == 0 {
		return []OpsUpstreamGroup{}
	}
	out := make([]OpsUpstreamGroup, 0, len(groups))
	for _, remote := range groups {
		group := OpsUpstreamGroup{
			RemoteGroupID:     remote.ID,
			Name:              remote.Name,
			Platform:          remote.Platform,
			Ratio:             remote.RateMultiplier,
			RateMultiplier:    remote.RateMultiplier,
			PeakEnabled:       remote.PeakEnabled,
			PeakStart:         remote.PeakStart,
			PeakEnd:           remote.PeakEnd,
			PeakMultiplier:    remote.PeakMultiplier,
			ImageSeparateRate: remote.ImageSeparateRate,
			VideoSeparateRate: remote.VideoSeparateRate,
			IsExclusive:       remote.IsExclusive,
			SubscriptionType:  remote.SubscriptionType,
			SyncedAt:          syncedAt,
		}
		if group.Ratio <= 0 {
			group.Ratio = 1
		}
		if group.RateMultiplier <= 0 {
			group.RateMultiplier = group.Ratio
		}
		if group.PeakMultiplier <= 0 {
			group.PeakMultiplier = 1
		}
		if rate, ok := rates[remote.ID]; ok && rate > 0 {
			group.RateMultiplier = rate
		}
		out = append(out, group)
	}
	return out
}

func (s *OpsSyncService) markSyncFailure(ctx context.Context, site *OpsUpstreamSite, cause error) {
	if s == nil || s.repo == nil || site == nil || cause == nil {
		return
	}
	message := cause.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	// syncedAt=nil 表示「不修改」：保留上一次成功同步时间，快照仍会被时效检查判为过期。
	if err := s.repo.UpdateSiteSyncState(ctx, site.ID, nil, message, OpsSiteStatusError); err != nil {
		logger.LegacyPrintf("service.ops_mgmt", "Warning: record sync failure for site %d failed: %v", site.ID, err)
	}
}

// SyncAll 同步全部站点（单个失败不影响其它站点）。
func (s *OpsSyncService) SyncAll(ctx context.Context) ([]OpsSyncResult, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops sync service is not configured")
	}
	sites, err := s.repo.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]OpsSyncResult, 0, len(sites))
	for i := range sites {
		result, syncErr := s.SyncSite(ctx, sites[i].ID)
		if syncErr != nil {
			logger.LegacyPrintf("service.ops_mgmt", "Warning: sync site %d failed: %v", sites[i].ID, syncErr)
			continue
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	return results, nil
}

// SyncDueSites 按站点各自的 sync_interval_minutes 触发同步（定时任务入口）。
func (s *OpsSyncService) SyncDueSites(ctx context.Context) []OpsSyncResult {
	if s == nil || s.repo == nil {
		return nil
	}
	sites, err := s.repo.ListSites(ctx)
	if err != nil {
		logger.LegacyPrintf("service.ops_mgmt", "Warning: list upstream sites failed: %v", err)
		return nil
	}
	now := s.now()
	results := make([]OpsSyncResult, 0, len(sites))
	for i := range sites {
		site := sites[i]
		interval := site.SyncIntervalMinutes
		if interval <= 0 {
			interval = opsDefaultSyncIntervalMinutes
		}
		if site.LastSyncedAt != nil && now.Before(site.LastSyncedAt.Add(time.Duration(interval)*time.Minute)) {
			continue
		}
		result, syncErr := s.SyncSite(ctx, site.ID)
		if syncErr != nil {
			logger.LegacyPrintf("service.ops_mgmt", "Warning: scheduled sync site %d failed: %v", site.ID, syncErr)
			continue
		}
		if result != nil {
			results = append(results, *result)
		}
	}
	return results
}

// StartScheduler 启动定时同步循环（按站点 sync_interval_minutes，默认 30 分钟）。
//
// 幂等：重复调用只启动一个 goroutine。停止依赖进程退出（与仓库内其它 Start 型服务一致）。
func (s *OpsSyncService) StartScheduler(ctx context.Context) {
	if s == nil {
		return
	}
	s.schedulerOnce.Do(func() {
		go s.schedulerLoop(ctx)
	})
}

func (s *OpsSyncService) schedulerLoop(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	// 启动后先跑一轮，避免重启后长时间等待。
	s.SyncDueSites(ctx)
	ticker := time.NewTicker(opsSyncSchedulerTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.SyncDueSites(ctx)
		}
	}
}

func (s *OpsSyncService) acquire(siteID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = make(map[int64]struct{})
	}
	if _, exists := s.running[siteID]; exists {
		return false
	}
	s.running[siteID] = struct{}{}
	return true
}

func (s *OpsSyncService) release(siteID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, siteID)
}

// SyncSiteAsync 手动同步（异步任务，带进度）。
func (s *OpsManagementService) SyncSiteAsync(ctx context.Context, siteID int64) (*OpsTask, error) {
	if s == nil || s.syncSvc == nil || s.repo == nil {
		return nil, fmt.Errorf("ops sync service is not configured")
	}
	if _, err := s.repo.GetSite(ctx, siteID); err != nil {
		return nil, err
	}
	task := s.tasks.create(OpsTaskKindSync, siteID, 1)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		s.tasks.markRunning(task.ID)
		result, err := s.syncSvc.SyncSite(bg, siteID)
		if err != nil {
			s.tasks.fail(task.ID, err.Error())
			return
		}
		s.tasks.complete(task.ID, fmt.Sprintf("synced %d groups", result.GroupCount), map[string]any{
			"group_count":   result.GroupCount,
			"account_count": result.AccountCount,
			"rate_changes":  len(result.RateChanges),
			"synced_at":     result.SyncedAt,
		})
	}()
	return s.tasks.get(task.ID), nil
}

// StartSyncScheduler 启动定时同步（wire 里在服务构造完成后调用）。
func (s *OpsManagementService) StartSyncScheduler(ctx context.Context) {
	if s == nil || s.syncSvc == nil {
		return
	}
	s.schedulerMu.Lock()
	if s.schedulerStarted {
		s.schedulerMu.Unlock()
		return
	}
	s.schedulerStarted = true
	s.schedulerMu.Unlock()
	s.syncSvc.StartScheduler(ctx)
}

// SyncSite 同步单个站点（同步执行，供 handler 的同步任务与测试直接调用）。
func (s *OpsManagementService) SyncSite(ctx context.Context, siteID int64) (*OpsSyncResult, error) {
	if s == nil || s.syncSvc == nil {
		return nil, fmt.Errorf("ops sync service is not configured")
	}
	return s.syncSvc.SyncSite(ctx, siteID)
}

func opsNearlyEqual(left, right float64) bool {
	diff := left - right
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.0001
}

// opsAccountsFromRemote 把上游账号映射为落库快照（password 模式不会有数据）。
func opsAccountsFromRemote(siteID int64, accounts []OpsUpstreamRemoteAccount, syncedAt time.Time) []OpsUpstreamAccount {
	if len(accounts) == 0 {
		return []OpsUpstreamAccount{}
	}
	out := make([]OpsUpstreamAccount, 0, len(accounts))
	for _, remote := range accounts {
		account := OpsUpstreamAccount{
			SiteID:          siteID,
			RemoteAccountID: remote.ID,
			Name:            remote.Name,
			Platform:        remote.Platform,
			Type:            remote.Type,
			Status:          remote.Status,
			Schedulable:     remote.Schedulable,
			RateMultiplier:  remote.RateMultiplier,
			GroupIDs:        remote.GroupIDs,
			Credentials:     remote.Credentials,
			SyncedAt:        syncedAt,
		}
		if account.GroupIDs == nil {
			account.GroupIDs = []int64{}
		}
		out = append(out, account)
	}
	return out
}
