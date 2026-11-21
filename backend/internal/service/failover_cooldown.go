package service

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ---------------------------------------------------------------------------
// T2 跨请求冷却层（Redis）：service 侧契约
// ---------------------------------------------------------------------------
//
// 设计依据：design.md §3.4（T1/T2/T3 三级状态）、§3.5（并发安全）、§6.2（Redis key 结构）；
// 任务：implementation.md T4.1。
//
// 分层硬约束（backend/.golangci.yml 的 `service-no-repository`）：
//   - service 层**不得** import repository / redis。本文件因此只有接口、数据类型与纯函数；
//     Redis 实现（3 段 Lua / MGET / SETNX）在
//     backend/internal/repository/failover_cooldown_cache.go。
//
// 三条必须守住的语义（改动前先读）：
//  1. **extend-only**：写冷却不得短于既有剩余 TTL（design §3.5.3 问题 1，对应 DB 侧
//     `SetTempUnschedulable` 的 `temp_unschedulable_until IS NULL OR < $1` 守卫）。
//     缩短冷却会让坏账号过早回到候选集，是 T2 唯一的正确性红线。
//  2. **fail-open**：Redis 不可用 / 超时视为「没有任何账号在冷却」，错误**不**向上抛给业务
//     （design §3.5.3 问题 4、验收 C-02：绝不允许 Redis 故障导致全站 503）；降级事件计入
//     FailoverCooldownDegradedCounter，供 P5 的 `failover_cooldown_degraded_total` 消费。
//  3. **门控在调用方**：`cooldown.enabled` 是分组可覆盖的策略项（design §3.4.6），而存储是
//     进程内单例——所以存储层**不**读 enabled，由调用方按生效策略短路（F-02：关闭时零 RTT）。
//
// 职责边界（避免同一张表被拆到两处）：按失败类型的差异化 TTL（design §3.4.3 的
// auth_invalid 60s / quota_exhausted 兜底 1800s & cap 24h / capacity_shed 0 等）属于**策略层**
// （T4.3 的 NoteFailure：它同时决定「连续 N 次才冷却」与 T3 写库），存储层只负责
// extend-only 语义与配置边界截断（default_ttl / max_ttl 全部读 config，不写死时长）。

// FailoverCooldownEntry 表示一个账号在 T2 中处于冷却中。
type FailoverCooldownEntry struct {
	// Class 写入该冷却时的失败类别（Redis value 的 class 段，design §6.2）。
	Class FailureClass
	// Deadline 冷却截止时间（Redis value 的 deadlineMs 段，Redis 服务端时钟，仅供日志/观测）。
	// **不参与判定**：是否排除某账号只看 key 是否存在——客户端不做 `until - now()` 比较
	// （design §3.5.4：时钟以 Redis TTL 为准）。
	Deadline time.Time
}

// FailoverCooldownWrite 是一次 T2 冷却写入请求。
type FailoverCooldownWrite struct {
	// AccountID 目标账号 ID。
	AccountID int64
	// Model 非空 => 账号×模型级冷却（key 追加 sha256(model)[:16]，design §6.2）；
	// 空 => 账号级冷却。
	Model string
	// Class 失败类别，写入 Redis value，读侧可见（用于日志/观测，不参与排除判定）。
	Class FailureClass
	// TTL 期望冷却时长（由策略层按 design §3.4.3 的差异化表算出，存储层只做边界截断）：
	//   - TTL <= 0 且 UseDefaultTTL=false → **不冷却**（capacity_shed / request_invalid /
	//     policy_blocked / binding_passthru），存储层直接跳过写入；
	//   - TTL > 配置 max_ttl → 按 max_ttl 截断。
	TTL time.Duration
	// UseDefaultTTL 表示「该类别要按上游 reset 计算，但上游没给」→ 用配置 default_ttl 兜底
	// （design §3.4.6）。与 TTL<=0 的「不冷却」语义互斥：本字段为 true 时 TTL<=0 按
	// default_ttl 处理。
	UseDefaultTTL bool
}

