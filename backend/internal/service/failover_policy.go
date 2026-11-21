package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 本文件实现「账号级容灾策略」的三级就近覆盖解析（design.md §3.3.5 / §5，
// implementation.md T0.3）。
//
// 分层与优先级（高 → 低）：
//
//	账号层  accounts.credentials["failover_policy"]      → source=account
//	分组层  groups.failover_on_400 /
//	        groups.failover_max_account_switches /
//	        groups.failover_policy（JSONB）                → source=group
//	全局层  gateway.failover_policy.*                      → source=global
//
// 合并语义是**字段级 merge**（不是对象级覆盖）：上层只写 `cooldown.enabled` 时，
// 其余字段必须回落下一层。因此每一层用指针字段表达「未设置（nil）」，禁止用零值
// 表示未设置——`failover_on_400=false` 本身是有效语义（"分组显式关闭 400 换号"），
// 必须与"未配置"可区分（与 groups.opsmgmt_min_margin_basis_points BIGINT NULL 同构）。
//
// 依赖方向：本文件只 import config（service 禁止 import repository/redis，
// 见 backend/.golangci.yml 的 depguard service-no-repository）；分组行的读取通过
// GroupFailoverPolicyReader 接口注入，实现落在 repository 层。
//
// P0 阶段本文件是**纯地基**：不接任何现有调用点，行为零变更。

// 生效策略来源标记（日志字段 policy_source，design §3.8.1）。
const (
	// FailoverPolicySourceAccount 生效值来自账号凭据覆盖。
	FailoverPolicySourceAccount = "account"
	// FailoverPolicySourceGroup 生效值来自分组级覆盖（列或 JSONB）。
	FailoverPolicySourceGroup = "group"
	// FailoverPolicySourceGlobal 生效值完全来自全局配置（无任何上层覆盖）。
	FailoverPolicySourceGlobal = "global"
)

// FailoverPolicy 是一层 failover 策略覆盖：每个字段都是「可空」的，
// nil = 该层未设置，回落下一层；非 nil = 该层显式覆盖（含显式 false / 0）。
//
// 它同时用于三种角色（这是刻意的，避免三套近似结构互相漂移）：
//   - 分组的 JSONB 覆盖（ParseGroupFailoverPolicyJSON 的产物）
//   - 账号凭据覆盖（ParseAccountFailoverPolicy 的产物，T6 前恒为 nil）
//   - 全局层 + merge 结果（FailoverPolicyFromConfig / ResolveFailoverPolicy 的产物，
//     此时所有字段都非 nil）
type FailoverPolicy struct {
	// Enabled 策略层总开关；false = 走改造前的硬编码判定路径（回滚开关）。
	Enabled *bool
	// StatusClasses 失败分类规则表；整体替换型覆盖（不与下一层按条目合并）。
	StatusClasses *[]config.FailoverStatusClassRuleConfig
	// NetworkErrorsEnabled 网络层错误是否参与分类。
	NetworkErrorsEnabled *bool
	// FailoverOn400 是否允许对「400 + 兼容性关键词白名单」换号。
	FailoverOn400 *bool
	// Compat400Keywords 400 兼容性关键词白名单。
	Compat400Keywords *[]string
	// SameAccountDefaultMode inherit / off。
	SameAccountDefaultMode *string

	// —— 预算层 ——
	BudgetTotalTimeout                *time.Duration
	BudgetPerAccountTimeout           *time.Duration
	BudgetMaxTotalUpstreamAttempts    *int
	BudgetMaxSameAccountAttemptsTotal *int
	BudgetMinSwitchCost               *time.Duration
	BudgetStopClockOnStream           *bool

	// FirstOutputTimeoutSeconds L1 首语义字节超时（秒）；0 = 关闭。
	FirstOutputTimeoutSeconds *int

	// —— T2 冷却层 ——
	CooldownEnabled                      *bool
	CooldownConsecutiveFailuresThreshold *int
	CooldownStreakWindow                 *time.Duration
	CooldownDefaultTTL                   *time.Duration
	CooldownMaxTTL                       *time.Duration
	CooldownPoolModeEnabled              *bool
	CooldownProbeEnabled                 *bool
	CooldownProbeMaxAttempts             *int
	CooldownRedisTimeout                 *time.Duration
	CooldownFailOpen                     *bool
	CooldownBatchReadMax                 *int

	// —— 切换顺序与选号 ——
	SwitchOrderRecordChain                 *bool
	SwitchOrderClearStickyOnAccountFailure *bool
	// MaxSelectionRetries 排除集清空次数上限（活锁护栏）。
	MaxSelectionRetries *int
	// MaxAccountSwitches 分组级切换次数覆盖。**只有分组层会设置**：全局值来自既有
	// 扁平键 gateway.max_account_switches（本结构不承载它，避免出现两个真源）。
	// nil = 仍用 gateway.max_account_switches。
	MaxAccountSwitches *int

	// —— 可切换判据 ——
	SwitchableUseSemanticBytes     *bool
	SwitchableTreatKeepaliveAsSafe *bool

	// —— 幂等审计 ——
	IdempotencyRequireKeyForSideEffecting *bool
	IdempotencyBlockedTools               *[]string

	// T3OnlyExtend T3 只延长不缩短。
	T3OnlyExtend *bool
	// ExposeFailoverDetail 终局错误是否暴露尝试明细。
	ExposeFailoverDetail *bool
	// TerminalMixedStatus503 混合类别终局错误是否统一成 503 + Retry-After（B3）。
	TerminalMixedStatus503 *bool
	// CostAttemptsEnabled 是否写成本尝试账本（P6）。
	CostAttemptsEnabled *bool
}

