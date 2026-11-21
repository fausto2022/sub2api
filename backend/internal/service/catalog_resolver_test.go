package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// catalogResolverEnv 构造一个只走目录价层的解析环境：override 条目直接注入，
// 基础价表由 LiteLLM 目录给出（buildPricingData 的结果）。
type catalogResolverEnv struct {
	resolver *ModelPricingResolver
	billing  *BillingService
	admin    *ModelPricingAdminService
}

// newCatalogResolverEnv 用给定的 override JSON 与目录 JSON 构造解析环境。
func newCatalogResolverEnv(t *testing.T, overrideJSON, catalogJSON string) *catalogResolverEnv {
	t.Helper()
	svc := newPeakPricingService(t, overrideJSON)
	built, err := svc.buildPricingData([]byte(catalogJSON))
	require.NoError(t, err)
	svc.mu.Lock()
	svc.pricingData = built.data
	svc.pricingDataSources = built.sources
	svc.overrideModels = built.overrideModels
	svc.cnyModels = built.cnyModels
	svc.peakConfigs = built.peakConfigs
	svc.mu.Unlock()

	billing := NewBillingService(&config.Config{}, svc)
	resolver := NewModelPricingResolver(nil, billing)
	// 开关由管理服务持有并下推给 resolver（settings.catalog_pricing_authoritative）。
	admin := NewModelPricingAdminService(svc.cfg, svc)
	admin.SetCatalogPricingResolver(resolver)
	return &catalogResolverEnv{resolver: resolver, billing: billing, admin: admin}
}

// catalogTestFloatPtr 返回浮点数的指针（本文件自包含的测试辅助）。
func catalogTestFloatPtr(v float64) *float64 { return &v }

// TestCatalogLayerTakesPrecedenceOverGroup 覆盖：目录价层优先于分组价。
//
// 同一个模型同时配了分组价与 `_catalog` 目录价时，Resolve 必须返回 catalog 层
// （design.md §3.3 的 catalog > group > channel > litellm > fallback）；渠道层在
// 目录价层之后才被查找，因此 group 让位即已证明优先级顺序。
func TestCatalogLayerTakesPrecedenceOverGroup(t *testing.T) {
	env := newCatalogResolverEnv(t, `{
		"claude-sonnet-4": {
			"input_cost_per_token": 1e-06,
			"output_cost_per_token": 5e-06,
			"_catalog": {"platform": "anthropic", "custom": true}
		}
	}`, minimalCatalogJSON)

	// 分组价配了明显更低的价，目录价层仍应胜出。
	group := &Group{ID: 100, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		InputPrice: catalogTestFloatPtr(0.1e-6), OutputPrice: catalogTestFloatPtr(0.2e-6),
	}}}
	gid := int64(100)

	resolved := env.resolver.Resolve(context.Background(), PricingInput{
		Model: "claude-sonnet-4", GroupID: &gid, Group: group,
	})

	require.NotNil(t, resolved)
	require.Equal(t, PricingSourceCatalog, resolved.Source, "目录价层必须优先于 group/channel")
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 1e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 5e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

