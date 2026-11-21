package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 运营管理利润引擎单测：对齐 design.md 第四部分 B2–B6。
//
//   B2 倍率 0.95→1.20 触发 status=risk
//   B3 连续 risk 达 risk_confirmations 才停用（防抖）
//   B4 成本不可靠（过期）时不动作
//   B5 AutoRecovery 生效后恢复账号（本地 Channel）
//   B6 灰度模式（auto_margin_protection=false）只记录不停用

type opsTestClock struct {
	at time.Time
}

func (c *opsTestClock) now() time.Time { return c.at }

func newOpsTestService(t *testing.T, repo *fakeOpsRepo, at time.Time) (*OpsManagementService, *opsTestClock) {
	t.Helper()
	clock := &opsTestClock{at: at}
	svc := NewOpsManagementService(repo, fakeOpsEncryptor{}, NewOpsUpstreamClient(nil), nil)
	svc.SetClock(clock.now)
	return svc, clock
}

// enableProtection 打开自动保护并把确认次数调小，便于断言。
func enableProtection(t *testing.T, repo *fakeOpsRepo, confirmations int) {
	t.Helper()
	repo.setSetting(OpsSettingAutoMarginProtection, "true")
	repo.setSetting(OpsSettingAutoRecovery, "true")
	repo.setSetting(OpsSettingRiskConfirmations, fmt.Sprintf("%d", confirmations))
	repo.setSetting(OpsSettingMinimumMarginBasisPoints, "1000")
	repo.setSetting(OpsSettingCostMaxAgeMinutes, "60")
	repo.setSetting(OpsSettingProfitWindowHours, "24")
}

func disableProtection(t *testing.T, repo *fakeOpsRepo) {
	t.Helper()
	repo.setSetting(OpsSettingAutoMarginProtection, "false")
	repo.setSetting(OpsSettingAutoRecovery, "true")
	repo.setSetting(OpsSettingRiskConfirmations, "2")
	repo.setSetting(OpsSettingMinimumMarginBasisPoints, "1000")
	repo.setSetting(OpsSettingCostMaxAgeMinutes, "60")
}

func TestOpsMarginPolicyDefaultsAndParsing(t *testing.T) {
	t.Parallel()

	defaults := DefaultOpsMgmtSettings()
	policy := defaults.marginPolicy()
	require.Equal(t, "observe", policy.Mode, "灰度开关默认关闭 → observe")
	require.Equal(t, int64(1000), policy.MinimumMarginBasisPoints, "默认最低利润率 10%")
	require.Equal(t, 2, policy.RiskConfirmations, "默认连续 2 次确认")
	require.Equal(t, 60, policy.CostMaxAgeMinutes, "默认成本时效 60 分钟")
	require.True(t, defaults.AutoRecovery, "AutoRecovery 默认开启")
	require.False(t, defaults.AutoMarginProtection, "AutoMarginProtection 默认关闭（灰度）")
	require.Equal(t, int64(10000), defaults.GuaranteedRevenueRatioBP, "保底比例默认 100%（不打折）")

	parsed := opsParseSettings(map[string]string{
		OpsSettingAutoMarginProtection:     "true",
		OpsSettingMinimumMarginBasisPoints: "1500",
		OpsSettingRiskConfirmations:        "3",
		OpsSettingCostMaxAgeMinutes:        "30",
		OpsSettingGuaranteedRevenueRatioBP: "9000",
	})
	require.True(t, parsed.AutoMarginProtection)
	require.Equal(t, int64(1500), parsed.MinimumMarginBasisPoints)
	require.Equal(t, 3, parsed.RiskConfirmations)
	require.Equal(t, 30, parsed.CostMaxAgeMinutes)
	require.Equal(t, int64(9000), parsed.GuaranteedRevenueRatioBP)
	require.Equal(t, "protect", parsed.marginPolicy().Mode)

	// 越界值回落默认，避免坏配置把保护关死。
	clamped := opsParseSettings(map[string]string{
		OpsSettingMinimumMarginBasisPoints: "-5",
		OpsSettingRiskConfirmations:        "0",
		OpsSettingCostMaxAgeMinutes:        "0",
		OpsSettingGuaranteedRevenueRatioBP: "0",
	})
	require.Equal(t, int64(opsDefaultMinimumMarginBasisPoints), clamped.MinimumMarginBasisPoints)
	require.Equal(t, opsDefaultRiskConfirmations, clamped.RiskConfirmations)
	require.Equal(t, opsDefaultCostMaxAgeMinutes, clamped.CostMaxAgeMinutes)
	require.Equal(t, opsDefaultGuaranteedRevenueRatioBP, clamped.GuaranteedRevenueRatioBP)

	// 配置校验：越界直接报错（不允许写入把保护关死的值）。
	invalid := DefaultOpsMgmtSettings()
	invalid.MinimumMarginBasisPoints = 10000
	require.ErrorIs(t, invalid.validate(), ErrOpsInvalidMargin)
}

