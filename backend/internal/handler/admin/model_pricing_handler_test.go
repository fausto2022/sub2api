package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// modelPricingHandlerCatalogJSON 是 handler 测试用目录。
const modelPricingHandlerCatalogJSON = `{
	"kimi-k2": {"litellm_provider": "moonshot", "mode": "chat",
		"input_cost_per_token": 0.56e-06, "output_cost_per_token": 2.24e-06},
	"gpt-5.4": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05}
}`

// modelPricingEnvelope 是 API 统一响应外壳。
type modelPricingEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Reason  string          `json:"reason"`
	Data    json.RawMessage `json:"data"`
}

// newModelPricingHandlerRouter 构造挂载全部模型价格端点的测试路由。
func newModelPricingHandlerRouter(t *testing.T) *gin.Engine {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Pricing.DataDir = dir
	cfg.Pricing.OverrideFile = filepath.Join(dir, "overrides.json")
	cfg.Pricing.CNYOverrideFile = filepath.Join(dir, "cny-overrides.json")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model_pricing.json"), []byte(modelPricingHandlerCatalogJSON), 0o644))

	pricingService := service.NewPricingService(cfg, nil)
	require.NoError(t, pricingService.ReloadCustomPricingLayers())
	adminService := service.NewModelPricingAdminService(cfg, pricingService)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewModelPricingHandler(adminService)
	router.GET("/admin/model-pricing", h.List)
	router.PUT("/admin/model-pricing/overrides/:model", h.UpdateOverride)
	router.DELETE("/admin/model-pricing/overrides/:model", h.DeleteOverride)
	router.GET("/admin/model-pricing/currency", h.GetCurrency)
	router.PUT("/admin/model-pricing/currency", h.UpdateCurrency)
	router.PUT("/admin/model-pricing/currency/models/:model", h.UpsertCurrencyModel)
	router.DELETE("/admin/model-pricing/currency/models/:model", h.DeleteCurrencyModel)
	router.POST("/admin/model-pricing/models", h.CreateModel)
	router.DELETE("/admin/model-pricing/models/:model", h.DeleteModel)
	router.GET("/admin/model-pricing/peak-settings", h.GetPeakSettings)
	router.PUT("/admin/model-pricing/peak-settings", h.UpdatePeakSettings)
	return router
}

// doModelPricingRequest 发起请求并解析统一响应外壳。
func doModelPricingRequest(t *testing.T, router *gin.Engine, method, path, body string) (*httptest.ResponseRecorder, modelPricingEnvelope) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	var envelope modelPricingEnvelope
	if recorder.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope), "body: %s", recorder.Body.String())
	}
	return recorder, envelope
}

// 列表与覆盖写入/恢复的完整链路。
func TestModelPricingHandlerListAndOverrideFlow(t *testing.T) {
	router := newModelPricingHandlerRouter(t)

	recorder, envelope := doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?page=1&page_size=1", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 0, envelope.Code)

	var list dto.ModelPricingListResponse
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 2, list.Total)
	require.Len(t, list.Items, 1, "服务端分页")
	require.Equal(t, 1, list.PageSize)
	require.Equal(t, "gpt-5.4", list.Items[0].Model, "未覆盖时按模型名升序")
	require.Equal(t, "convert", list.Meta.CNYMode)
	require.InDelta(t, 7.14, list.Meta.CNYPerUSD, 1e-12)
	require.NotEmpty(t, list.Meta.OverrideFile)
	require.True(t, list.Meta.Editable)

	// 写入：input 覆盖，output 用 null 删除（回退目录价）。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut, "/admin/model-pricing/overrides/kimi-k2",
		`{"input_usd_per_mtok": 1.2, "output_usd_per_mtok": null}`)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())
	var item dto.ModelPricingItem
	require.NoError(t, json.Unmarshal(envelope.Data, &item))
	require.Equal(t, "kimi-k2", item.Model)
	require.InDelta(t, 1.2, item.InputUSDPerMTok, 1e-9)
	require.InDelta(t, 2.24, item.OutputUSDPerMTok, 1e-9, "null 删除后回退目录价")
	require.True(t, item.Overridden)

	// 覆盖筛选。
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?overridden=true", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 1, list.Total)
	require.Equal(t, "kimi-k2", list.Items[0].Model)

	// 恢复内置。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodDelete, "/admin/model-pricing/overrides/kimi-k2", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &item))
	require.False(t, item.Overridden)
	require.InDelta(t, 0.56, item.InputUSDPerMTok, 1e-9)
}