type resolvedGatewayFailoverPolicyKey struct{}

func WithResolvedGatewayFailoverPolicy(ctx context.Context, snapshot FailoverPolicySnapshot) context.Context {
	return context.WithValue(ctx, resolvedGatewayFailoverPolicyKey{}, snapshot)
}

func ContextWithResolvedGatewayFailoverPolicy(ctx, source context.Context) context.Context {
	if snapshot, ok := source.Value(resolvedGatewayFailoverPolicyKey{}).(FailoverPolicySnapshot); ok {
		return WithResolvedGatewayFailoverPolicy(ctx, snapshot)
	}
	return ctx
}

func ResolvedGatewayFailoverGroupID(ctx context.Context, fallback *int64) *int64 {
	if snapshot, ok := ctx.Value(resolvedGatewayFailoverPolicyKey{}).(FailoverPolicySnapshot); ok && snapshot.GroupID > 0 {
		id := snapshot.GroupID
		return &id
	}
	return fallback
}

func ResolvedGatewayFailoverPolicy(ctx context.Context) (*FailoverPolicy, string) {
	if snapshot, ok := ctx.Value(resolvedGatewayFailoverPolicyKey{}).(FailoverPolicySnapshot); ok {
		policy := snapshot.Policy()
		return &policy, snapshot.Source
	}
	return nil, FailoverPolicySourceGlobal
}

// Legacy 报告调用方是否应当走改造前的硬编码判定路径。
//
// nil 接收者、Enabled 未设置都按 legacy 处理：「不知道新判定是否启用」时选旧路径是
// 唯一的零风险默认（宁可不启用新判定，也不要半启用）。
func (p *FailoverPolicy) Legacy() bool {
	return p == nil || p.Enabled == nil || !*p.Enabled
}

// HasAnyField 报告该层是否给出过任何覆盖（任一指针字段非 nil），用于判定 source。
//
// 用反射而不是手写 36 个判空：字段增多时手写判定极易漏一项，而漏项的后果是
// source 被静默判成 "global"（排障时误导），属于难以发现的错误。
func (p *FailoverPolicy) HasAnyField() bool {
	if p == nil {
		return false
	}
	value := reflect.ValueOf(p).Elem()
	for i := 0; i < value.NumField(); i++ {
		if value.Type().Field(i).Type.Kind() != reflect.Ptr {
			continue
		}
		if !value.Field(i).IsNil() {
			return true
		}
	}
	return false
}