// B2：上游倍率 0.95 → 1.20 触发 status=risk。
func TestOpsBuildProfitCheckRateChangeTriggersRisk(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-5 * time.Minute)
	policy := marginPolicy{MinimumMarginBasisPoints: 300, RiskConfirmations: 2, CostMaxAgeMinutes: 60}

	healthy := buildProfitCheck(opsProfitCheckInput{
		SiteID:        1,
		RemoteGroupID: 100,
		GroupName:     "claude-pro",
		ChannelRef:    opsChannelRef(7),
		SaleMicros:    opsToMicros(1.0),
		Cost:          opsResolveCostMultiplier(&OpsUpstreamGroup{RemoteGroupID: 100, RateMultiplier: 0.95, SyncedAt: syncedAt}, policy, now),
		Policy:        policy,
		Now:           now,
	})
	require.Equal(t, OpsProfitStatusHealthy, healthy.Status, "0.95 成本对 1.0 售价：利润率 500bps ≥ 300bps")
	require.Equal(t, int64(500), healthy.MarginBasisPoints)
	require.True(t, healthy.CostReliable)
	require.Equal(t, "upstream_group_snapshot", healthy.CostSource)

	risky := buildProfitCheck(opsProfitCheckInput{
		SiteID:        1,
		RemoteGroupID: 100,
		GroupName:     "claude-pro",
		ChannelRef:    opsChannelRef(7),
		SaleMicros:    opsToMicros(1.0),
		Cost:          opsResolveCostMultiplier(&OpsUpstreamGroup{RemoteGroupID: 100, RateMultiplier: 1.20, SyncedAt: syncedAt}, policy, now),
		Policy:        policy,
		Now:           now,
	})
	require.Equal(t, OpsProfitStatusRisk, risky.Status, "成本涨到 1.20 超过售价 1.0：必须 risk")
	require.Equal(t, int64(-2000), risky.MarginBasisPoints)
	require.Contains(t, risky.CostReason, "does not exceed")
}

func TestOpsBuildProfitCheckUnknownWhenCostUnreliable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := marginPolicy{MinimumMarginBasisPoints: 1000, RiskConfirmations: 2, CostMaxAgeMinutes: 60}

	// 快照过期：synced 2 小时前 > 60 分钟时效。
	stale := opsResolveCostMultiplier(&OpsUpstreamGroup{
		RemoteGroupID:  100,
		RateMultiplier: 1.5,
		SyncedAt:       now.Add(-2 * time.Hour),
	}, policy, now)
	require.False(t, stale.Reliable)
	require.Contains(t, stale.Reason, "expired")

	check := buildProfitCheck(opsProfitCheckInput{
		SaleMicros: opsToMicros(1.0),
		Cost:       stale,
		Policy:     policy,
		Now:        now,
	})
	require.Equal(t, OpsProfitStatusUnknown, check.Status, "成本不可靠时不得判定 risk")
	require.False(t, check.CostReliable)
	require.Contains(t, check.CostReason, "expired")

	// 缺失倍率同样是 unknown。
	missing := buildProfitCheck(opsProfitCheckInput{
		SaleMicros: opsToMicros(1.0),
		Cost:       resolvedCost{Reason: "no usable upstream cost multiplier"},
		Policy:     policy,
		Now:        now,
	})
	require.Equal(t, OpsProfitStatusUnknown, missing.Status)
	require.Equal(t, "no usable upstream cost multiplier", missing.CostReason)
}

