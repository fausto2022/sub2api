package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// adminPricingCatalogJSON 是价格管理测试用的目录：多供应商、多模型，便于断言分页、
// 搜索、供应商筛选与排序。
const adminPricingCatalogJSON = `{
	"kimi-k2": {"litellm_provider": "moonshot", "mode": "chat",
		"input_cost_per_token": 0.56e-06, "output_cost_per_token": 2.24e-06,
		"cache_read_input_token_cost": 0.14e-06},
	"kimi-k2-thinking": {"litellm_provider": "moonshot", "mode": "chat",
		"input_cost_per_token": 0.56e-06, "output_cost_per_token": 2.24e-06},
	"gpt-5.4": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05},
	"gpt-5.4-mini": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 7.5e-07, "output_cost_per_token": 4.5e-06},
	"deepseek-v4-pro": {"litellm_provider": "deepseek", "mode": "chat",
		"input_cost_per_token": 1e-06, "output_cost_per_token": 9e-06},
	"zhipu-glm-5": {"litellm_provider": "zhipu", "mode": "chat",
		"input_cost_per_token": 1e-06, "output_cost_per_token": 4e-06}
}`

// modelPricingAdminEnv 构造带临时目录、目录缓存与 override/人民币层文件的完整环境。
type modelPricingAdminEnv struct {
	admin        *ModelPricingAdminService
	pricing      *PricingService
	cfg          *config.Config
	dir          string
	catalogFile  string
	overrideFile string
	cnyFile      string
}

func newModelPricingAdminEnv(t *testing.T) *modelPricingAdminEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Pricing.DataDir = dir
	cfg.Pricing.OverrideFile = filepath.Join(dir, "overrides.json")
	cfg.Pricing.CNYOverrideFile = filepath.Join(dir, "cny-overrides.json")
	catalogFile := filepath.Join(dir, "model_pricing.json")
	require.NoError(t, os.WriteFile(catalogFile, []byte(adminPricingCatalogJSON), 0o644))

	pricing := &PricingService{cfg: cfg}
	require.NoError(t, pricing.loadPricingData(catalogFile))

	return &modelPricingAdminEnv{
		admin:        NewModelPricingAdminService(cfg, pricing),
		pricing:      pricing,
		cfg:          cfg,
		dir:          dir,
		catalogFile:  catalogFile,
		overrideFile: cfg.Pricing.OverrideFile,
		cnyFile:      cfg.Pricing.CNYOverrideFile,
	}
}

// writeOverride 预置 override 文件并热重载。
func (e *modelPricingAdminEnv) writeOverride(t *testing.T, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(e.overrideFile, []byte(body), 0o644))
	require.NoError(t, e.pricing.ReloadCustomPricingLayers())
}

// writeCNY 预置人民币层文件并热重载。
func (e *modelPricingAdminEnv) writeCNY(t *testing.T, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(e.cnyFile, []byte(body), 0o644))
	require.NoError(t, e.pricing.ReloadCustomPricingLayers())
}

// requireErrorReason 断言错误对象携带指定的 reason 码。
func requireErrorReason(t *testing.T, err error, want string) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, want, infraerrors.Reason(err), "error: %v", err)
}

// 列表：排序（overridden desc, model asc）、分页上限、搜索与筛选。
func TestModelPricingAdminListSortingPaginationAndFilters(t *testing.T) {
	env := newModelPricingAdminEnv(t)

	all := env.admin.List(ModelPricingListFilter{})
	require.Equal(t, 6, all.Total)
	require.Equal(t, 1, all.Page)
	require.Equal(t, defaultModelPricingPageSize, all.PageSize)
	require.Len(t, all.Items, 6)
	require.Equal(t, "deepseek-v4-pro", all.Items[0].Model, "未覆盖时按模型名升序")
	require.Equal(t, "convert", all.Meta.CNYMode)
	require.InDelta(t, 7.14, all.Meta.CNYPerUSD, 1e-12)
	require.Equal(t, env.overrideFile, all.Meta.OverrideFile)
	require.Equal(t, env.cnyFile, all.Meta.CNYFile)
	require.True(t, all.Meta.Editable)
	require.True(t, all.Meta.CNYEditable)
	require.InDelta(t, 2.5, all.Items[1].InputUSDPerMTok, 1e-9)

	paged := env.admin.List(ModelPricingListFilter{Page: 2, PageSize: 2})
	require.Equal(t, 6, paged.Total, "total 是筛选后的总数，不随分页变化")
	require.Equal(t, 2, paged.Page)
	require.Equal(t, 2, paged.PageSize)
	require.Len(t, paged.Items, 2)
	require.Equal(t, "gpt-5.4-mini", paged.Items[0].Model)
	require.Equal(t, "kimi-k2", paged.Items[1].Model)

	clamped := env.admin.List(ModelPricingListFilter{PageSize: 500})
	require.Equal(t, modelPricingPageSizeLimit, clamped.PageSize, "page_size 上限 100")

	require.Equal(t, 2, env.admin.List(ModelPricingListFilter{Search: "KIMI"}).Total, "大小写不敏感")
	require.Equal(t, 2, env.admin.List(ModelPricingListFilter{Search: "moonshot"}).Total, "可搜供应商")
	require.Equal(t, 2, env.admin.List(ModelPricingListFilter{Provider: "OpenAI"}).Total)

	// 覆盖后 overridden 优先排序，并可用于筛选。
	env.writeOverride(t, `{"zhipu-glm-5": {"input_cost_per_token": 2e-06}}`)
	overridden := true
	filtered := env.admin.List(ModelPricingListFilter{Overridden: &overridden})
	require.Equal(t, 1, filtered.Total)
	require.Equal(t, "zhipu-glm-5", filtered.Items[0].Model)
	require.True(t, filtered.Items[0].Overridden)

	sorted := env.admin.List(ModelPricingListFilter{})
	require.Equal(t, "zhipu-glm-5", sorted.Items[0].Model, "overridden desc 优先")
}