// 非法请求体/非法价格统一 400 INVALID_MODEL_PRICE。
func TestModelPricingHandlerOverrideValidation(t *testing.T) {
	router := newModelPricingHandlerRouter(t)

	cases := map[string]string{
		"negative":       `{"input_usd_per_mtok": -1}`,
		"at upper bound": `{"input_usd_per_mtok": 10000}`,
		"unknown field":  `{"cache_hit_usd_per_mtok": 1}`,
		"wrong type":     `{"input_usd_per_mtok": "abc"}`,
		"malformed json": `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			recorder, envelope := doModelPricingRequest(t, router, http.MethodPut,
				"/admin/model-pricing/overrides/kimi-k2", body)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "INVALID_MODEL_PRICE", envelope.Reason, "body: %s", recorder.Body.String())
		})
	}
}

// 人民币口径读写与校验错误码。
func TestModelPricingHandlerCurrencyFlow(t *testing.T) {
	router := newModelPricingHandlerRouter(t)

	recorder, envelope := doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing/currency", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	var currency dto.ModelPricingCurrencyResponse
	require.NoError(t, json.Unmarshal(envelope.Data, &currency))
	require.Equal(t, "convert", currency.Mode)
	require.InDelta(t, 7.14, currency.CNYPerUSD, 1e-12)
	require.Empty(t, currency.Models)
	require.Len(t, currency.SeedModels, 3, "导入种子随响应下发")

	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut, "/admin/model-pricing/currency", `{"mode":"direct"}`)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &currency))
	require.Equal(t, "direct", currency.Mode)

	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut,
		"/admin/model-pricing/currency/models/kimi-k2",
		`{"input_cny_per_mtok": 4, "output_cny_per_mtok": 16}`)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())
	require.NoError(t, json.Unmarshal(envelope.Data, &currency))
	require.Len(t, currency.Models, 1)
	require.Equal(t, "kimi-k2", currency.Models[0].Model)
	require.InDelta(t, 4, currency.Models[0].InputCNYPerMTok, 1e-12)
	require.InDelta(t, 4, currency.Models[0].EffectiveInputUSDPerMTok, 1e-9, "direct 口径有效 USD 价等于人民币价")

	recorder, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing/currency", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &currency))
	require.Equal(t, "direct", currency.Mode, "口径切换持久化")
	require.Len(t, currency.Models, 1)

	recorder, envelope = doModelPricingRequest(t, router, http.MethodDelete,
		"/admin/model-pricing/currency/models/kimi-k2", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &currency))
	require.Empty(t, currency.Models)

	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut,
		"/admin/model-pricing/currency/models/kimi-k2", `{"input_cny_per_mtok": 1000000}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "INVALID_CNY_PRICE", envelope.Reason)

	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut, "/admin/model-pricing/currency", `{"mode":"bogus"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "INVALID_CNY_MODE", envelope.Reason)

	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut, "/admin/model-pricing/currency",
		`{"mode":"convert","cny_per_usd":0}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "INVALID_CNY_RATE", envelope.Reason)

	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut, "/admin/model-pricing/currency", `{`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "INVALID_CNY_MODE", envelope.Reason, "请求体绑定失败也走明确错误码")
}

