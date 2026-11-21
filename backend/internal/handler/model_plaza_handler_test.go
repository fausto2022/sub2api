//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func plazaGroups() []service.PlazaGroup {
	return []service.PlazaGroup{
		{ID: 1, Name: "public-standard", Platform: "anthropic", SubscriptionType: "standard", RateMultiplier: 1},
		{ID: 2, Name: "exclusive-a", Platform: "anthropic", IsExclusive: true, RateMultiplier: 0.5},
		{ID: 3, Name: "public-subscription", Platform: "openai", SubscriptionType: "subscription", RateMultiplier: 1},
		{ID: 4, Name: "exclusive-b", Platform: "openai", IsExclusive: true, RateMultiplier: 0.8},
	}
}

func TestFilterPlazaVisibleGroups_AnonymousSeesOnlyNonExclusive(t *testing.T) {
	// 匿名(allowedExclusive == nil):仅非专属分组;订阅型公开分组照常可见(橱窗语义)。
	visible := filterPlazaVisibleGroups(plazaGroups(), nil, false)
	require.Len(t, visible, 2)
	ids := []int64{visible[0].ID, visible[1].ID}
	require.ElementsMatch(t, []int64{1, 3}, ids)
}

func TestFilterPlazaVisibleGroups_AuthedSeesGrantedExclusive(t *testing.T) {
	// 登录:非专属 + 授权的专属;未授权的专属仍不可见。
	allowed := map[int64]struct{}{2: {}}
	visible := filterPlazaVisibleGroups(plazaGroups(), allowed, false)
	require.Len(t, visible, 3)
	ids := make([]int64, 0, len(visible))
	for _, g := range visible {
		ids = append(ids, g.ID)
	}
	require.ElementsMatch(t, []int64{1, 2, 3}, ids)
}

func TestFilterPlazaVisibleGroups_AuthedEmptySetSeesNoExclusive(t *testing.T) {
	// 登录但无任何专属授权(空集合,非 nil):与匿名同样只见非专属,
	// 但语义区分要保持——空集合不能被当作 nil 匿名分支。
	visible := filterPlazaVisibleGroups(plazaGroups(), map[int64]struct{}{}, false)
	require.Len(t, visible, 2)
}

func TestFilterPlazaVisibleGroups_RestrictedUserSeesOnlyGrantedPublic(t *testing.T) {
	// 开启公开分组限制后，公开分组也必须落在授权集合内，否则用户会在广场
	// 看到自己实际绑定不了的分组。
	allowed := map[int64]struct{}{1: {}, 2: {}}
	visible := filterPlazaVisibleGroups(plazaGroups(), allowed, true)
	ids := make([]int64, 0, len(visible))
	for _, g := range visible {
		ids = append(ids, g.ID)
	}
	// 3 是未授权的公开分组，受限后不可见；4 是未授权的专属分组，一贯不可见。
	require.ElementsMatch(t, []int64{1, 2}, ids)
}

func TestFilterPlazaVisibleGroups_RestrictionDoesNotAffectAnonymous(t *testing.T) {
	// 匿名没有用户记录，限制标志无从谈起，可见性必须与未受限时一致。
	visible := filterPlazaVisibleGroups(plazaGroups(), nil, true)
	ids := make([]int64, 0, len(visible))
	for _, g := range visible {
		ids = append(ids, g.ID)
	}
	require.ElementsMatch(t, []int64{1, 3}, ids)
}