// 写入逐模型覆盖：字段级浅合并、per-token 落盘、保留其它条目、无临时文件残留、热重载生效。
func TestModelPricingAdminUpdateOverrideAtomicWriteAndHotReload(t *testing.T) {
	env := newModelPricingAdminEnv(t)
	env.writeOverride(t, `{"zhipu-glm-5": {"input_cost_per_token": 2e-06}}`)

	entry, err := env.admin.UpdateOverride(ModelPricingOverrideInput{
		Model: "kimi-k2",
		Fields: map[string]*float64{
			"input_usd_per_mtok":           floatPtr(1.2),
			"cache_write_usd_per_mtok":     floatPtr(0),
			"priority_output_usd_per_mtok": nil,
		},
	})
	require.NoError(t, err)
	require.InDelta(t, 1.2, entry.InputUSDPerMTok, 1e-9)
	require.InDelta(t, 2.24, entry.OutputUSDPerMTok, 1e-9, "未提交字段保持目录价")
	require.Zero(t, entry.CacheWriteUSDPerMTok, "显式 0 生效")
	require.True(t, entry.Overridden)

	var persisted map[string]map[string]float64
	body, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &persisted), "覆盖文件必须是合法 JSON")
	require.InDelta(t, 1.2e-6, persisted["kimi-k2"]["input_cost_per_token"], 1e-15, "落盘为 per-token")
	require.Equal(t, float64(0), persisted["kimi-k2"]["cache_creation_input_token_cost"])
	require.Contains(t, persisted, "zhipu-glm-5", "其它模型的覆盖条目必须保留")

	tempFiles, err := filepath.Glob(filepath.Join(env.dir, ".overrides.json.*.tmp"))
	require.NoError(t, err)
	require.Empty(t, tempFiles, "原子写不得残留临时文件")

	billing := NewBillingService(&config.Config{}, env.pricing)
	cost, err := billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 1.2, cost.InputCost, 1e-9, "写入后无需重启即生效")
}

// 字段级 null：删除该字段覆盖，回退人民币层（而不是写 0）。
func TestModelPricingAdminNullFieldFallsBackToCNY(t *testing.T) {
	env := newModelPricingAdminEnv(t)
	env.writeCNY(t, `{"version":1,"mode":"convert","cny_per_usd":7.14,
		"models":{"kimi-k2":{"input_cny_per_mtok":4,"output_cny_per_mtok":16}}}`)

	_, err := env.admin.UpdateOverride(ModelPricingOverrideInput{
		Model: "kimi-k2",
		Fields: map[string]*float64{
			"input_usd_per_mtok":  floatPtr(9),
			"output_usd_per_mtok": floatPtr(20),
		},
	})
	require.NoError(t, err)

	entry, err := env.admin.UpdateOverride(ModelPricingOverrideInput{
		Model:  "kimi-k2",
		Fields: map[string]*float64{"input_usd_per_mtok": nil},
	})
	require.NoError(t, err)
	require.InDelta(t, 4.0/7.14, entry.InputUSDPerMTok, 1e-9, "null 删除后回退人民币层")
	require.InDelta(t, 20, entry.OutputUSDPerMTok, 1e-9, "其它覆盖字段保持不变")

	var persisted map[string]map[string]float64
	body, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &persisted))
	_, hasInput := persisted["kimi-k2"]["input_cost_per_token"]
	require.False(t, hasInput, "被 null 删除的字段不得写回文件")
	require.Contains(t, persisted["kimi-k2"], "output_cost_per_token")
}

// 恢复内置：删除 override 条目后回到目录价。
func TestModelPricingAdminDeleteOverrideRestoresBuiltin(t *testing.T) {
	env := newModelPricingAdminEnv(t)
	_, err := env.admin.UpdateOverride(ModelPricingOverrideInput{
		Model:  "kimi-k2",
		Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(9)},
	})
	require.NoError(t, err)

	entry, err := env.admin.DeleteOverride("kimi-k2")
	require.NoError(t, err)
	require.False(t, entry.Overridden)
	require.InDelta(t, 0.56, entry.InputUSDPerMTok, 1e-9, "恢复目录价")

	body, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(body), "删除后文件为空对象")
}

