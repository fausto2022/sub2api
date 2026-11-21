package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 利润评估引擎：移植 relaydeck 的 marginPolicy / resolvedCost / buildProfitCheck 语义
// （design.md §2.3 与 §2.5.2），数据源换成本系统的「上游分组倍率快照 + usage_logs」。
//
// 与 relaydeck 的对应关系：
//   - sale multiplier：本地分组 rate_multiplier（我们卖给用户的价格倍率）；
//   - cost multiplier：上游分组 rate_multiplier（上游卖给我们，峰时乘 peak_multiplier）；
//   - margin = (sale − cost) / sale × 10000（design §2.5.2 的 revenue 分母口径）；
//   - resolvedCost 带 Source/Observed/ExpiresAt/Reliable/Reason，不可靠时不动作。
const opsMicrosScale int64 = 1_000_000

// resolvedCost 成本倍率的信任模型（对齐 relaydeck）。
type resolvedCost struct {
	Micros    int64
	Source    string
	Observed  time.Time
	ExpiresAt *time.Time
	Reliable  bool
	Reason    string
}

// opsProfitCheckInput buildProfitCheck 的输入。
type opsProfitCheckInput struct {
	SiteID        int64
	RemoteGroupID int64
	GroupName     string
	ChannelRef    string
	// SaleMicros 本地分组卖给用户的倍率（1e6 = 1.0x）；<=0 表示缺失。
	SaleMicros int64
	// Cost 上游成本倍率（含可靠性）。
	Cost resolvedCost
	// Policy 利润率策略（阈值/确认次数/成本时效）。
	Policy marginPolicy
	// RevenueMicros / CostMicros 是 usage_logs 的窗口金额聚合（可全 0，仅用于展示）。
	RevenueMicros int64
	CostMicros    int64
	// Unsupported 标记上游分组定价非线性（图片/视频独立计费、订阅制等），不参与自动保护。
	Unsupported bool
	// UnsupportedReason 具体原因（写入 cost_reason）。
	UnsupportedReason string
	Now               time.Time
}

// buildProfitCheck 构造一次利润检查（对齐 relaydeck buildProfitCheck）。
//
// 状态语义：
//   - unknown：成本来源缺失/过期/不可靠、售价缺失（不动作）
//   - unsupported：上游分组定价非线性（不动作）
//   - risk：margin < policy.MinimumMarginBasisPoints
//   - healthy：margin ≥ 阈值
func buildProfitCheck(input opsProfitCheckInput) OpsProfitCheck {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	observed := input.Cost.Observed
	if observed.IsZero() {
		observed = now
	}
	check := OpsProfitCheck{
		SiteID:               input.SiteID,
		RemoteGroupID:        input.RemoteGroupID,
		GroupName:            input.GroupName,
		ChannelRef:           input.ChannelRef,
		SaleMultiplierMicros: input.SaleMicros,
		CostMultiplierMicros: input.Cost.Micros,
		RevenueMicros:        input.RevenueMicros,
		CostMicros:           input.CostMicros,
		CostSource:           input.Cost.Source,
		CostObservedAt:       &observed,
		CostReliable:         input.Cost.Reliable,
		CreatedAt:            now,
	}

	if input.Unsupported {
		check.Status = OpsProfitStatusUnsupported
		check.CostReason = strings.TrimSpace(input.UnsupportedReason)
		if check.CostReason == "" {
			check.CostReason = "upstream group uses non-linear pricing (image/video separate rate or subscription)"
		}
		return check
	}
	if input.SaleMicros <= 0 {
		check.Status = OpsProfitStatusUnknown
		check.CostReason = "local sale multiplier is missing"
		return check
	}
	if input.Cost.Micros <= 0 {
		check.Status = OpsProfitStatusUnknown
		check.CostReason = firstNonEmptyOpsString(input.Cost.Reason, "no usable upstream cost multiplier")
		return check
	}
	if !input.Cost.Reliable {
		check.Status = OpsProfitStatusUnknown
		check.CostReason = firstNonEmptyOpsString(input.Cost.Reason, "upstream cost multiplier is unreliable")
		return check
	}
	if input.Cost.ExpiresAt != nil && !now.Before(*input.Cost.ExpiresAt) {
		check.Status = OpsProfitStatusUnknown
		check.CostReason = "upstream cost multiplier snapshot is expired"
		return check
	}

	check.MarginBasisPoints = profitBasisPoints(input.SaleMicros, input.Cost.Micros)
	if input.SaleMicros <= input.Cost.Micros {
		check.Status = OpsProfitStatusRisk
		check.CostReason = fmt.Sprintf("sale multiplier %.4f does not exceed cost multiplier %.4f",
			opsFromMicros(input.SaleMicros), opsFromMicros(input.Cost.Micros))
		return check
	}
	if check.MarginBasisPoints < input.Policy.MinimumMarginBasisPoints {
		check.Status = OpsProfitStatusRisk
		check.CostReason = fmt.Sprintf("margin %d bps is below threshold %d bps",
			check.MarginBasisPoints, input.Policy.MinimumMarginBasisPoints)
		return check
	}
	check.Status = OpsProfitStatusHealthy
	return check
}

