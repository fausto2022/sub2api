package handler

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

type failoverIdempotencyInputKey struct{}

type failoverIdempotencyInput struct {
	c       *gin.Context
	headers http.Header
	body    []byte // immutable ingress bytes, before per-account rewrites
}

func captureFailoverIdempotencyInput(c *gin.Context, body []byte) {
	input := failoverIdempotencyInput{c: c, body: body}
	// Snapshot only the input needed by the gate. This is not a dedup key.
	if service.HasIdempotencyKey(c.Request.Header) {
		input.headers = http.Header{"Idempotency-Key": {"present"}}
	}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), failoverIdempotencyInputKey{}, input))
}

type failoverIdempotencyCheck struct {
	input     failoverIdempotencyInput
	evaluator service.FailoverIdempotencyEvaluator
	decision  *service.FailoverIdempotencyDecision
	retryID   int64
}

func (s *FailoverState) denyIdempotencySwitch(ctx context.Context, accountID int64, upstreamStatus int) bool {
	check := s.idempotency
	if check == nil {
		return false
	}
	if check.decision == nil {
		decision, _ := check.evaluator.Evaluate(check.input.headers, check.input.body)
		check.decision = &decision
		if decision.ParseFailed {
			logger.FromContext(ctx).Warn("gateway.failover_idempotency_parse_failed")
		}
	}
	if check.decision.ApplyToSwitchDecision(true) {
		return false
	}
	reason := service.FailoverSuppressedSideEffectNoKey
	service.SuppressFailoverSideEffectsForRequest(check.input.c, accountID, reason)
	if observation := requestFailoverObservation(check.input.c); observation != nil {
		observation.suppression = string(reason)
	}
	gatewayIsolationFrom(ctx).discardFailure(ctx, accountID)
	s.AppendHop(SwitchHop{Kind: SwitchHopExhausted, FromAccountID: accountID, Reason: string(reason), UpstreamStatus: upstreamStatus})
	logger.FromContext(ctx).Warn("gateway.failover_suppressed", zap.String("reason", string(reason)), zap.Int64("account_id", accountID), zap.Int("upstream_status", upstreamStatus))
	s.LogSwitchChain(ctx, "exhausted")
	return true
}

// Until the scheduler pins same-account retries, a legacy retry can pick a
// different account. Do not allow that path to bypass the cross-account gate.
func (s *FailoverState) rejectIdempotencyRetrySelection(ctx context.Context, selection *service.AccountSelectionResult) bool {
	check := s.idempotency
	if check == nil || check.retryID == 0 {
		return false
	}
	id := check.retryID
	check.retryID = 0
	if selection.Account.ID == id || !s.denyIdempotencySwitch(ctx, id, s.LastFailoverErr.StatusCode) {
		return false
	}
	if selection.Acquired && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	return true
}

func (r *gatewayIsolationRequest) discardFailure(ctx context.Context, accountID int64) {
	if r == nil {
		return
	}
	delete(r.writes, accountID)
	for _, attempt := range r.probeFailures {
		if attempt.selection.Account.ID == accountID {
			attempt.complete(ctx, service.FailoverProbeRelease)
		}
	}
}

type gatewayIsolationKey struct{}

type gatewayIsolationRequest struct {
	machine       *service.FailoverIsolation
	failed        map[int64]struct{}
	writes        map[int64]service.FailoverIsolationFailure
	switched      map[int64]bool
	probeFailures []*gatewayIsolationAttempt
	current       *gatewayIsolationAttempt
}

type gatewayIsolationAttempt struct {
	selection       *service.AccountSelectionResult
	leases          []service.FailoverProbeLease
	policy          *service.FailoverPolicy
	sent            bool
	upstreamFailure bool
	finished        bool
}

func gatewayIsolationFrom(ctx context.Context) *gatewayIsolationRequest {
	r, _ := ctx.Value(gatewayIsolationKey{}).(*gatewayIsolationRequest)
	return r
}

func (r *gatewayIsolationRequest) admit(ctx context.Context, selection *service.AccountSelectionResult, release func()) error {
	if r == nil {
		return nil
	}
	r.releaseCurrent(ctx)
	leases, err := selection.AcquireFailoverProbeAfterSlot(ctx, release)
	if err != nil {
		return err
	}
	policy, _ := service.ResolvedGatewayFailoverPolicy(ctx)
	r.current = &gatewayIsolationAttempt{selection: selection, leases: leases, policy: policy}
	return nil
}

func (r *gatewayIsolationRequest) onSend(accountID int64) func(*http.Response, error) {
	if r == nil || r.current == nil || r.current.selection.Account.ID != accountID {
		return nil
	}
	attempt := r.current
	attempt.sent = true
	for failed := range r.writes {
		if failed != accountID {
			r.switched[failed] = true
		}
	}
	return func(resp *http.Response, err error) {
		if err != nil || (resp != nil && resp.StatusCode >= 400) {
			attempt.upstreamFailure = true
		}
	}
}

func (r *gatewayIsolationRequest) recordForward(ctx context.Context, result *service.ForwardResult, err error, canSwitch bool) {
	if r == nil || r.current == nil || r.current.finished {
		return
	}
	a := r.current
	a.finished = true
	if !a.sent || ctx.Err() != nil {
		a.complete(ctx, service.FailoverProbeRelease)
		return
	}
	account := a.selection.Account
	if a.upstreamFailure {
		r.failed[account.ID] = struct{}{}
	}
	if err == nil && result != nil {
		delete(r.writes, account.ID)
		if len(a.leases) != 0 {
			a.complete(ctx, service.FailoverProbeSuccess)
		} else if a.selection.IsolationEnabled() && !a.selection.FailoverProbePending() {
			r.machine.NoteSuccess(ctx, a.policy, account)
		}
		return
	}
	var failure *service.UpstreamFailoverError
	if !errors.As(err, &failure) || isFailoverBudgetError(err) {
		a.complete(ctx, service.FailoverProbeRelease)
		return
	}
	r.failed[account.ID] = struct{}{}
	decision, advice := service.ClassifyGatewayFailover(ctx, account, failure, canSwitch)
	if !decision.MarkAccount || decision.Scope != service.GatewayFailureScopeAccount || !canSwitch {
		a.complete(ctx, service.FailoverProbeRelease)
		return
	}
	if len(a.leases) != 0 {
		r.probeFailures = append(r.probeFailures, a)
		return
	}
	if a.selection.IsolationEnabled() && decision.Switch && !a.selection.FailoverProbePending() {
		r.writes[account.ID] = service.FailoverIsolationFailure{Policy: a.policy, Account: account, Decision: decision, Advice: &advice}
	}
}

