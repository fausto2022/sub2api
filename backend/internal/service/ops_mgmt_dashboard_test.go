package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 运营看板聚合单测（design.md §2.4 / 第四部分 C1）。
//
// 数据源必须是本系统 usage_logs（收入=actual_cost，成本=total_cost），
// 不依赖上游报表；保证收入 = 收入 ÷ 保底比例（guaranteed_revenue_ratio_bp，默认 10000）。
// 数据不可用时返回 available=false，而不是把「查不到」伪装成 0。

func newOpsDashboardForTest(t *testing.T, repo *fakeOpsRepo, now time.Time, loc *time.Location) *OpsDashboardService {
	t.Helper()
	settings := newOpsSettingsCache(repo)
	svc := NewOpsDashboardService(repo, repo, settings)
	svc.SetClock(func() time.Time { return now }, loc)
	return svc
}

func TestOpsDashboardProfitSummary(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	now := time.Date(2026, 3, 10, 14, 30, 0, 0, loc)
	repo := newFakeOpsRepo()

	todayStart := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	sevenDayStart := todayStart.AddDate(0, 0, -6)
	todayEnd := todayStart.AddDate(0, 0, 1)

	repo.usageByFrom[todayStart] = OpsUsageTotals{Revenue: 100, Cost: 60, Requests: 8, LastAt: &now}
	repo.usageByFrom[sevenDayStart] = OpsUsageTotals{Revenue: 500, Cost: 300, Requests: 42}
	repo.usageDays = 7
	repo.seedLocalGroup(5, "claude-pro", 1.0)
	repo.seedLocalGroup(9, "gpt-pro", 1.0)
	repo.usageGroup = []OpsUsageGroupTotals{
		{GroupID: 5, Revenue: 300, Cost: 180, Requests: 20},
		{GroupID: 9, Revenue: 120, Cost: 90, Requests: 5},
	}

	svc := newOpsDashboardForTest(t, repo, now, loc)
	summary, err := svc.ProfitSummary(context.Background(), "today")
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.True(t, summary.Available)
	require.Equal(t, OpsDashboardRangeToday, summary.Range)

	// 今日：收入 100 - 成本 60 = 利润 40，利润率 4000bps。
	require.InDelta(t, 100.0, summary.TodayRevenue, 1e-9)
	require.InDelta(t, 60.0, summary.TodayCost, 1e-9)
	require.InDelta(t, 40.0, summary.TodayProfit, 1e-9)
	require.Equal(t, int64(4000), summary.TodayMarginBasisPoints)

	// 近 7 日：500 - 300 = 200，利润率 4000bps。
	require.InDelta(t, 500.0, summary.SevenDayRevenue, 1e-9)
	require.InDelta(t, 300.0, summary.SevenDayCost, 1e-9)
	require.InDelta(t, 200.0, summary.SevenDayProfit, 1e-9)
	require.Equal(t, int64(4000), summary.SevenDayMarginBasisPoints)

	// 保证收入：默认保底比例 10000（不打折）→ 与收入相等。
	require.InDelta(t, 100.0, summary.TodayGuaranteedRevenue, 1e-9)
	require.InDelta(t, 500.0, summary.SevenDayGuaranteedRevenue, 1e-9)
	require.Equal(t, int64(10000), summary.GuaranteedRevenueRatioBP)
	require.Equal(t, int64(10000), summary.GuaranteedRevenueConfigBP)

	// 采样天数与完整性。
	require.Equal(t, 7, summary.SampledDays)
	require.True(t, summary.Complete)
	require.NotNil(t, summary.LastSampledAt)

	// 分组盈利榜：按收入降序，且带分组名。
	require.Len(t, summary.TopGroups, 2)
	require.Equal(t, int64(5), summary.TopGroups[0].GroupID)
	require.Equal(t, "claude-pro", summary.TopGroups[0].GroupName)
	require.InDelta(t, 300.0, summary.TopGroups[0].Revenue, 1e-9)
	require.InDelta(t, 120.0, summary.TopGroups[0].Profit, 1e-9)
	require.Equal(t, int64(4000), summary.TopGroups[0].MarginBasisPoints)
	require.Equal(t, int64(9), summary.TopGroups[1].GroupID)

	// 看板必须按「今日」与「近 7 日」两个窗口各查一次。
	ranges := repo.recordedUsageRanges()
	require.Len(t, ranges, 2, "today 与 7d 必须分别聚合")
	require.True(t, ranges[0][0].Equal(todayStart))
	require.True(t, ranges[0][1].Equal(todayEnd))
	require.True(t, ranges[1][0].Equal(sevenDayStart))
	require.True(t, ranges[1][1].Equal(todayEnd))

	// range=7d 时 From 指向 7 日窗口起点。
	seven, err := svc.ProfitSummary(context.Background(), "7d")
	require.NoError(t, err)
	require.Equal(t, OpsDashboardRange7D, seven.Range)
	require.True(t, seven.From.Equal(sevenDayStart))
}