// profitBasisPoints 利润率（基点），分母为售价倍率（design §2.5.2）。
func profitBasisPoints(saleMicros, costMicros int64) int64 {
	if saleMicros <= 0 {
		return 0
	}
	return opsFixedMul(saleMicros-costMicros, 10_000, saleMicros)
}

// opsFixedMul 定点乘法：left × right / divisor，防止 1e6 级倍率相乘时溢出。
func opsFixedMul(left, right, divisor int64) int64 {
	if divisor == 0 {
		return 0
	}
	product := left * right
	if left != 0 && product/left != right {
		// 溢出保护：退化为浮点近似（倍率量级远小于 float64 精度上限）。
		return int64(float64(left) * float64(right) / float64(divisor))
	}
	return product / divisor
}

// opsResolveCostMultiplier 解析上游成本倍率（对齐 relaydeck resolveSourceRateCost）。
//
// 成本来源只有一个：上游分组倍率快照（/groups/available + /groups/rates）。
// 快照超过 policy.CostMaxAgeMinutes 即视为过期 → Reliable=false，评估侧不动作。
func opsResolveCostMultiplier(group *OpsUpstreamGroup, policy marginPolicy, now time.Time) resolvedCost {
	if group == nil {
		return resolvedCost{Reason: "upstream group snapshot is missing"}
	}
	maxAge := time.Duration(policy.CostMaxAgeMinutes) * time.Minute
	if maxAge <= 0 {
		maxAge = time.Duration(opsDefaultCostMaxAgeMinutes) * time.Minute
	}
	syncedAt := group.SyncedAt
	if syncedAt.IsZero() {
		return resolvedCost{
			Micros:   opsToMicros(group.RateMultiplier),
			Source:   "upstream_group_snapshot",
			Observed: now,
			Reliable: false,
			Reason:   "upstream group snapshot has no sync timestamp",
		}
	}
	expiresAt := syncedAt.Add(maxAge)
	cost := resolvedCost{
		Micros:    opsToMicros(group.RateMultiplier),
		Source:    "upstream_group_snapshot",
		Observed:  syncedAt,
		ExpiresAt: &expiresAt,
	}
	if cost.Micros <= 0 {
		cost.Reliable = false
		cost.Reason = "upstream group rate_multiplier is missing"
		return cost
	}
	if !now.Before(expiresAt) {
		cost.Reliable = false
		cost.Reason = fmt.Sprintf("upstream group snapshot expired (synced_at=%s, ttl=%dm)",
			syncedAt.UTC().Format(time.RFC3339), policy.CostMaxAgeMinutes)
		return cost
	}
	// 峰时倍率作用在成本侧（上游涨价）。
	if group.PeakEnabled {
		active, err := opsPeakWindowActive(now, group.PeakStart, group.PeakEnd)
		if err != nil {
			cost.Reliable = false
			cost.Reason = "upstream peak window is invalid: " + err.Error()
			return cost
		}
		if active {
			peak := group.PeakMultiplier
			if peak <= 0 {
				cost.Reliable = false
				cost.Reason = "upstream peak multiplier is missing"
				return cost
			}
			cost.Micros = opsFixedMul(cost.Micros, opsToMicros(peak), opsMicrosScale)
			cost.Source += "+peak"
		}
	}
	cost.Reliable = true
	return cost
}