func (a *gatewayIsolationAttempt) complete(ctx context.Context, outcome service.FailoverProbeOutcome) {
	if len(a.leases) == 0 {
		return
	}
	if outcome == service.FailoverProbeRelease {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
	}
	for _, lease := range a.leases {
		a.selection.CompleteFailoverProbe(ctx, lease, outcome)
	}
	a.leases = nil
}

func (r *gatewayIsolationRequest) releaseCurrent(ctx context.Context) {
	if r != nil && r.current != nil && !r.current.finished {
		r.current.complete(ctx, service.FailoverProbeRelease)
	}
}

func (r *gatewayIsolationRequest) finish(ctx context.Context) {
	if r == nil {
		return
	}
	r.releaseCurrent(ctx)
	for _, probe := range r.probeFailures {
		outcome := service.FailoverProbeRelease
		if ctx.Err() == nil && len(r.failed) == 1 {
			outcome = service.FailoverProbeFailure
		}
		probe.complete(ctx, outcome)
	}
	if ctx.Err() != nil || len(r.failed) != 1 {
		return
	}
	for account := range r.failed {
		if write, ok := r.writes[account]; ok && r.switched[account] {
			write.FailedAccountIDs = []int64{account}
			r.machine.NoteFailure(ctx, write)
		}
	}
}

func gatewayIsolationCanSwitch(attempt service.UpstreamWriteAttempt) bool {
	blocked, _ := service.FailoverBlockedForAttempt(attempt)
	return !blocked
}

func (s *FailoverState) useSemanticBytes(legacy bool) bool {
	if s.effectivePolicy != nil && s.effectivePolicy.SwitchableUseSemanticBytes != nil {
		return *s.effectivePolicy.SwitchableUseSemanticBytes
	}
	return legacy
}

func (s *FailoverState) selectionExclusions() map[int64]struct{} {
	if len(s.probeDenied) == 0 {
		return s.FailedAccountIDs
	}
	return mergeAccountIDSets(s.probeDenied, s.FailedAccountIDs)
}

func (s *FailoverState) admitIsolation(ctx context.Context, selection *service.AccountSelectionResult, release func()) bool {
	if err := gatewayIsolationFrom(ctx).admit(ctx, selection, release); err != nil {
		if release != nil {
			release()
		}
		if s.probeDenied == nil {
			s.probeDenied = make(map[int64]struct{})
		}
		s.probeDenied[selection.Account.ID] = struct{}{}
		return false
	}
	return true
}

// TempUnscheduler 用于 HandleFailoverError 中同账号重试耗尽后的临时封禁。
// GatewayService 隐式实现此接口。
type TempUnscheduler interface {
	TempUnscheduleRetryableError(ctx context.Context, accountID int64, failoverErr *service.UpstreamFailoverError)
}

// FailoverAction 表示 failover 错误处理后的下一步动作
type FailoverAction int

const (
	// FailoverContinue 继续循环（同账号重试或切换账号，调用方统一 continue）
	FailoverContinue FailoverAction = iota
	// FailoverExhausted 切换次数耗尽（调用方应返回错误响应）
	FailoverExhausted
	// FailoverCanceled context 已取消（调用方应直接 return）
	FailoverCanceled
)

const (
	// maxSameAccountRetries 同账号重试次数默认上限（针对 RetryableOnSameAccount 错误）。
	// 生产调用方通常传入账号级配置 account.GetPoolModeRetryCount()，该常量仅作兜底/测试默认值。
	maxSameAccountRetries = 3
	// sameAccountRetryDelay 同账号重试间隔
	sameAccountRetryDelay = 500 * time.Millisecond
	// maxRequestScopedRetryDelay 限制请求级瞬时错误的指数退避上限，避免高重试配置
	// 将单次请求拖入分钟级等待。
	maxRequestScopedRetryDelay = 8 * time.Second
	// singleAccountBackoffDelay 单账号分组 503 退避重试固定延时。
	// Service 层在 SingleAccountRetry 模式下已做充分原地重试（最多 3 次、总等待 30s），
	// Handler 层只需短暂间隔后重新进入 Service 层即可。
	singleAccountBackoffDelay = 2 * time.Second
	// maxProfitVetoAttempts 单次请求内允许的分组利润门终检否决次数上限。
	// 利润否决不产生上游请求，因此不会推进 SwitchCount；没有独立上限的话，
	// 「选号 → 终检否决 → 重选」在候选池与账号快照短暂不一致时可以空转很久。
	// 取值与 maxAccountSwitches 默认值一致：混合定价的大分组仍有充分重选机会，
	// 同时把整池越线时的无谓选号开销限制在常数级。
	maxProfitVetoAttempts = 10
)

// profitVetoExhaustedMessage 是利润否决次数耗尽时返回给客户端的文案。
// 语义上等同于「无可用账号」：候选账号都不满足分组的利润约束。
const profitVetoExhaustedMessage = "No available accounts: all candidates rejected by group profit control"

func sameAccountRetryDelayFor(failoverErr *service.UpstreamFailoverError, retryCount int) time.Duration {
	if failoverErr == nil {
		return sameAccountRetryDelay
	}
	if failoverErr.SameAccountRetryDelay > 0 {
		return failoverErr.SameAccountRetryDelay
	}
	if !failoverErr.RequestScopedTransient || retryCount <= 1 {
		return sameAccountRetryDelay
	}

	delay := sameAccountRetryDelay
	for i := 1; i < retryCount; i++ {
		if delay >= maxRequestScopedRetryDelay/2 {
			return maxRequestScopedRetryDelay
		}
		delay *= 2
	}
	return delay
}

func sameAccountRetryAllowed(failoverErr *service.UpstreamFailoverError, retryCount, retryLimit int) bool {
	if failoverErr == nil || !failoverErr.RetryableOnSameAccount {
		return false
	}
	if !sameAccountRetryDeadlineAllows(failoverErr) {
		return false
	}
	// Error-specific caps (Grok capacity/stream-idle) remain hard limits even
	// when the error also carries a freshly reconstructed deadline.
	if failoverErr.SameAccountRetryMax > 0 {
		if retryLimit <= 0 {
			return false
		}
		if failoverErr.SameAccountRetryMax < retryLimit {
			retryLimit = failoverErr.SameAccountRetryMax
		}
		return retryCount < retryLimit
	}
	// OAuth 429 explicitly opts into a deadline window. It is intentionally not
	// bounded by the ordinary/default pool retry count.
	if !failoverErr.SameAccountRetryDeadline.IsZero() {
		return true
	}
	return retryLimit > 0 && retryCount < retryLimit
}

// sameAccountRetryDeadlineAllows prevents a retry from starting after the
// service-provided same-account retry window has elapsed.
func sameAccountRetryDeadlineAllows(failoverErr *service.UpstreamFailoverError) bool {
	return failoverErr == nil || failoverErr.SameAccountRetryDeadline.IsZero() || time.Now().Before(failoverErr.SameAccountRetryDeadline)
}