func TestModelPlazaHandler_NilSettingServiceFailsClosed404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ModelPlazaHandler{} // settingService == nil → fail-closed
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza", nil)

	h.Get(c)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestToModelPlazaGroupDTO_UserRateAndFieldWhitelist(t *testing.T) {
	g := service.PlazaGroup{
		ID: 2, Name: "vip", Description: "d", Platform: "anthropic",
		SubscriptionType: "standard", RateMultiplier: 1, IsExclusive: true,
		VideoRateIndependent: true, VideoRateMultiplier: 0.7,
		Models: []service.PlazaModel{{
			Name:     "claude-sonnet",
			Platform: "anthropic",
			Pricing: &service.ChannelModelPricing{
				BillingMode: service.BillingModeToken,
				InputPrice:  testPtr(3e-6),
			},
			OfficialPricing: &service.PlazaOfficialPricing{
				InputPrice:     testPtr(3e-6),
				CacheReadPrice: testPtr(3e-7),
			},
		}},
	}

	// 有专属倍率:user_rate_multiplier 序列化输出
	dto := toModelPlazaGroupDTO(&g, map[int64]float64{2: 0.5})
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	for _, key := range []string{
		"id", "name", "description", "platform", "subscription_type",
		"rate_multiplier", "user_rate_multiplier", "is_exclusive", "models",
		"peak_rate_enabled", "peak_start", "peak_end", "peak_rate_multiplier",
		"image_rate_independent", "image_rate_multiplier", "long_context_pricing_enabled",
		"video_rate_independent", "video_rate_multiplier",
	} {
		_, exists := decoded[key]
		require.Truef(t, exists, "plaza group DTO must expose %q", key)
	}
	require.InDelta(t, 0.5, decoded["user_rate_multiplier"].(float64), 1e-9)
	require.Equal(t, true, decoded["video_rate_independent"])
	require.Equal(t, 0.7, decoded["video_rate_multiplier"])

	// 模型条目:pricing + official_pricing 并存;official 缺失字段输出 null 而非省略
	models := decoded["models"].([]any)
	require.Len(t, models, 1)
	model := models[0].(map[string]any)
	require.Contains(t, model, "pricing")
	require.Contains(t, model, "official_pricing")
	official := model["official_pricing"].(map[string]any)
	require.Contains(t, official, "input_price")
	require.Contains(t, official, "cache_read_price")
	_, has1h := official["cache_write_1h_price"]
	require.False(t, has1h, "1h 缓存写价为 nil 时应 omitempty")
	_, hasOfficialIntervals := official["intervals"]
	require.False(t, hasOfficialIntervals, "官方无阶梯时 intervals 应 omitempty")
	_, hasBasis := model["long_context_basis"]
	require.False(t, hasBasis, "单档模型不输出 long_context_basis")
	_, hasTimePricing := model["time_pricing"]
	require.False(t, hasTimePricing, "无分时时不输出 time_pricing")

	// 无专属倍率:user_rate_multiplier 整个字段省略
	dtoNoRate := toModelPlazaGroupDTO(&g, nil)
	rawNoRate, err := json.Marshal(dtoNoRate)
	require.NoError(t, err)
	var decodedNoRate map[string]any
	require.NoError(t, json.Unmarshal(rawNoRate, &decodedNoRate))
	_, hasRate := decodedNoRate["user_rate_multiplier"]
	require.False(t, hasRate, "无专属倍率时 user_rate_multiplier 应 omitempty")
}

func TestToModelPlazaOfficialPricing_NilPassthrough(t *testing.T) {
	require.Nil(t, toModelPlazaOfficialPricing(nil))
}

// TestToModelPlazaGroupDTO_CatalogDiscountedAndPeakPricing 覆盖 §4.3 的四类价格
// 数据与峰价：catalog_pricing / pricing / discounted_pricing / official_pricing /
// peak_pricing 同时输出，命名沿用现有 camel_case → JSON 下划线约定。
func TestToModelPlazaGroupDTO_CatalogDiscountedAndPeakPricing(t *testing.T) {
	g := service.PlazaGroup{
		ID: 5, Name: "catalog-group", Platform: "deepseek", SubscriptionType: "standard",
		RateMultiplier: 0.8, CatalogModelCount: 1,
		Models: []service.PlazaModel{{
			Name:     "deepseek-v4-flash",
			Platform: "deepseek",
			CatalogPricing: &service.ChannelModelPricing{
				BillingMode: service.BillingModeToken,
				InputPrice:  testPtr(4e-6),
				OutputPrice: testPtr(1.6e-5),
			},
			Pricing: &service.ChannelModelPricing{
				BillingMode: service.BillingModeToken,
				InputPrice:  testPtr(4e-6),
				OutputPrice: testPtr(1.6e-5),
			},
			// service 层用分组默认倍率预计算；handler 会按生效倍率重算。
			DiscountedPricing: &service.ChannelModelPricing{
				BillingMode: service.BillingModeToken,
				InputPrice:  testPtr(3.2e-6),
			},
			PeakPricing: &service.PlazaOfficialPricing{
				InputPrice:  testPtr(8e-6),
				OutputPrice: testPtr(3.2e-5),
			},
			OfficialPricing: &service.PlazaOfficialPricing{
				InputPrice:  testPtr(5.6e-7),
				OutputPrice: testPtr(2.24e-6),
			},
		}},
	}

	raw, err := json.Marshal(toModelPlazaGroupDTO(&g, nil))
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	require.Equal(t, float64(1), decoded["catalog_model_count"], "分组应输出目录价模型数")

	model := decoded["models"].([]any)[0].(map[string]any)
	for _, key := range []string{"catalog_pricing", "pricing", "discounted_pricing", "peak_pricing", "official_pricing"} {
		_, exists := model[key]
		require.Truef(t, exists, "plaza model DTO must expose %q", key)
	}

	catalog := model["catalog_pricing"].(map[string]any)
	require.InDelta(t, 4e-6, catalog["input_price"].(float64), 1e-15)
	require.InDelta(t, 1.6e-5, catalog["output_price"].(float64), 1e-15)

	discounted := model["discounted_pricing"].(map[string]any)
	require.InDelta(t, 4e-6*0.8, discounted["input_price"].(float64), 1e-15,
		"折后价 = 目录价 × 分组倍率")
	require.InDelta(t, 1.6e-5*0.8, discounted["output_price"].(float64), 1e-15)

	peak := model["peak_pricing"].(map[string]any)
	require.InDelta(t, 8e-6, peak["input_price"].(float64), 1e-15)

	official := model["official_pricing"].(map[string]any)
	require.InDelta(t, 5.6e-7, official["input_price"].(float64), 1e-15)
}

