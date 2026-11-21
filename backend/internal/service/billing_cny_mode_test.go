package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 验收 §3.5.1/§3.5.2：1M tokens 输入成本在两种口径下的数值断言。
func TestBillingCNYModeOneMillionInputCost(t *testing.T) {
	t.Run("convert divides by cny_per_usd", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`, cnyTestFileJSON(CNYModeConvert, 7.14))
		billing := NewBillingService(&config.Config{}, env.svc)

		cost, err := billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
		require.NoError(t, err)
		require.InDelta(t, 4.0/7.14, cost.InputCost, 1e-9)

		cost, err = billing.CalculateCost("kimi-k2", UsageTokens{
			InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000,
		}, 1)
		require.NoError(t, err)
		require.InDelta(t, 4.0/7.14, cost.InputCost, 1e-9)
		require.InDelta(t, 16.0/7.14, cost.OutputCost, 1e-9)
		require.InDelta(t, 1.0/7.14, cost.CacheReadCost, 1e-9)
	})

	t.Run("direct uses one to one", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`, cnyTestFileJSON(CNYModeDirect, 7.14))
		billing := NewBillingService(&config.Config{}, env.svc)

		cost, err := billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
		require.NoError(t, err)
		require.InDelta(t, 4, cost.InputCost, 1e-9)

		cost, err = billing.CalculateCost("kimi-k2", UsageTokens{OutputTokens: 1_000_000}, 1)
		require.NoError(t, err)
		require.InDelta(t, 16, cost.OutputCost, 1e-9)
	})

	// 验收 §3.5.3：切换口径只影响人民币登记模型。
	t.Run("unregistered model is unaffected by mode", func(t *testing.T) {
		convert := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`, cnyTestFileJSON(CNYModeConvert, 7.14))
		direct := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`, cnyTestFileJSON(CNYModeDirect, 7.14))
		for name, env := range map[string]*cnyPricingTestEnv{"convert": convert, "direct": direct} {
			billing := NewBillingService(&config.Config{}, env.svc)
			cost, err := billing.CalculateCost("gpt-5.4", UsageTokens{InputTokens: 1_000_000}, 1)
			require.NoError(t, err)
			require.InDelta(t, 2.5, cost.InputCost, 1e-9, name)
		}
	})
}

// 验收 §2.4：DeepSeek 强制官方价在存在管理员覆盖/人民币登记时让位；未覆盖时行为不变。
func TestBillingDeepSeekOfficialPriceYieldsToManagedPricing(t *testing.T) {
	t.Run("uncovered model keeps official price", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`, "")
		billing := NewBillingService(&config.Config{}, env.svc)

		pricing, err := billing.GetModelPricing("deepseek-v4-pro")
		require.NoError(t, err)
		require.InDelta(t, deepseekProOffPeakInputPrice, pricing.InputPricePerToken, 1e-15)

		cost, err := billing.CalculateCost("deepseek-v4-pro", UsageTokens{InputTokens: 1_000_000}, 1)
		require.NoError(t, err)
		require.InDelta(t, 0.66, cost.InputCost, 1e-9, "官方 $0.66/MTok")
	})

	t.Run("USD override wins", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON,
			`{"deepseek-v4-pro": {"input_cost_per_token": 5e-06, "output_cost_per_token": 1e-05}}`, "")
		billing := NewBillingService(&config.Config{}, env.svc)

		pricing, err := billing.GetModelPricing("deepseek-v4-pro")
		require.NoError(t, err)
		require.InDelta(t, 5e-6, pricing.InputPricePerToken, 1e-15, "官方价不得覆盖管理员配置")

		cost, err := billing.CalculateCost("deepseek-v4-pro", UsageTokens{InputTokens: 1_000_000}, 1)
		require.NoError(t, err)
		require.InDelta(t, 5.0, cost.InputCost, 1e-9)
	})

	t.Run("CNY registration wins", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`,
			`{"version":1,"mode":"convert","cny_per_usd":7.14,"models":{
				"deepseek-v4-pro":{"input_cny_per_mtok":7.14,"output_cny_per_mtok":14.28}}}`)
		billing := NewBillingService(&config.Config{}, env.svc)

		pricing, err := billing.GetModelPricing("deepseek-v4-pro")
		require.NoError(t, err)
		require.InDelta(t, 1e-6, pricing.InputPricePerToken, 1e-15, "7.14 CNY ÷ 7.14 = 1 USD/MTok")

		cost, err := billing.CalculateCost("deepseek-v4-pro", UsageTokens{InputTokens: 1_000_000}, 1)
		require.NoError(t, err)
		require.InDelta(t, 1.0, cost.InputCost, 1e-9)
	})
}

// 峰谷倍率同样让位：未覆盖模型高峰按 2× 官方价，管理员接管后按配置价计费（无隐性放大）。
func TestBillingDeepSeekPeakMultiplierSkipsManagedPricing(t *testing.T) {
	// 2026-11-16 是周一；UTC 02:00 落在高峰时段 01:00–04:00。
	peak := time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC)
	require.InDelta(t, 2.0, deepseekPeakMultiplierAt(peak), 1e-12)

	unifiedInputCost := func(t *testing.T, billing *BillingService, model string) float64 {
		t.Helper()
		resolver := NewModelPricingResolver(nil, billing)
		breakdown, err := billing.CalculateCostUnified(CostInput{
			Ctx:            context.Background(),
			Model:          model,
			Tokens:         UsageTokens{InputTokens: 1_000_000},
			RateMultiplier: 1,
			Resolver:       resolver,
			PricingAt:      peak,
		})
		require.NoError(t, err)
		return breakdown.InputCost
	}

	t.Run("uncovered model applies peak multiplier", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`, "")
		billing := NewBillingService(&config.Config{}, env.svc)
		require.InDelta(t, 1.32, unifiedInputCost(t, billing, "deepseek-v4-pro"), 1e-9, "$0.66 × 2")
	})

	t.Run("USD override skips peak multiplier", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON,
			`{"deepseek-v4-pro": {"input_cost_per_token": 5e-06}}`, "")
		billing := NewBillingService(&config.Config{}, env.svc)
		require.InDelta(t, 5.0, unifiedInputCost(t, billing, "deepseek-v4-pro"), 1e-9)
	})

	t.Run("CNY registration skips peak multiplier", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`,
			`{"version":1,"mode":"direct","models":{"deepseek-v4-pro":{"input_cny_per_mtok":3}}}`)
		billing := NewBillingService(&config.Config{}, env.svc)
		require.InDelta(t, 3.0, unifiedInputCost(t, billing, "deepseek-v4-pro"), 1e-9)
	})

	t.Run("non deepseek models unaffected by peak logic", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`, "")
		billing := NewBillingService(&config.Config{}, env.svc)
		require.InDelta(t, 2.5, unifiedInputCost(t, billing, "gpt-5.4"), 1e-9)
	})
}