// effectiveSameAccountRetryLimit applies an error-specific cap without
// overriding an explicit account setting of zero (which disables retries).
func effectiveSameAccountRetryLimit(failoverErr *service.UpstreamFailoverError, account *service.Account) int {
	if account == nil {
		return 0
	}
	limit := account.GetPoolModeRetryCount()
	if limit > 0 && failoverErr != nil && failoverErr.SameAccountRetryMax > 0 && failoverErr.SameAccountRetryMax < limit {
		return failoverErr.SameAccountRetryMax
	}
	return limit
}

// FailoverState 跨循环迭代共享的 failover 状态
type FailoverState struct {
	SwitchCount           int
	MaxSwitches           int
	FailedAccountIDs      map[int64]struct{}
	SameAccountRetryCount map[int64]int
	LastFailoverErr       *service.UpstreamFailoverError
	ForceCacheBilling     bool
	hasBoundSession       bool
	Budget                *FailoverBudget
	Outcome               *FailoverOutcomeError
	maxSelectionRetries   int
	jitterEnabled         bool

	// profitVetoedAccountIDs 记录被分组利润门终检否决的账号，是 FailedAccountIDs
	// 的子集。之所以单独维护：HandleSelectionExhausted 的 503 退避分支会清空
	// FailedAccountIDs，而利润否决在同一请求内的判定不会改变（下游倍率 D 已在
	// 请求开始冻结），被清空的账号会被立即重选并再次否决，形成没有任何上游请求、
	// SwitchCount 也不前进的活锁。清空后必须把它们放回排除集。
	profitVetoedAccountIDs map[int64]struct{}
	// profitVetoCount 本次请求累计的利润否决次数，用于 maxProfitVetoAttempts 上限。
	profitVetoCount int

	// —— P2/T2.4 切换链（可解释性）——
	//
	// 改造前每次重选都重跑全量调度（优先级→最早重置→负载→LRU→组内随机），事后只能
	// 从零散的 warn 日志里猜「第 N 次切到谁、为什么」。SwitchChain 把每一跳显式记下来，
	// 请求结束时一次性输出，使切换顺序可从日志复现。
	//
	// **不引入切换游标**（design §维度 1 已论证：游标与负载/并发模型冲突——游标落在
	// 哪个实例上就会把该实例的视角强加给全池，且并发请求会互相推进游标）。这里只做
	// 记录，不改变选号行为。
	SwitchChain []SwitchHop
	// policySource 生效策略来源（account/group/global），由调用方注入（P3/P5 接线）。
	policySource    string
	effectivePolicy *service.FailoverPolicy
	retryAccountID  int64
	retryPlatform   string
	idempotency     *failoverIdempotencyCheck
	probeDenied     map[int64]struct{}
	// semanticBytes 首语义字节数（T2.1 的写状态快照），仅用于日志。
	semanticBytes int64
	// selectionRetryCount 选号失败后清空排除集重选的次数（活锁观测）。
	selectionRetryCount int
	// hopSeq 切换链已分配的序号（含因上限被丢弃的跳）。
	hopSeq int
	// droppedHops 因超出 switchChainMaxHops 而未记录的跳数。
	droppedHops int
}

// SwitchHopKind 是切换链一跳的类型。
type SwitchHopKind string

const (
	// SwitchHopSameAccountRetry 同账号重试（不是换号）。
	SwitchHopSameAccountRetry SwitchHopKind = "same_account_retry"
	// SwitchHopAccountSwitch 真正换到下一个账号。
	SwitchHopAccountSwitch SwitchHopKind = "switch"
	// SwitchHopExhausted 放弃换号（切换次数用尽或既有语义禁止）。
	SwitchHopExhausted SwitchHopKind = "exhausted"
	// SwitchHopProfitVeto 分组利润门否决了该账号。
	SwitchHopProfitVeto SwitchHopKind = "profit_veto"
	// SwitchHopSelectionExhausted 所有候选都在排除集里，触发退避重选。
	SwitchHopSelectionExhausted SwitchHopKind = "selection_exhausted"
)

// switchChainMaxHops 是切换链的保留上限：超过后只累加计数，避免日志与内存无界。
const switchChainMaxHops = 16

// SwitchHop 是切换链的一跳：**为什么、从谁、到谁**。
type SwitchHop struct {
	// Seq 从 1 开始的序号（与 SwitchCount 区分：同账号重试也占序号但不推进切换）。
	Seq int `json:"seq"`
	// At 该跳发生时间。
	At time.Time `json:"at"`
	// Kind 跳类型。
	Kind SwitchHopKind `json:"kind"`
	// FromAccountID 失败的账号。
	FromAccountID int64 `json:"from_account_id"`
	// ToAccountID 换到的账号；0 表示「已决定换号，但目标由调用方随后选号」。
	ToAccountID int64 `json:"to_account_id"`
	// Reason 失败原因（失败类别或既有语义标记）。
	Reason string `json:"reason"`
	// UpstreamStatus 上游状态码（0 = 传输层失败）。
	UpstreamStatus int `json:"upstream_status"`
	// FailureClass 分类器给出的失败类别（P0 产物；空串表示调用方未提供）。
	FailureClass string `json:"failure_class,omitempty"`
	// SameAccountRetryCount 该账号本次请求已重试次数。
	SameAccountRetryCount int `json:"same_account_retry_count,omitempty"`
	// SwitchCount 记录该跳时的切换计数。
	SwitchCount int `json:"switch_count"`
	// SelectionRetry 记录该跳时的「清空排除集重选」次数。
	SelectionRetry int `json:"selection_retry,omitempty"`
}

// AppendHop 追加一跳。超过上限后只累加 Seq 不再存内容（防日志/内存无界）。
func (s *FailoverState) AppendHop(hop SwitchHop) {
	if s == nil {
		return
	}
	s.hopSeq++
	hop.Seq = s.hopSeq
	hop.At = time.Now()
	hop.SwitchCount = s.SwitchCount
	hop.SelectionRetry = s.selectionRetryCount
	if len(s.SwitchChain) >= switchChainMaxHops {
		s.droppedHops++
		return
	}
	s.SwitchChain = append(s.SwitchChain, hop)
}

// RecordSelectedAccount 回填「换到了哪个账号」：由调用方在选号成功后调用。
//
// 只在最后一跳还是「未定目标」的 switch 跳时回填，避免把同账号重试的跳也写上目标。
func (s *FailoverState) RecordSelectedAccount(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	for i := len(s.SwitchChain) - 1; i >= 0; i-- {
		hop := &s.SwitchChain[i]
		if hop.Kind != SwitchHopAccountSwitch {
			continue
		}
		if hop.ToAccountID == 0 {
			hop.ToAccountID = accountID
		}
		return
	}
}

