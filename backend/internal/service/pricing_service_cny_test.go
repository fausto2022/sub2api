package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// cnyTestCatalogJSON 是人民币口径测试用的目录：kimi-k2 在目录里已有 USD 价（0.56/2.24），
// 用于断言人民币层优先于目录；gpt-5.4 未登记，用于断言未登记模型价格不变；
// deepseek-v4-pro 用于让位规则（见 billing_cny_mode_test.go）。
const cnyTestCatalogJSON = `{
	"kimi-k2": {"litellm_provider": "moonshot", "mode": "chat",
		"input_cost_per_token": 0.56e-06, "output_cost_per_token": 2.24e-06,
		"cache_read_input_token_cost": 0.14e-06},
	"gpt-5.4": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05,
		"cache_read_input_token_cost": 2.5e-07},
	"deepseek-v4-pro": {"litellm_provider": "deepseek", "mode": "chat",
		"input_cost_per_token": 1e-06, "output_cost_per_token": 9e-06,
		"cache_read_input_token_cost": 1e-07}
}`

// cnyTestOverrideJSON 预置一条与人民币层无关的 USD override（另一模型）。
const cnyTestOverrideJSON = `{"gpt-5.4": {"output_cost_per_token": 2.5e-05}}`

// cnyTestFileJSON 组装人民币层文件内容。mode 为 convert/direct，rate 为 cny_per_usd。
func cnyTestFileJSON(mode string, rate float64) string {
	return fmt.Sprintf(`{
	"version": 1, "mode": %q, "cny_per_usd": %v,
	"models": {
		"kimi-k2": {"input_cny_per_mtok": 4, "output_cny_per_mtok": 16, "cache_read_cny_per_mtok": 1},
		"kimi-k2-thinking": {"input_cny_per_mtok": 4, "output_cny_per_mtok": 16, "cache_read_cny_per_mtok": 1},
		"doubao-embedding-vision": {"input_cny_per_mtok": 0.7, "image_input_cny_per_mtok": 1.8}
	}
}`, mode, rate)
}

// cnyPricingTestEnv 在临时数据目录里放好目录缓存、override 与人民币层文件并完成首次加载。
// overrideJSON / cnyJSON 传空串表示不写对应文件（人民币层文件缺失属合法状态）。
type cnyPricingTestEnv struct {
	svc          *PricingService
	catalogFile  string
	overrideFile string
	cnyFile      string
}

func newCNYPricingTestEnv(t *testing.T, catalogJSON, overrideJSON, cnyJSON string) *cnyPricingTestEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Pricing.DataDir = dir
	cfg.Pricing.OverrideFile = filepath.Join(dir, "overrides.json")
	cfg.Pricing.CNYOverrideFile = filepath.Join(dir, "cny-overrides.json")

	env := &cnyPricingTestEnv{
		svc:          &PricingService{cfg: cfg},
		catalogFile:  filepath.Join(dir, "model_pricing.json"),
		overrideFile: cfg.Pricing.OverrideFile,
		cnyFile:      cfg.Pricing.CNYOverrideFile,
	}
	require.NoError(t, os.WriteFile(env.catalogFile, []byte(catalogJSON), 0o644))
	if overrideJSON != "" {
		require.NoError(t, os.WriteFile(env.overrideFile, []byte(overrideJSON), 0o644))
	}
	if cnyJSON != "" {
		require.NoError(t, os.WriteFile(env.cnyFile, []byte(cnyJSON), 0o644))
	}
	require.NoError(t, env.svc.loadPricingData(env.catalogFile))
	return env
}

// usdPerMTok 把内存里的 per-token 价换算成 USD / 1M tokens。
func usdPerMTok(perToken float64) float64 { return perToken * 1e6 }