// mergeFailoverPolicy 返回 base 与 override 字段级 merge 的结果（不修改入参）。
// override 中非 nil 的字段胜出，nil 字段回落 base。
func mergeFailoverPolicy(base, override *FailoverPolicy) *FailoverPolicy {
	if override == nil {
		return base
	}
	if base == nil {
		return override
	}
	out := *base
	if v := override.Enabled; v != nil {
		out.Enabled = v
	}
	if v := override.StatusClasses; v != nil {
		out.StatusClasses = v
	}
	if v := override.NetworkErrorsEnabled; v != nil {
		out.NetworkErrorsEnabled = v
	}
	if v := override.FailoverOn400; v != nil {
		out.FailoverOn400 = v
	}
	if v := override.Compat400Keywords; v != nil {
		out.Compat400Keywords = v
	}
	if v := override.SameAccountDefaultMode; v != nil {
		out.SameAccountDefaultMode = v
	}
	if v := override.BudgetTotalTimeout; v != nil {
		out.BudgetTotalTimeout = v
	}
	if v := override.BudgetPerAccountTimeout; v != nil {
		out.BudgetPerAccountTimeout = v
	}
	if v := override.BudgetMaxTotalUpstreamAttempts; v != nil {
		out.BudgetMaxTotalUpstreamAttempts = v
	}
	if v := override.BudgetMaxSameAccountAttemptsTotal; v != nil {
		out.BudgetMaxSameAccountAttemptsTotal = v
	}
	if v := override.BudgetMinSwitchCost; v != nil {
		out.BudgetMinSwitchCost = v
	}
	if v := override.BudgetStopClockOnStream; v != nil {
		out.BudgetStopClockOnStream = v
	}
	if v := override.FirstOutputTimeoutSeconds; v != nil {
		out.FirstOutputTimeoutSeconds = v
	}
	if v := override.CooldownEnabled; v != nil {
		out.CooldownEnabled = v
	}
	if v := override.CooldownConsecutiveFailuresThreshold; v != nil {
		out.CooldownConsecutiveFailuresThreshold = v
	}
	if v := override.CooldownStreakWindow; v != nil {
		out.CooldownStreakWindow = v
	}
	if v := override.CooldownDefaultTTL; v != nil {
		out.CooldownDefaultTTL = v
	}
	if v := override.CooldownMaxTTL; v != nil {
		out.CooldownMaxTTL = v
	}
	if v := override.CooldownPoolModeEnabled; v != nil {
		out.CooldownPoolModeEnabled = v
	}
	if v := override.CooldownProbeEnabled; v != nil {
		out.CooldownProbeEnabled = v
	}
	if v := override.CooldownProbeMaxAttempts; v != nil {
		out.CooldownProbeMaxAttempts = v
	}
	if v := override.CooldownRedisTimeout; v != nil {
		out.CooldownRedisTimeout = v
	}
	if v := override.CooldownFailOpen; v != nil {
		out.CooldownFailOpen = v
	}
	if v := override.CooldownBatchReadMax; v != nil {
		out.CooldownBatchReadMax = v
	}
	if v := override.SwitchOrderRecordChain; v != nil {
		out.SwitchOrderRecordChain = v
	}
	if v := override.SwitchOrderClearStickyOnAccountFailure; v != nil {
		out.SwitchOrderClearStickyOnAccountFailure = v
	}
	if v := override.MaxSelectionRetries; v != nil {
		out.MaxSelectionRetries = v
	}
	if v := override.MaxAccountSwitches; v != nil {
		out.MaxAccountSwitches = v
	}
	if v := override.SwitchableUseSemanticBytes; v != nil {
		out.SwitchableUseSemanticBytes = v
	}
	if v := override.SwitchableTreatKeepaliveAsSafe; v != nil {
		out.SwitchableTreatKeepaliveAsSafe = v
	}
	if v := override.IdempotencyRequireKeyForSideEffecting; v != nil {
		out.IdempotencyRequireKeyForSideEffecting = v
	}
	if v := override.IdempotencyBlockedTools; v != nil {
		out.IdempotencyBlockedTools = v
	}
	if v := override.T3OnlyExtend; v != nil {
		out.T3OnlyExtend = v
	}
	if v := override.ExposeFailoverDetail; v != nil {
		out.ExposeFailoverDetail = v
	}
	if v := override.TerminalMixedStatus503; v != nil {
		out.TerminalMixedStatus503 = v
	}
	if v := override.CostAttemptsEnabled; v != nil {
		out.CostAttemptsEnabled = v
	}
	return &out
}