// 非法值统一 400 INVALID_MODEL_PRICE，且不落盘。
func TestModelPricingAdminInvalidPriceRejected(t *testing.T) {
	env := newModelPricingAdminEnv(t)
	env.writeOverride(t, `{"zhipu-glm-5": {"input_cost_per_token": 2e-06}}`)
	before, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)

	cases := map[string]map[string]*float64{
		"negative":       {"input_usd_per_mtok": floatPtr(-1)},
		"at upper bound": {"input_usd_per_mtok": floatPtr(ModelPriceUSDPerMTokLimit)},
		"above bound":    {"output_usd_per_mtok": floatPtr(1e5)},
		"unknown field":  {"cache_hit_usd_per_mtok": floatPtr(1)},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := env.admin.UpdateOverride(ModelPricingOverrideInput{Model: "kimi-k2", Fields: fields})
			requireErrorReason(t, err, "INVALID_MODEL_PRICE")
		})
	}

	after, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)
	require.Equal(t, before, after, "非法输入不得修改覆盖文件")
}

// 未配置 override_file 时编辑禁用（明确错误码）。
func TestModelPricingAdminOverrideFileNotConfigured(t *testing.T) {
	env := newModelPricingAdminEnv(t)
	env.cfg.Pricing.OverrideFile = ""

	_, err := env.admin.UpdateOverride(ModelPricingOverrideInput{
		Model:  "kimi-k2",
		Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(1)},
	})
	requireErrorReason(t, err, "PRICING_OVERRIDE_FILE_NOT_CONFIGURED")

	_, err = env.admin.DeleteOverride("kimi-k2")
	requireErrorReason(t, err, "PRICING_OVERRIDE_FILE_NOT_CONFIGURED")
}

// 人民币口径：默认值、模式切换、汇率校验、种子导入数据。
func TestModelPricingAdminCurrencyModeAndRate(t *testing.T) {
	env := newModelPricingAdminEnv(t)

	state := env.admin.Currency()
	require.Equal(t, CNYModeConvert, state.Mode)
	require.InDelta(t, 7.14, state.CNYPerUSD, 1e-12)
	require.Empty(t, state.Models)
	require.Len(t, state.SeedModels, 3, "预填 3 个已知人民币口径模型")
	var kimiSeed *ModelPricingCNYEntry
	for i := range state.SeedModels {
		if state.SeedModels[i].Model == "kimi-k2" {
			kimiSeed = &state.SeedModels[i]
		}
	}
	require.NotNil(t, kimiSeed)
	require.InDelta(t, 4, kimiSeed.InputCNYPerMTok, 1e-12)
	require.InDelta(t, 4.0/7.14, kimiSeed.EffectiveInputUSDPerMTok, 1e-9)

	_, err := env.admin.UpsertCurrencyModel("kimi-k2", map[string]*float64{
		"input_cny_per_mtok":  floatPtr(4),
		"output_cny_per_mtok": floatPtr(16),
	})
	require.NoError(t, err)
	require.FileExists(t, env.cnyFile)

	billing := NewBillingService(&config.Config{}, env.pricing)
	cost, err := billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 4.0/7.14, cost.InputCost, 1e-9)

	direct, err := env.admin.UpdateCurrency(CNYModeDirect, nil)
	require.NoError(t, err)
	require.Equal(t, CNYModeDirect, direct.Mode)
	require.Len(t, direct.Models, 1)
	require.InDelta(t, 4, direct.Models[0].EffectiveInputUSDPerMTok, 1e-9)
	cost, err = billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 4, cost.InputCost, 1e-9, "direct 模式 ¥1 = $1")

	convert, err := env.admin.UpdateCurrency(CNYModeConvert, floatPtr(7.5))
	require.NoError(t, err)
	require.InDelta(t, 7.5, convert.CNYPerUSD, 1e-12)
	require.InDelta(t, 4.0/7.5, convert.Models[0].EffectiveInputUSDPerMTok, 1e-9)
	cost, err = billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 4.0/7.5, cost.InputCost, 1e-9)

	_, err = env.admin.UpdateCurrency("bogus", nil)
	requireErrorReason(t, err, "INVALID_CNY_MODE")
	_, err = env.admin.UpdateCurrency(CNYModeConvert, floatPtr(0))
	requireErrorReason(t, err, "INVALID_CNY_RATE")
	_, err = env.admin.UpdateCurrency(CNYModeConvert, floatPtr(-1))
	requireErrorReason(t, err, "INVALID_CNY_RATE")

	tempFiles, err := filepath.Glob(filepath.Join(env.dir, ".cny-overrides.json.*.tmp"))
	require.NoError(t, err)
	require.Empty(t, tempFiles, "原子写不得残留临时文件")
}