// 验收 §3.5.1/§3.5.2：convert 模式 USD = CNY / 7.14，direct 模式 USD = CNY。
func TestCNYPricingConvertAndDirectRates(t *testing.T) {
	t.Run("convert divides by cny_per_usd", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, cnyTestOverrideJSON, cnyTestFileJSON(CNYModeConvert, 7.14))
		pricing := env.svc.pricingData["kimi-k2"]
		require.NotNil(t, pricing)
		require.InDelta(t, 4.0/7.14, usdPerMTok(pricing.InputCostPerToken), 1e-9)
		require.InDelta(t, 16.0/7.14, usdPerMTok(pricing.OutputCostPerToken), 1e-9)
		require.InDelta(t, 1.0/7.14, usdPerMTok(pricing.CacheReadInputTokenCost), 1e-9)
		settings := env.svc.CurrentCNYSettings()
		require.Equal(t, CNYModeConvert, settings.Mode)
		require.Equal(t, 7.14, settings.CNYPerUSD)
		require.Equal(t, env.cnyFile, settings.File)
	})

	t.Run("direct uses one to one", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, cnyTestOverrideJSON, cnyTestFileJSON(CNYModeDirect, 7.14))
		pricing := env.svc.pricingData["kimi-k2"]
		require.InDelta(t, 4.0, usdPerMTok(pricing.InputCostPerToken), 1e-9)
		require.InDelta(t, 16.0, usdPerMTok(pricing.OutputCostPerToken), 1e-9)
		require.InDelta(t, 1.0, usdPerMTok(pricing.CacheReadInputTokenCost), 1e-9)
		settings := env.svc.CurrentCNYSettings()
		require.Equal(t, CNYModeDirect, settings.Mode)
		require.Equal(t, 7.14, settings.CNYPerUSD, "direct 模式仍展示配置汇率，但换算按 1:1")
	})
}

// 未登记模型保持目录价，切换口径不影响它们。
func TestCNYPricingUnregisteredModelsUnchanged(t *testing.T) {
	convert := newCNYPricingTestEnv(t, cnyTestCatalogJSON, cnyTestOverrideJSON, cnyTestFileJSON(CNYModeConvert, 7.14))
	direct := newCNYPricingTestEnv(t, cnyTestCatalogJSON, cnyTestOverrideJSON, cnyTestFileJSON(CNYModeDirect, 7.14))

	for _, env := range []*cnyPricingTestEnv{convert, direct} {
		pricing := env.svc.pricingData["gpt-5.4"]
		require.NotNil(t, pricing)
		require.InDelta(t, 2.5, usdPerMTok(pricing.InputCostPerToken), 1e-9, "目录输入价不变")
		require.InDelta(t, 25.0, usdPerMTok(pricing.OutputCostPerToken), 1e-9, "USD override 仍生效")
	}
}

// 目录外模型只有人民币登记时也要可建条目（复用 mergeOverrideOnlyModels 思路）。
func TestCNYPricingAddsCatalogExcludedModel(t *testing.T) {
	env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, cnyTestOverrideJSON, cnyTestFileJSON(CNYModeConvert, 7.14))
	pricing := env.svc.pricingData["doubao-embedding-vision"]
	require.NotNil(t, pricing, "目录外模型由人民币层并入")
	require.Equal(t, cnyOnlyProvider, pricing.LiteLLMProvider)
	require.InDelta(t, 0.7/7.14, usdPerMTok(pricing.InputCostPerToken), 1e-9)
	require.InDelta(t, 1.8/7.14, usdPerMTok(pricing.InputCostPerImageToken), 1e-9)
	require.True(t, env.svc.HasManagedPricing("doubao-embedding-vision"))
}

// 验收 §3.5.4：USD override 优先于人民币登记；字段级 null 删除后回退人民币层。
func TestCNYPricingUSDPriorityAndNullFallback(t *testing.T) {
	t.Run("USD override wins over CNY layer", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON,
			`{"kimi-k2": {"input_cost_per_token": 9e-06}}`,
			cnyTestFileJSON(CNYModeConvert, 7.14))
		pricing := env.svc.pricingData["kimi-k2"]
		require.InDelta(t, 9.0, usdPerMTok(pricing.InputCostPerToken), 1e-9, "USD 显式覆盖最优先")
		require.InDelta(t, 16.0/7.14, usdPerMTok(pricing.OutputCostPerToken), 1e-9, "未覆盖字段仍由人民币层提供")
	})

	t.Run("null field deletes override and falls back to CNY", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON,
			`{"kimi-k2": {"input_cost_per_token": null, "cache_creation_input_token_cost": null}}`,
			cnyTestFileJSON(CNYModeConvert, 7.14))
		pricing := env.svc.pricingData["kimi-k2"]
		require.InDelta(t, 4.0/7.14, usdPerMTok(pricing.InputCostPerToken), 1e-9, "null 删除后回退人民币层，而不是目录价 0.56")
		require.InDelta(t, 16.0/7.14, usdPerMTok(pricing.OutputCostPerToken), 1e-9)
	})

	t.Run("null on field absent from catalog and CNY falls back to zero", func(t *testing.T) {
		env := newCNYPricingTestEnv(t, cnyTestCatalogJSON,
			`{"kimi-k2": {"cache_creation_input_token_cost": null}}`,
			cnyTestFileJSON(CNYModeConvert, 7.14))
		pricing := env.svc.pricingData["kimi-k2"]
		require.Zero(t, pricing.CacheCreationInputTokenCost)
		require.InDelta(t, 4.0/7.14, usdPerMTok(pricing.InputCostPerToken), 1e-9)
	})
}

