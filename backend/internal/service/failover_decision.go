package service

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 本文件是 P2/T2.2 的**决策门控**：把「是否换号」与「是否标记账号」合并成一次判定，
// 让副作用只在决策之后发生（修 R-17）。
//
// # 缺陷（R-17）
//
// 改造前 `handleFailoverSideEffects` 在 `Forward` **内部无条件**执行（标记账号失败、
// 写冷却、清粘性）。handler 之后即使决定「放弃换号」（例如已写出语义字节），也无法
// 撤销已经落库的标记 —— 于是「在 handler 层做可配置判定」整体失效：
// 决定换不换号的人和改账号状态的人不是同一个。
//
// # 解法
//
//  1. 判定收敛到 `DecideFailover`（纯函数）：输入 = 分类器建议 + 既有语义标志 +
//     写状态，输出 = `FailoverDecision{Switch, MarkAccount, Cooldown, ClearSticky,
//     Scope, Class}`。
//  2. 副作用执行收敛到 `ApplyFailoverSideEffects`（由 handler 在**确认要换号**之后调用）。
//  3. 门控 `SetFailoverDecisionGate(true)` 打开后：service 不再就地标记，而是把
//     「该怎么标记」挂到每请求的 `FailoverSideEffectSink` 上；handler 放弃换号就
//     `Suppress`（一条都不执行），决定换号才 `Apply`。
//
// # 默认零变更（本任务的硬约束）
//
// 门控**默认关闭**。关闭时 `handleFailoverSideEffects` 的执行路径与改造前逐字相同
// （无条件交给 RateLimitService，由它自己按池模式/自定义错误码决定标不标），因此
// 生产行为一个字节都没变。门控开启是 P5 灰度期的事；配置键不在本任务 in-scope
// （`backend/internal/config/config.go`），所以先由 `SetFailoverDecisionGate` 暴露机制，
// 供测试与后续阶段接线。
//
// # 不做的事
//
// 「已写语义字节后重放」（R-11）不在本文件：那会产生第二条账单，属 P6/T6.2。

// failoverDecisionGate 是副作用延迟执行的进程级开关，**默认关闭**（= 现状）。
var failoverDecisionGate atomic.Bool

// FailoverDecisionGateEnabled 报告副作用门控是否开启（默认 false）。
func FailoverDecisionGateEnabled() bool { return failoverDecisionGate.Load() }

// SetFailoverDecisionGate 打开/关闭副作用门控。仅供灰度接线与测试使用。
//
// 关闭时 `handleFailoverSideEffects` 走改造前的无条件路径；开启时由决策决定是否标记。
func SetFailoverDecisionGate(enabled bool) { failoverDecisionGate.Store(enabled) }

// FailoverDecision 是一次失败之后「要不要换号、要不要动账号状态」的唯一结论。
type FailoverDecision struct {
	// Switch 是否换到下一个账号。
	Switch bool
	// MarkAccount 是否把这次失败记到账号健康上（T2 冷却 / T3 写库的前提）。
	MarkAccount bool
	// Cooldown T2 冷却时长建议（0 = 不冷却；真正写 Redis 由 T4 接管）。
	Cooldown time.Duration
	// ClearSticky 是否清除粘性绑定（B2）。
	ClearSticky bool
	// Scope 失败归属：account / provider / request。
	Scope GatewayFailureScope
	// Class 失败类别（观测用）。
	Class FailureClass
	// SuppressedReason 非空表示「本该换号但被抑制」，取值见 FailoverSuppressedReason。
	SuppressedReason FailoverSuppressedReason
}

// FailoverDecisionInput 是决策的全部输入（值语义，便于穷举测试）。
type FailoverDecisionInput struct {
	// Class / Advice 来自分类器（P0 的产物）。
	Class  FailureClass
	Advice RetryAdvice

	// 既有语义字段（UpstreamFailoverError 上已有的那些）。
	Scope                    GatewayFailureScope
	IsCredentialFailure      bool
	RequestScopedTransient   bool
	BoundUpstreamPassthrough bool
	NextAccountAction        NextAccountAction

	// CanFailover 来自写状态判据（UpstreamWriteAttempt.CanFailover）。
	CanFailover bool
	// WriteBlockReason CanFailover=false 时的原因（semantic_bytes）。
	WriteBlockReason FailoverSuppressedReason
}