// 目录价 `_catalog`：PATCH 写峰谷 + 平台，列表回读峰谷字段与 catalog 元数据。
func TestModelPricingHandlerCatalogMetaFlow(t *testing.T) {
	router := newModelPricingHandlerRouter(t)

	recorder, envelope := doModelPricingRequest(t, router, http.MethodPut,
		"/admin/model-pricing/overrides/kimi-k2",
		`{"input_usd_per_mtok": 2.142, "platform": "moonshot", "custom": true,
		  "peak_enabled": true,
		  "peak": {"input_usd_per_mtok": 3, "output_usd_per_mtok": 12},
		  "off_peak": {"input_usd_per_mtok": 1.5}}`)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())
	var item dto.ModelPricingItem
	require.NoError(t, json.Unmarshal(envelope.Data, &item))
	require.Equal(t, "moonshot", item.Platform)
	require.True(t, item.Custom)
	require.True(t, item.PeakEnabled)
	require.InDelta(t, 3, item.Peak.InputUSDPerMTok, 1e-9)
	require.InDelta(t, 1.5, item.OffPeak.InputUSDPerMTok, 1e-9)
	require.Equal(t, "catalog", item.Source, "自定义条目来源标为 catalog")

	// 列表：峰谷字段与 meta 都在。
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?search=kimi-k2", "")
	var list dto.ModelPricingListResponse
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 1, list.Total)
	require.True(t, list.Items[0].PeakEnabled)
	require.InDelta(t, 12, list.Items[0].Peak.OutputUSDPerMTok, 1e-9)
	require.True(t, list.Meta.CatalogAuthoritative)
	require.Equal(t, "Asia/Shanghai", list.Meta.PeakSettings.Timezone)

	// peak_enabled / custom 筛选。
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?peak_enabled=true", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 1, list.Total)
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?custom=true", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 1, list.Total)

	// platform 筛选（向后兼容）：不传 platform 时不过滤，返回全量；
	// 传了才按 `_catalog.platform` 精确匹配，且大小写不敏感。
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 2, list.Total, "不传 platform 时不得筛选（向后兼容）")

	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?platform=moonshot", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 1, list.Total)
	require.Equal(t, "kimi-k2", list.Items[0].Model)

	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?platform=MOONSHOT", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 1, list.Total, "platform 匹配大小写不敏感")

	// 空串等价于不传（不能把 platform= 当成筛「空平台」）。
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?platform=", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 2, list.Total, "platform= 空值不得筛选")

	// 未自定义的模型没有 `_catalog.platform`，所以按平台筛只命中已接管的模型。
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?platform=openai", "")
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 0, list.Total, "未自定义模型无 platform，应由 provider 筛")

	// 非法峰谷价 → 400 INVALID_MODEL_PRICE。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut,
		"/admin/model-pricing/overrides/kimi-k2", `{"peak": {"input_usd_per_mtok": -1}}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "INVALID_MODEL_PRICE", envelope.Reason)

	// 非法平台 → 400 INVALID_MODEL_PRICE。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut,
		"/admin/model-pricing/overrides/kimi-k2", `{"platform": "not-a-platform"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "INVALID_MODEL_PRICE", envelope.Reason)

	// peak 不是对象 → 400。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut,
		"/admin/model-pricing/overrides/kimi-k2", `{"peak": "abc"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "INVALID_MODEL_PRICE", envelope.Reason)
}

// 新增/删除模型：目录外可建、重名 409 MODEL_ALREADY_EXISTS、删除自定义成功、内置拒绝。
func TestModelPricingHandlerCreateAndDeleteModel(t *testing.T) {
	router := newModelPricingHandlerRouter(t)

	recorder, envelope := doModelPricingRequest(t, router, http.MethodPost,
		"/admin/model-pricing/models",
		`{"model": "my-private-model", "platform": "openai",
		  "input_usd_per_mtok": 1.5, "output_usd_per_mtok": 6}`)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())
	var item dto.ModelPricingItem
	require.NoError(t, json.Unmarshal(envelope.Data, &item))
	require.Equal(t, "my-private-model", item.Model)
	require.True(t, item.Custom)
	require.Equal(t, "openai", item.Platform)
	require.InDelta(t, 1.5, item.InputUSDPerMTok, 1e-9)

	// 重名（目录内置）→ 409 MODEL_ALREADY_EXISTS。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodPost,
		"/admin/model-pricing/models",
		`{"model": "kimi-k2", "platform": "moonshot", "input_usd_per_mtok": 1}`)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, "MODEL_ALREADY_EXISTS", envelope.Reason)

	// 重名（自定义）→ 409。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodPost,
		"/admin/model-pricing/models",
		`{"model": "my-private-model", "platform": "openai", "input_usd_per_mtok": 2}`)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, "MODEL_ALREADY_EXISTS", envelope.Reason)

	// 校验错误 → 400 INVALID_MODEL_PRICE。
	cases := map[string]string{
		"bad name":         `{"model": "bad model!", "platform": "openai", "input_usd_per_mtok": 1}`,
		"missing platform": `{"model": "m1", "input_usd_per_mtok": 1}`,
		"bad platform":     `{"model": "m2", "platform": "nope", "input_usd_per_mtok": 1}`,
		"negative price":   `{"model": "m3", "platform": "openai", "input_usd_per_mtok": -1}`,
		"no price":         `{"model": "m4", "platform": "openai"}`,
		"malformed json":   `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			recorder, envelope := doModelPricingRequest(t, router, http.MethodPost, "/admin/model-pricing/models", body)
			require.Equal(t, http.StatusBadRequest, recorder.Code, "body: %s", recorder.Body.String())
			require.Equal(t, "INVALID_MODEL_PRICE", envelope.Reason)
		})
	}

	// 删除自定义模型。
	recorder, _ = doModelPricingRequest(t, router, http.MethodDelete, "/admin/model-pricing/models/my-private-model", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing?search=my-private-model", "")
	var list dto.ModelPricingListResponse
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 0, list.Total)

	// 删除内置模型 → 400 MODEL_DELETE_BUILTIN_REJECTED：先给内置模型打上
	// `_catalog`（custom 保持 false，表示「覆盖」而非「自定义模型」），
	// 删除应被拒绝，引导走「恢复内置」。
	recorder, _ = doModelPricingRequest(t, router, http.MethodPut,
		"/admin/model-pricing/overrides/kimi-k2", `{"platform": "moonshot", "custom": false}`)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())
	recorder, envelope = doModelPricingRequest(t, router, http.MethodDelete, "/admin/model-pricing/models/kimi-k2", "")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "MODEL_DELETE_BUILTIN_REJECTED", envelope.Reason)

	// 没有 override 条目的目录模型 → 404（没有可删的自定义定义）。
	recorder, envelope = doModelPricingRequest(t, router, http.MethodDelete, "/admin/model-pricing/models/gpt-5.4", "")
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "MODEL_PRICING_NOT_FOUND", envelope.Reason)
}