// 人民币逐模型增删：0 表示不登记，全空即删除条目；非法值 400 INVALID_CNY_PRICE。
func TestModelPricingAdminCurrencyModelUpsertAndDelete(t *testing.T) {
	env := newModelPricingAdminEnv(t)

	state, err := env.admin.UpsertCurrencyModel("kimi-k2", map[string]*float64{
		"input_cny_per_mtok":       floatPtr(4),
		"output_cny_per_mtok":      floatPtr(16),
		"cache_read_cny_per_mtok":  floatPtr(0),
		"image_input_cny_per_mtok": nil,
	})
	require.NoError(t, err)
	require.Len(t, state.Models, 1)
	require.InDelta(t, 4, state.Models[0].InputCNYPerMTok, 1e-12)
	require.Zero(t, state.Models[0].CacheReadCNYPerMTok, "0 表示该字段不登记")

	billing := NewBillingService(&config.Config{}, env.pricing)
	cost, err := billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 4.0/7.14, cost.InputCost, 1e-9)

	// 归零全部字段 → 删除该模型条目。
	state, err = env.admin.UpsertCurrencyModel("kimi-k2", map[string]*float64{
		"input_cny_per_mtok":  floatPtr(0),
		"output_cny_per_mtok": floatPtr(0),
	})
	require.NoError(t, err)
	require.Empty(t, state.Models)
	cost, err = billing.CalculateCost("kimi-k2", UsageTokens{InputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.56, cost.InputCost, 1e-9, "人民币登记删除后回到目录价")

	// 重新登记后 DELETE 删除。
	_, err = env.admin.UpsertCurrencyModel("gpt-5.4", map[string]*float64{"input_cny_per_mtok": floatPtr(2)})
	require.NoError(t, err)
	require.Len(t, env.admin.Currency().Models, 1)
	state, err = env.admin.DeleteCurrencyModel("gpt-5.4")
	require.NoError(t, err)
	require.Empty(t, state.Models)

	_, err = env.admin.UpsertCurrencyModel("kimi-k2", map[string]*float64{
		"input_cny_per_mtok": floatPtr(ModelPriceCNYPerMTokLimit),
	})
	requireErrorReason(t, err, "INVALID_CNY_PRICE")
	_, err = env.admin.UpsertCurrencyModel("kimi-k2", map[string]*float64{
		"cache_hit_cny_per_mtok": floatPtr(1),
	})
	requireErrorReason(t, err, "INVALID_CNY_PRICE")
}

// 美元覆盖与人民币登记同时存在时，USD override 在列表元数据里标记 overridden，
// 人民币等值随口径换算。
func TestModelPricingAdminListShowsCNYEquivalentAndOverridden(t *testing.T) {
	env := newModelPricingAdminEnv(t)
	env.writeCNY(t, `{"version":1,"mode":"direct","models":{
		"kimi-k2":{"input_cny_per_mtok":4,"output_cny_per_mtok":16}}}`)
	env.writeOverride(t, `{"kimi-k2": {"cache_read_input_token_cost": 5e-06}}`)

	result := env.admin.List(ModelPricingListFilter{Search: "kimi-k2"})
	require.Equal(t, 2, result.Total)
	for _, item := range result.Items {
		require.Equal(t, "direct", result.Meta.CNYMode)
		if item.Model != "kimi-k2" {
			continue
		}
		require.True(t, item.Overridden)
		require.NotNil(t, item.CNY)
		require.InDelta(t, 4, item.CNY.Input, 1e-12)
		require.InDelta(t, 16, item.CNY.Output, 1e-12)
		require.InDelta(t, 4, item.InputUSDPerMTok, 1e-9, "direct 口径下有效 USD 价等于人民币价")
		require.InDelta(t, 5, item.CacheReadUSDPerMTok, 1e-9, "USD 覆盖优先于人民币层")
	}
}

// ===== catalog admin API 覆盖（_catalog 元数据、新增/删除模型、峰谷时段）=====

// catalogAdminEnv 在既有 admin 环境之上补一个可用的 settings 仓储，
// 让峰谷时段设置能真正持久化（不依赖数据库）。
type catalogAdminEnv struct {
	*modelPricingAdminEnv
	settings *memorySettingRepository
	adminSvc *ModelPricingAdminService
}

// memorySettingRepository 是 SettingRepository 的内存实现（测试专用）。
type memorySettingRepository struct {
	values map[string]string
}

func newMemorySettingRepository() *memorySettingRepository {
	return &memorySettingRepository{values: map[string]string{}}
}

func (r *memorySettingRepository) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *memorySettingRepository) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *memorySettingRepository) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *memorySettingRepository) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *memorySettingRepository) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *memorySettingRepository) GetAll(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *memorySettingRepository) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

// newCatalogAdminEnv 构造带 settings 仓储的模型价格管理环境。
func newCatalogAdminEnv(t *testing.T) *catalogAdminEnv {
	t.Helper()
	base := newModelPricingAdminEnv(t)
	repo := newMemorySettingRepository()
	return &catalogAdminEnv{
		modelPricingAdminEnv: base,
		settings:             repo,
		adminSvc:             NewModelPricingAdminServiceWithSettings(base.cfg, base.pricing, repo),
	}
}