// TestToModelPlazaGroupDTO_DiscountedPricingUsesUserRate 覆盖用户专属倍率：
// 登录用户的折后价按专属倍率重算，而非分组默认倍率。
func TestToModelPlazaGroupDTO_DiscountedPricingUsesUserRate(t *testing.T) {
	g := service.PlazaGroup{
		ID: 6, Name: "vip", Platform: "anthropic", RateMultiplier: 1,
		Models: []service.PlazaModel{{
			Name:     "claude-sonnet",
			Platform: "anthropic",
			CatalogPricing: &service.ChannelModelPricing{
				BillingMode: service.BillingModeToken,
				InputPrice:  testPtr(4e-6),
			},
			Pricing: &service.ChannelModelPricing{
				BillingMode: service.BillingModeToken,
				InputPrice:  testPtr(4e-6),
			},
		}},
	}

	raw, err := json.Marshal(toModelPlazaGroupDTO(&g, map[int64]float64{6: 0.25}))
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	require.InDelta(t, 0.25, decoded["user_rate_multiplier"].(float64), 1e-15)
	model := decoded["models"].([]any)[0].(map[string]any)
	discounted := model["discounted_pricing"].(map[string]any)
	require.InDelta(t, 1e-6, discounted["input_price"].(float64), 1e-15,
		"折后价应按用户专属倍率 0.25 折算")
	// 目录价与实收单价不随倍率变化。
	catalog := model["catalog_pricing"].(map[string]any)
	require.InDelta(t, 4e-6, catalog["input_price"].(float64), 1e-15)
}

// TestToModelPlazaGroupDTO_NoCatalogPricingYieldsNulls 覆盖未纳入目录价的模型：
// catalog_pricing / peak_pricing 输出 null（字段恒在，前端据此隐藏目录价列）。
func TestToModelPlazaGroupDTO_NoCatalogPricingYieldsNulls(t *testing.T) {
	g := service.PlazaGroup{
		ID: 7, Name: "plain", Platform: "anthropic", RateMultiplier: 1,
		Models: []service.PlazaModel{{
			Name:     "claude-haiku",
			Platform: "anthropic",
			Pricing:  &service.ChannelModelPricing{BillingMode: service.BillingModeToken, InputPrice: testPtr(1e-6)},
		}},
	}

	raw, err := json.Marshal(toModelPlazaGroupDTO(&g, nil))
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	require.Equal(t, float64(0), decoded["catalog_model_count"])
	model := decoded["models"].([]any)[0].(map[string]any)
	catalog, exists := model["catalog_pricing"]
	require.True(t, exists, "字段恒在")
	require.Nil(t, catalog, "未纳入目录价时为 null")
	peak, exists := model["peak_pricing"]
	require.True(t, exists, "字段恒在")
	require.Nil(t, peak, "未启用峰谷时为 null")
}

// TestModelPlazaRuntime_DefaultEnabledPublic 覆盖默认开放：未写入开关时
// model_plaza_enabled 视为 true、model_plaza_require_auth 视为 false，
// 且设置读取失败也保持开放（默认值即 opt-out）。
func TestModelPlazaRuntime_DefaultEnabledPublic(t *testing.T) {
	rt := service.NewSettingService(plazaEmptySettingRepo{}, &config.Config{}).
		GetModelPlazaRuntime(context.Background())
	require.True(t, rt.Enabled, "未配置时模型广场默认开启")
	require.False(t, rt.RequireAuth, "默认公开可见，不强制登录")

	// 显式关闭仍然生效。
	off := service.NewSettingService(plazaToggleSettingRepo{
		values: map[string]string{service.SettingKeyModelPlazaEnabled: "false"},
	}, &config.Config{}).GetModelPlazaRuntime(context.Background())
	require.False(t, off.Enabled, "显式 false 必须关闭")

	// 读取失败时保持开放（fail-open，与 opt-out 默认一致）。
	failed := service.NewSettingService(plazaFailingSettingRepo{}, &config.Config{}).
		GetModelPlazaRuntime(context.Background())
	require.True(t, failed.Enabled, "读取失败时按默认开启处理")
}