// SetPolicySource 注入生效策略来源（account/group/global），仅用于日志。
func (s *FailoverState) SetPolicySource(source string) {
	if s == nil {
		return
	}
	s.policySource = source
}

// SetSemanticBytes 注入首语义字节数快照，仅用于日志。
func (s *FailoverState) SetSemanticBytes(n int64) {
	if s == nil {
		return
	}
	s.semanticBytes = n
}

// RecordSelectionRetry 记录一次「排除集清空后重选」。
func (s *FailoverState) RecordSelectionRetry() {
	if s == nil {
		return
	}
	s.selectionRetryCount++
}

// SwitchChainSnapshot 返回切换链的拷贝（供日志与测试使用，避免调用方改内部状态）。
func (s *FailoverState) SwitchChainSnapshot() []SwitchHop {
	if s == nil || len(s.SwitchChain) == 0 {
		return nil
	}
	out := make([]SwitchHop, len(s.SwitchChain))
	copy(out, s.SwitchChain)
	return out
}

// DroppedSwitchHops 返回因超出上限而未记录的跳数。
func (s *FailoverState) DroppedSwitchHops() int {
	if s == nil {
		return 0
	}
	return s.droppedHops
}

// LogSwitchChain 输出一次完整的切换链（结构化日志，可据此复现切换顺序）。
//
// outcome ∈ {"exhausted","switched","canceled","ok"}，由调用方给出；空则记 "unknown"。
func (s *FailoverState) LogSwitchChain(ctx context.Context, outcome string) {
	if s == nil || len(s.SwitchChain) == 0 {
		return
	}
	if outcome == "" {
		outcome = "unknown"
	}
	hops := make([]string, 0, len(s.SwitchChain))
	for _, hop := range s.SwitchChain {
		hops = append(hops, fmt.Sprintf("%d:%s:%d->%d:%s:%d",
			hop.Seq, hop.Kind, hop.FromAccountID, hop.ToAccountID, hop.Reason, hop.UpstreamStatus))
	}
	logger.FromContext(ctx).Warn("gateway.failover_switch_chain",
		zap.String("outcome", outcome),
		zap.Int("hops", len(s.SwitchChain)),
		zap.Int("dropped_hops", s.droppedHops),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("max_switches", s.MaxSwitches),
		zap.Int("selection_retry_count", s.selectionRetryCount),
		zap.String("policy_source", s.policySource),
		zap.Int64("semantic_bytes", s.semanticBytes),
		zap.Strings("chain", hops),
	)
}

// NewFailoverState 创建 failover 状态
func NewFailoverState(maxSwitches int, hasBoundSession bool) *FailoverState {
	return &FailoverState{
		MaxSwitches:            maxSwitches,
		FailedAccountIDs:       make(map[int64]struct{}),
		SameAccountRetryCount:  make(map[int64]int),
		hasBoundSession:        hasBoundSession,
		profitVetoedAccountIDs: make(map[int64]struct{}),
	}
}

// RecordProfitVeto 记录一次分组利润门终检否决：把账号加入排除列表（同时登记到
// 利润否决集，使其不被 503 退避分支清掉）并递增否决计数。
//
// 返回 FailoverContinue 表示调用方可以继续重选下一个账号；返回 FailoverExhausted
// 表示本次请求的利润否决次数已达上限，调用方应按「无可用账号」终止，
// 不得继续 continue。
func (s *FailoverState) RecordProfitVeto(accountID int64) FailoverAction {
	s.FailedAccountIDs[accountID] = struct{}{}
	if s.profitVetoedAccountIDs == nil {
		s.profitVetoedAccountIDs = make(map[int64]struct{})
	}
	s.profitVetoedAccountIDs[accountID] = struct{}{}
	s.profitVetoCount++
	// P2/T2.4：利润否决也是一跳（它解释了「为什么这个账号没被用」）。
	s.AppendHop(SwitchHop{Kind: SwitchHopProfitVeto, FromAccountID: accountID, Reason: "profit_veto"})
	if s.profitVetoCount >= maxProfitVetoAttempts {
		return FailoverExhausted
	}
	return FailoverContinue
}

// switchHopReason 把 UpstreamFailoverError 的既有语义翻译成切换链里的人类可读原因。
//
// 刻意不重新跑分类器：链上的原因要反映**本次决策实际依据**的语义（凭据/绑定透传/
// 请求级瞬时/状态码），而不是再算一遍可能与调用方不同的分类。
func switchHopReason(failoverErr *service.UpstreamFailoverError) string {
	if failoverErr == nil {
		return "nil_error"
	}
	switch {
	case failoverErr.BoundUpstreamPassthrough:
		return "bound_passthru"
	case failoverErr.RequestScopedTransient:
		return "request_scoped_transient"
	case failoverErr.IsCredentialFailure():
		return "credential_failure"
	case failoverErr.RetryableOnSameAccount:
		return "retryable_on_same_account"
	case failoverErr.StatusCode == 0:
		return "transport_error"
	default:
		return fmt.Sprintf("upstream_status_%d", failoverErr.StatusCode)
	}
}

// ProfitVetoCount 返回本次请求累计的利润否决次数（供日志使用）。
func (s *FailoverState) ProfitVetoCount() int { return s.profitVetoCount }

// allExclusionsAreProfitVetoed 判断排除列表是否已全部由利润门否决贡献。
// 此时清空 FailedAccountIDs 会被原样恢复，退避重试不会带来任何新候选。
func (s *FailoverState) allExclusionsAreProfitVetoed() bool {
	if len(s.profitVetoedAccountIDs) == 0 || len(s.FailedAccountIDs) == 0 {
		return false
	}
	for id := range s.FailedAccountIDs {
		if _, ok := s.profitVetoedAccountIDs[id]; !ok {
			return false
		}
	}
	return true
}

func openAITextFailoverContext(c *gin.Context, account *service.Account, imageIntent bool) context.Context {
	if account.Platform == service.PlatformOpenAI && !imageIntent {
		return gatewayFailoverForwardContext(c)
	}
	return c.Request.Context()
}