// FailoverPolicyFromConfig 把全局配置块转成第 3 层（全局层）覆盖：所有字段都非 nil，
// 因此它是 merge 的基座（任何上层没写的字段都能在这里拿到值）。
//
// 语义等价于 design §2.1 L0 的「每请求解析一次并缓存」的输入构造。
func FailoverPolicyFromConfig(cfg config.GatewayFailoverPolicyConfig) FailoverPolicy {
	enabled := cfg.Enabled
	networkErrorsEnabled := cfg.NetworkErrors.Enabled
	failoverOn400 := cfg.FailoverOn400
	sameAccountDefaultMode := cfg.SameAccount.DefaultMode
	firstOutputTimeoutSeconds := cfg.FirstOutputTimeoutSeconds
	exposeFailoverDetail := cfg.ExposeFailoverDetail
	terminalMixedStatus503 := cfg.Terminal.MixedStatus503
	costAttemptsEnabled := cfg.CostAttempts.Enabled

	// 规则表与字符串切片按值复制，避免上层覆盖/调用方修改时串改全局快照。
	var statusClasses *[]config.FailoverStatusClassRuleConfig
	if cfg.StatusClasses != nil {
		classes := make([]config.FailoverStatusClassRuleConfig, len(cfg.StatusClasses))
		copy(classes, cfg.StatusClasses)
		statusClasses = &classes
	}
	var compat400Keywords *[]string
	if cfg.Compat400Keywords != nil {
		keywords := make([]string, len(cfg.Compat400Keywords))
		copy(keywords, cfg.Compat400Keywords)
		compat400Keywords = &keywords
	}
	var blockedTools *[]string
	if cfg.Idempotency.BlockedTools != nil {
		tools := make([]string, len(cfg.Idempotency.BlockedTools))
		copy(tools, cfg.Idempotency.BlockedTools)
		blockedTools = &tools
	}

	budgetTotalTimeout := cfg.Budget.TotalTimeout
	budgetPerAccountTimeout := cfg.Budget.PerAccountTimeout
	budgetMaxTotalUpstreamAttempts := cfg.Budget.MaxTotalUpstreamAttempts
	budgetMaxSameAccountAttemptsTotal := cfg.Budget.MaxSameAccountAttemptsTotal
	budgetMinSwitchCost := cfg.Budget.MinSwitchCost
	budgetStopClockOnStream := cfg.Budget.StopClockOnStream

	cooldownEnabled := cfg.Cooldown.Enabled
	cooldownConsecutiveFailuresThreshold := cfg.Cooldown.ConsecutiveFailuresThreshold
	cooldownStreakWindow := cfg.Cooldown.StreakWindow
	cooldownDefaultTTL := cfg.Cooldown.DefaultTTL
	cooldownMaxTTL := cfg.Cooldown.MaxTTL
	cooldownPoolModeEnabled := cfg.Cooldown.PoolModeEnabled
	cooldownProbeEnabled := cfg.Cooldown.ProbeEnabled
	cooldownProbeMaxAttempts := cfg.Cooldown.ProbeMaxAttempts
	cooldownRedisTimeout := cfg.Cooldown.RedisTimeout
	cooldownFailOpen := cfg.Cooldown.FailOpen
	cooldownBatchReadMax := cfg.Cooldown.BatchReadMax

	switchOrderRecordChain := cfg.SwitchOrder.RecordChain
	switchOrderClearSticky := cfg.SwitchOrder.ClearStickyOnAccountFailure
	maxSelectionRetries := cfg.MaxSelectionRetries
	switchableUseSemanticBytes := cfg.Switchable.UseSemanticBytes
	switchableTreatKeepaliveAsSafe := cfg.Switchable.TreatKeepaliveAsSafe

	idempotencyRequireKey := cfg.Idempotency.RequireKeyForSideEffecting
	t3OnlyExtend := cfg.T3.OnlyExtend

	return FailoverPolicy{
		Enabled:                                &enabled,
		StatusClasses:                          statusClasses,
		NetworkErrorsEnabled:                   &networkErrorsEnabled,
		FailoverOn400:                          &failoverOn400,
		Compat400Keywords:                      compat400Keywords,
		SameAccountDefaultMode:                 &sameAccountDefaultMode,
		BudgetTotalTimeout:                     &budgetTotalTimeout,
		BudgetPerAccountTimeout:                &budgetPerAccountTimeout,
		BudgetMaxTotalUpstreamAttempts:         &budgetMaxTotalUpstreamAttempts,
		BudgetMaxSameAccountAttemptsTotal:      &budgetMaxSameAccountAttemptsTotal,
		BudgetMinSwitchCost:                    &budgetMinSwitchCost,
		BudgetStopClockOnStream:                &budgetStopClockOnStream,
		FirstOutputTimeoutSeconds:              &firstOutputTimeoutSeconds,
		CooldownEnabled:                        &cooldownEnabled,
		CooldownConsecutiveFailuresThreshold:   &cooldownConsecutiveFailuresThreshold,
		CooldownStreakWindow:                   &cooldownStreakWindow,
		CooldownDefaultTTL:                     &cooldownDefaultTTL,
		CooldownMaxTTL:                         &cooldownMaxTTL,
		CooldownPoolModeEnabled:                &cooldownPoolModeEnabled,
		CooldownProbeEnabled:                   &cooldownProbeEnabled,
		CooldownProbeMaxAttempts:               &cooldownProbeMaxAttempts,
		CooldownRedisTimeout:                   &cooldownRedisTimeout,
		CooldownFailOpen:                       &cooldownFailOpen,
		CooldownBatchReadMax:                   &cooldownBatchReadMax,
		SwitchOrderRecordChain:                 &switchOrderRecordChain,
		SwitchOrderClearStickyOnAccountFailure: &switchOrderClearSticky,
		MaxSelectionRetries:                    &maxSelectionRetries,
		SwitchableUseSemanticBytes:             &switchableUseSemanticBytes,
		SwitchableTreatKeepaliveAsSafe:         &switchableTreatKeepaliveAsSafe,
		IdempotencyRequireKeyForSideEffecting:  &idempotencyRequireKey,
		IdempotencyBlockedTools:                blockedTools,
		T3OnlyExtend:                           &t3OnlyExtend,
		ExposeFailoverDetail:                   &exposeFailoverDetail,
		TerminalMixedStatus503:                 &terminalMixedStatus503,
		CostAttemptsEnabled:                    &costAttemptsEnabled,
	}
}