func TestOpsBuildProfitCheckUnsupportedPricing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := marginPolicy{MinimumMarginBasisPoints: 1000, RiskConfirmations: 2, CostMaxAgeMinutes: 60}

	imageGroup := &OpsUpstreamGroup{RemoteGroupID: 100, RateMultiplier: 1.5, SyncedAt: now, ImageSeparateRate: true}
	unsupported, reason := opsUnsupportedPricing(imageGroup)
	require.True(t, unsupported)
	require.Contains(t, reason, "image")

	check := buildProfitCheck(opsProfitCheckInput{
		SaleMicros:        opsToMicros(1.0),
		Cost:              opsResolveCostMultiplier(imageGroup, policy, now),
		Policy:            policy,
		Unsupported:       unsupported,
		UnsupportedReason: reason,
		Now:               now,
	})
	require.Equal(t, OpsProfitStatusUnsupported, check.Status, "图片独立计费不参与自动保护")
	require.Contains(t, check.CostReason, "image")

	subscriptionGroup := &OpsUpstreamGroup{RemoteGroupID: 101, RateMultiplier: 0.5, SyncedAt: now, SubscriptionType: "subscription"}
	unsupported, reason = opsUnsupportedPricing(subscriptionGroup)
	require.True(t, unsupported)
	require.Contains(t, reason, "subscription")

	standardGroup := &OpsUpstreamGroup{RemoteGroupID: 102, RateMultiplier: 0.5, SyncedAt: now, SubscriptionType: "standard"}
	unsupported, _ = opsUnsupportedPricing(standardGroup)
	require.False(t, unsupported)
}

func TestOpsProfitBasisPointsAndPeakWindow(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(2000), profitBasisPoints(opsToMicros(1.0), opsToMicros(0.8)))
	require.Equal(t, int64(-2500), profitBasisPoints(opsToMicros(0.8), opsToMicros(1.0)))
	require.Equal(t, int64(0), profitBasisPoints(0, opsToMicros(0.8)))

	// 峰时窗口：普通窗口与跨零点窗口。
	base := time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)
	active, err := opsPeakWindowActive(base, "22:00", "02:00")
	require.NoError(t, err)
	require.True(t, active, "23:30 落在跨零点窗口 22:00–02:00 内")

	active, err = opsPeakWindowActive(base.Add(3*time.Hour), "22:00", "02:00")
	require.NoError(t, err)
	require.False(t, active, "次日 02:30 已出窗口")

	active, err = opsPeakWindowActive(base, "08:00", "20:00")
	require.NoError(t, err)
	require.False(t, active)

	_, err = opsPeakWindowActive(base, "8点", "20:00")
	require.Error(t, err, "非法时间必须报错（评估侧据此判 unreliable）")

	// 峰时倍率作用在成本侧：1.0 × 1.5 = 1.5。
	now := time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)
	policy := marginPolicy{MinimumMarginBasisPoints: 1000, RiskConfirmations: 2, CostMaxAgeMinutes: 60}
	cost := opsResolveCostMultiplier(&OpsUpstreamGroup{
		RemoteGroupID:  100,
		RateMultiplier: 1.0,
		PeakEnabled:    true,
		PeakStart:      "22:00",
		PeakEnd:        "02:00",
		PeakMultiplier: 1.5,
		SyncedAt:       now,
	}, policy, now)
	require.True(t, cost.Reliable)
	require.Equal(t, opsToMicros(1.5), cost.Micros)
	require.Contains(t, cost.Source, "peak")
}

func TestOpsEffectiveMinMarginPriority(t *testing.T) {
	t.Parallel()

	upstream := int64(1500)
	local := int64(1200)

	value, source := resolveEffectiveMinMargin(&upstream, &local, 1000)
	require.Equal(t, int64(1500), value)
	require.Equal(t, "upstream_group", source, "上游分组配置优先级最高")

	value, source = resolveEffectiveMinMargin(nil, &local, 1000)
	require.Equal(t, int64(1200), value)
	require.Equal(t, "local_group", source)

	value, source = resolveEffectiveMinMargin(nil, nil, 1000)
	require.Equal(t, int64(1000), value)
	require.Equal(t, "global", source)
}