// FailoverCooldownWriteResult 是一次冷却写入的结果。
type FailoverCooldownWriteResult struct {
	// Applied 本次是否真的写入（false = 已有更长冷却保持不动 / 不需要冷却 / Redis 降级）。
	Applied bool
	// TTL 写入后实际生效的冷却时长；Applied=false 且既有冷却更长时，为既有剩余 TTL。
	TTL time.Duration
	// Degraded fail-open：Redis 不可用，写入未生效（调用方据此记 cooldown_written=false）。
	Degraded bool
}

// FailoverCooldownDegradeReason 是 fail-open 降级的原因（日志与指标维度）。
type FailoverCooldownDegradeReason string

const (
	// FailoverCooldownDegradeRead 批量读（MGET）降级：不排除任何账号。
	FailoverCooldownDegradeRead FailoverCooldownDegradeReason = "read"
	// FailoverCooldownDegradeWrite 冷却写入降级：不写冷却。
	FailoverCooldownDegradeWrite FailoverCooldownDegradeReason = "write"
	// FailoverCooldownDegradeClear 冷却清除降级：冷却按 TTL 自然过期。
	FailoverCooldownDegradeClear FailoverCooldownDegradeReason = "clear"
	// FailoverCooldownDegradeStreak 连续失败计数降级：按 0 次失败处理（不冷却）。
	FailoverCooldownDegradeStreak FailoverCooldownDegradeReason = "streak"
	// FailoverCooldownDegradeProbe 半开探测降级：不放行探测（保持现状，不额外排除）。
	FailoverCooldownDegradeProbe FailoverCooldownDegradeReason = "probe"
)

// FailoverCooldownDegradeReporter 接收 T2 的 fail-open 降级事件。
//
// P5 的 `failover_cooldown_degraded_total` 从这里取数（design §3.8.2：该指标 >0 即 P1 告警，
// 非零代表 T2 失效 = 等价于 cooldown.enabled=false）。
type FailoverCooldownDegradeReporter interface {
	ReportFailoverCooldownDegraded(reason FailoverCooldownDegradeReason)
}

// FailoverCooldownStore 是 T2 跨请求冷却存储（Redis 实现见 repository）。
//
// 所有方法都实现 fail-open：Redis 不可用 / 超时时返回「未冷却」语义，**不返回 error**
// （错误不得向上抛给业务），同时通过 FailoverCooldownDegradeReporter 计数降级。
type FailoverCooldownStore interface {
	// IsCooledBatch 一次 MGET 批量判定账号级冷却（design §3.5.3 问题 4）。
	// 返回**只包含**处于冷却的账号；Redis 故障时返回空 map（= 一个都不排除）。
	IsCooledBatch(ctx context.Context, accountIDs []int64) map[int64]FailoverCooldownEntry
	// IsModelCooledBatch 账号×模型级冷却判定（design §6.2 的 {modelHash} key）。
	// model 为空时返回空 map（不回落账号级：账号级由 IsCooledBatch 负责）。
	IsModelCooledBatch(ctx context.Context, accountIDs []int64, model string) map[int64]FailoverCooldownEntry
	// Cool 写入冷却，遵守 extend-only（只延长不缩短，design §3.5.3 问题 1）。
	Cool(ctx context.Context, write FailoverCooldownWrite) FailoverCooldownWriteResult
	// Clear 清除账号级冷却与连续失败计数（design §3.4.4「成功即清除」）。
	Clear(ctx context.Context, accountID int64)
	// IncrFailureStreak 原子自增连续失败计数，并保证该 key 一定有 TTL
	// （design §3.5.3 问题 2：INCR 与首个 PEXPIRE 必须同一脚本，否则崩溃会留下**永久 key**
	// → 账号永久冷却）。window<=0 时使用配置 streak_window。
	// Redis 故障返回 0（= 未计数 → 不冷却，fail-open）。
	IncrFailureStreak(ctx context.Context, accountID int64, window time.Duration) int64
	// AcquireProbeToken 取半开探测令牌：只有拿到令牌的请求被放行
	// （design §3.4.4 / §3.5.3 问题 3：`SET key 1 EX 10 NX`）。
	AcquireProbeToken(ctx context.Context, accountID int64) bool
	// ClearProbeToken 释放探测令牌（探测失败再升级、或手动清除时使用）。
	ClearProbeToken(ctx context.Context, accountID int64)
}