// TestCatalogLayerFallsBackToGroupWhenDisabled 覆盖开关回退：settings 里
// catalog_pricing_authoritative=false 时跳过目录价层，优先级退回旧口径。
func TestCatalogLayerFallsBackToGroupWhenDisabled(t *testing.T) {
	env := newCatalogResolverEnv(t, `{
		"claude-sonnet-4": {
			"input_cost_per_token": 1e-06,
			"_catalog": {"platform": "anthropic", "custom": true}
		}
	}`, minimalCatalogJSON)
	group := &Group{ID: 100, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		InputPrice: catalogTestFloatPtr(0.1e-6), OutputPrice: catalogTestFloatPtr(0.2e-6),
	}}}

	// 开关默认 true → catalog 生效。
	require.True(t, env.admin.CatalogPricingAuthoritative(), "未配置时默认 true")
	require.Equal(t, PricingSourceCatalog,
		env.resolver.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", Group: group}).Source)

	// 关闭后回退到 group。
	env.admin.SetCatalogPricingAuthoritative(false)
	require.False(t, env.resolver.CatalogPricingAuthoritative(), "开关关闭后下推给 resolver")

	fellBack := env.resolver.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", Group: group})
	require.Equal(t, PricingSourceGroup, fellBack.Source, "开关关闭时必须回退旧优先级")
	require.InDelta(t, 0.1e-6, fellBack.BasePricing.InputPricePerToken, 1e-12)

	// 再打开恢复 catalog。
	env.admin.SetCatalogPricingAuthoritative(true)
	require.Equal(t, PricingSourceCatalog,
		env.resolver.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", Group: group}).Source)
}

// TestCatalogLayerDisabledFallsBackToLiteLLM 覆盖开关回退到 LiteLLM 层的情形：
// 无分组无渠道时，关闭目录价层后来源必须回到 litellm。
//
// 注意 override 的标准价格字段本身仍会并进目录表（旧口径），所以关闭目录价层只是
// 「取消它的最高优先级与峰谷双价」，不是丢弃管理员写下的价格。
func TestCatalogLayerDisabledFallsBackToLiteLLM(t *testing.T) {
	env := newCatalogResolverEnv(t, `{
		"catalog-seed-model": {
			"input_cost_per_token": 9e-06,
			"_catalog": {"platform": "openai", "custom": true}
		}
	}`, minimalCatalogJSON)

	enabled := env.resolver.Resolve(context.Background(), PricingInput{Model: "catalog-seed-model"})
	require.Equal(t, PricingSourceCatalog, enabled.Source)
	require.InDelta(t, 9e-6, enabled.BasePricing.InputPricePerToken, 1e-12, "目录价层用 override 里的显式价")

	env.admin.SetCatalogPricingAuthoritative(false)
	disabled := env.resolver.Resolve(context.Background(), PricingInput{Model: "catalog-seed-model"})
	require.Equal(t, PricingSourceLiteLLM, disabled.Source, "关闭后来源回到 litellm 层")
	require.InDelta(t, 9e-6, disabled.BasePricing.InputPricePerToken, 1e-12,
		"override 价格仍经目录表生效（旧口径），回滚的是优先级与峰谷")
}

// TestCatalogLayerIgnoresPlainOverrideWithoutMeta 覆盖：普通 USD 覆盖（无 `_catalog`）
// 不跳层，保持原有「进入 LiteLLM 层价格表」语义。
func TestCatalogLayerIgnoresPlainOverrideWithoutMeta(t *testing.T) {
	env := newCatalogResolverEnv(t, `{
		"catalog-seed-model": {"input_cost_per_token": 4e-06}
	}`, minimalCatalogJSON)

	resolved := env.resolver.Resolve(context.Background(), PricingInput{Model: "catalog-seed-model"})
	require.Equal(t, PricingSourceLiteLLM, resolved.Source, "没有 _catalog 元数据时不进目录价层")
	require.InDelta(t, 4e-6, resolved.BasePricing.InputPricePerToken, 1e-12, "覆盖价已合并进目录表")
}

// TestCatalogLayerCarriesPeakConfig 覆盖：目录价层把 `_catalog` 峰谷配置带进解析结果。
func TestCatalogLayerCarriesPeakConfig(t *testing.T) {
	env := newCatalogResolverEnv(t, `{
		"deepseek-v4-flash": {
			"input_cost_per_token": 3e-07,
			"output_cost_per_token": 1.2e-06,
			"_catalog": {
				"platform": "deepseek", "custom": true, "peak_enabled": true,
				"peak": {"input_cost_per_token": 6e-07, "output_cost_per_token": 2.4e-06},
				"off_peak": {"input_cost_per_token": 3e-07, "output_cost_per_token": 1.2e-06}
			}
		}
	}`, minimalCatalogJSON)

	resolved := env.resolver.Resolve(context.Background(), PricingInput{Model: "deepseek-v4-flash"})
	require.Equal(t, PricingSourceCatalog, resolved.Source)
	require.True(t, resolved.peakAware.Enabled)
	require.InDelta(t, 6e-7, resolved.peakAware.Peak.Input, 1e-12)
	require.InDelta(t, 1.2e-6, resolved.peakAware.OffPeak.Output, 1e-12)
}

// TestCatalogLayerUnpriceableModelIsNotTakenOver 覆盖：只带元数据、没有任何价格字段
// 的条目不会被目录价层接管（否则会把模型变成 $0 计费）。
func TestCatalogLayerUnpriceableModelIsNotTakenOver(t *testing.T) {
	env := newCatalogResolverEnv(t, `{
		"mystery-model": {"_catalog": {"platform": "openai", "custom": true}}
	}`, minimalCatalogJSON)

	resolved := env.resolver.Resolve(context.Background(), PricingInput{Model: "mystery-model"})
	require.NotEqual(t, PricingSourceCatalog, resolved.Source, "无可计价字段时不得接管")
}

// TestCatalogLayerNilResolverIsSafe 覆盖 nil 安全。
func TestCatalogLayerNilResolverIsSafe(t *testing.T) {
	var resolver *ModelPricingResolver
	require.True(t, resolver.CatalogPricingAuthoritative(), "nil resolver 默认 true")
	require.Nil(t, resolver.resolveCatalogPricing("m"))
	resolver.SetCatalogPricingAuthoritative(false)
}

// TestCatalogResolverLookupIsCaseInsensitive 覆盖模型名大小写不敏感。
func TestCatalogResolverLookupIsCaseInsensitive(t *testing.T) {
	env := newCatalogResolverEnv(t, `{
		"Mixed-Case-Model": {
			"input_cost_per_token": 2e-06,
			"_catalog": {"platform": "openai"}
		}
	}`, minimalCatalogJSON)

	for _, name := range []string{"Mixed-Case-Model", "mixed-case-model", "MIXED-CASE-MODEL"} {
		resolved := env.resolver.Resolve(context.Background(), PricingInput{Model: name})
		require.Equal(t, PricingSourceCatalog, resolved.Source, "name=%s", name)
	}
}