// 验收 §3.5.6：损坏的人民币层文件不影响启动与其它模型定价。
func TestCNYPricingCorruptFileDegrades(t *testing.T) {
	env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, cnyTestOverrideJSON, `{not json`)

	require.NoError(t, env.svc.loadPricingData(env.catalogFile), "损坏文件不得阻断启动")
	require.InDelta(t, 0.56, usdPerMTok(env.svc.pricingData["kimi-k2"].InputCostPerToken), 1e-9, "人民币层失效后回到目录价")
	require.InDelta(t, 2.5, usdPerMTok(env.svc.pricingData["gpt-5.4"].InputCostPerToken), 1e-9, "其它模型定价不受影响")
	require.False(t, env.svc.HasManagedPricing("kimi-k2"))

	// 文件修好后热重载恢复人民币层。
	require.NoError(t, os.WriteFile(env.cnyFile, []byte(cnyTestFileJSON(CNYModeConvert, 7.14)), 0o644))
	env.svc.reloadIfCustomFilesChanged()
	require.InDelta(t, 4.0/7.14, usdPerMTok(env.svc.pricingData["kimi-k2"].InputCostPerToken), 1e-9)
}

// 验收 §3.5.7：写文件后热重载生效（定时指纹比对与显式 Reload 两条路径）。
func TestCNYPricingHotReload(t *testing.T) {
	env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, cnyTestOverrideJSON, cnyTestFileJSON(CNYModeConvert, 7.14))
	require.InDelta(t, 4.0/7.14, usdPerMTok(env.svc.pricingData["kimi-k2"].InputCostPerToken), 1e-9)

	// 定时比对路径：内容指纹包含人民币层。
	require.NoError(t, os.WriteFile(env.cnyFile, []byte(`{"version":1,"mode":"convert","cny_per_usd":6,
		"models":{"kimi-k2":{"input_cny_per_mtok":6,"output_cny_per_mtok":12}}}`), 0o644))
	env.svc.reloadIfCustomFilesChanged()
	require.InDelta(t, 1.0, usdPerMTok(env.svc.pricingData["kimi-k2"].InputCostPerToken), 1e-9)
	require.InDelta(t, 2.0, usdPerMTok(env.svc.pricingData["kimi-k2"].OutputCostPerToken), 1e-9)

	// 显式热重载路径：管理端写文件后调用。
	require.NoError(t, os.WriteFile(env.cnyFile, []byte(`{"version":1,"mode":"direct","models":{
		"kimi-k2":{"input_cny_per_mtok":7}}}`), 0o644))
	require.NoError(t, env.svc.ReloadCustomPricingLayers())
	require.InDelta(t, 7.0, usdPerMTok(env.svc.pricingData["kimi-k2"].InputCostPerToken), 1e-9)
	require.InDelta(t, 2.24, usdPerMTok(env.svc.pricingData["kimi-k2"].OutputCostPerToken), 1e-9,
		"未登记的 output 字段回退目录价")
}