// GroupFailoverPolicyRow 是 groups 表三列的原生读取结果（迁移 254）。
// 三列全部可空：nil = 该层未配置，回落全局层。
type GroupFailoverPolicyRow struct {
	// GroupID 分组 id（排障与日志用）。
	GroupID int64
	// On400 来自 groups.failover_on_400。
	On400 *bool
	// MaxAccountSwitches 来自 groups.failover_max_account_switches。
	MaxAccountSwitches *int
	// Policy 来自 groups.failover_policy（JSONB），解析失败时由读取方决定降级策略。
	Policy *FailoverPolicy
}

// Override 把三列归一成一层覆盖。
//
// 单列优先于 JSONB 同名字段：`failover_on_400` / `failover_max_account_switches` 是
// 高频标量列（运维在 pgAdmin 里直接改一列即生效），JSONB 是低频组合项；同一字段
// 两处都写时以单列为准，避免"改了列却不生效"。
func (r *GroupFailoverPolicyRow) Override() *FailoverPolicy {
	if r == nil {
		return nil
	}
	var out *FailoverPolicy
	if r.Policy != nil {
		clone := *r.Policy
		out = &clone
	} else {
		out = &FailoverPolicy{}
	}
	if r.On400 != nil {
		out.FailoverOn400 = r.On400
	}
	if r.MaxAccountSwitches != nil {
		out.MaxAccountSwitches = r.MaxAccountSwitches
	}
	return out
}

// ResolveFailoverPolicy 按「账号 > 分组 > 全局」解析出生效策略与来源标记。
//
// 纯函数（无 I/O）：分组行必须由调用方先经 GroupFailoverPolicyReader 取好再传入，
// 账号层由 ParseAccountFailoverPolicy 从凭据解析（T6 前恒为 nil）。返回值中的
// source ∈ {account, group, global}，表示**最高一层真正给出过覆盖**的层级，
// 用于日志 policy_source 与排障（design §3.8.1）。
func ResolveFailoverPolicy(account *FailoverPolicy, group *GroupFailoverPolicyRow, global FailoverPolicy) (FailoverPolicy, string) {
	groupOverride := group.Override()

	base := global
	effective := mergeFailoverPolicy(&base, groupOverride)
	effective = mergeFailoverPolicy(effective, account)
	if effective == nil {
		// 正常不会发生：全局层字段恒非 nil。保底返回零值策略而不是 panic。
		return FailoverPolicy{}, FailoverPolicySourceGlobal
	}

	source := FailoverPolicySourceGlobal
	switch {
	case account.HasAnyField():
		source = FailoverPolicySourceAccount
	case groupOverride.HasAnyField():
		source = FailoverPolicySourceGroup
	}
	return *effective, source
}