// readOverrideFile 读回 override 文件为通用 map，便于断言 `_catalog` 落盘结构。
func (e *catalogAdminEnv) readOverrideFile(t *testing.T) map[string]map[string]json.RawMessage {
	t.Helper()
	body, err := os.ReadFile(e.overrideFile)
	require.NoError(t, err)
	var entries map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &entries), "override 文件必须是合法 JSON")
	return entries
}

// 写入 `_catalog`：峰谷双价与元数据落盘，列表视图回读，计费按峰谷生效。
func TestModelPricingAdminUpdateOverrideWritesCatalogPeakMeta(t *testing.T) {
	env := newCatalogAdminEnv(t)

	peakEnabled := true
	platform := "moonshot"
	entry, err := env.adminSvc.UpdateOverride(ModelPricingOverrideInput{
		Model:       "kimi-k2",
		Fields:      map[string]*float64{"input_usd_per_mtok": floatPtr(2.142)},
		Platform:    &platform,
		Custom:      &peakEnabled,
		PeakEnabled: &peakEnabled,
		Peak: map[string]*float64{
			"input_usd_per_mtok":  floatPtr(3),
			"output_usd_per_mtok": floatPtr(12),
		},
		OffPeak: map[string]*float64{
			"input_usd_per_mtok": floatPtr(1.5),
		},
		UpdatedBy: 7,
	})
	require.NoError(t, err)

	// 视图：基础价、平台、峰谷、操作者都要回读出来。
	require.Equal(t, "moonshot", entry.Platform)
	require.True(t, entry.Custom)
	require.True(t, entry.PeakEnabled)
	require.InDelta(t, 2.142, entry.InputUSDPerMTok, 1e-9)
	require.InDelta(t, 3, entry.Peak.InputUSDPerMTok, 1e-9)
	require.InDelta(t, 12, entry.Peak.OutputUSDPerMTok, 1e-9)
	require.InDelta(t, 1.5, entry.OffPeak.InputUSDPerMTok, 1e-9)
	require.Zero(t, entry.OffPeak.OutputUSDPerMTok, "未提交的峰谷键保持 0")
	require.Equal(t, int64(7), entry.UpdatedBy)
	require.NotEmpty(t, entry.UpdatedAt, "updated_at 由服务端自动填写")

	// 落盘：`_catalog` 用 LiteLLM 字段名 + per-token 单位。
	persisted := env.readOverrideFile(t)
	require.Contains(t, persisted, "kimi-k2")
	var meta map[string]any
	require.NoError(t, json.Unmarshal(persisted["kimi-k2"][CatalogMetaKey], &meta))
	require.Equal(t, "moonshot", meta["platform"])
	require.Equal(t, true, meta["custom"])
	require.Equal(t, true, meta["peak_enabled"])
	peakSet, ok := meta["peak"].(map[string]any)
	require.True(t, ok, "peak 必须是对象")
	require.InDelta(t, 3e-6, peakSet["input_cost_per_token"], 1e-15, "峰价落盘为 per-token")
	var basePrice float64
	require.NoError(t, json.Unmarshal(persisted["kimi-k2"]["input_cost_per_token"], &basePrice))
	require.InDelta(t, 2.142e-6, basePrice, 1e-15, "基础价落盘为 per-token")

	// 计费：启用峰谷后成本只会落在峰价或谷价，不会叠加其他倍率。
	// 峰谷在统一计费入口（网关走的 CalculateCostUnified）按请求时刻生效。
	billing := NewBillingService(&config.Config{}, env.pricing)
	resolver := NewModelPricingResolver(nil, billing)
	cost, err := billing.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "kimi-k2",
		Tokens: UsageTokens{InputTokens: 1_000_000}, RateMultiplier: 1, Resolver: resolver,
	})
	require.NoError(t, err)
	require.True(t,
		almostEqual(cost.InputCost, 1.5) || almostEqual(cost.InputCost, 3),
		"成本 %v 必须是谷价 1.5 或峰价 3", cost.InputCost)
}

// almostEqual 是浮点近似比较（1e-9 容差）。
func almostEqual(a, b float64) bool {
	diff := a - b
	return diff < 1e-9 && diff > -1e-9
}

// `peak_enabled=false` 只关开关，保留已配置的峰谷价（便于再次开启）。
func TestModelPricingAdminPeakEnabledFalseKeepsRates(t *testing.T) {
	env := newCatalogAdminEnv(t)
	enabled := true
	_, err := env.adminSvc.UpdateOverride(ModelPricingOverrideInput{
		Model:       "kimi-k2",
		Fields:      map[string]*float64{"input_usd_per_mtok": floatPtr(2)},
		PeakEnabled: &enabled,
		Peak:        map[string]*float64{"input_usd_per_mtok": floatPtr(4)},
	})
	require.NoError(t, err)

	disabled := false
	entry, err := env.adminSvc.UpdateOverride(ModelPricingOverrideInput{
		Model:       "kimi-k2",
		PeakEnabled: &disabled,
	})
	require.NoError(t, err)
	require.False(t, entry.PeakEnabled)
	require.InDelta(t, 4, entry.Peak.InputUSDPerMTok, 1e-9, "关闭开关不应清掉已填的峰价")

	// 关闭后计费只用基础价（峰谷不再选取）。
	billing := NewBillingService(&config.Config{}, env.pricing)
	resolver := NewModelPricingResolver(nil, billing)
	cost, err := billing.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "kimi-k2",
		Tokens: UsageTokens{InputTokens: 1_000_000}, RateMultiplier: 1, Resolver: resolver,
	})
	require.NoError(t, err)
	require.InDelta(t, 2, cost.InputCost, 1e-9)
}