// 人民币层文件路径推导规则（design.md §3.2）。
func TestCNYPricingFilePathDerivation(t *testing.T) {
	dir := t.TempDir()

	t.Run("explicit config wins", func(t *testing.T) {
		svc := &PricingService{cfg: &config.Config{Pricing: config.PricingConfig{
			DataDir:         dir,
			OverrideFile:    filepath.Join(dir, "overrides.json"),
			CNYOverrideFile: filepath.Join(dir, "custom-cny.json"),
		}}}
		require.Equal(t, filepath.Join(dir, "custom-cny.json"), svc.EffectiveCNYOverrideFile())
	})

	t.Run("derived next to override file", func(t *testing.T) {
		svc := &PricingService{cfg: &config.Config{Pricing: config.PricingConfig{
			DataDir:      dir,
			OverrideFile: filepath.Join(dir, "pricing", "overrides.json"),
		}}}
		require.Equal(t, filepath.Join(dir, "pricing", "cny-overrides.json"), svc.EffectiveCNYOverrideFile())
	})

	t.Run("falls back to data dir", func(t *testing.T) {
		svc := &PricingService{cfg: &config.Config{Pricing: config.PricingConfig{DataDir: dir}}}
		require.Equal(t, filepath.Join(dir, "cny-overrides.json"), svc.EffectiveCNYOverrideFile())
	})

	t.Run("disabled when no directory configured", func(t *testing.T) {
		svc := &PricingService{cfg: &config.Config{}}
		require.Empty(t, svc.EffectiveCNYOverrideFile())
	})
}

// Snapshot 必须返回深拷贝与元数据；HasManagedPricing 命中 override 与人民币层。
func TestPricingSnapshotDeepCopyAndManagedModels(t *testing.T) {
	env := newCNYPricingTestEnv(t, cnyTestCatalogJSON,
		`{"gpt-5.4": {"output_cost_per_token": 2.5e-05}}`,
		cnyTestFileJSON(CNYModeConvert, 7.14))

	snapshot := env.svc.Snapshot()
	require.Equal(t, len(env.svc.pricingData), snapshot.ModelCount)
	require.NotEmpty(t, snapshot.CatalogFile)
	require.Equal(t, env.overrideFile, snapshot.OverrideFile)
	require.Equal(t, env.cnyFile, snapshot.CNYOverrideFile)
	require.Equal(t, "litellm", snapshot.Sources["kimi-k2"], "目录已存在的模型来源仍记为目录，人民币价见 cny 字段")
	require.Equal(t, "cny", snapshot.Sources["doubao-embedding-vision"])
	require.Equal(t, "litellm", snapshot.Sources["gpt-5.4"])
	_, overridden := snapshot.OverriddenModels["gpt-5.4"]
	require.True(t, overridden)

	// 修改快照不得影响计费数据。
	entry := snapshot.Models["kimi-k2"]
	entry.InputCostPerToken = 999
	snapshot.Models["kimi-k2"] = entry
	delete(snapshot.Models, "gpt-5.4")
	require.InDelta(t, 4.0/7.14, usdPerMTok(env.svc.pricingData["kimi-k2"].InputCostPerToken), 1e-9)
	require.Contains(t, env.svc.pricingData, "gpt-5.4")

	require.True(t, env.svc.HasManagedPricing("gpt-5.4"), "override 命中")
	require.True(t, env.svc.HasManagedPricing("KIMI-K2"), "人民币登记命中，大小写不敏感")
	require.False(t, env.svc.HasManagedPricing("deepseek-v4-pro"))
	require.False(t, env.svc.HasManagedPricing(""))
}

// 人民币层对 0 与缺失字段的处理：0 表示不登记，不覆盖下层价格。
func TestCNYPricingZeroMeansUnregistered(t *testing.T) {
	env := newCNYPricingTestEnv(t, cnyTestCatalogJSON, `{}`,
		`{"version":1,"mode":"convert","cny_per_usd":7.14,
		"models":{"kimi-k2":{"input_cny_per_mtok":4,"output_cny_per_mtok":0}}}`)
	pricing := env.svc.pricingData["kimi-k2"]
	require.InDelta(t, 4.0/7.14, usdPerMTok(pricing.InputCostPerToken), 1e-9)
	require.InDelta(t, 2.24, usdPerMTok(pricing.OutputCostPerToken), 1e-9, "0 不登记，保持目录价")
}

// USD override 里的非价格字段仍然生效；人民币层不得抹掉它们。
func TestCNYPricingKeepsUSDMetadataFields(t *testing.T) {
	env := newCNYPricingTestEnv(t, cnyTestCatalogJSON,
		`{"kimi-k2": {"litellm_provider": "moonshot-cn", "supports_prompt_caching": true}}`,
		cnyTestFileJSON(CNYModeDirect, 7.14))
	pricing := env.svc.pricingData["kimi-k2"]
	require.Equal(t, "moonshot-cn", pricing.LiteLLMProvider)
	require.True(t, pricing.SupportsPromptCaching)
	require.InDelta(t, 4.0, usdPerMTok(pricing.InputCostPerToken), 1e-9)
}