// DecideFailover 计算一次失败后的处置结论（纯函数，无 IO）。
//
// 「是否标记账号」三态（implementation.md T2.2 的 T-17）：
//   - provider 级 / request 级 / 请求级瞬时故障 → **不算**账号问题（MarkAccount=false）；
//   - 凭据失效（401/403 凭据类）→ **算**（MarkAccount=true）；
//   - 其余 account 归属的失败 → 算，但**由既有 RateLimitService 做最终收敛**
//     （池模式、custom_error_codes 语义不变）。
func DecideFailover(in FailoverDecisionInput) FailoverDecision {
	decision := FailoverDecision{
		Class:       in.Class,
		Scope:       in.Scope,
		Cooldown:    in.Advice.Cooldown,
		ClearSticky: in.Advice.ClearSticky,
	}

	// —— 是否换号 ——
	switch {
	case in.NextAccountAction == NextAccountStop:
		decision.Switch = false
		decision.SuppressedReason = FailoverSuppressedNextAccountStop
	case in.BoundUpstreamPassthrough:
		decision.Switch = false
		decision.SuppressedReason = FailoverSuppressedBoundPassthru
	case !in.CanFailover:
		decision.Switch = false
		decision.SuppressedReason = in.WriteBlockReason
		if decision.SuppressedReason == "" {
			decision.SuppressedReason = FailoverSuppressedSemanticBytes
		}
	default:
		decision.Switch = in.Advice.Failover
	}

	// —— 是否动账号状态 ——
	// 三条否决：与账号无关的降载、请求级失败、绑定透传（原样回写，账号没问题）。
	switch {
	case in.BoundUpstreamPassthrough:
		decision.MarkAccount = false
	case in.RequestScopedTransient:
		decision.MarkAccount = false
	case in.Scope == GatewayFailureScopeProvider, in.Scope == GatewayFailureScopeRequest:
		decision.MarkAccount = false
	case in.NextAccountAction == NextAccountStop:
		// provider 级故障（如共享凭据状态不可用）：不记到被选中的账号头上。
		decision.MarkAccount = false
	case in.IsCredentialFailure:
		decision.MarkAccount = true
	default:
		decision.MarkAccount = true
	}

	// 不标记账号时，冷却与粘性清理都没有意义（冷却建在账号健康之上）。
	if !decision.MarkAccount {
		decision.Cooldown = 0
	}
	if decision.Scope != GatewayFailureScopeAccount {
		decision.ClearSticky = false
	}
	return decision
}

// —— 延迟执行的副作用 ——————————————————————————————————————————————————————

// failoverSideEffectSinkKey 是 gin/context 里副作用收集器的键。
type failoverSideEffectSinkKeyType struct{}

var failoverSideEffectSinkKey failoverSideEffectSinkKeyType

// pendingFailoverSideEffect 是一条「待执行」的副作用。
type pendingFailoverSideEffect struct {
	accountID  int64
	decision   FailoverDecision
	apply      func()
	applied    bool
	suppressed bool
	reason     FailoverSuppressedReason
}

// FailoverSideEffectSink 收集一次请求内「延迟执行」的副作用。
//
// 每请求一个实例（放在 context 里），**不得**做成包级单例：否则并发请求会互相触发
// 对方的账号标记。handler 在确认要换号时 `Apply`，放弃换号时 `Suppress`。
type FailoverSideEffectSink struct {
	mu           sync.Mutex
	httpDeferred bool
	pending      []*pendingFailoverSideEffect
	approved     map[int64]bool
	mutations    map[int64]map[string]func(context.Context)
	committed    map[int64]map[string]bool
}

// DeferFailoverMutation returns true if this HTTP request owns commit timing.
// A scope identifies the affected state, not the error that led to the write.
func DeferFailoverMutation(ctx context.Context, accountID int64, scope string, apply func(context.Context)) bool {
	s := FailoverSideEffectSinkFrom(ctx)
	if s == nil || !s.httpDeferred {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.committed[accountID][scope] {
		return true
	}
	if s.mutations == nil {
		s.mutations = make(map[int64]map[string]func(context.Context))
	}
	if s.mutations[accountID] == nil {
		s.mutations[accountID] = make(map[string]func(context.Context))
	}
	s.mutations[accountID][scope] = apply
	return true
}

func (s *FailoverSideEffectSink) ApproveCrossAccount(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approved == nil {
		s.approved = make(map[int64]bool)
	}
	s.approved[accountID] = true
}

// CommitCrossAccount runs only after a different account has passed send admission.
func (s *FailoverSideEffectSink) CommitCrossAccount(ctx context.Context, nextID int64) {
	if s == nil || ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	var writes []func(context.Context)
	for id := range s.approved {
		if id == nextID {
			continue
		}
		delete(s.approved, id)
		for scope, apply := range s.mutations[id] {
			if s.committed == nil {
				s.committed = make(map[int64]map[string]bool)
			}
			if s.committed[id] == nil {
				s.committed[id] = make(map[string]bool)
			}
			s.committed[id][scope] = true
			writes = append(writes, apply)
		}
		delete(s.mutations, id)
	}
	s.mu.Unlock()
	if len(writes) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithValue(context.WithoutCancel(ctx), failoverSideEffectSinkKey, (*FailoverSideEffectSink)(nil)), 3*time.Second)
	defer cancel()
	// Explicitly clear the collector so nested state helpers execute instead of staging again.
	for _, apply := range writes {
		apply(writeCtx)
	}
}