// FailoverCooldownScope identifies an account or an explicit account-model limit.
type FailoverCooldownScope struct {
	AccountID int64
	Model     string
}

// FailoverCooldownSnapshot separates active isolation from probe candidates.
// A pending scope is not excluded until a selected candidate requests a lease.
type FailoverCooldownSnapshot struct {
	Active   map[FailoverCooldownScope]FailoverCooldownEntry
	Pending  map[FailoverCooldownScope]struct{}
	Degraded bool
}

type FailoverProbeStatus string

const (
	FailoverProbeNotRequired FailoverProbeStatus = "not_required"
	FailoverProbeCooling     FailoverProbeStatus = "cooling"
	FailoverProbeAcquired    FailoverProbeStatus = "acquired"
	FailoverProbeBusy        FailoverProbeStatus = "busy"
	FailoverProbeDegraded    FailoverProbeStatus = "degraded"
)

// FailoverProbeLease identifies a single acquisition. The token expires even
// when the holder never reports an outcome.
type FailoverProbeLease struct {
	Scope FailoverCooldownScope
	Token string
	State FailoverIsolationProbeState
}

type FailoverProbeAcquisition struct {
	Status FailoverProbeStatus
	Lease  FailoverProbeLease
}

type FailoverProbeOutcome string

const (
	FailoverProbeRelease FailoverProbeOutcome = "release"
	FailoverProbeSuccess FailoverProbeOutcome = "success"
	FailoverProbeFailure FailoverProbeOutcome = "failure"
)

type FailoverProbeCompletion struct {
	Applied  bool
	Degraded bool
}

// FailoverRecoveryStore extends the legacy store with atomic, scoped recovery.
// Callers should batch observations, acquire only for a selected pending scope,
// and complete acquired leases with the policy in effect for that scope.
type FailoverRecoveryStore interface {
	FailoverCooldownStore
	CoolWithRecovery(context.Context, FailoverCooldownWrite, bool) FailoverCooldownWriteResult
	ReadRecoveryBatch(context.Context, []FailoverCooldownScope) FailoverCooldownSnapshot
	AcquireRecoveryProbe(context.Context, FailoverCooldownScope) FailoverProbeAcquisition
	CompleteRecoveryProbe(context.Context, FailoverProbeLease, FailoverProbeOutcome, *FailoverPolicy) FailoverProbeCompletion
}

// FailoverCooldownTTLBounds is the configured fallback and limit.
//
// 独立成值而不是直接用 config：调用方拿到的是**分组覆盖后**的生效数值，存储层不应该知道
// 策略来源（组/账号/全局），只负责按边界截断。
type FailoverCooldownTTLBounds struct {
	// DefaultTTL 「类别未给出固定时长」时的兜底冷却（配置 cooldown.default_ttl）。
	DefaultTTL time.Duration
	// MaxTTL 上游 reset 时间的截断上限（配置 cooldown.max_ttl）。
	MaxTTL time.Duration
}

// FailoverCooldownTTLBoundsFromConfig 从全局配置构造冷却时长边界。
func FailoverCooldownTTLBoundsFromConfig(cfg config.FailoverCooldownConfig) FailoverCooldownTTLBounds {
	return FailoverCooldownTTLBounds{
		DefaultTTL: cfg.DefaultTTL,
		MaxTTL:     cfg.MaxTTL,
	}
}

// NormalizeFailoverCooldownTTL 把一次冷却写入的时长规范化到配置允许的区间（纯函数，无 I/O）。
//
//	useDefault && ttl <= 0        → bounds.DefaultTTL（「按上游 reset 算但上游没给」的兜底）
//	ttl <= 0                      → 0（不冷却，调用方据此跳过写入）
//	bounds.MaxTTL > 0 && ttl > 上限 → bounds.MaxTTL（上游 reset 截断）
//	其余                          → ttl
//
// 两个边界值**全部来自配置**（config.FailoverCooldownConfig 的 default_ttl / max_ttl），
// 这里不写死任何时长常量——改默认值只改 config，不改本函数。
func NormalizeFailoverCooldownTTL(ttl time.Duration, bounds FailoverCooldownTTLBounds, useDefault bool) time.Duration {
	if useDefault && ttl <= 0 {
		return bounds.DefaultTTL
	}
	if ttl <= 0 {
		return 0
	}
	if bounds.MaxTTL > 0 && ttl > bounds.MaxTTL {
		return bounds.MaxTTL
	}
	return ttl
}