func TestOpsDashboardGuaranteedRevenueUsesConfiguredRatio(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, loc)
	repo := newFakeOpsRepo()
	todayStart := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	repo.usageByFrom[todayStart] = OpsUsageTotals{Revenue: 100, Cost: 60, Requests: 3}
	repo.usageDays = 2
	repo.setSetting(OpsSettingGuaranteedRevenueRatioBP, "9000")

	svc := newOpsDashboardForTest(t, repo, now, loc)
	summary, err := svc.ProfitSummary(context.Background(), "today")
	require.NoError(t, err)
	require.True(t, summary.Available)

	// 保底收入 = 收入 ÷ 0.9 = 111.11111111。
	require.InDelta(t, 111.11111111, summary.TodayGuaranteedRevenue, 1e-6)
	require.Equal(t, int64(9000), summary.GuaranteedRevenueConfigBP)
	// 实际保底占比（基点）= 111.11/100 × 10000 ≈ 11111。
	require.Equal(t, int64(11111), summary.GuaranteedRevenueRatioBP)
	require.False(t, summary.Complete, "只有 2 天数据 → 不完整")
	require.Equal(t, 2, summary.SampledDays)

	// 零收入时保底收入归零，占比回落配置值（不出现除零/NaN）。
	emptyRepo := newFakeOpsRepo()
	emptyRepo.setSetting(OpsSettingGuaranteedRevenueRatioBP, "9000")
	emptySvc := newOpsDashboardForTest(t, emptyRepo, now, loc)
	empty, err := emptySvc.ProfitSummary(context.Background(), "today")
	require.NoError(t, err)
	require.True(t, empty.Available)
	require.InDelta(t, 0.0, empty.TodayGuaranteedRevenue, 1e-9)
	require.Equal(t, int64(9000), empty.GuaranteedRevenueRatioBP)
}

func TestOpsDashboardUnavailableWhenUsageFails(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, loc)
	repo := newFakeOpsRepo()
	repo.usageErr = errors.New("database is unavailable")

	svc := newOpsDashboardForTest(t, repo, now, loc)
	summary, err := svc.ProfitSummary(context.Background(), "today")
	require.NoError(t, err, "数据不可用不应把 handler 打成 500")
	require.NotNil(t, summary)
	require.False(t, summary.Available, "数据不可用必须 available=false，而不是返回 0")
	require.Contains(t, summary.UnavailableReason, "usage logs unavailable")
	require.InDelta(t, 0.0, summary.TodayRevenue, 1e-9)

	// 未配置 usage 仓储时同样 available=false。
	bare := NewOpsDashboardService(nil, nil, nil)
	summary, err = bare.ProfitSummary(context.Background(), "today")
	require.NoError(t, err)
	require.False(t, summary.Available)
	require.NotEmpty(t, summary.UnavailableReason)
}

func TestOpsDashboardSingleDayWindowDoesNotFabricateSevenDay(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, loc)
	repo := newFakeOpsRepo()
	todayStart := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	repo.usageByFrom[todayStart] = OpsUsageTotals{Revenue: 20, Cost: 25, Requests: 2}
	repo.usageDays = 1

	svc := newOpsDashboardForTest(t, repo, now, loc)
	summary, err := svc.ProfitSummary(context.Background(), "today")
	require.NoError(t, err)
	require.Equal(t, int64(-2500), summary.TodayMarginBasisPoints, "亏损时利润率为负")
	require.InDelta(t, -5.0, summary.TodayProfit, 1e-9)
	require.False(t, summary.Complete)
}
