package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// minimalCatalogJSON 是一份最小的 LiteLLM 目录正文：buildPricingData 要求
// 目录中至少有一个可解析的价格条目，否则整个加载会失败退出。
const minimalCatalogJSON = `{
	"catalog-seed-model": {
		"litellm_provider": "openai",
		"mode": "chat",
		"input_cost_per_token": 1e-06,
		"output_cost_per_token": 2e-06
	}
}`

// newPeakPricingService 构造一个带 override 文件的 PricingService，便于注入
// `_catalog` 峰谷配置。
func newPeakPricingService(t *testing.T, overrideJSON string) *PricingService {
	t.Helper()
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(overrideJSON), 0o644))
	svc := &PricingService{cfg: &config.Config{}}
	svc.cfg.Pricing.OverrideFile = overridePath
	return svc
}

// TestCatalogMetaParsedIntoPeakConfigs 覆盖：`_catalog` 峰谷声明会被读进
// peakConfigs，并与标准价格字段共存。
func TestCatalogMetaParsedIntoPeakConfigs(t *testing.T) {
	svc := newPeakPricingService(t, `{
		"deepseek-v4-flash": {
			"input_cost_per_token": 3e-07,
			"output_cost_per_token": 1.2e-06,
			"_catalog": {
				"platform": "deepseek",
				"custom": true,
				"peak_enabled": true,
				"peak": {"input_cost_per_token": 6e-07, "output_cost_per_token": 2.4e-06},
				"off_peak": {"input_cost_per_token": 3e-07, "output_cost_per_token": 1.2e-06}
			}
		}
	}`)

	built, err := svc.buildPricingData([]byte(minimalCatalogJSON))
	require.NoError(t, err)

	peak, ok := built.peakConfigs["deepseek-v4-flash"]
	require.True(t, ok, "_catalog.peak_enabled 应产生峰谷配置")
	require.True(t, peak.Enabled)
	require.InDelta(t, 6e-7, peak.Peak.Input, 1e-12)
	require.InDelta(t, 2.4e-6, peak.Peak.Output, 1e-12)
	require.InDelta(t, 3e-7, peak.OffPeak.Input, 1e-12)

	// 标准价格字段仍需生效（覆盖层照常合并）。
	pricing, ok := built.data["deepseek-v4-flash"]
	require.True(t, ok)
	require.InDelta(t, 3e-7, pricing.InputCostPerToken, 1e-12)
}

// TestCatalogMetaWithoutPeakIsIgnored 覆盖：未启用峰谷时不产生配置。
func TestCatalogMetaWithoutPeakIsIgnored(t *testing.T) {
	svc := newPeakPricingService(t, `{
		"m1": {"input_cost_per_token": 1e-06, "_catalog": {"custom": true, "peak_enabled": false}}
	}`)
	built, err := svc.buildPricingData([]byte(minimalCatalogJSON))
	require.NoError(t, err)
	require.Empty(t, built.peakConfigs)
}

// TestCatalogMetaMalformedDegrades 覆盖：`_catalog` 损坏时标准价格仍可用。
func TestCatalogMetaMalformedDegrades(t *testing.T) {
	svc := newPeakPricingService(t, `{
		"m2": {"input_cost_per_token": 2e-06, "_catalog": "not-an-object"}
	}`)
	built, err := svc.buildPricingData([]byte(minimalCatalogJSON))
	require.NoError(t, err)
	require.Empty(t, built.peakConfigs, "损坏的元数据应降级为无峰谷")

	pricing, ok := built.data["m2"]
	require.True(t, ok, "标准价格字段不应受影响")
	require.InDelta(t, 2e-6, pricing.InputCostPerToken, 1e-12)
}

// TestPeakConfigsAccessibleAfterBuild 覆盖：PeakPricingFor 在数据提交后可读。
func TestPeakConfigsAccessibleAfterBuild(t *testing.T) {
	svc := newPeakPricingService(t, `{
		"gpt-x": {
			"input_cost_per_token": 1e-06,
			"_catalog": {"peak_enabled": true, "peak": {"input_cost_per_token": 2e-06}}
		}
	}`)
	built, err := svc.buildPricingData([]byte(minimalCatalogJSON))
	require.NoError(t, err)

	svc.mu.Lock()
	svc.peakConfigs = built.peakConfigs
	svc.mu.Unlock()

	peak, ok := svc.PeakPricingFor("GPT-X")
	require.True(t, ok, "模型名大小写不敏感")
	require.InDelta(t, 2e-6, peak.Peak.Input, 1e-12)

	_, ok = svc.PeakPricingFor("unknown")
	require.False(t, ok)
}