// FailoverCooldownDegradedCounter 是 fail-open 降级的进程内计数器（P5 观测消费）。
//
// 按原因分桶，Total() 对应指标 `failover_cooldown_degraded_total`（design §3.8.2）。
// 并发安全（每桶 atomic），可在选号热路径无锁累加。
type FailoverCooldownDegradedCounter struct {
	read   atomic.Int64
	write  atomic.Int64
	clear  atomic.Int64
	streak atomic.Int64
	probe  atomic.Int64
	other  atomic.Int64
}

// ReportFailoverCooldownDegraded 实现 FailoverCooldownDegradeReporter。
func (c *FailoverCooldownDegradedCounter) ReportFailoverCooldownDegraded(reason FailoverCooldownDegradeReason) {
	switch reason {
	case FailoverCooldownDegradeRead:
		c.read.Add(1)
	case FailoverCooldownDegradeWrite:
		c.write.Add(1)
	case FailoverCooldownDegradeClear:
		c.clear.Add(1)
	case FailoverCooldownDegradeStreak:
		c.streak.Add(1)
	case FailoverCooldownDegradeProbe:
		c.probe.Add(1)
	default:
		c.other.Add(1)
	}
}

// Count 返回某个降级原因的累计次数（未知原因返回 other 桶）。
func (c *FailoverCooldownDegradedCounter) Count(reason FailoverCooldownDegradeReason) int64 {
	switch reason {
	case FailoverCooldownDegradeRead:
		return c.read.Load()
	case FailoverCooldownDegradeWrite:
		return c.write.Load()
	case FailoverCooldownDegradeClear:
		return c.clear.Load()
	case FailoverCooldownDegradeStreak:
		return c.streak.Load()
	case FailoverCooldownDegradeProbe:
		return c.probe.Load()
	default:
		return c.other.Load()
	}
}

// Total 返回全部降级次数（= failover_cooldown_degraded_total）。
func (c *FailoverCooldownDegradedCounter) Total() int64 {
	return c.read.Load() + c.write.Load() + c.clear.Load() + c.streak.Load() + c.probe.Load() + c.other.Load()
}

// FailoverIsolation is the T2 service state machine. The caller records the
// request-local exclusion and handles sticky clearance before committing T2.
// The caller also owns any subsequent T3 action.
type FailoverIsolation struct {
	store FailoverCooldownStore
}

func NewFailoverIsolation(store FailoverCooldownStore) *FailoverIsolation {
	return &FailoverIsolation{store: store}
}

// FailoverIsolationFailure describes one account failure from a request whose
// actual upstream failures have already been collected. FailedAccountIDs must
// contain the target ID and exclude profit vetoes and local selection failures.
// Call NoteFailure at most once for an account in that request.
type FailoverIsolationFailure struct {
	Policy           *FailoverPolicy // effective policy after all overlays
	Account          *Account
	Decision         FailoverDecision // requires explicit account attribution
	FailedAccountIDs []int64

	// Advice, when present, is the resolved classifier advice for Decision.Class.
	Advice *RetryAdvice
	// ModelScoped requires explicit evidence of a model-only limit. Model is the
	// canonical upstream model for that evidence. An unidentified model is skipped.
	ModelScoped bool
	Model       string
	// UpstreamResetAfter is an observed reset delay, or nil when absent. A known
	// elapsed reset is distinct from missing evidence and does not start a cooldown.
	UpstreamResetAfter *time.Duration
}

// FailoverIsolationFailureResult reports the observed streak and write outcome.
// A streak of zero conveys no usable counter result.
type FailoverIsolationFailureResult struct {
	Streak int64
	Write  FailoverCooldownWriteResult
}