// GroupFailoverPolicyReader 读取分组级 failover 覆盖（groups 三列）。
//
// 接口定义在 service、实现落在 repository：service 禁止 import repository/redis
// （backend/.golangci.yml 的 depguard service-no-repository），先例见
// ConcurrencyCache / SessionLimitCache。
type GroupFailoverPolicyReader interface {
	// LoadGroupFailoverPolicy 读取一个分组的覆盖行。
	// 分组不存在时返回 (nil, nil)（不是错误）——调用方据此走全局层。
	LoadGroupFailoverPolicy(ctx context.Context, groupID int64) (*GroupFailoverPolicyRow, error)
}

// ParseAccountFailoverPolicy 从账号凭据（accounts.credentials["failover_policy"]）
// 解析账号级覆盖，即三层里的最高优先级层。
//
// T6 之前按 design §5.4 的建议**不实现**：账号级覆盖先不开，恒返回 nil 表示该层
// 未覆盖，解析结果自然回落到分组/全局。签名先固定，后续开启账号级覆盖时调用方
// 无需改动。
func ParseAccountFailoverPolicy(_ map[string]any) *FailoverPolicy {
	return nil
}

// ---------------------------------------------------------------------------
// 分组 JSONB 的反序列化
// ---------------------------------------------------------------------------

// failoverPolicyJSON 是 groups.failover_policy（JSONB）的反序列化形状。
// 键名与 gateway.failover_policy.* 逐项一致（少一层前缀），时长用 "60s"/"500ms"
// 这类 Go duration 字符串表达，便于运维直接在 pgAdmin 里写 JSON。
//
// 解析使用 DisallowUnknownFields：写错键名要**立刻报错**而不是被静默忽略——
// 静默忽略会让"我配了却不生效"变成排障黑洞（risks.md R-49 配置漂移）。
type failoverPolicyJSON struct {
	Enabled                   *bool                      `json:"enabled"`
	StatusClasses             []failoverStatusClassJSON  `json:"status_classes"`
	NetworkErrors             *failoverNetworkErrorsJSON `json:"network_errors"`
	FailoverOn400             *bool                      `json:"failover_on_400"`
	Compat400Keywords         *[]string                  `json:"compat_400_keywords"`
	SameAccount               *failoverSameAccountJSON   `json:"same_account"`
	Budget                    *failoverBudgetJSON        `json:"budget"`
	FirstOutputTimeoutSeconds *int                       `json:"first_output_timeout_seconds"`
	Cooldown                  *failoverCooldownJSON      `json:"cooldown"`
	SwitchOrder               *failoverSwitchOrderJSON   `json:"switch_order"`
	MaxSelectionRetries       *int                       `json:"max_selection_retries"`
	MaxAccountSwitches        *int                       `json:"max_account_switches"`
	Switchable                *failoverSwitchableJSON    `json:"switchable"`
	Idempotency               *failoverIdempotencyJSON   `json:"idempotency"`
	T3                        *failoverT3JSON            `json:"t3"`
	ExposeFailoverDetail      *bool                      `json:"expose_failover_detail"`
	Terminal                  *failoverTerminalJSON      `json:"terminal"`
	CostAttempts              *failoverCostAttemptsJSON  `json:"cost_attempts"`
}

type failoverStatusClassJSON struct {
	Class  string              `json:"class"`
	Codes  []int               `json:"codes"`
	Ranges []string            `json:"ranges"`
	Advice *failoverAdviceJSON `json:"advice"`
}

type failoverAdviceJSON struct {
	Failover       *bool   `json:"failover"`
	SameAccount    *string `json:"same_account"`
	Cooldown       *string `json:"cooldown"`
	CooldownSource *string `json:"cooldown_source"`
	TempUnschedule *bool   `json:"temp_unschedule"`
	ClearSticky    *bool   `json:"clear_sticky"`
	Scope          *string `json:"scope"`
}

type failoverNetworkErrorsJSON struct {
	Enabled *bool `json:"enabled"`
}

type failoverSameAccountJSON struct {
	DefaultMode *string `json:"default_mode"`
}

type failoverBudgetJSON struct {
	TotalTimeout                *string `json:"total_timeout"`
	PerAccountTimeout           *string `json:"per_account_timeout"`
	MaxTotalUpstreamAttempts    *int    `json:"max_total_upstream_attempts"`
	MaxSameAccountAttemptsTotal *int    `json:"max_same_account_attempts_total"`
	MinSwitchCost               *string `json:"min_switch_cost"`
	StopClockOnStream           *bool   `json:"stop_clock_on_stream"`
}

