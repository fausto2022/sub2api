package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mustLoadLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	return loc
}

// TestPeakSettingsNormalizeFallsBackToOfficialDefaults 覆盖：未配置/非法时段时
// 回落官方口径，避免「开启峰谷但永远高峰」。
func TestPeakSettingsNormalizeFallsBackToOfficialDefaults(t *testing.T) {
	got := PeakPricingSettings{}.Normalize()
	require.Equal(t, "Asia/Shanghai", got.Timezone)
	require.Len(t, got.Periods, 2)

	invalid := PeakPricingSettings{Periods: []PeakPricingPeriod{{Start: "bad", End: "also-bad"}}}.Normalize()
	require.Len(t, invalid.Periods, 2, "非法时段应整体回落官方口径")

	same := PeakPricingSettings{Periods: []PeakPricingPeriod{{Start: "10:00", End: "10:00"}}}.Normalize()
	require.Len(t, same.Periods, 2, "起止相同的空区间应被丢弃并回落")
}

// TestPeakSettingsIsPeakAtWeekendOffPeak 覆盖：周末全天低谷。
func TestPeakSettingsIsPeakAtWeekendOffPeak(t *testing.T) {
	loc := mustLoadLoc(t, "Asia/Shanghai")
	settings := defaultPeakPricingSettings()

	// 2026-11-14 是周六，UTC 02:00 = 北京时间 10:00，落在官方高峰时段也应判为低谷。
	saturday := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	require.False(t, settings.IsPeakAt(saturday))
	require.Equal(t, time.Saturday, saturday.In(loc).Weekday())
}

// TestPeakSettingsIsPeakAtInsideAndOutsideWindow 覆盖：窗口内为高峰、窗口外为低谷。
func TestPeakSettingsIsPeakAtInsideAndOutsideWindow(t *testing.T) {
	settings := PeakPricingSettings{
		Timezone:       "UTC",
		WeekendOffPeak: true,
		Periods:        []PeakPricingPeriod{{Start: "01:00", End: "04:00"}},
	}
	// 2026-11-16 是周一。
	inside := time.Date(2026, 11, 16, 2, 30, 0, 0, time.UTC)
	require.True(t, settings.IsPeakAt(inside))

	atStart := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	require.True(t, settings.IsPeakAt(atStart), "左闭：起点算高峰")

	atEnd := time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC)
	require.False(t, settings.IsPeakAt(atEnd), "右开：终点不算高峰")
}

// TestPeakSettingsSupportsCrossMidnightWindow 覆盖：跨天区间（如 22:00-02:00）。
func TestPeakSettingsSupportsCrossMidnightWindow(t *testing.T) {
	settings := PeakPricingSettings{
		Timezone:       "UTC",
		WeekendOffPeak: false,
		Periods:        []PeakPricingPeriod{{Start: "22:00", End: "02:00"}},
	}
	require.True(t, settings.IsPeakAt(time.Date(2026, 11, 16, 23, 0, 0, 0, time.UTC)))
	require.True(t, settings.IsPeakAt(time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)))
	require.False(t, settings.IsPeakAt(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)))
}

// TestResolvePeakPricingSelectsPeakAndOffPeak 覆盖：峰时取 peak、谷时取 off_peak。
func TestResolvePeakPricingSelectsPeakAndOffPeak(t *testing.T) {
	base := &ModelPricing{InputPricePerToken: 3e-7, OutputPricePerToken: 1.2e-6}
	peak := PeakAwarePricing{
		Enabled: true,
		Peak:    PeakPricingAmounts{Input: 6e-7, Output: 2.4e-6},
		OffPeak: PeakPricingAmounts{Input: 3e-7, Output: 1.2e-6},
	}
	settings := PeakPricingSettings{
		Timezone:       "UTC",
		WeekendOffPeak: false,
		Periods:        []PeakPricingPeriod{{Start: "01:00", End: "04:00"}},
	}
	monday := time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC)

	got := ResolvePeakPricing(base, peak, settings, monday)
	require.InDelta(t, 6e-7, got.InputPricePerToken, 1e-12)
	require.InDelta(t, 2.4e-6, got.OutputPricePerToken, 1e-12)

	off := ResolvePeakPricing(base, peak, settings, time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC))
	require.InDelta(t, 3e-7, off.InputPricePerToken, 1e-12)
	require.InDelta(t, 1.2e-6, off.OutputPricePerToken, 1e-12)

	require.InDelta(t, 3e-7, base.InputPricePerToken, 1e-12, "入参不得被修改")
}

// TestResolvePeakPricingOffPeakEmptyFallsBackToBase 覆盖：谷价留空回落基础价。
func TestResolvePeakPricingOffPeakEmptyFallsBackToBase(t *testing.T) {
	base := &ModelPricing{InputPricePerToken: 3e-7, OutputPricePerToken: 1.2e-6}
	peak := PeakAwarePricing{Enabled: true, Peak: PeakPricingAmounts{Input: 6e-7, Output: 2.4e-6}}
	settings := PeakPricingSettings{Timezone: "UTC", WeekendOffPeak: false, Periods: []PeakPricingPeriod{{Start: "01:00", End: "04:00"}}}

	off := ResolvePeakPricing(base, peak, settings, time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC))
	require.InDelta(t, 3e-7, off.InputPricePerToken, 1e-12, "谷时回落基础价")
	require.InDelta(t, 1.2e-6, off.OutputPricePerToken, 1e-12)
}

// TestResolvePeakPricingPeakEmptyFallsBackToDoubleOffPeak 覆盖：峰价留空回落谷价×2。
func TestResolvePeakPricingPeakEmptyFallsBackToDoubleOffPeak(t *testing.T) {
	base := &ModelPricing{InputPricePerToken: 9e-7}
	peak := PeakAwarePricing{Enabled: true, OffPeak: PeakPricingAmounts{Input: 3e-7, Output: 1.2e-6}}
	settings := PeakPricingSettings{Timezone: "UTC", WeekendOffPeak: false, Periods: []PeakPricingPeriod{{Start: "01:00", End: "04:00"}}}

	got := ResolvePeakPricing(base, peak, settings, time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC))
	require.InDelta(t, 6e-7, got.InputPricePerToken, 1e-12)
	require.InDelta(t, 2.4e-6, got.OutputPricePerToken, 1e-12)
}

// TestResolvePeakPricingDisabledLeavesBaseUntouched 覆盖：未启用时原样返回。
func TestResolvePeakPricingDisabledLeavesBaseUntouched(t *testing.T) {
	base := &ModelPricing{InputPricePerToken: 3e-7}
	got := ResolvePeakPricing(base, PeakAwarePricing{Enabled: false}, defaultPeakPricingSettings(), time.Now())
	require.Same(t, base, got)
}

// TestResolvePeakPricingNilBaseIsSafe 覆盖：nil 安全。
func TestResolvePeakPricingNilBaseIsSafe(t *testing.T) {
	require.Nil(t, ResolvePeakPricing(nil, PeakAwarePricing{Enabled: true}, defaultPeakPricingSettings(), time.Now()))
}