func (s *FailoverSideEffectSink) discardMutationsLocked(accountID int64) {
	delete(s.approved, accountID)
	delete(s.mutations, accountID)
}

func HTTPFailoverMutationEnabled(ctx context.Context) bool {
	s := FailoverSideEffectSinkFrom(ctx)
	return s != nil && s.httpDeferred
}

func InstallHTTPFailoverMutationSink(c *gin.Context) *FailoverSideEffectSink {
	sink := InstallFailoverSideEffectSinkForRequest(c)
	if sink != nil {
		sink.httpDeferred = true
	}
	return sink
}

// deferRateLimitFailure stages the original mutation with stable input. Its
// return value is a routing prediction, not a claim that a write succeeded.
func predictRateLimitDisable(account *Account, status int, body []byte, model string) bool {
	if !account.ShouldHandleErrorCode(status) {
		return false
	}
	if tempUnschedRulesApply(account, status, body, model) && len(matchTempUnschedulableRules(account, status, body)) > 0 {
		return true
	}
	if account.IsPoolMode() && !account.IsCustomErrorCodesEnabled() {
		return false
	}
	if model != "" && isUpstreamModelNotFoundError(status, body) {
		return true
	}
	switch status {
	case http.StatusUnauthorized:
		return true
	case http.StatusPaymentRequired:
		return !account.IsCline()
	case http.StatusForbidden:
		return !isCloudflareBotBlockResponse(body) && (!(account.Platform == PlatformOpenAI || account.IsMultiProtocolAPIKey()) || !isHTMLResponse(body))
	case http.StatusBadRequest:
		message := strings.ToLower(extractUpstreamErrorMessage(body))
		return strings.Contains(message, "organization has been disabled") || strings.Contains(message, "identity verification is required") || (account.Platform == PlatformAnthropic && strings.Contains(message, "credit balance"))
	case http.StatusTooManyRequests:
		return false
	default:
		return account.IsCustomErrorCodesEnabled()
	}
}

func failoverMutationScope(account *Account, status int, body []byte, model string) string {
	if model != "" {
		if isUpstreamModelNotFoundError(status, body) {
			return "model:" + model
		}
		if matches := matchTempUnschedulableRules(account, status, body); tempUnschedRulesApply(account, status, body, model) && len(matches) > 0 && matches[0].rule.Scope == TempUnschedScopeModel {
			return "model:" + model
		}
	}
	return "account"
}

func (s *GatewayService) handleRateLimitFailure(ctx context.Context, account *Account, status int, headers http.Header, body []byte, models ...string) bool {
	if HTTPFailoverMutationEnabled(ctx) && account != nil {
		model := firstRequestedModel(models)
		frozenHeaders, frozenBody := headers.Clone(), append([]byte(nil), body...)
		DeferFailoverMutation(ctx, account.ID, failoverMutationScope(account, status, body, model), func(writeCtx context.Context) {
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(writeCtx, account, status, frozenHeaders, frozenBody, models...)
			}
		})
		return s.rateLimitService != nil && predictRateLimitDisable(account, status, body, model)
	}
	if s.rateLimitService == nil {
		return false
	}
	return s.rateLimitService.HandleUpstreamError(ctx, account, status, headers, body, models...)
}

// NewFailoverSideEffectSink 创建收集器（测试与 handler 用）。
func NewFailoverSideEffectSink() *FailoverSideEffectSink { return &FailoverSideEffectSink{} }

// WithFailoverSideEffectSink 把收集器放进 context（handler 在 Forward 之前调用）。
func WithFailoverSideEffectSink(ctx context.Context, sink *FailoverSideEffectSink) context.Context {
	if ctx == nil || sink == nil {
		return ctx
	}
	return context.WithValue(ctx, failoverSideEffectSinkKey, sink)
}