type failoverCooldownJSON struct {
	Enabled                      *bool   `json:"enabled"`
	ConsecutiveFailuresThreshold *int    `json:"consecutive_failures_threshold"`
	StreakWindow                 *string `json:"streak_window"`
	DefaultTTL                   *string `json:"default_ttl"`
	MaxTTL                       *string `json:"max_ttl"`
	PoolModeEnabled              *bool   `json:"pool_mode_enabled"`
	ProbeEnabled                 *bool   `json:"probe_enabled"`
	ProbeMaxAttempts             *int    `json:"probe_max_attempts"`
	RedisTimeout                 *string `json:"redis_timeout"`
	FailOpen                     *bool   `json:"fail_open"`
	BatchReadMax                 *int    `json:"batch_read_max"`
}

type failoverSwitchOrderJSON struct {
	RecordChain                 *bool `json:"record_chain"`
	ClearStickyOnAccountFailure *bool `json:"clear_sticky_on_account_failure"`
}

type failoverSwitchableJSON struct {
	UseSemanticBytes     *bool `json:"use_semantic_bytes"`
	TreatKeepaliveAsSafe *bool `json:"treat_keepalive_as_safe"`
}

type failoverIdempotencyJSON struct {
	RequireKeyForSideEffecting *bool     `json:"require_key_for_side_effecting"`
	BlockedTools               *[]string `json:"blocked_tools"`
}

type failoverT3JSON struct {
	OnlyExtend *bool `json:"only_extend"`
}

type failoverTerminalJSON struct {
	MixedStatus503 *bool `json:"mixed_status_503"`
}

type failoverCostAttemptsJSON struct {
	Enabled *bool `json:"enabled"`
}

// ParseGroupFailoverPolicyJSON 解析 groups.failover_policy 的 JSONB 值。
//
// 空值（NULL / 空字节）返回 (nil, nil)：该列未配置是最常见情况，不是错误。
// 键名写错、时长写错、类型不匹配一律返回 error —— 由调用方（repository 的读取路径
// 或 handler 的入口）决定是 fail-open 到全局还是报错，解析层不替它决定。
func ParseGroupFailoverPolicyJSON(raw []byte) (*FailoverPolicy, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var payload failoverPolicyJSON
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("parse group failover policy json: %w", err)
	}

	policy := &FailoverPolicy{
		Enabled:                   payload.Enabled,
		FailoverOn400:             payload.FailoverOn400,
		Compat400Keywords:         payload.Compat400Keywords,
		FirstOutputTimeoutSeconds: payload.FirstOutputTimeoutSeconds,
		MaxSelectionRetries:       payload.MaxSelectionRetries,
		MaxAccountSwitches:        payload.MaxAccountSwitches,
		ExposeFailoverDetail:      payload.ExposeFailoverDetail,
	}
	if payload.NetworkErrors != nil {
		policy.NetworkErrorsEnabled = payload.NetworkErrors.Enabled
	}
	if payload.SameAccount != nil {
		policy.SameAccountDefaultMode = payload.SameAccount.DefaultMode
	}
	if payload.StatusClasses != nil {
		classes := make([]config.FailoverStatusClassRuleConfig, 0, len(payload.StatusClasses))
		for index, rule := range payload.StatusClasses {
			converted, err := rule.toConfig(index)
			if err != nil {
				return nil, err
			}
			classes = append(classes, converted)
		}
		policy.StatusClasses = &classes
	}
	if payload.Budget != nil {
		budget := payload.Budget
		policy.BudgetMaxTotalUpstreamAttempts = budget.MaxTotalUpstreamAttempts
		policy.BudgetMaxSameAccountAttemptsTotal = budget.MaxSameAccountAttemptsTotal
		policy.BudgetStopClockOnStream = budget.StopClockOnStream
		total, err := parseFailoverDurationField("budget.total_timeout", budget.TotalTimeout)
		if err != nil {
			return nil, err
		}
		policy.BudgetTotalTimeout = total
		perAccount, err := parseFailoverDurationField("budget.per_account_timeout", budget.PerAccountTimeout)
		if err != nil {
			return nil, err
		}
		policy.BudgetPerAccountTimeout = perAccount
		minSwitchCost, err := parseFailoverDurationField("budget.min_switch_cost", budget.MinSwitchCost)
		if err != nil {
			return nil, err
		}
		policy.BudgetMinSwitchCost = minSwitchCost
	}
	if payload.Cooldown != nil {
		cooldown := payload.Cooldown
		policy.CooldownEnabled = cooldown.Enabled
		policy.CooldownConsecutiveFailuresThreshold = cooldown.ConsecutiveFailuresThreshold
		policy.CooldownPoolModeEnabled = cooldown.PoolModeEnabled
		policy.CooldownProbeEnabled = cooldown.ProbeEnabled
		policy.CooldownProbeMaxAttempts = cooldown.ProbeMaxAttempts
		policy.CooldownFailOpen = cooldown.FailOpen
		policy.CooldownBatchReadMax = cooldown.BatchReadMax
		streakWindow, err := parseFailoverDurationField("cooldown.streak_window", cooldown.StreakWindow)
		if err != nil {
			return nil, err
		}
		policy.CooldownStreakWindow = streakWindow
		defaultTTL, err := parseFailoverDurationField("cooldown.default_ttl", cooldown.DefaultTTL)
		if err != nil {
			return nil, err
		}
		policy.CooldownDefaultTTL = defaultTTL
		maxTTL, err := parseFailoverDurationField("cooldown.max_ttl", cooldown.MaxTTL)
		if err != nil {
			return nil, err
		}
		policy.CooldownMaxTTL = maxTTL
		redisTimeout, err := parseFailoverDurationField("cooldown.redis_timeout", cooldown.RedisTimeout)
		if err != nil {
			return nil, err
		}
		policy.CooldownRedisTimeout = redisTimeout
	}
	if payload.SwitchOrder != nil {
		policy.SwitchOrderRecordChain = payload.SwitchOrder.RecordChain
		policy.SwitchOrderClearStickyOnAccountFailure = payload.SwitchOrder.ClearStickyOnAccountFailure
	}
	if payload.Switchable != nil {
		policy.SwitchableUseSemanticBytes = payload.Switchable.UseSemanticBytes
		policy.SwitchableTreatKeepaliveAsSafe = payload.Switchable.TreatKeepaliveAsSafe
	}
	if payload.Idempotency != nil {
		policy.IdempotencyRequireKeyForSideEffecting = payload.Idempotency.RequireKeyForSideEffecting
		policy.IdempotencyBlockedTools = payload.Idempotency.BlockedTools
	}
	if payload.T3 != nil {
		policy.T3OnlyExtend = payload.T3.OnlyExtend
	}
	if payload.Terminal != nil {
		policy.TerminalMixedStatus503 = payload.Terminal.MixedStatus503
	}
	if payload.CostAttempts != nil {
		policy.CostAttemptsEnabled = payload.CostAttempts.Enabled
	}
	return policy, nil
}