func selectOpenAIFailoverRetry(ctx context.Context, selectAccount func(context.Context) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error), gateway *service.OpenAIGatewayService, retry **service.Account, failure *service.UpstreamFailoverError, excluded map[int64]struct{}, switches *int, maxSwitches int, oauth429 *service.OpenAIOAuth429FailoverState) (*service.AccountSelectionResult, service.OpenAIAccountScheduleDecision, error) {
	account := *retry
	*retry = nil
	if account == nil {
		return selectAccount(ctx)
	}
	selection, decision, err := selectAccount(service.WithFailoverRetryAccount(ctx, account.ID))
	if err == nil && selection != nil && selection.Account != nil && selection.Account.ID == account.ID {
		return selection, decision, nil
	}
	if selection != nil && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	if ctx.Err() != nil {
		return nil, decision, ctx.Err()
	}
	if err != nil && !errors.Is(err, service.ErrNoAvailableAccounts) && !errors.Is(err, service.ErrNoAvailableCompactAccounts) {
		return nil, decision, err
	}
	excluded[account.ID] = struct{}{}
	if *switches >= maxSwitches {
		return nil, decision, service.ErrNoAvailableAccounts
	}
	*switches++
	if gateway.ShouldStopOpenAIOAuth429Failover(account, failure.StatusCode, *switches, oauth429) {
		return nil, decision, service.ErrNoAvailableAccounts
	}
	gateway.RecordOpenAIAccountSwitch()
	service.FailoverSideEffectSinkFrom(ctx).ApproveCrossAccount(account.ID)
	return selectAccount(ctx)
}

var errFailoverRetrySelectionExhausted = errors.New("failover retry selection exhausted")

func (s *FailoverState) selectGatewayAccount(ctx context.Context, gateway *service.GatewayService, groupID *int64, session, model string, excluded map[int64]struct{}, metadataUser string, userID int64) (*service.AccountSelectionResult, error) {
	selectAccount := func(ctx context.Context, skip map[int64]struct{}) (*service.AccountSelectionResult, error) {
		return gateway.SelectAccountWithLoadAwareness(ctx, groupID, session, model, skip, metadataUser, userID)
	}
	retryID := s.retryAccountID
	if retryID == 0 {
		return selectAccount(ctx, excluded)
	}
	selection, err := selectAccount(service.WithFailoverRetryAccount(ctx, retryID), excluded)
	if err == nil && selection != nil && selection.Account != nil && selection.Account.ID == retryID {
		return selection, nil
	}
	if selection != nil && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && !errors.Is(err, service.ErrNoAvailableAccounts) {
		return nil, errFailoverRetrySelectionExhausted
	}
	if s.handleCrossAccountFailure(ctx, gateway, retryID, s.retryPlatform, s.LastFailoverErr) != FailoverContinue {
		return nil, errFailoverRetrySelectionExhausted
	}
	return selectAccount(ctx, mergeAccountIDSets(excluded, s.selectionExclusions()))
}