// opsUnsupportedPricing 非线性定价（图片/视频独立计费、订阅/专属）不参与自动保护，
// 避免拿单一倍率算出错误的利润率（对齐 relaydeck unsupportedPricing）。
func opsUnsupportedPricing(group *OpsUpstreamGroup) (bool, string) {
	if group == nil {
		return false, ""
	}
	if group.ImageSeparateRate {
		return true, "upstream group bills image separately"
	}
	if group.VideoSeparateRate {
		return true, "upstream group bills video separately"
	}
	typeName := strings.ToLower(strings.TrimSpace(group.SubscriptionType))
	switch typeName {
	case "", "standard", "usage", "token", "payg":
		return false, ""
	default:
		return true, "upstream group subscription type is " + typeName
	}
}

// marginRiskConfirmed 连续 risk 达 risk_confirmations 次才确认（防抖）。
//
// checks 必须是「按时间倒序」的最近检查（最新在前）；要求最近 N 条全部为 risk。
func marginRiskConfirmed(checks []OpsProfitCheck, policy marginPolicy) bool {
	required := policy.RiskConfirmations
	if required <= 0 {
		required = opsDefaultRiskConfirmations
	}
	if len(checks) < required {
		return false
	}
	for i := 0; i < required; i++ {
		if checks[i].Status != OpsProfitStatusRisk {
			return false
		}
	}
	return true
}

// marginRecoveryConfirmed 恢复确认：最近 N 条全部 healthy 才解锁（防抖动）。
func marginRecoveryConfirmed(checks []OpsProfitCheck, policy marginPolicy) bool {
	required := policy.RiskConfirmations
	if required <= 0 {
		required = opsDefaultRiskConfirmations
	}
	if len(checks) < required {
		return false
	}
	for i := 0; i < required; i++ {
		if checks[i].Status != OpsProfitStatusHealthy {
			return false
		}
	}
	return true
}

// opsPeakWindowActive 判断当前是否落在峰时窗口内（支持跨零点，如 22:00–02:00）。
func opsPeakWindowActive(now time.Time, startRaw, endRaw string) (bool, error) {
	startMinutes, err := opsParseClockMinutes(startRaw)
	if err != nil {
		return false, fmt.Errorf("peak start: %w", err)
	}
	endMinutes, err := opsParseClockMinutes(endRaw)
	if err != nil {
		return false, fmt.Errorf("peak end: %w", err)
	}
	if startMinutes == endMinutes {
		return false, nil
	}
	current := now.Hour()*60 + now.Minute()
	if startMinutes < endMinutes {
		return current >= startMinutes && current < endMinutes, nil
	}
	// 跨零点窗口
	return current >= startMinutes || current < endMinutes, nil
}

func opsParseClockMinutes(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("empty time")
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("time %q must be HH:MM", value)
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || hour < 0 || hour > 23 {
		return 0, fmt.Errorf("time %q has invalid hour", value)
	}
	minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("time %q has invalid minute", value)
	}
	return hour*60 + minute, nil
}

// opsToMicros 倍率 → micros（1e6 = 1.0x），四舍五入。
func opsToMicros(value float64) int64 {
	if value <= 0 {
		return 0
	}
	if value > 1e6 {
		return opsMicrosScale * 1_000_000
	}
	return int64(value*float64(opsMicrosScale) + 0.5)
}

// opsFromMicros micros → 倍率。
func opsFromMicros(micros int64) float64 {
	return float64(micros) / float64(opsMicrosScale)
}

// opsMicrosFromFloat 金额 → micros（四舍五入，负值归零）。
func opsMicrosFromFloat(value float64) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value*float64(opsMicrosScale) + 0.5)
}

func firstNonEmptyOpsString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