// 峰谷价非法（负值/越界/未知键/不支持的键）统一 400 INVALID_MODEL_PRICE，且不落盘。
func TestModelPricingAdminPeakRatesValidation(t *testing.T) {
	env := newCatalogAdminEnv(t)
	env.writeOverride(t, `{"kimi-k2": {"input_cost_per_token": 5.6e-07}}`)
	before, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)

	cases := map[string]struct {
		peak    map[string]*float64
		offPeak map[string]*float64
	}{
		"negative peak":    {peak: map[string]*float64{"input_usd_per_mtok": floatPtr(-1)}},
		"peak over bound":  {peak: map[string]*float64{"input_usd_per_mtok": floatPtr(ModelPriceUSDPerMTokLimit)}},
		"unknown peak key": {peak: map[string]*float64{"cache_hit_usd_per_mtok": floatPtr(1)}},
		"unsupported key":  {peak: map[string]*float64{"priority_input_usd_per_mtok": floatPtr(1)}},
		"unknown offpeak":  {offPeak: map[string]*float64{"bogus": floatPtr(1)}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := env.adminSvc.UpdateOverride(ModelPricingOverrideInput{
				Model:   "kimi-k2",
				Peak:    tc.peak,
				OffPeak: tc.offPeak,
			})
			requireErrorReason(t, err, "INVALID_MODEL_PRICE")
		})
	}

	after, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)
	require.Equal(t, before, after, "非法输入不得修改覆盖文件")
}

// 平台白名单：未登记的平台被拒，已登记的平台通过。
func TestModelPricingAdminPlatformWhitelist(t *testing.T) {
	env := newCatalogAdminEnv(t)

	bogus := "not-a-platform"
	_, err := env.adminSvc.UpdateOverride(ModelPricingOverrideInput{
		Model:    "kimi-k2",
		Platform: &bogus,
	})
	requireErrorReason(t, err, "INVALID_MODEL_PRICE")

	valid := "deepseek"
	entry, err := env.adminSvc.UpdateOverride(ModelPricingOverrideInput{
		Model:    "kimi-k2",
		Platform: &valid,
	})
	require.NoError(t, err)
	require.Equal(t, "deepseek", entry.Platform)
}

// 新增模型：目录外模型可独立成条，`_catalog.custom` 与平台落盘，列表可见。
func TestModelPricingAdminCreateModelOutsideCatalog(t *testing.T) {
	env := newCatalogAdminEnv(t)

	entry, err := env.adminSvc.CreateModel(ModelPricingCreateInput{
		Model:    "my-private-model",
		Platform: "openai",
		Fields: map[string]*float64{
			"input_usd_per_mtok":  floatPtr(1.5),
			"output_usd_per_mtok": floatPtr(6),
		},
		UpdatedBy: 3,
	})
	require.NoError(t, err)
	require.Equal(t, "my-private-model", entry.Model)
	require.True(t, entry.Custom)
	require.Equal(t, "openai", entry.Platform)
	require.InDelta(t, 1.5, entry.InputUSDPerMTok, 1e-9)
	require.InDelta(t, 6, entry.OutputUSDPerMTok, 1e-9)
	require.Equal(t, int64(3), entry.UpdatedBy)

	// 列表可见，并且可被 custom 筛选命中。
	list := env.adminSvc.List(ModelPricingListFilter{Search: "my-private-model"})
	require.Equal(t, 1, list.Total)
	customOnly := true
	require.Equal(t, 1, env.adminSvc.List(ModelPricingListFilter{Custom: &customOnly}).Total)

	// 目录外模型必须真正参与计费。
	billing := NewBillingService(&config.Config{}, env.pricing)
	cost, err := billing.CalculateCost("my-private-model", UsageTokens{InputTokens: 1_000_000}, 1)
	require.NoError(t, err)
	require.InDelta(t, 1.5, cost.InputCost, 1e-9)
}