// HandleFailoverError 处理 UpstreamFailoverError，返回下一步动作。
// 包含：缓存计费判断、同账号重试、临时封禁、切换计数、Antigravity 延时。
func (s *FailoverState) HandleFailoverError(
	ctx context.Context,
	gatewayService TempUnscheduler,
	accountID int64,
	platform string,
	retryLimit int,
	failoverErr *service.UpstreamFailoverError,
) FailoverAction {
	// 客户端已断开：failover 只会用已取消的 context 重新选号并必然失败，
	// 不应再被当成账号耗尽处理（误报 502）。
	if ctx != nil && ctx.Err() != nil {
		return FailoverCanceled
	}
	s.LastFailoverErr = failoverErr
	if failoverErr == nil || !failoverErr.ShouldRetryNextAccount() {
		// P2/T2.4：既有语义禁止换号（NextAccountStop / 绑定透传）也是一跳。
		s.AppendHop(SwitchHop{Kind: SwitchHopExhausted, FromAccountID: accountID, Reason: switchHopReason(failoverErr)})
		s.LogSwitchChain(ctx, "exhausted")
		return FailoverExhausted
	}

	// 同账号重试不算切换账号，粘性会话仅在实际切换时强制缓存计费。
	retryCount := s.SameAccountRetryCount[accountID]
	sameAccountRetry := sameAccountRetryAllowed(failoverErr, retryCount, retryLimit)
	if !s.Budget.CanAttempt(time.Now()) {
		return s.exhaustBudget(ctx, accountID)
	}
	now := time.Now()
	retryDelay := time.Duration(0)
	if sameAccountRetry {
		retryDelay = s.retryDelay(sameAccountRetryDelayFor(failoverErr, retryCount+1))
		sameAccountRetry = s.Budget.allowsAccount(accountID, now.Add(retryDelay))
	}
	if sameAccountRetry {
		if needForceCacheBilling(s.hasBoundSession, failoverErr, true) {
			s.ForceCacheBilling = true
		}
		s.retryAccountID, s.retryPlatform = accountID, platform
		if s.idempotency != nil {
			s.idempotency.retryID = accountID
		}
		s.SameAccountRetryCount[accountID]++
		// P2/T2.4：同账号重试不推进切换计数，但必须进链（否则日志里看不出「重试了几次」）。
		s.AppendHop(SwitchHop{
			Kind:                  SwitchHopSameAccountRetry,
			FromAccountID:         accountID,
			Reason:                switchHopReason(failoverErr),
			UpstreamStatus:        failoverErr.StatusCode,
			SameAccountRetryCount: s.SameAccountRetryCount[accountID],
		})
		logger.FromContext(ctx).Warn("gateway.failover_same_account_retry",
			zap.Int64("account_id", accountID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("same_account_retry_count", s.SameAccountRetryCount[accountID]),
			zap.Int("same_account_retry_max", retryLimit),
			zap.Duration("retry_delay", retryDelay),
		)
		return s.waitForRetry(ctx, retryDelay, accountID, false)
	}

	return s.handleCrossAccountFailure(ctx, gatewayService, accountID, platform, failoverErr)
}

func (s *FailoverState) handleCrossAccountFailure(ctx context.Context, gatewayService TempUnscheduler, accountID int64, platform string, failoverErr *service.UpstreamFailoverError) FailoverAction {
	s.retryAccountID = 0
	if s.idempotency != nil {
		s.idempotency.retryID = 0
	}
	if s.denyIdempotencySwitch(ctx, accountID, failoverErr.StatusCode) {
		return FailoverExhausted
	}
	if needForceCacheBilling(s.hasBoundSession, failoverErr, false) {
		s.ForceCacheBilling = true
	}

	// 同账号重试用尽，执行临时封禁
	if failoverErr.RetryableOnSameAccount && gatewayService != nil && s.Budget.allowsAccount(accountID, time.Now()) {
		if !service.DeferFailoverMutation(ctx, accountID, "account", func(writeCtx context.Context) {
			gatewayService.TempUnscheduleRetryableError(writeCtx, accountID, failoverErr)
		}) {
			gatewayService.TempUnscheduleRetryableError(ctx, accountID, failoverErr)
		}
	}

	// 加入失败列表
	s.FailedAccountIDs[accountID] = struct{}{}

	// 检查是否耗尽
	if s.SwitchCount >= s.MaxSwitches {
		s.AppendHop(SwitchHop{
			Kind:           SwitchHopExhausted,
			FromAccountID:  accountID,
			Reason:         "switch_budget_exhausted",
			UpstreamStatus: failoverErr.StatusCode,
		})
		s.LogSwitchChain(ctx, "exhausted")
		return FailoverExhausted
	}

	if !s.Budget.CanSwitch(time.Now()) {
		return s.exhaustBudget(ctx, accountID)
	}

	// 递增切换计数
	service.FailoverSideEffectSinkFrom(ctx).ApproveCrossAccount(accountID)
	s.SwitchCount++
	// P2/T2.4：记一跳「从这里换到下一个」（ToAccountID 由调用方选号后回填）。
	s.AppendHop(SwitchHop{
		Kind:           SwitchHopAccountSwitch,
		FromAccountID:  accountID,
		Reason:         switchHopReason(failoverErr),
		UpstreamStatus: failoverErr.StatusCode,
	})
	logger.FromContext(ctx).Warn("gateway.failover_switch_account",
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("max_switches", s.MaxSwitches),
	)

	// Antigravity 平台换号线性递增延时
	if platform == service.PlatformAntigravity {
		delay := s.retryDelay(time.Duration(s.SwitchCount-1) * time.Second)
		return s.waitForRetry(ctx, delay, accountID, true)
	}

	return FailoverContinue
}

// HandleSelectionExhausted 处理选号失败（所有候选账号都在排除列表中）时的退避重试决策。
// 针对 Antigravity 单账号分组的 503 (MODEL_CAPACITY_EXHAUSTED) 场景：
// 清除排除列表、等待退避后重新选号。
//
// 返回 FailoverContinue 时，调用方应设置 SingleAccountRetry context 并 continue。
// 返回 FailoverExhausted 时，调用方应返回错误响应。
// 返回 FailoverCanceled 时，调用方应直接 return。
func (s *FailoverState) HandleSelectionExhausted(ctx context.Context) FailoverAction {
	// 客户端已断开时选号失败是 context canceled 的必然结果，
	// 不代表账号耗尽，直接按取消终止。
	if ctx.Err() != nil {
		return FailoverCanceled
	}

	if !s.Budget.CanSwitch(time.Now()) {
		return s.exhaustBudget(ctx, 0)
	}
	if s.LastFailoverErr != nil &&
		s.LastFailoverErr.StatusCode == http.StatusServiceUnavailable &&
		s.SwitchCount <= s.MaxSwitches {

		if s.maxSelectionRetries > 0 && s.selectionRetryCount >= s.maxSelectionRetries {
			s.AppendHop(SwitchHop{Kind: SwitchHopExhausted, Reason: "selection_retry_limit"})
			s.LogSwitchChain(ctx, "exhausted")
			return FailoverExhausted
		}
		// 排除列表全由利润门否决贡献时，清空后会被原样恢复：退避重试拿不到
		// 任何新候选，而利润否决不推进 SwitchCount，退避条件将永远成立。
		// 这里直接判定耗尽，避免每 2s 空转一轮的活锁。
		if s.allExclusionsAreProfitVetoed() {
			logger.FromContext(ctx).Warn("gateway.failover_selection_exhausted_by_profit_veto",
				zap.Int("profit_veto_count", s.profitVetoCount),
				zap.Int("excluded_accounts", len(s.FailedAccountIDs)),
			)
			return FailoverExhausted
		}

		delay := s.retryDelay(singleAccountBackoffDelay)
		logger.FromContext(ctx).Warn("gateway.failover_single_account_backoff",
			zap.Duration("backoff_delay", delay),
			zap.Int("switch_count", s.SwitchCount),
			zap.Int("max_switches", s.MaxSwitches),
		)
		// P2/T2.4：清空排除集重选也是一跳，否则「同一账号被反复选中」在日志里不可见。
		s.RecordSelectionRetry()
		s.AppendHop(SwitchHop{Kind: SwitchHopSelectionExhausted, Reason: "all_candidates_excluded"})
		if action := s.waitForRetry(ctx, delay, 0, true); action != FailoverContinue {
			return action
		}
		logger.FromContext(ctx).Warn("gateway.failover_single_account_retry",
			zap.Int("switch_count", s.SwitchCount),
			zap.Int("max_switches", s.MaxSwitches),
		)
		s.FailedAccountIDs = make(map[int64]struct{})
		// 利润门否决的账号不参与退避重试的解除：判定依据（冻结的下游倍率）在
		// 同一请求内不变，放它们回池只会被再次否决。
		for id := range s.profitVetoedAccountIDs {
			s.FailedAccountIDs[id] = struct{}{}
		}
		return FailoverContinue
	}
	s.AppendHop(SwitchHop{Kind: SwitchHopExhausted, Reason: "selection_exhausted"})
	s.LogSwitchChain(ctx, "exhausted")
	return FailoverExhausted
}

// needForceCacheBilling 判断 failover 时是否需要强制缓存计费。
// 粘性会话实际切换账号、或上游明确标记时，将 input_tokens 转为 cache_read 计费。
func needForceCacheBilling(hasBoundSession bool, failoverErr *service.UpstreamFailoverError, sameAccountRetry bool) bool {
	return (hasBoundSession && !sameAccountRetry) || (failoverErr != nil && failoverErr.ForceCacheBilling)
}

// failoverClientGone 判断下游客户端是否已断开（请求 context 已取消）。
// 客户端断开后 failover 必须静默终止：用已取消的 context 重新选号只会得到
// context.Canceled，并被误报成账号耗尽（通用 502）；上游 detach 的在途请求
// 照常完成计费，但不再为无人接收的响应启动新的上游尝试。
// 响应尚未提交时把状态码标记为 499（client closed request），供访问日志归类。
func failoverClientGone(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Context().Err() == nil {
		return false
	}
	// 先停 compact 心跳（接管 ResponseWriter，建立 happens-before），与
	// handleStreamingAwareError/errorResponse 等终结路径对齐，避免心跳
	// goroutine 与下面的状态标记并发触碰同一 writer。心跳已提交 200 时
	// 状态码已固化，不再标 499。
	if service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		return true
	}
	if !c.Writer.Written() {
		c.Status(statusClientClosedRequest)
	}
	return true
}

// sleepWithContext 等待指定时长，返回 false 表示 context 已取消。
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// retryDelay keeps the legacy timings when the failover policy is disabled.
func (s *FailoverState) retryDelay(delay time.Duration) time.Duration {
	if !s.jitterEnabled || delay <= 0 {
		return delay
	}
	spread := delay / 5
	return delay - spread + time.Duration(rand.Int64N(int64(2*spread+1)))
}

func (s *FailoverState) waitForRetry(ctx context.Context, delay time.Duration, accountID int64, switching bool) FailoverAction {
	if ctx.Err() != nil {
		return FailoverCanceled
	}
	if left, limited := s.Budget.Remaining(time.Now()); limited && delay >= left {
		return s.exhaustBudget(ctx, accountID)
	}
	if !sleepWithContext(ctx, delay) || ctx.Err() != nil {
		return FailoverCanceled
	}
	if !s.Budget.CanAttempt(time.Now()) || (switching && !s.Budget.CanSwitch(time.Now())) {
		return s.exhaustBudget(ctx, accountID)
	}
	return FailoverContinue
}

