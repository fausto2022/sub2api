package service

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
)

// 运营看板聚合（design.md §2.4）。
//
// 数据源是本系统 usage_logs（不依赖上游报表，也不需要上游管理员权限）：
//
//	今日收入 = Σ usage_logs.actual_cost      （用户实付，含分组倍率折扣）
//	今日成本 = Σ usage_logs.total_cost       （按上游价计算的成本）
//	今日利润 = 收入 − 成本
//	保底收入 = 收入 ÷ 保底比例（guaranteed_revenue_ratio_bp，默认 10000 = 不打折）
//
// 数据不可用时返回 Available=false 而不是 0，避免运营者把「查不到」误读成「没赚钱」。
const (
	// OpsDashboardRangeToday / OpsDashboardRange7D 看板时间范围。
	OpsDashboardRangeToday = "today"
	OpsDashboardRange7D    = "7d"
)

// OpsGroupProfitRow 分组盈利榜的一行。
type OpsGroupProfitRow struct {
	GroupID           int64   `json:"group_id"`
	GroupName         string  `json:"group_name"`
	Revenue           float64 `json:"revenue"`
	Cost              float64 `json:"cost"`
	Profit            float64 `json:"profit"`
	MarginBasisPoints int64   `json:"margin_basis_points"`
	Requests          int64   `json:"requests"`
}

// OpsProfitSummary 运营看板数据（对齐 design §2.4.1 的 ProfitSummary）。
type OpsProfitSummary struct {
	Available bool   `json:"available"`
	Range     string `json:"range"`

	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	// 今日
	TodayRevenue           float64 `json:"today_revenue"`
	TodayGuaranteedRevenue float64 `json:"today_guaranteed_revenue"`
	TodayCost              float64 `json:"today_cost"`
	TodayProfit            float64 `json:"today_profit"`
	TodayMarginBasisPoints int64   `json:"today_margin_basis_points"`

	// 近 7 日
	SevenDayRevenue           float64 `json:"seven_day_revenue"`
	SevenDayGuaranteedRevenue float64 `json:"seven_day_guaranteed_revenue"`
	SevenDayCost              float64 `json:"seven_day_cost"`
	SevenDayProfit            float64 `json:"seven_day_profit"`
	SevenDayMarginBasisPoints int64   `json:"seven_day_margin_basis_points"`

	SampledDays int  `json:"sampled_days"`
	Complete    bool `json:"complete"`

	// GuaranteedRevenueRatioBP 是「保底收入 / 实际收入」的实际基点（design 公式），
	// GuaranteedRevenueConfigBP 是配置值，两者在收入为 0 时相等。
	GuaranteedRevenueRatioBP  int64 `json:"guaranteed_revenue_ratio_bp"`
	GuaranteedRevenueConfigBP int64 `json:"guaranteed_revenue_config_bp"`

	LastSampledAt     *time.Time `json:"last_sampled_at,omitempty"`
	UnavailableReason string     `json:"unavailable_reason,omitempty"`
	GeneratedAt       time.Time  `json:"generated_at"`

	TopGroups []OpsGroupProfitRow `json:"top_groups"`
}

// OpsDashboardService 运营看板聚合服务。
type OpsDashboardService struct {
	usageRepo OpsUsageStatsRepository
	groupRepo OpsLocalGroupRepository
	settings  *opsSettingsCache
	now       func() time.Time
	location  *time.Location
}

// NewOpsDashboardService 构造看板聚合服务。
func NewOpsDashboardService(
	usageRepo OpsUsageStatsRepository,
	groupRepo OpsLocalGroupRepository,
	settings *opsSettingsCache,
) *OpsDashboardService {
	return &OpsDashboardService{
		usageRepo: usageRepo,
		groupRepo: groupRepo,
		settings:  settings,
		now:       time.Now,
		location:  time.Local,
	}
}

// SetClock 注入时钟（测试用）。loc 为 nil 时保持当前时区。
func (s *OpsDashboardService) SetClock(now func() time.Time, loc *time.Location) {
	if s == nil || now == nil {
		return
	}
	s.now = now
	if loc != nil {
		s.location = loc
	}
}