// B3：连续 risk 达 risk_confirmations 次才停用；GuardLock 幂等。
func TestOpsEvaluateDebouncesRiskBeforeDisabling(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	repo := newFakeOpsRepo()
	enableProtection(t, repo, 2)
	site := repo.seedSite("up-a", "https://up-a.example.com")
	repo.seedUpstreamGroup(site.ID, 100, 1.20, now.Add(-2*time.Minute))
	repo.seedLocalGroup(5, "claude-pro", 1.0)
	repo.seedChannel(7, site.ID, 100, 5)

	svc, _ := newOpsTestService(t, repo, now)

	first, err := svc.EvaluateSite(ctx, site.ID, "manual")
	require.NoError(t, err)
	require.Equal(t, 1, first.Risk)
	require.Equal(t, []string{opsChannelRef(7)}, first.WouldDisable)
	require.Empty(t, first.ProtectionApplied, "只确认 1 次 risk，不得动作（防抖）")
	require.Equal(t, 0, repo.activeLockCount())
	link, err := repo.GetChannelLink(ctx, 7)
	require.NoError(t, err)
	require.Nil(t, link.AutoDisabledAt)

	second, err := svc.EvaluateSite(ctx, site.ID, "manual")
	require.NoError(t, err)
	require.Equal(t, []string{opsChannelRef(7)}, second.ProtectionApplied, "连续 2 次 risk 才停用")
	require.Equal(t, 1, repo.activeLockCount())
	link, err = repo.GetChannelLink(ctx, 7)
	require.NoError(t, err)
	require.NotNil(t, link.AutoDisabledAt)
	require.Equal(t, OpsAutoDisabledReasonMargin, link.AutoDisabledReason)
	require.Equal(t, "disabled", link.Status)
	require.Equal(t, "margin", repo.lastDisabledWhy)

	// 第三次：锁幂等，不产生重复锁，也不重复置停用。
	third, err := svc.EvaluateSite(ctx, site.ID, "manual")
	require.NoError(t, err)
	require.Empty(t, third.ProtectionApplied)
	require.Equal(t, 1, repo.activeLockCount(), "重复 Activate 不产生重复锁")
	require.Equal(t, 1, repo.disabledCalls, "已停用的 Channel 不重复写停用时间")

	// 幂等性直接验证：再次 Activate 返回既有锁且 created=false。
	lock, created, err := svc.ActivateGuardLock(ctx, site.ID, opsChannelRef(7), OpsGuardLockTypeMargin, "manual", nil)
	require.NoError(t, err)
	require.False(t, created)
	require.NotNil(t, lock)
	require.Equal(t, 1, repo.activeLockCount())

	// 审计历史留痕。
	checks, total, err := svc.ListProfitChecks(ctx, OpsProfitCheckFilter{SiteID: site.ID})
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, checks, 3)
}

// B4：成本不可靠（过期）时不动作——即使配置了自动保护且连续多次评估。
func TestOpsEvaluateDoesNotActWhenCostUnreliable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	repo := newFakeOpsRepo()
	enableProtection(t, repo, 1)
	site := repo.seedSite("up-b", "https://up-b.example.com")
	// 快照 2 小时前同步，超过 60 分钟时效 → Reliable=false。
	repo.seedUpstreamGroup(site.ID, 200, 1.30, now.Add(-2*time.Hour))
	repo.seedLocalGroup(6, "gpt-pro", 1.0)
	repo.seedChannel(8, site.ID, 200, 6)

	svc, _ := newOpsTestService(t, repo, now)

	for i := 0; i < 3; i++ {
		result, err := svc.EvaluateSite(ctx, site.ID, "manual")
		require.NoError(t, err)
		require.Equal(t, 0, result.Risk, "成本不可靠时不能判 risk")
		require.Equal(t, 1, result.Unknown)
		require.Empty(t, result.WouldDisable)
		require.Empty(t, result.ProtectionApplied)
	}
	require.Equal(t, 0, repo.activeLockCount(), "成本不可靠时不得加锁")
	link, err := repo.GetChannelLink(ctx, 8)
	require.NoError(t, err)
	require.Nil(t, link.AutoDisabledAt, "成本不可靠时不得停用")
}