func (s *FailoverState) exhaustBudget(ctx context.Context, accountID int64) FailoverAction {
	if s.Outcome == nil {
		s.Outcome = s.Budget.outcome()
		if s.Outcome.LastStatus == 0 && s.LastFailoverErr != nil {
			s.Outcome.LastStatus = s.LastFailoverErr.StatusCode
		}
		s.AppendHop(SwitchHop{Kind: SwitchHopExhausted, FromAccountID: accountID, Reason: failoverReasonBudget, UpstreamStatus: s.Outcome.LastStatus})
		logger.FromContext(ctx).Warn("gateway.failover_budget_exhausted",
			zap.String("exhausted_reason", s.Outcome.Reason),
			zap.Int("attempts", s.Outcome.Attempts),
			zap.Int("last_status", s.Outcome.LastStatus))
		s.LogSwitchChain(ctx, "exhausted")
	}
	return FailoverExhausted
}

const failoverObservationKey = "gateway.failover.observation"
const failoverAttemptEvent = "failover_attempt"

// failoverObservation is local to the request and spans a fallback group.
// Snapshots are bounded; attempts and switches retain their exact counts.
type failoverObservation struct {
	state              *FailoverState
	attempts, switches int
	lastAccount        int64
	lastStatus         int
	statusKinds        uint8
	attemptsLog        []failoverAttemptObservation
	started            time.Time
	firstFailure       time.Time
	reason             string
	suppression        string
	logged             bool
	platform           string
	model              string
	groupID            int64
	cost               *requestCostAudit
}

// requestCostAudit keeps only the pending send. The next admitted send decides
// whether this was a same-account retry or a cross-account switch.
type requestCostAudit struct {
	recorder     *service.UpstreamCostAttemptRecorder
	id           uuid.UUID
	pending      *costAuditSend
	forwardStart int
}

type costAuditSend struct {
	row     service.UpstreamCostAttempt
	started time.Time
	ended   time.Time
	endOnce sync.Once
	headers http.Header
}

func installCostAttemptAudit(c *gin.Context, recorder *service.UpstreamCostAttemptRecorder) {
	if recorder == nil {
		return
	}
	// Identity originates on the server and spans bucket and fallback rebuilds.
	o := &failoverObservation{started: time.Now(), cost: &requestCostAudit{recorder: recorder, id: uuid.New()}}
	c.Set(failoverObservationKey, o)
}

func (a *requestCostAudit) begin(seq int, accountID int64) *costAuditSend {
	if pending := a.pending; pending != nil {
		decision := "switch_account"
		if pending.row.AccountID == accountID {
			decision = "retry_same_account"
		}
		a.submit(decision)
	}
	send := &costAuditSend{started: time.Now(), row: service.UpstreamCostAttempt{
		ExecutionID: a.id, AttemptIndex: seq, AccountID: accountID, Decision: "forward_failed",
	}}
	a.pending = send
	return send
}

func (s *costAuditSend) result(resp *http.Response, _ error) {
	if resp != nil {
		status := resp.StatusCode
		s.row.Status = &status
		s.headers = resp.Header
	}
}

func (s *costAuditSend) end() {
	s.endOnce.Do(func() { s.ended = time.Now() })
}

func (a *requestCostAudit) submit(decision string) bool {
	send := a.pending
	if send == nil {
		return false
	}
	a.pending = nil
	send.end() // Covers a forward that returns without draining its response.
	send.row.ElapsedMS = send.ended.Sub(send.started).Milliseconds()
	if decision != "" {
		send.row.Decision = decision
	}
	return a.recorder.Submit(send.row)
}

func (o *failoverObservation) finishCost(c *gin.Context) {
	if o == nil || o.cost == nil || o.cost.pending == nil {
		return
	}
	decision := ""
	switch {
	case c.Request.Context().Err() != nil:
		decision = "client_gone"
	case o.suppression != "":
		decision = "suppressed"
	case o.reason == failoverReasonBudget:
		decision = "budget_exhausted"
	case o.reason != "":
		decision = "exhausted"
	}
	accepted := o.cost.submit(decision)
	logger.L().Info("gateway.upstream_cost_attempts", zap.String("execution_id", o.cost.id.String()),
		zap.Int("attempts", o.attempts), zap.Bool("final_attempt_enqueued", accepted))
}

func recordForwardCost(c *gin.Context, err error, headers http.Header, usage service.UpstreamCostAttemptUsage) {
	o := requestFailoverObservation(c)
	if o == nil || o.cost == nil || o.cost.pending == nil || o.attempts == o.cost.forwardStart {
		return
	}
	send := o.cost.pending
	if err == nil {
		send.row.Decision = "succeeded"
	}
	if usage == (service.UpstreamCostAttemptUsage{}) || send.row.Status == nil || *send.row.Status < 200 || *send.row.Status >= 300 {
		return
	}
	// For multisend Forward calls require the actual response header object.
	// An upstream ID may be absent, reused or copied to another response.
	if headers != nil {
		if reflect.ValueOf(headers).Pointer() != reflect.ValueOf(send.headers).Pointer() {
			return
		}
	} else if o.attempts-o.cost.forwardStart != 1 {
		return
	}
	send.row.BilledUsage = &usage
	if err != nil {
		send.row.Decision = "partial_usage"
	}
}

func recordGatewayForwardCost(c *gin.Context, result *service.ForwardResult, err error) {
	if result == nil {
		recordForwardCost(c, err, nil, service.UpstreamCostAttemptUsage{})
		return
	}
	u := result.Usage
	recordForwardCost(c, err, result.UpstreamHeaders, service.UpstreamCostAttemptUsage{
		InputTokens: int64(u.InputTokens), OutputTokens: int64(u.OutputTokens),
		CacheCreationTokens: int64(u.CacheCreationInputTokens), CacheReadTokens: int64(u.CacheReadInputTokens),
		ImageCount: int64(result.ImageCount),
	})
}

func recordOpenAIForwardCost(c *gin.Context, result *service.OpenAIForwardResult, err error) {
	if result == nil {
		recordForwardCost(c, err, nil, service.UpstreamCostAttemptUsage{})
		return
	}
	u := result.Usage
	recordForwardCost(c, err, result.UpstreamHeaders, service.UpstreamCostAttemptUsage{
		InputTokens: int64(u.InputTokens), OutputTokens: int64(u.OutputTokens),
		CacheCreationTokens: int64(u.CacheCreationInputTokens), CacheReadTokens: int64(u.CacheReadInputTokens),
		ImageCount: int64(result.ImageCount),
	})
}