// FailoverSideEffectSinkFrom 取出 context 里的收集器；没有则返回 nil。
//
// ctx 通常是 `c.Request.Context()` 的派生值，因此 service 侧不需要额外的 gin 依赖
// 就能拿到「同一个请求」的收集器。
func FailoverSideEffectSinkFrom(ctx context.Context) *FailoverSideEffectSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(failoverSideEffectSinkKey).(*FailoverSideEffectSink)
	return sink
}

// Add 登记一条待执行副作用，返回它（便于调用方在决定后精确 Apply/Suppress）。
func (s *FailoverSideEffectSink) Add(accountID int64, decision FailoverDecision, apply func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, &pendingFailoverSideEffect{
		accountID: accountID,
		decision:  decision,
		apply:     apply,
	})
}

// Len 返回登记的副作用条数（测试与日志用）。
func (s *FailoverSideEffectSink) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.pending)
	for _, actions := range s.mutations {
		count += len(actions)
	}
	return count
}

// Apply 执行该账号**全部未决**副作用；同一账号重复调用只执行一次（R-18：
// 同请求同账号 4 次失败只做一次账号级副作用）。
func (s *FailoverSideEffectSink) Apply(accountID int64) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	applied := 0
	for _, item := range s.pending {
		if item.accountID != accountID || item.applied || item.suppressed {
			continue
		}
		if item.apply != nil {
			item.apply()
		}
		item.applied = true
		applied++
	}
	return applied
}

// Suppress 放弃该账号的全部未决副作用（handler 决定不换号时调用），返回被放弃的条数。
func (s *FailoverSideEffectSink) Suppress(accountID int64, reason FailoverSuppressedReason) int {
	if s == nil {
		return 0
	}
	if reason == "" {
		reason = FailoverSuppressedSemanticBytes
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discardMutationsLocked(accountID)
	suppressed := 0
	for _, item := range s.pending {
		if item.accountID != accountID || item.applied || item.suppressed {
			continue
		}
		item.suppressed = true
		item.reason = reason
		suppressed++
	}
	return suppressed
}

// —— 副作用执行（唯一入口）——————————————————————————————————————————————————————

// applyUpstreamErrorSideEffects 是改造前 `handleFailoverSideEffects` 的函数体：
// 读上游错误体并交给 RateLimitService（它自己按池模式 / custom_error_codes 决定标记）。
func (s *GatewayService) applyUpstreamErrorSideEffects(ctx context.Context, resp *http.Response, account *Account, requestedModel ...string) {
	body, _ := s.readUpstreamErrorBody(resp)
	if len(requestedModel) > 0 {
		s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel[0])
		return
	}
	s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)
}

// ApplyFailoverSideEffects 执行一次失败之后该做的账号级副作用（T2 冷却 / T3 写库）。
//
// 门控关闭时由 service 就地调用（改造前语义）；门控开启时由 handler 在确认换号后
// 通过 `FailoverSideEffectSink.Apply` 触发。`decision.MarkAccount=false` 时**什么都不做**
// —— 这正是 R-17 的修复点：判定为「不算账号问题」就不会改写账号状态。
func (s *GatewayService) ApplyFailoverSideEffects(ctx context.Context, resp *http.Response, account *Account, decision FailoverDecision, requestedModel ...string) {
	if s == nil || account == nil {
		return
	}
	if !decision.MarkAccount {
		logFailoverSuppressed(account, decision, "not_account_failure")
		return
	}
	s.applyUpstreamErrorSideEffects(ctx, resp, account, requestedModel...)
}

// ClassifyGatewayFailover applies the same policy to the observed upstream
// failure and the decision on whether account state may change.
func ClassifyGatewayFailover(ctx context.Context, account *Account, failure *UpstreamFailoverError, canSwitch bool) (FailoverDecision, RetryAdvice) {
	policy, _ := ResolvedGatewayFailoverPolicy(ctx)
	if policy.Legacy() {
		policy = nil
	}
	input := ClassifyInput{Policy: policy, Platform: account.Platform, Canceled: ctx.Err() != nil,
		StatusCode: failure.StatusCode, ResponseHeaders: failure.ResponseHeaders, ResponseBody: failure.ResponseBody,
		Stage: failure.Stage, Scope: failure.Scope, NextAccountAction: failure.NextAccountAction,
		RetryableOnSameAccount: failure.RetryableOnSameAccount, RequestScopedTransient: failure.RequestScopedTransient,
		BoundUpstreamPassthrough: failure.BoundUpstreamPassthrough, IsCredentialFailure: failure.IsCredentialFailure()}
	class, advice := ClassifyUpstreamFailure(input)
	scope := failure.Scope
	if scope == "" && failure.StatusCode >= 400 && !failure.IsCredentialFailure() {
		scope = advice.Scope
	}
	decision := DecideFailover(FailoverDecisionInput{Class: class, Advice: advice, Scope: scope,
		IsCredentialFailure: failure.IsCredentialFailure(), RequestScopedTransient: failure.RequestScopedTransient,
		BoundUpstreamPassthrough: failure.BoundUpstreamPassthrough, NextAccountAction: failure.NextAccountAction,
		CanFailover: canSwitch && ctx.Err() == nil})
	return decision, advice
}