// ProfitSummary 聚合今日 / 近 7 日收入-成本-利润-利润率与分组盈利榜。
//
// 聚合失败时返回 Available=false + UnavailableReason，不返回 0 值误导调用方。
func (s *OpsDashboardService) ProfitSummary(ctx context.Context, rangeKey string) (*OpsProfitSummary, error) {
	if s == nil || s.usageRepo == nil {
		return &OpsProfitSummary{Available: false, UnavailableReason: "usage stats repository is not configured"}, nil
	}
	normalizedRange := strings.ToLower(strings.TrimSpace(rangeKey))
	switch normalizedRange {
	case "", OpsDashboardRangeToday:
		normalizedRange = OpsDashboardRangeToday
	case OpsDashboardRange7D, "week", "7days":
		normalizedRange = OpsDashboardRange7D
	}

	now := s.now()
	loc := s.location
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	todayEnd := todayStart.AddDate(0, 0, 1)
	sevenDayStart := todayStart.AddDate(0, 0, -6)

	settings, err := s.settings.Get(ctx)
	if err != nil {
		// 配置读失败不阻断看板：回落默认值（保底 100%）。
		settings = DefaultOpsMgmtSettings()
	}
	summary := &OpsProfitSummary{
		Available:                 true,
		Range:                     normalizedRange,
		From:                      todayStart,
		To:                        todayEnd,
		GuaranteedRevenueConfigBP: settings.GuaranteedRevenueRatioBP,
		GuaranteedRevenueRatioBP:  settings.GuaranteedRevenueRatioBP,
		GeneratedAt:               now,
	}
	if normalizedRange == OpsDashboardRange7D {
		summary.From = sevenDayStart
	}

	todayTotals, err := s.usageRepo.SumUsageTotals(ctx, todayStart, todayEnd, nil)
	if err != nil {
		summary.Available = false
		summary.UnavailableReason = "usage logs unavailable: " + err.Error()
		return summary, nil
	}
	sevenDayTotals, err := s.usageRepo.SumUsageTotals(ctx, sevenDayStart, todayEnd, nil)
	if err != nil {
		summary.Available = false
		summary.UnavailableReason = "usage logs unavailable: " + err.Error()
		return summary, nil
	}

	ratioBP := settings.GuaranteedRevenueRatioBP
	summary.TodayRevenue = roundOpsMoney(todayTotals.Revenue)
	summary.TodayCost = roundOpsMoney(todayTotals.Cost)
	summary.TodayProfit = roundOpsMoney(todayTotals.Revenue - todayTotals.Cost)
	summary.TodayMarginBasisPoints = opsMarginBasisPointsMoney(todayTotals.Revenue, todayTotals.Cost)
	summary.TodayGuaranteedRevenue = opsGuaranteedRevenue(todayTotals.Revenue, ratioBP)

	summary.SevenDayRevenue = roundOpsMoney(sevenDayTotals.Revenue)
	summary.SevenDayCost = roundOpsMoney(sevenDayTotals.Cost)
	summary.SevenDayProfit = roundOpsMoney(sevenDayTotals.Revenue - sevenDayTotals.Cost)
	summary.SevenDayMarginBasisPoints = opsMarginBasisPointsMoney(sevenDayTotals.Revenue, sevenDayTotals.Cost)
	summary.SevenDayGuaranteedRevenue = opsGuaranteedRevenue(sevenDayTotals.Revenue, ratioBP)

	// 实际保底占比（基点）：收入为 0 时等于配置值，避免除零与误报。
	focusRevenue := todayTotals.Revenue
	focusGuaranteed := summary.TodayGuaranteedRevenue
	if normalizedRange == OpsDashboardRange7D {
		focusRevenue = sevenDayTotals.Revenue
		focusGuaranteed = summary.SevenDayGuaranteedRevenue
	}
	if focusRevenue > 0 {
		summary.GuaranteedRevenueRatioBP = int64(math.Round(focusGuaranteed / focusRevenue * 10_000))
	}

	days, err := s.usageRepo.CountUsageDays(ctx, sevenDayStart, todayEnd)
	if err != nil {
		summary.Available = false
		summary.UnavailableReason = "usage logs unavailable: " + err.Error()
		return summary, nil
	}
	summary.SampledDays = days
	summary.Complete = days >= 7
	summary.LastSampledAt = todayTotals.LastAt

	// 分组盈利榜（收入降序，最多 20 条）。失败不影响主指标可用性，只留空榜。
	if s.groupRepo != nil {
		if rows, rowsErr := s.usageRepo.SumUsageByGroup(ctx, sevenDayStart, todayEnd); rowsErr == nil {
			summary.TopGroups = s.buildTopGroups(ctx, rows)
		}
	}
	return summary, nil
}

func (s *OpsDashboardService) buildTopGroups(ctx context.Context, rows []OpsUsageGroupTotals) []OpsGroupProfitRow {
	if len(rows) == 0 {
		return []OpsGroupProfitRow{}
	}
	ids := make([]int64, 0, len(rows))
	byID := make(map[int64]OpsUsageGroupTotals, len(rows))
	for _, row := range rows {
		ids = append(ids, row.GroupID)
		byID[row.GroupID] = row
	}
	names := make(map[int64]string, len(ids))
	if groups, err := s.groupRepo.ListLocalGroups(ctx, ids); err == nil {
		for _, group := range groups {
			names[group.ID] = group.Name
		}
	}
	out := make([]OpsGroupProfitRow, 0, len(rows))
	for _, row := range rows {
		name := names[row.GroupID]
		if strings.TrimSpace(name) == "" {
			name = "group-" + opsInt64ToString(row.GroupID)
		}
		out = append(out, OpsGroupProfitRow{
			GroupID:           row.GroupID,
			GroupName:         name,
			Revenue:           roundOpsMoney(row.Revenue),
			Cost:              roundOpsMoney(row.Cost),
			Profit:            roundOpsMoney(row.Revenue - row.Cost),
			MarginBasisPoints: opsMarginBasisPointsMoney(row.Revenue, row.Cost),
			Requests:          row.Requests,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Revenue == out[j].Revenue {
			return out[i].GroupID < out[j].GroupID
		}
		return out[i].Revenue > out[j].Revenue
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// opsGuaranteedRevenue 保底收入 = 收入 ÷ (配置保底比例/10000)。
func opsGuaranteedRevenue(revenue float64, ratioBP int64) float64 {
	if revenue <= 0 {
		return 0
	}
	if ratioBP <= 0 {
		ratioBP = opsDefaultGuaranteedRevenueRatioBP
	}
	return roundOpsMoney(revenue / (float64(ratioBP) / 10_000))
}

// opsMarginBasisPointsMoney 金额口径利润率（分母 revenue，design §2.5.2）。
func opsMarginBasisPointsMoney(revenue, cost float64) int64 {
	if revenue <= 0 {
		return 0
	}
	return int64(math.Round((revenue - cost) / revenue * 10_000))
}

func roundOpsMoney(value float64) float64 {
	return math.Round(value*1e8) / 1e8
}

func opsInt64ToString(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}