// plazaEmptySettingRepo 是未写入任何设置的 settings 仓储。
type plazaEmptySettingRepo struct{ service.SettingRepository }

func (plazaEmptySettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

// plazaToggleSettingRepo 返回预置的开关值。
type plazaToggleSettingRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r plazaToggleSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}

// plazaFailingSettingRepo 模拟 settings 存储不可用。
type plazaFailingSettingRepo struct{ service.SettingRepository }

func (plazaFailingSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("settings unavailable")
}

// TestFilterPlazaVisibleGroups_AnonymousHidesExclusiveWithCatalogData 覆盖专属分组
// 过滤：即使分组带目录价数据，未登录用户也看不到专属分组。
func TestFilterPlazaVisibleGroups_AnonymousHidesExclusiveWithCatalogData(t *testing.T) {
	groups := []service.PlazaGroup{
		{
			ID: 1, Name: "public", RateMultiplier: 1, CatalogModelCount: 2,
			Models: []service.PlazaModel{{Name: "public-model", CatalogPricing: &service.ChannelModelPricing{}}},
		},
		{
			ID: 2, Name: "vip", RateMultiplier: 0.5, IsExclusive: true, CatalogModelCount: 3,
			Models: []service.PlazaModel{{Name: "vip-model", CatalogPricing: &service.ChannelModelPricing{}}},
		},
	}

	visible := filterPlazaVisibleGroups(groups, nil, false)
	require.Len(t, visible, 1, "未登录只见非专属分组")
	require.Equal(t, int64(1), visible[0].ID)

	// 登录且被授权后才可见。
	granted := filterPlazaVisibleGroups(groups, map[int64]struct{}{2: {}}, false)
	require.Len(t, granted, 2)
}

func TestToModelPlazaGroupDTO_LongContextTiersAndBasis(t *testing.T) {
	maxTokens := 272000
	g := service.PlazaGroup{
		ID: 3, Name: "ladder", Platform: "openai", SubscriptionType: "standard", RateMultiplier: 1,
		LongContextPricingEnabled: true,
		Models: []service.PlazaModel{{
			Name:     "gpt-5.4",
			Platform: "openai",
			Pricing: &service.ChannelModelPricing{
				BillingMode: service.BillingModeToken,
				InputPrice:  testPtr(2.5e-6),
				Intervals: []service.PricingInterval{
					{MinTokens: 0, MaxTokens: &maxTokens, TierLabel: "≤272K", InputPrice: testPtr(2.5e-6)},
					{MinTokens: 272000, TierLabel: ">272K", InputPrice: testPtr(5e-6)},
				},
			},
			OfficialPricing: &service.PlazaOfficialPricing{
				InputPrice: testPtr(2.5e-6),
				Intervals: []service.PricingInterval{
					{MinTokens: 0, MaxTokens: &maxTokens, TierLabel: "≤272K", InputPrice: testPtr(2.5e-6)},
					{MinTokens: 272000, TierLabel: ">272K", InputPrice: testPtr(5e-6)},
				},
			},
			LongContextBasis: service.ContextPricingBasisWholeRequest,
		}},
	}

	raw, err := json.Marshal(toModelPlazaGroupDTO(&g, nil))
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, true, decoded["long_context_pricing_enabled"])

	model := decoded["models"].([]any)[0].(map[string]any)
	require.Equal(t, "whole_request", model["long_context_basis"])

	pricing := model["pricing"].(map[string]any)
	paidTiers := pricing["intervals"].([]any)
	require.Len(t, paidTiers, 2)
	require.Equal(t, ">272K", paidTiers[1].(map[string]any)["tier_label"])

	official := model["official_pricing"].(map[string]any)
	officialTiers := official["intervals"].([]any)
	require.Len(t, officialTiers, 2)
	first := officialTiers[0].(map[string]any)
	require.Equal(t, "≤272K", first["tier_label"])
	require.InDelta(t, 272000, first["max_tokens"].(float64), 0)
	require.Contains(t, first, "cache_write_price", "区间 DTO 字段齐全（nil 输出 null）")
}