// buildFailoverDecisionForResponse 从一次上游错误响应算出决策（纯计算，不执行副作用）。
//
// 它只用「响应 + 账号」能拿到的信息：分类器给类别与建议，既有标志位由调用方后续补充。
func buildFailoverDecisionForResponse(resp *http.Response, account *Account) FailoverDecision {
	in := FailoverDecisionInput{CanFailover: true}
	if resp != nil {
		platform := ""
		if account != nil {
			platform = account.Platform
		}
		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode: resp.StatusCode,
			Platform:   platform,
		})
		in.Class = class
		in.Advice = advice
		in.Scope = advice.Scope
	}
	return DecideFailover(in)
}

// logFailoverSuppressed 输出结构化的「放弃换号 / 不标记」日志（R-17 可观测性要求）。
func logFailoverSuppressed(account *Account, decision FailoverDecision, reason string) {
	fields := []zap.Field{
		zap.String("reason", reason),
		zap.String("class", string(decision.Class)),
		zap.String("scope", string(decision.Scope)),
		zap.Bool("switch", decision.Switch),
		zap.Bool("mark_account", decision.MarkAccount),
	}
	if decision.SuppressedReason != "" {
		fields = append(fields, zap.String("suppressed_reason", string(decision.SuppressedReason)))
	}
	if account != nil {
		fields = append(fields,
			zap.Int64("account_id", account.ID),
			zap.String("platform", account.Platform),
		)
	}
	logger.L().With(fields...).Warn("gateway.failover_suppressed")
}

// —— gin.Context 侧的收集器安装 / 取用（handler 用）————————————————————————————

// failoverSideEffectSinkGinKey 是 gin.Context 里的收集器键。
const failoverSideEffectSinkGinKey = "service.failover_side_effect_sink"

// InstallFailoverSideEffectSinkForRequest 给本次请求安装副作用收集器，并把携带收集器的
// context 写回 `c.Request`（service 侧通过 ctx 取用，不需要 import handler）。
//
// 只在门控开启时由 handler 调用；返回收集器便于同一函数内 Apply/Suppress。
func InstallFailoverSideEffectSinkForRequest(c *gin.Context) *FailoverSideEffectSink {
	if c == nil {
		return nil
	}
	if sink := FailoverSideEffectSinkForRequest(c); sink != nil {
		return sink
	}
	sink := NewFailoverSideEffectSink()
	c.Set(failoverSideEffectSinkGinKey, sink)
	if c.Request != nil {
		c.Request = c.Request.WithContext(WithFailoverSideEffectSink(c.Request.Context(), sink))
	}
	return sink
}

// FailoverSideEffectSinkForRequest 取出本次请求的收集器；没装过返回 nil。
func FailoverSideEffectSinkForRequest(c *gin.Context) *FailoverSideEffectSink {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(failoverSideEffectSinkGinKey); ok {
		if sink, ok := v.(*FailoverSideEffectSink); ok {
			return sink
		}
	}
	if c.Request != nil {
		return FailoverSideEffectSinkFrom(c.Request.Context())
	}
	return nil
}

const FailoverSuppressedSideEffectNoKey FailoverSuppressedReason = FailoverIdempotencySuppressedReasonSideEffectNoKey

// ApplyFailoverSideEffectsForRequest 执行该账号的待定副作用（handler 确认要换号后调用）。
func ApplyFailoverSideEffectsForRequest(c *gin.Context, accountID int64) int {
	return FailoverSideEffectSinkForRequest(c).Apply(accountID)
}

// SuppressFailoverSideEffectsForRequest 放弃该账号的待定副作用（handler 决定不换号时调用，
// 这就是 R-17 的修复：判定与副作用同源，放弃换号就不再改写账号状态）。
func SuppressFailoverSideEffectsForRequest(c *gin.Context, accountID int64, reason FailoverSuppressedReason) int {
	return FailoverSideEffectSinkForRequest(c).Suppress(accountID, reason)
}