// TestPeakPricingForNilServiceIsSafe 覆盖：nil 服务安全。
func TestPeakPricingForNilServiceIsSafe(t *testing.T) {
	var svc *PricingService
	_, ok := svc.PeakPricingFor("m")
	require.False(t, ok)
	require.Equal(t, defaultPeakPricingSettings(), svc.PeakPricingSettings())
}

// TestPeakSettingsRoundTrip 覆盖：设置写入后按归一化值读回。
func TestPeakSettingsRoundTrip(t *testing.T) {
	svc := &PricingService{}
	svc.SetPeakPricingSettings(PeakPricingSettings{
		Timezone: "UTC",
		Periods:  []PeakPricingPeriod{{Start: "09:00", End: "18:00"}},
	})
	got := svc.PeakPricingSettings()
	require.Equal(t, "UTC", got.Timezone)
	require.Len(t, got.Periods, 1)
	require.Equal(t, "09:00", got.Periods[0].Start)
}

// TestBillingCustomPeakSuppressesOfficialDeepSeekMultiplier 是 A7 的核心断言：
// 模型启用自定义峰谷后，计费入口必须走自定义峰谷（而不是官方倍率），
// 无论当前时刻落在峰还是谷，成本都应等于自定义价，绝不能是「自定义 × 官方」。
func TestBillingCustomPeakSuppressesOfficialDeepSeekMultiplier(t *testing.T) {
	// 前提锚定：UTC 周一 02:00 官方倍率为 2×。若自定义峰谷未生效，
	// 该时刻的成本会被放大成峰价的 2 倍。
	require.InDelta(t, 2.0, deepseekPeakMultiplierAt(time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC)), 1e-9)

	svc := newPeakPricingService(t, `{
		"deepseek-v4-pro": {
			"input_cost_per_token": 6.6e-07,
			"output_cost_per_token": 1.98e-06,
			"cache_read_input_token_cost": 2.2e-08,
			"_catalog": {
				"peak_enabled": true,
				"off_peak": {"input_cost_per_token": 6.6e-07, "output_cost_per_token": 1.98e-06},
				"peak": {"input_cost_per_token": 9.9e-07, "output_cost_per_token": 2.97e-06}
			}
		}
	}`)
	built, err := svc.buildPricingData([]byte(minimalCatalogJSON))
	require.NoError(t, err)
	svc.mu.Lock()
	svc.pricingData = built.data
	svc.pricingDataSources = built.sources
	svc.overrideModels = built.overrideModels
	svc.cnyModels = built.cnyModels
	svc.peakConfigs = built.peakConfigs
	svc.mu.Unlock()

	billing := NewBillingService(&config.Config{}, svc)
	tokens := UsageTokens{InputTokens: 1_000_000}

	cost, err := billing.CalculateCost("deepseek-v4-pro", tokens, 1)
	require.NoError(t, err)

	// 无论当前时刻是峰是谷，生效价只能是两个自定义档之一：
	//   谷价 0.66 / 峰价 0.99，且都不得再乘官方 2×（否则会出现 1.32 / 1.98）。
	allowed := []float64{0.66, 0.99}
	matched := false
	for _, want := range allowed {
		if diff := cost.InputCost - want; diff < 1e-9 && diff > -1e-9 {
			matched = true
			break
		}
	}
	require.True(t, matched,
		"自定义峰谷应完全接管：成本 %v 必须是谷价 0.66 或峰价 0.99，不得出现官方倍率叠加（1.32/1.98）",
		cost.InputCost)
}

// TestBillingOfficialMultiplierStillAppliesWithoutCustomPeak 是 A7 的反向断言：
// 未启用自定义峰谷时，DeepSeek 官方倍率行为保持不变。
func TestBillingOfficialMultiplierStillAppliesWithoutCustomPeak(t *testing.T) {
	svc := &PricingService{cfg: &config.Config{}}
	billing := NewBillingService(&config.Config{}, svc)

	// 直接验证官方倍率函数仍按原口径工作。
	require.InDelta(t, 2.0, deepseekPeakMultiplierAt(time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC)), 1e-9)
	require.InDelta(t, 1.0, deepseekPeakMultiplierAt(time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)), 1e-9,
		"周末全天低谷")

	_, ok := billing.lookupPeakPricing("deepseek-v4-pro")
	require.False(t, ok, "无 _catalog 时不应返回峰谷配置")
}