func testPtr(v float64) *float64 { return &v }

func TestToModelPlazaGroupDTO_TimePricing(t *testing.T) {
	g := service.PlazaGroup{
		ID: 4, Name: "cn", Platform: "deepseek", SubscriptionType: "standard", RateMultiplier: 1,
		Models: []service.PlazaModel{{
			Name:     "deepseek-chat",
			Platform: "deepseek",
			Pricing:  &service.ChannelModelPricing{BillingMode: service.BillingModeToken, InputPrice: testPtr(0.28e-6)},
			TimePricing: &service.TimePricingSchedule{Timezone: "Asia/Shanghai", Periods: []service.TimePricingPeriod{
				{StartTime: "00:30", EndTime: "08:30", Multiplier: 0.5},
			}},
		}, {
			Name:     "deepseek-reasoner",
			Platform: "deepseek",
			Pricing:  &service.ChannelModelPricing{BillingMode: service.BillingModeToken, InputPrice: testPtr(0.56e-6)},
			TimePricing: &service.TimePricingSchedule{Timezone: "Asia/Shanghai", WeekdaysOnly: true, Periods: []service.TimePricingPeriod{
				{StartTime: "00:30", EndTime: "08:30", Multiplier: 0.5},
			}},
		}},
	}
	raw, err := json.Marshal(toModelPlazaGroupDTO(&g, nil))
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	model := decoded["models"].([]any)[0].(map[string]any)
	tp := model["time_pricing"].(map[string]any)
	require.Equal(t, "Asia/Shanghai", tp["timezone"])
	_, hasWeekdaysOnly := tp["weekdays_only"]
	require.False(t, hasWeekdaysOnly, "未开启仅工作日时字段省略")
	periods := tp["periods"].([]any)
	require.Len(t, periods, 1)
	first := periods[0].(map[string]any)
	require.Equal(t, "00:30", first["start_time"])
	require.Equal(t, "08:30", first["end_time"])
	require.InDelta(t, 0.5, first["multiplier"].(float64), 1e-12)

	weekdaysModel := decoded["models"].([]any)[1].(map[string]any)
	weekdaysTP := weekdaysModel["time_pricing"].(map[string]any)
	require.Equal(t, true, weekdaysTP["weekdays_only"])
}

func TestFilterPlazaVisibleGroups_SubscribedExclusiveGroup(t *testing.T) {
	groups := []service.PlazaGroup{
		{ID: 42, IsExclusive: true, SubscriptionType: "subscription"},
		{ID: 43, IsExclusive: true, SubscriptionType: "subscription"},
		{ID: 44, IsExclusive: true, SubscriptionType: "standard"},
	}
	require.Empty(t, filterPlazaVisibleGroups(groups, nil, false))
	for _, restricted := range []bool{false, true} {
		visible := filterPlazaVisibleGroups(groups, map[int64]struct{}{42: {}}, restricted)
		require.Len(t, visible, 1)
		require.Equal(t, int64(42), visible[0].ID)
	}
}

type plazaVarySettingRepo struct{ service.SettingRepository }

func (plazaVarySettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{service.SettingKeyModelPlazaEnabled: "true"}, nil
}

type plazaVaryChannelRepo struct{ service.ChannelRepository }

func (plazaVaryChannelRepo) ListAll(context.Context) ([]service.Channel, error) { return nil, nil }

type plazaVaryGroupRepo struct{ service.GroupRepository }

func (plazaVaryGroupRepo) ListActive(context.Context) ([]service.Group, error) { return nil, nil }

func TestModelPlazaHandler_VaryKeepsCORSOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewModelPlazaHandler(
		service.NewModelPlazaService(plazaVaryChannelRepo{}, plazaVaryGroupRepo{}, nil, nil, nil),
		nil,
		service.NewSettingService(plazaVarySettingRepo{}, &config.Config{}),
	)
	r := gin.New()
	r.Use(middleware.CORS(config.CORSConfig{AllowedOrigins: []string{"https://a.example", "https://b.example"}}))
	r.GET("/api/v1/model-plaza", h.Get)

	for _, origin := range []string{"https://a.example", "https://b.example"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Accept-Language", "en")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, origin, w.Header().Get("Access-Control-Allow-Origin"))
		// 共享缓存需同时按来源与语言区分，否则 A 来源的响应可能被复用给 B。
		require.Equal(t, []string{"Origin", "Accept-Language"}, w.Header().Values("Vary"))
	}
}