// 峰谷时段读写：默认官方口径 → PUT 整体替换 → GET 回读 → 非法值 400。
func TestModelPricingHandlerPeakSettingsFlow(t *testing.T) {
	router := newModelPricingHandlerRouter(t)

	recorder, envelope := doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing/peak-settings", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	var settings dto.PeakPricingSettingsResponse
	require.NoError(t, json.Unmarshal(envelope.Data, &settings))
	require.Equal(t, "Asia/Shanghai", settings.Timezone)
	require.Len(t, settings.Periods, 2, "未配置时回落官方口径")

	recorder, envelope = doModelPricingRequest(t, router, http.MethodPut, "/admin/model-pricing/peak-settings",
		`{"timezone": "UTC", "weekday_only": true, "weekend_off_peak": true,
		  "periods": [{"start": "09:00", "end": "12:00"}]}`)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())
	require.NoError(t, json.Unmarshal(envelope.Data, &settings))
	require.Equal(t, "UTC", settings.Timezone)
	require.Len(t, settings.Periods, 1)
	require.Equal(t, "09:00", settings.Periods[0].Start)

	recorder, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing/peak-settings", "")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, &settings))
	require.Equal(t, "UTC", settings.Timezone, "保存后回读一致")
	require.Len(t, settings.Periods, 1)

	// 列表 meta 也带峰谷设置。
	_, envelope = doModelPricingRequest(t, router, http.MethodGet, "/admin/model-pricing", "")
	var list dto.ModelPricingListResponse
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, "UTC", list.Meta.PeakSettings.Timezone)

	// 非法时区 / 非法时段 / 请求体损坏 → 400 INVALID_PEAK_PRICING_SETTINGS。
	cases := map[string]string{
		"bad timezone":   `{"timezone": "Mars/Olympus", "periods": [{"start": "09:00", "end": "12:00"}]}`,
		"equal start":    `{"periods": [{"start": "09:00", "end": "09:00"}]}`,
		"bad clock":      `{"periods": [{"start": "9am", "end": "12:00"}]}`,
		"malformed json": `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			recorder, envelope := doModelPricingRequest(t, router, http.MethodPut, "/admin/model-pricing/peak-settings", body)
			require.Equal(t, http.StatusBadRequest, recorder.Code, "body: %s", recorder.Body.String())
			require.Equal(t, "INVALID_PEAK_PRICING_SETTINGS", envelope.Reason)
		})
	}
}