// TestModelPricingAdminCreateModelConflict 覆盖新增模型与目录/自定义条目重名。
func TestModelPricingAdminCreateModelConflict(t *testing.T) {
	env := newCatalogAdminEnv(t)

	_, err := env.adminSvc.CreateModel(ModelPricingCreateInput{
		Model:    "kimi-k2", // 目录里已有
		Platform: "moonshot",
		Fields:   map[string]*float64{"input_usd_per_mtok": floatPtr(1)},
	})
	requireErrorReason(t, err, "MODEL_ALREADY_EXISTS")
	require.True(t, infraerrors.IsConflict(err), "重名必须是 409 Conflict，前端据此提示转为覆盖")

	// 自定义模型重名同样冲突。
	_, err = env.adminSvc.CreateModel(ModelPricingCreateInput{
		Model:    "new-model",
		Platform: "openai",
		Fields:   map[string]*float64{"input_usd_per_mtok": floatPtr(1)},
	})
	require.NoError(t, err)
	_, err = env.adminSvc.CreateModel(ModelPricingCreateInput{
		Model:    "NEW-MODEL",
		Platform: "openai",
		Fields:   map[string]*float64{"input_usd_per_mtok": floatPtr(2)},
	})
	requireErrorReason(t, err, "MODEL_ALREADY_EXISTS")

	after, err := os.ReadFile(env.overrideFile)
	require.NoError(t, err)
	var entries map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(after, &entries))
	require.Len(t, entries, 1, "冲突的新增不得写盘")
	require.Contains(t, entries, "new-model")
	require.NotContains(t, entries, "kimi-k2", "目录内置模型未被新增流程改动")
}

// 新增模型校验：模型名格式、平台必填/白名单、价格范围、至少要有一个价格。
func TestModelPricingAdminCreateModelValidation(t *testing.T) {
	env := newCatalogAdminEnv(t)

	cases := map[string]ModelPricingCreateInput{
		"empty model":       {Model: "", Platform: "openai", Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(1)}},
		"invalid chars":     {Model: "bad model!", Platform: "openai", Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(1)}},
		"underscore prefix": {Model: "_catalog", Platform: "openai", Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(1)}},
		"missing platform":  {Model: "m1", Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(1)}},
		"bad platform":      {Model: "m2", Platform: "nope", Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(1)}},
		"negative price":    {Model: "m3", Platform: "openai", Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(-1)}},
		"price over bound":  {Model: "m4", Platform: "openai", Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(ModelPriceUSDPerMTokLimit)}},
		"unknown price key": {Model: "m5", Platform: "openai", Fields: map[string]*float64{"cache_hit_usd_per_mtok": floatPtr(1)}},
		"no price at all":   {Model: "m6", Platform: "openai"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := env.adminSvc.CreateModel(input)
			requireErrorReason(t, err, "INVALID_MODEL_PRICE")
		})
	}
}

// 删除自定义模型：条目消失、恢复内置价；内置模型不允许删除。
func TestModelPricingAdminDeleteModel(t *testing.T) {
	env := newCatalogAdminEnv(t)

	_, err := env.adminSvc.CreateModel(ModelPricingCreateInput{
		Model:    "temp-model",
		Platform: "openai",
		Fields:   map[string]*float64{"input_usd_per_mtok": floatPtr(2)},
	})
	require.NoError(t, err)
	require.Equal(t, 1, env.adminSvc.List(ModelPricingListFilter{Search: "temp-model"}).Total)

	require.NoError(t, env.adminSvc.DeleteModel("temp-model"))
	require.Equal(t, 0, env.adminSvc.List(ModelPricingListFilter{Search: "temp-model"}).Total,
		"删除后模型从价格表消失")

	persisted := env.readOverrideFile(t)
	require.NotContains(t, persisted, "temp-model")

	// 目录内置模型不可删（应走「恢复内置」）：即使它有 `_catalog` 元数据，
	// 只要 custom 不为 true 就拒绝，避免把运营者的覆盖配置当自定义模型删掉。
	env.writeOverride(t, `{"kimi-k2": {"input_cost_per_token": 5.6e-07,
		"_catalog": {"platform": "moonshot", "peak_enabled": true}}}`)
	err = env.adminSvc.DeleteModel("kimi-k2")
	requireErrorReason(t, err, "MODEL_DELETE_BUILTIN_REJECTED")

	// 不存在的模型返回 404。
	err = env.adminSvc.DeleteModel("never-existed")
	requireErrorReason(t, err, "MODEL_PRICING_NOT_FOUND")
}

// 删除时未配置 override 文件 → 明确错误码。
func TestModelPricingAdminCreateDeleteRequireOverrideFile(t *testing.T) {
	env := newCatalogAdminEnv(t)
	env.cfg.Pricing.OverrideFile = ""

	_, err := env.adminSvc.CreateModel(ModelPricingCreateInput{
		Model: "m", Platform: "openai",
		Fields: map[string]*float64{"input_usd_per_mtok": floatPtr(1)},
	})
	requireErrorReason(t, err, "PRICING_OVERRIDE_FILE_NOT_CONFIGURED")
	requireErrorReason(t, env.adminSvc.DeleteModel("m"), "PRICING_OVERRIDE_FILE_NOT_CONFIGURED")
}