// NoteFailure records an eligible account-attributed failure in T2. F-01 is
// checked before every store operation so a multi-account failure cannot affect
// cooling or streak state created by any other request.
func (s *FailoverIsolation) NoteFailure(ctx context.Context, in FailoverIsolationFailure) FailoverIsolationFailureResult {
	var result FailoverIsolationFailureResult
	if !s.enabled(ctx, in.Policy, in.Account) || !in.Decision.MarkAccount || in.Decision.Scope != GatewayFailureScopeAccount ||
		!isOnlyFailedIsolationAccount(in.Account.ID, in.FailedAccountIDs) {
		return result
	}
	class := in.Decision.Class
	var thresholded bool
	switch class {
	case ClassAuthInvalid, ClassRateLimited, ClassQuotaExhausted:
	case ClassUpstream5xx, ClassUpstreamTimeout, ClassNetwork:
		thresholded = true
	default:
		return result
	}

	var model string
	if in.ModelScoped && (class == ClassRateLimited || class == ClassQuotaExhausted) {
		model = strings.TrimSpace(in.Model)
		if model == "" {
			return result
		}
	}
	advice := builtinFailoverAdvice(class)
	if in.Advice != nil {
		advice = *in.Advice
	}
	if advice.CooldownSource == cooldownSourceNone {
		return result
	}
	ttl := advice.Cooldown
	if advice.CooldownSource == cooldownSourceUpstreamReset && in.UpstreamResetAfter != nil {
		ttl = *in.UpstreamResetAfter
	}
	ttl = NormalizeFailoverCooldownTTL(ttl, isolationTTLBounds(in.Policy), false)
	if ttl <= 0 {
		return result
	}
	if thresholded {
		threshold, window := in.Policy.CooldownConsecutiveFailuresThreshold, in.Policy.CooldownStreakWindow
		if threshold == nil || *threshold <= 0 || window == nil || *window <= 0 {
			return result
		}
		result.Streak = s.store.IncrFailureStreak(ctx, in.Account.ID, *window)
		if result.Streak < int64(*threshold) {
			return result
		}
	}
	write := FailoverCooldownWrite{AccountID: in.Account.ID, Model: model, Class: class, TTL: ttl}
	if recovery, ok := s.store.(FailoverRecoveryStore); ok {
		probeEnabled := in.Policy.CooldownProbeEnabled != nil && *in.Policy.CooldownProbeEnabled &&
			in.Policy.CooldownProbeMaxAttempts != nil && *in.Policy.CooldownProbeMaxAttempts > 0
		result.Write = recovery.CoolWithRecovery(ctx, write, probeEnabled)
	} else {
		result.Write = s.store.Cool(ctx, write)
	}
	return result
}

func (s *FailoverIsolation) enabled(ctx context.Context, policy *FailoverPolicy, account *Account) bool {
	return s != nil && s.store != nil && ctx != nil && ctx.Err() == nil &&
		account != nil && account.ID > 0 && policy != nil && policy.CooldownEnabled != nil && *policy.CooldownEnabled &&
		(!account.IsPoolMode() || (policy.CooldownPoolModeEnabled != nil && *policy.CooldownPoolModeEnabled))
}

func isOnlyFailedIsolationAccount(accountID int64, failedIDs []int64) bool {
	found := false
	for _, id := range failedIDs {
		if id <= 0 {
			continue
		}
		if id != accountID {
			return false
		}
		found = true
	}
	return found
}

func isolationTTLBounds(policy *FailoverPolicy) FailoverCooldownTTLBounds {
	var bounds FailoverCooldownTTLBounds
	if policy.CooldownMaxTTL != nil {
		bounds.MaxTTL = *policy.CooldownMaxTTL
	}
	return bounds
}

// NoteSuccess clears the account cooldown and streak on confirmed upstream
// success. Callers should exclude local or cached responses. Model limits have
// independent reset windows and expire on their own in the current store.
func (s *FailoverIsolation) NoteSuccess(ctx context.Context, policy *FailoverPolicy, account *Account) {
	if s.enabled(ctx, policy, account) {
		s.store.Clear(ctx, account.ID)
	}
}