// toConfig 把 JSON 形状的规则转成配置层的规则类型（FailoverPolicy 使用 config 的
// 规则表类型，避免出现两份几乎相同的规则结构）。
func (r failoverStatusClassJSON) toConfig(index int) (config.FailoverStatusClassRuleConfig, error) {
	out := config.FailoverStatusClassRuleConfig{
		Class:  r.Class,
		Codes:  r.Codes,
		Ranges: r.Ranges,
	}
	if r.Advice == nil {
		return out, nil
	}
	advice := &config.FailoverRetryAdviceConfig{
		Failover:       r.Advice.Failover,
		SameAccount:    r.Advice.SameAccount,
		CooldownSource: r.Advice.CooldownSource,
		TempUnschedule: r.Advice.TempUnschedule,
		ClearSticky:    r.Advice.ClearSticky,
		Scope:          r.Advice.Scope,
	}
	cooldown, err := parseFailoverDurationField(
		fmt.Sprintf("status_classes[%d].advice.cooldown", index), r.Advice.Cooldown)
	if err != nil {
		return config.FailoverStatusClassRuleConfig{}, err
	}
	advice.Cooldown = cooldown
	out.Advice = advice
	return out, nil
}

// parseFailoverDurationField 解析 JSON 里的 duration 字符串（"60s"/"500ms"/"0"）。
// nil 输入返回 nil（= 该层未设置），把「未设置」与「显式 0」分开。
func parseFailoverDurationField(name string, raw *string) (*time.Duration, error) {
	if raw == nil {
		return nil, nil
	}
	text := strings.TrimSpace(*raw)
	if text == "" {
		parsed := time.Duration(0)
		return &parsed, nil
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return nil, fmt.Errorf("parse group failover policy %s: %w", name, err)
	}
	return &parsed, nil
}