// B5：AutoRecovery 生效后（连续 healthy 达确认次数）解锁并恢复 Channel。
func TestOpsAutoRecoveryRestoresChannel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	repo := newFakeOpsRepo()
	enableProtection(t, repo, 2)
	site := repo.seedSite("up-c", "https://up-c.example.com")
	repo.seedUpstreamGroup(site.ID, 300, 1.20, now.Add(-time.Minute))
	repo.seedLocalGroup(9, "mixed", 1.0)
	repo.seedChannel(11, site.ID, 300, 9)

	svc, _ := newOpsTestService(t, repo, now)

	// 先连续两次 risk → 停用。
	for i := 0; i < 2; i++ {
		_, err := svc.EvaluateSite(ctx, site.ID, "manual")
		require.NoError(t, err)
	}
	link, err := repo.GetChannelLink(ctx, 11)
	require.NoError(t, err)
	require.NotNil(t, link.AutoDisabledAt)
	require.Equal(t, 1, repo.activeLockCount())

	// 上游倍率回落到 0.8（利润率 2000bps > 1000bps）→ 第一次 healthy 还不够（防抖）。
	repo.mu.Lock()
	repo.groups[site.ID][0].RateMultiplier = 0.8
	repo.groups[site.ID][0].SyncedAt = now
	repo.mu.Unlock()

	first, err := svc.EvaluateSite(ctx, site.ID, "manual")
	require.NoError(t, err)
	require.Equal(t, 1, first.Healthy)
	require.Empty(t, first.Recovered, "只恢复 1 次 healthy，不得解锁（防抖）")
	link, err = repo.GetChannelLink(ctx, 11)
	require.NoError(t, err)
	require.NotNil(t, link.AutoDisabledAt)

	second, err := svc.EvaluateSite(ctx, site.ID, "manual")
	require.NoError(t, err)
	require.Equal(t, []string{opsChannelRef(11)}, second.Recovered, "连续 2 次 healthy → 自动恢复")
	require.Equal(t, 0, repo.activeLockCount(), "恢复时清锁")
	link, err = repo.GetChannelLink(ctx, 11)
	require.NoError(t, err)
	require.Nil(t, link.AutoDisabledAt)
	require.Equal(t, "active", link.Status)
}

// B6：灰度模式（auto_margin_protection=false）只记录 wouldDisable，不真停用。
func TestOpsGrayModeRecordsWithoutDisabling(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	repo := newFakeOpsRepo()
	disableProtection(t, repo)
	site := repo.seedSite("up-d", "https://up-d.example.com")
	repo.seedUpstreamGroup(site.ID, 400, 1.25, now.Add(-time.Minute))
	repo.seedLocalGroup(12, "claude-pro-max", 1.0)
	repo.seedChannel(21, site.ID, 400, 12)

	svc, _ := newOpsTestService(t, repo, now)

	warnings := make([]string, 0, 8)
	for i := 0; i < 3; i++ {
		result, err := svc.EvaluateSite(ctx, site.ID, "manual")
		require.NoError(t, err)
		require.True(t, result.GrayMode)
		require.Equal(t, []string{opsChannelRef(21)}, result.WouldDisable, "灰度模式仍需记录 wouldDisable")
		require.Empty(t, result.ProtectionApplied)
		warnings = append(warnings, result.Warnings...)
	}
	require.NotEmpty(t, resultWarningsContaining(warnings, "gray mode"), "灰度模式必须告警")
	require.Equal(t, 0, repo.activeLockCount())
	link, err := repo.GetChannelLink(ctx, 21)
	require.NoError(t, err)
	require.Nil(t, link.AutoDisabledAt, "灰度模式不得真停用")

	// 打开开关后（同一份历史，再评估一次即满足确认次数）才真正停用。
	repo.setSetting(OpsSettingAutoMarginProtection, "true")
	svc.settings.Invalidate()
	enabled, err := svc.EvaluateSite(ctx, site.ID, "manual")
	require.NoError(t, err)
	require.False(t, enabled.GrayMode)
	require.Equal(t, []string{opsChannelRef(21)}, enabled.ProtectionApplied)
	require.Equal(t, 1, repo.activeLockCount())
}

func resultWarningsContaining(warnings []string, needle string) []string {
	matched := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if strings.Contains(strings.ToLower(warning), strings.ToLower(needle)) {
			matched = append(matched, warning)
		}
	}
	return matched
}

// clockHint 供辅助函数取一个足够新的时钟（与 seed 数据同一时间基准）。
func (r *fakeOpsRepo) clockHint() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, groups := range r.groups {
		for _, group := range groups {
			return group.SyncedAt.Add(time.Minute)
		}
	}
	return time.Now()
}
