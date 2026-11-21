package service

import (
	"strings"
	"time"
)

// PeakPricingSettings 是全局峰谷时段设置（settings.peak_pricing_settings）。
//
// 语义：启用「模型级自定义峰谷价」的模型，在 High 时段使用 peak 价、其余时段
// 使用 off_peak 价。为空（未配置）时回落内置 DeepSeek 官方口径：
// UTC 01:00–04:00 与 06:00–10:00 为高峰，仅工作日生效，周末全天低谷。
type PeakPricingSettings struct {
	// Timezone 是时段判定所用时区；为空按 Asia/Shanghai。
	Timezone string `json:"timezone,omitempty"`
	// WeekdayOnly 为 true 时仅工作日进入高峰（默认 true）。
	WeekdayOnly bool `json:"weekday_only,omitempty"`
	// WeekendOffPeak 为 true 时周末全天低谷（默认 true）。
	WeekendOffPeak bool `json:"weekend_off_peak,omitempty"`
	// Periods 是高峰时段列表，格式 HH:mm-HH:mm，左闭右开，仅支持当日区间。
	Periods []PeakPricingPeriod `json:"periods,omitempty"`
}

// PeakPricingPeriod 是单个高峰时段，格式为 "HH:mm"。
type PeakPricingPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// defaultPeakPricingSettings 是内置 DeepSeek 官方峰谷口径。
func defaultPeakPricingSettings() PeakPricingSettings {
	return PeakPricingSettings{
		Timezone:       "Asia/Shanghai",
		WeekdayOnly:    true,
		WeekendOffPeak: true,
		Periods: []PeakPricingPeriod{
			{Start: "01:00", End: "04:00"},
			{Start: "06:00", End: "10:00"},
		},
	}
}

// Normalize 归一化峰谷设置：补齐默认值、丢弃非法时段。
// 返回值保证可直接用于判定。
func (s PeakPricingSettings) Normalize() PeakPricingSettings {
	out := s
	if strings.TrimSpace(out.Timezone) == "" {
		out.Timezone = "Asia/Shanghai"
	}
	periods := make([]PeakPricingPeriod, 0, len(out.Periods))
	for _, p := range out.Periods {
		start, okStart := parseClockMinutes(p.Start)
		end, okEnd := parseClockMinutes(p.End)
		if !okStart || !okEnd || start == end {
			continue
		}
		periods = append(periods, PeakPricingPeriod{Start: p.Start, End: p.End})
	}
	if len(periods) == 0 {
		// 未配置任何合法时段时回落官方口径，避免「开启峰谷但永远高峰」。
		return defaultPeakPricingSettings()
	}
	out.Periods = periods
	return out
}

// IsPeakAt 报告 at 时刻是否落在高峰时段。
func (s PeakPricingSettings) IsPeakAt(at time.Time) bool {
	if at.IsZero() {
		return false
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	local := at.In(loc)

	if s.WeekendOffPeak {
		switch local.Weekday() {
		case time.Saturday, time.Sunday:
			return false
		}
	}

	minute := local.Hour()*60 + local.Minute()
	for _, p := range s.Periods {
		start, okStart := parseClockMinutes(p.Start)
		end, okEnd := parseClockMinutes(p.End)
		if !okStart || !okEnd {
			continue
		}
		if start < end {
			if minute >= start && minute < end {
				return true
			}
			continue
		}
		// 跨天区间（如 22:00-02:00）按两段处理，便于运营配置夜间高峰。
		if minute >= start || minute < end {
			return true
		}
	}
	return false
}

// parseClockMinutes 把 HH:mm 解析为当日分钟数。支持 24:00 表示当日结束。
func parseClockMinutes(value string) (int, bool) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, false
	}
	hourText, minuteText, ok := strings.Cut(raw, ":")
	if !ok {
		return 0, false
	}
	hour, okHour := parseSmallInt(hourText)
	minute, okMinute := parseSmallInt(minuteText)
	if !okHour || !okMinute {
		return 0, false
	}
	if hour < 0 || hour > 24 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// parseSmallInt 解析最多两位的非负整数。
func parseSmallInt(value string) (int, bool) {
	if value == "" || len(value) > 2 {
		return 0, false
	}
	result := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		result = result*10 + int(r-'0')
	}
	return result, true
}

// PeakAwarePricing 表示模型级峰谷双价配置。
//
// 与 DeepSeek 官方倍率互斥：模型一旦启用自定义峰谷价，计费链路不再叠加
// deepseekPeakMultiplierAt，避免双重放大。
type PeakAwarePricing struct {
	// Enabled 表示该模型启用了自定义峰谷价。
	Enabled bool
	// Peak 是高峰时段价格（per-token USD），字段与 ModelPricing 同义。
	Peak PeakPricingAmounts
	// OffPeak 是低谷时段价格；缺省字段回落基础价。
	OffPeak PeakPricingAmounts
}

// PeakPricingAmounts 是一组时段的 token 价格（per-token USD，0 表示未配置）。
type PeakPricingAmounts struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// IsZero 报告该组价格是否完全未配置。
func (p PeakPricingAmounts) IsZero() bool {
	return p.Input == 0 && p.Output == 0 && p.CacheRead == 0 && p.CacheWrite == 0
}

// ResolvePeakPricing 按 at 时刻与基础价合成最终生效价。
//
// 回落规则（与 design.md §0.4 一致）：
//   - off_peak 留空 → 回落基础价
//   - peak 留空 → 回落 off_peak × 2（保持官方「高峰=低谷×2」语义）
//   - peak 与 off_peak 同时留空 → 不改变基础价，视为未启用
//
// 返回新的 ModelPricing 拷贝，不修改入参。
func ResolvePeakPricing(base *ModelPricing, peak PeakAwarePricing, settings PeakPricingSettings, at time.Time) *ModelPricing {
	if base == nil || !peak.Enabled {
		return base
	}
	offPeak := peak.OffPeak
	if offPeak.IsZero() {
		offPeak = PeakPricingAmounts{
			Input:      base.InputPricePerToken,
			Output:     base.OutputPricePerToken,
			CacheRead:  base.CacheReadPricePerToken,
			CacheWrite: base.CacheCreationPricePerToken,
		}
	}
	peakAmounts := peak.Peak
	if peakAmounts.IsZero() {
		peakAmounts = PeakPricingAmounts{
			Input:      offPeak.Input * 2,
			Output:     offPeak.Output * 2,
			CacheRead:  offPeak.CacheRead * 2,
			CacheWrite: offPeak.CacheWrite * 2,
		}
	}

	selected := offPeak
	if settings.Normalize().IsPeakAt(at) {
		selected = peakAmounts
	}

	cloned := *base
	cloned.InputPricePerToken = selected.Input
	cloned.OutputPricePerToken = selected.Output
	cloned.CacheReadPricePerToken = selected.CacheRead
	cloned.CacheCreationPricePerToken = selected.CacheWrite
	return &cloned
}