type failoverAttemptObservation struct {
	seq, switchSeq     int
	from, to           int64
	status             int
	class              service.FailureClass
	kind               service.TransportFailureKind
	at                 time.Time
	remaining, elapsed time.Duration
}

func requestFailoverObservation(c *gin.Context) *failoverObservation {
	if c == nil {
		return nil
	}
	value, _ := c.Get(failoverObservationKey)
	observation, _ := value.(*failoverObservation)
	return observation
}

func (s *FailoverState) observeForward(c *gin.Context, account *service.Account, groupID *int64, model string) {
	observation := requestFailoverObservation(c)
	if observation == nil {
		observation = &failoverObservation{started: time.Now()}
		c.Set(failoverObservationKey, observation)
	}
	observation.state = s
	if observation.cost != nil {
		observation.cost.forwardStart = observation.attempts
	}
	observation.platform = account.Platform
	observation.groupID = derefGroupID(groupID)
	observation.model = model
}

func (o *failoverObservation) beginSend(accountID int64) func(*http.Response, error) {
	if o == nil {
		return nil
	}
	o.attempts++
	snapshot := failoverAttemptObservation{seq: o.attempts, from: o.lastAccount, to: accountID, at: time.Now()}
	if o.lastAccount != 0 && o.lastAccount != accountID {
		o.switches++
	}
	o.lastAccount = accountID
	snapshot.switchSeq = o.switches
	if remaining, limited := o.state.Budget.Remaining(snapshot.at); limited {
		snapshot.remaining = max(remaining, 0)
	}
	return func(response *http.Response, sendErr error) {
		snapshot.elapsed = time.Since(snapshot.at)
		if response != nil {
			snapshot.status = response.StatusCode
		}
		if sendErr != nil {
			snapshot.kind = service.ClassifyTransportFailureKind(sendErr)
		}
		if snapshot.status >= 400 || sendErr != nil {
			if o.firstFailure.IsZero() {
				o.firstFailure = snapshot.at
			}
			snapshot.class, _ = service.ClassifyUpstreamFailure(service.ClassifyInput{StatusCode: snapshot.status, Platform: o.platform, TransportKind: snapshot.kind})
			o.lastStatus = snapshot.status
			switch {
			case snapshot.status == 429:
				o.statusKinds |= 1
			case snapshot.status == 503 || snapshot.status == 529:
				o.statusKinds |= 2
			case snapshot.status == 0 || snapshot.status >= 500:
				o.statusKinds |= 4
			default:
				o.statusKinds |= 8
			}
		}
		if len(o.attemptsLog) < 32 {
			o.attemptsLog = append(o.attemptsLog, snapshot)
		}
	}
}

func finishFailoverObservation(c *gin.Context) {
	o := requestFailoverObservation(c)
	o.finishCost(c)
	if o == nil || o.logged || (o.attempts < 2 && o.firstFailure.IsZero() && o.reason == "") {
		return
	}
	o.logged = true
	ctx := c.Request.Context()
	requestID, _ := ctx.Value(ctxkey.RequestID).(string)
	if requestID == "" {
		requestID = c.Writer.Header().Get("X-Request-Id")
	}
	log := logger.FromContext(ctx).With(zap.String("request_id", requestID), zap.Int64("group_id", o.groupID), zap.String("platform", o.platform), zap.String("model", o.model))
	for _, attempt := range o.attemptsLog {
		suppressed := o.suppression != "" && attempt.seq == o.attempts
		log.Info("failover.attempt", zap.Int("attempt_seq", attempt.seq), zap.Int("switch_seq", attempt.switchSeq),
			zap.Int64("from_account_id", attempt.from), zap.Int64("to_account_id", attempt.to),
			zap.String("failure_class", string(attempt.class)), zap.Int("upstream_status", attempt.status), zap.String("upstream_kind", string(attempt.kind)),
			zap.Bool("same_account_retry", attempt.from != 0 && attempt.from == attempt.to), zap.Bool("suppressed", suppressed),
			zap.Int64("budget_remaining_ms", attempt.remaining.Milliseconds()), zap.Int64("elapsed_ms", attempt.elapsed.Milliseconds()),
			zap.String("policy_source", o.state.policySource))
	}
	outcome := "success"
	switch {
	case ctx.Err() != nil:
		outcome = "client_gone"
	case o.reason == failoverReasonBudget:
		outcome = "budget_exhausted"
	case o.reason == "no_candidate", o.reason == "policy_blocked":
		outcome = o.reason
	case o.reason != "", c.Writer.Status() >= 400:
		outcome = "exhausted"
	}
	from := o.started
	if !o.firstFailure.IsZero() {
		from = o.firstFailure
	}
	elapsed := time.Since(from)
	exhausted := ""
	if o.reason != "stream_started" && o.reason != "policy_blocked" {
		exhausted = o.reason
	}
	service.RecordFailoverMetrics(o.attempts, o.switches, exhausted, o.suppression != "", elapsed)
	log.Info("failover.outcome", zap.String("outcome", outcome), zap.Int("attempts", o.attempts),
		zap.Int("switch_count", o.switches), zap.String("exhausted_reason", o.reason), zap.String("suppressed_reason", o.suppression),
		zap.Int64("elapsed_ms", elapsed.Milliseconds()), zap.Int("last_status", o.lastStatus),
		zap.Any("chain", o.state.SwitchChainSnapshot()))
}

// Append after legacy attribution and monitoring decisions so a recovered send
// or decision event cannot replace the last actual failure.
func appendFailoverOpsTelemetry(c *gin.Context, entry *service.OpsInsertErrorLogInput) {
	o := requestFailoverObservation(c)
	if o == nil || !o.logged {
		return
	}
	events := make([]*service.OpsUpstreamErrorEvent, 0, len(entry.UpstreamErrors)+len(o.attemptsLog)+2)
	for _, attempt := range o.attemptsLog {
		events = append(events, &service.OpsUpstreamErrorEvent{Kind: failoverAttemptEvent, AttemptSeq: attempt.seq,
			AtUnixMs: attempt.at.UnixMilli(), AccountID: attempt.to, UpstreamStatusCode: attempt.status, FailureClass: attempt.class, Message: "Observed upstream send"})
	}
	if o.switches > 0 {
		events = append(events, &service.OpsUpstreamErrorEvent{Kind: service.OpsFailoverSwitchEvent, Message: "Observed account switch"})
	}
	if o.reason != "" && o.reason != "stream_started" && o.reason != "policy_blocked" {
		events = append(events, &service.OpsUpstreamErrorEvent{Kind: service.OpsFailoverExhaustedEvent, Reason: o.reason, Message: "Observed failover exhaustion"})
	}
	entry.UpstreamErrors = append(events, entry.UpstreamErrors...)
}