// Clear explicitly resets account-level T2 state and its probe token for an
// operator-initiated recovery. It is independent of request policy flags.
func (s *FailoverIsolation) Clear(ctx context.Context, accountID int64) {
	if s == nil || s.store == nil || ctx == nil || ctx.Err() != nil || accountID <= 0 {
		return
	}
	s.store.Clear(ctx, accountID)
	s.store.ClearProbeToken(ctx, accountID)
}

// FailoverIsolationProbeState is observed account-level recovery state. Pending
// must come from evidence of a previous cooldown whose TTL has elapsed. The
// current Redis store cannot discover this state once the cooldown key expires.
// FailedAttempts belongs to that same recovery cycle, and BaseTTL is the
// cooldown that preceded that cycle before any probe backoff.
type FailoverIsolationProbeState struct {
	Pending        bool
	FailedAttempts int
	BaseTTL        time.Duration
}

type FailoverIsolationProbeResult string

const (
	FailoverIsolationProbeNotRequired FailoverIsolationProbeResult = "not_required"
	FailoverIsolationProbeAcquired    FailoverIsolationProbeResult = "acquired"
	// Unavailable is ambiguous with the current store. Callers must preserve
	// fail-open until the store can distinguish contention from degradation.
	FailoverIsolationProbeUnavailable FailoverIsolationProbeResult = "unavailable"
)

// Probe acquires a token only for a known pending recovery cycle.
func (s *FailoverIsolation) Probe(ctx context.Context, policy *FailoverPolicy, account *Account, state FailoverIsolationProbeState) FailoverIsolationProbeResult {
	if !s.enabled(ctx, policy, account) || !eligibleIsolationProbe(policy, state) {
		return FailoverIsolationProbeNotRequired
	}
	if s.store.AcquireProbeToken(ctx, account.ID) {
		return FailoverIsolationProbeAcquired
	}
	return FailoverIsolationProbeUnavailable
}

// FailoverIsolationProbeFailure is a plan for completing an acquired probe.
// A caller with an owned recovery generation applies this plan atomically to
// that generation. The legacy unversioned store cannot perform that operation.
type FailoverIsolationProbeFailure struct {
	FailedAttempts int
	RetryAfter     time.Duration
	Clear          bool
}

// PlanProbeFailure uses previously observed state and an attributable probe
// failure. It doubles the original cooldown per failed probe and clips it at
// the effective max TTL. Exhausted recovery clears the isolation state.
func PlanProbeFailure(policy *FailoverPolicy, state FailoverIsolationProbeState, result FailoverIsolationProbeResult) (FailoverIsolationProbeFailure, bool) {
	if policy == nil || policy.CooldownEnabled == nil || !*policy.CooldownEnabled ||
		result != FailoverIsolationProbeAcquired || !eligibleIsolationProbe(policy, state) {
		return FailoverIsolationProbeFailure{}, false
	}
	plan := FailoverIsolationProbeFailure{FailedAttempts: state.FailedAttempts + 1}
	if plan.FailedAttempts >= *policy.CooldownProbeMaxAttempts {
		plan.Clear = true
		return plan, true
	}
	capTTL := isolationTTLBounds(policy).MaxTTL
	if capTTL <= 0 {
		return FailoverIsolationProbeFailure{}, false
	}
	plan.RetryAfter = min(state.BaseTTL, capTTL)
	for attempt := 0; attempt < plan.FailedAttempts; attempt++ {
		if plan.RetryAfter > capTTL/2 {
			plan.RetryAfter = capTTL
			break
		}
		plan.RetryAfter *= 2
	}
	return plan, true
}

func eligibleIsolationProbe(policy *FailoverPolicy, state FailoverIsolationProbeState) bool {
	return state.Pending && state.BaseTTL > 0 && state.FailedAttempts >= 0 &&
		policy.CooldownProbeEnabled != nil && *policy.CooldownProbeEnabled &&
		policy.CooldownProbeMaxAttempts != nil && state.FailedAttempts < *policy.CooldownProbeMaxAttempts
}