// 峰谷时段：默认官方口径 → 保存自定义 → 持久化 → 重新 LoadPeakSettings 回填 → 热生效。
func TestModelPricingAdminPeakSettingsRoundTrip(t *testing.T) {
	env := newCatalogAdminEnv(t)
	ctx := context.Background()

	defaults := env.adminSvc.PeakSettings(ctx)
	require.Equal(t, "Asia/Shanghai", defaults.Timezone)
	require.Len(t, defaults.Periods, 2, "未配置时回落官方口径")

	saved, err := env.adminSvc.UpdatePeakSettings(ctx, PeakPricingSettings{
		Timezone:       "UTC",
		WeekdayOnly:    true,
		WeekendOffPeak: true,
		Periods:        []PeakPricingPeriod{{Start: "09:00", End: "12:00"}, {Start: "14:00", End: "18:00"}},
	})
	require.NoError(t, err)
	require.Equal(t, "UTC", saved.Timezone)
	require.Len(t, saved.Periods, 2)

	// 持久化到 settings 的 peak_pricing_settings 键。
	raw, err := env.settings.GetValue(ctx, SettingKeyPeakPricingSettings)
	require.NoError(t, err)
	var stored PeakPricingSettings
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, "UTC", stored.Timezone)
	require.Len(t, stored.Periods, 2)

	// 热生效：PricingService 立即读到新设置。
	require.Equal(t, "UTC", env.pricing.PeakPricingSettings().Timezone)

	// 模拟重启：新建一个 PricingService，再从 settings 回填。
	restarted := &PricingService{cfg: env.cfg}
	restartedSvc := NewModelPricingAdminServiceWithSettings(env.cfg, restarted, env.settings)
	require.Equal(t, "Asia/Shanghai", restarted.PeakPricingSettings().Timezone, "重启前是默认值")
	loaded := restartedSvc.LoadPeakSettings(ctx)
	require.Equal(t, "UTC", loaded.Timezone)
	require.Equal(t, "UTC", restarted.PeakPricingSettings().Timezone, "启动时注入 PricingService")
	require.Len(t, restarted.PeakPricingSettings().Periods, 2)
}

// 峰谷时段校验：非法时区与非法时段 400 INVALID_PEAK_PRICING_SETTINGS，且不落盘。
func TestModelPricingAdminPeakSettingsValidation(t *testing.T) {
	env := newCatalogAdminEnv(t)
	ctx := context.Background()

	_, err := env.adminSvc.UpdatePeakSettings(ctx, PeakPricingSettings{
		Timezone: "Mars/Olympus",
		Periods:  []PeakPricingPeriod{{Start: "09:00", End: "12:00"}},
	})
	requireErrorReason(t, err, "INVALID_PEAK_PRICING_SETTINGS")

	_, err = env.adminSvc.UpdatePeakSettings(ctx, PeakPricingSettings{
		Periods: []PeakPricingPeriod{{Start: "09:00", End: "09:00"}},
	})
	requireErrorReason(t, err, "INVALID_PEAK_PRICING_SETTINGS")

	_, err = env.adminSvc.UpdatePeakSettings(ctx, PeakPricingSettings{
		Periods: []PeakPricingPeriod{{Start: "9am", End: "12:00"}},
	})
	requireErrorReason(t, err, "INVALID_PEAK_PRICING_SETTINGS")

	_, err = env.settings.GetValue(ctx, SettingKeyPeakPricingSettings)
	require.Error(t, err, "校验失败不得写入 settings")
}

// 列表返回峰谷字段与 catalog 元数据；无自定义时为零值（不是省略）。
func TestModelPricingAdminListReturnsPeakAndCatalogFields(t *testing.T) {
	env := newCatalogAdminEnv(t)
	enabled := true
	_, err := env.adminSvc.UpdateOverride(ModelPricingOverrideInput{
		Model:       "kimi-k2",
		Fields:      map[string]*float64{"input_usd_per_mtok": floatPtr(2.142)},
		Custom:      &enabled,
		PeakEnabled: &enabled,
		Peak:        map[string]*float64{"input_usd_per_mtok": floatPtr(4)},
		OffPeak:     map[string]*float64{"input_usd_per_mtok": floatPtr(2.142)},
	})
	require.NoError(t, err)

	list := env.adminSvc.List(ModelPricingListFilter{Search: "kimi-k2"})
	require.Equal(t, 2, list.Total)
	for _, item := range list.Items {
		if item.Model != "kimi-k2" {
			// 未自定义的模型：字段存在且为零值。
			require.False(t, item.Custom)
			require.False(t, item.PeakEnabled)
			require.Empty(t, item.Platform)
			require.True(t, item.Peak.isEmpty())
			require.True(t, item.OffPeak.isEmpty())
			continue
		}
		require.True(t, item.Custom)
		require.True(t, item.PeakEnabled)
		require.InDelta(t, 4, item.Peak.InputUSDPerMTok, 1e-9)
		require.InDelta(t, 2.142, item.OffPeak.InputUSDPerMTok, 1e-9)
		require.Equal(t, PricingSourceCatalog, item.Source, "自定义条目来源标为 catalog")
	}

	// meta 里带 catalog 开关与峰谷设置。
	require.True(t, list.Meta.CatalogAuthoritative)
	require.Equal(t, "Asia/Shanghai", list.Meta.PeakSettings.Timezone)

	// peak_enabled 筛选。
	peakOnly := true
	require.Equal(t, 1, env.adminSvc.List(ModelPricingListFilter{PeakEnabled: &peakOnly}).Total)
}
