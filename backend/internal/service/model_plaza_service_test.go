//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// newPlazaService 构造 ListGroups 测试用的 ModelPlazaService（不接计费服务：展示定价原样透传）。
func newPlazaService(channels []Channel, groups []Group, pricing *PricingService) *ModelPlazaService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
	}
	return NewModelPlazaService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, pricing, nil, nil)
}

func plazaPricedChannel(id int64, name string, groupIDs []int64, platform string, models ...string) Channel {
	return Channel{
		ID:       id,
		Name:     name,
		Status:   StatusActive,
		GroupIDs: groupIDs,
		ModelPricing: []ChannelModelPricing{{
			Platform:    platform,
			Models:      models,
			BillingMode: BillingModeToken,
			InputPrice:  testPtrFloat64(3e-6),
			OutputPrice: testPtrFloat64(1.5e-5),
		}},
	}
}

func TestListPlazaGroups_GroupCentricAggregation(t *testing.T) {
	// 两个渠道挂同一分组:模型并入同一 PlazaGroup;无模型的分组不返回。
	channels := []Channel{
		plazaPricedChannel(1, "chA", []int64{10}, "anthropic", "claude-sonnet"),
		plazaPricedChannel(2, "chB", []int64{10}, "anthropic", "claude-opus"),
	}
	groups := []Group{
		{ID: 10, Name: "g-main", Description: "desc", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-empty", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1, "无模型的分组不应返回")
	require.Equal(t, int64(10), out[0].ID)
	require.Equal(t, "desc", out[0].Description)
	require.Len(t, out[0].Models, 2)
	// 组内模型按名称排序
	require.Equal(t, "claude-opus", out[0].Models[0].Name)
	require.Equal(t, "claude-sonnet", out[0].Models[1].Name)
}

func TestListPlazaGroups_Fable51HasNoImplicitReasoningMultiplier(t *testing.T) {
	ch := plazaPricedChannel(1, "ch", []int64{10}, "anthropic", "claude-fable-5-1")
	svc := newPlazaService([]Channel{ch}, []Group{{ID: 10, Platform: "anthropic"}}, nil)
	groups, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 1)
	require.Empty(t, groups[0].Models[0].Pricing.ReasoningEffortMultipliers)
}

func TestListPlazaGroups_DedupFirstWinsWithPricingUpgrade(t *testing.T) {
	// 同名模型:先见者胜;仅当已存条目无定价而新条目有定价时升级替换。
	unpriced := Channel{
		ID: 1, Name: "alpha", Status: StatusActive, GroupIDs: []int64{10},
		// mapping-only → SupportedModels 产出无定价条目
		ModelMapping: map[string]map[string]string{
			"anthropic": {"claude-sonnet": "claude-sonnet"},
		},
	}
	priced := plazaPricedChannel(2, "beta", []int64{10}, "anthropic", "claude-sonnet")
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}

	// alpha(无价)按名称序先于 beta(有价):先见者无价,应被有价条目升级。
	svc := newPlazaService([]Channel{priced, unpriced}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	require.NotNil(t, out[0].Models[0].Pricing, "无价条目应被有价条目升级")
	require.NotNil(t, out[0].Models[0].Pricing.InputPrice)
}

func TestListPlazaGroups_PlatformIsolation(t *testing.T) {
	// 渠道同时有 anthropic/openai 定价,anthropic 分组只应看到 anthropic 模型。
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: "anthropic", Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: "openai", Models: []string{"gpt-5"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	groups := []Group{
		{ID: 10, Name: "g-claude", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-gpt", Platform: "openai", RateMultiplier: 1},
	}
	svc := newPlazaService([]Channel{ch}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string][]PlazaModel{}
	for _, g := range out {
		byName[g.Name] = g.Models
	}
	require.Len(t, byName["g-claude"], 1)
	require.Equal(t, "claude-sonnet", byName["g-claude"][0].Name)
	require.Len(t, byName["g-gpt"], 1)
	require.Equal(t, "gpt-5", byName["g-gpt"][0].Name)
}

func TestListPlazaGroups_CompositeIncludesConfiguredConcretePlatforms(t *testing.T) {
	anthropicPrice := 3e-6
	openAIPrice := 2e-6
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"shared-model"}, InputPrice: &anthropicPrice},
			{Platform: PlatformOpenAI, Models: []string{"shared-model"}, InputPrice: &openAIPrice},
			{Platform: "", Models: []string{"empty-platform"}},
			{Platform: PlatformComposite, Models: []string{"nested-composite"}},
			{Platform: "unknown-platform", Models: []string{"unknown-platform"}},
		},
	}
	groups := []Group{{ID: 10, Name: "composite", Platform: PlatformComposite, RateMultiplier: 1}}

	out, err := newPlazaService([]Channel{ch}, groups, nil).ListGroups(context.Background())

	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 2, "only concrete platforms are included and same-named models remain distinct")
	require.Equal(t, PlatformAnthropic, out[0].Models[0].Platform)
	require.Equal(t, PlatformOpenAI, out[0].Models[1].Platform)
	require.InDelta(t, anthropicPrice, *out[0].Models[0].Pricing.InputPrice, 1e-12)
	require.InDelta(t, openAIPrice, *out[0].Models[1].Pricing.InputPrice, 1e-12)
}

func TestListPlazaGroups_CompositeAndOrdinaryGroupsDoNotLeakPlatforms(t *testing.T) {
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: PlatformOpenAI, Models: []string{"gpt-5"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	groups := []Group{
		{ID: 10, Name: "anthropic-only", Platform: PlatformAnthropic, RateMultiplier: 1},
		{ID: 20, Name: "composite", Platform: PlatformComposite, RateMultiplier: 1},
	}

	out, err := newPlazaService([]Channel{ch}, groups, nil).ListGroups(context.Background())

	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string]PlazaGroup{}
	for _, group := range out {
		byName[group.Name] = group
	}
	require.Len(t, byName["anthropic-only"].Models, 1)
	// 只比对模型身份与展示单价：折后价/官方价等展示增强字段不参与本用例断言。
	single := byName["anthropic-only"].Models[0]
	require.Equal(t, "claude-sonnet", single.Name)
	require.Equal(t, PlatformAnthropic, single.Platform)
	require.NotNil(t, single.Pricing)
	require.Len(t, byName["composite"].Models, 2)
	require.Equal(t, []string{"claude-sonnet", "gpt-5"}, []string{
		byName["composite"].Models[0].Name,
		byName["composite"].Models[1].Name,
	})
	require.Equal(t, []string{PlatformAnthropic, PlatformOpenAI}, []string{
		byName["composite"].Models[0].Platform,
		byName["composite"].Models[1].Platform,
	})
}

func TestListPlazaGroups_InactiveChannelSkipped(t *testing.T) {
	inactive := plazaPricedChannel(1, "off", []int64{10}, "anthropic", "claude-sonnet")
	inactive.Status = "inactive"
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaService([]Channel{inactive}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestListPlazaGroups_SortedByRateMultiplierAsc(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10, 20, 30}, "anthropic", "claude-sonnet"),
	}
	groups := []Group{
		{ID: 10, Name: "b-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "a-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 30, Name: "cheap", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.Equal(t, "cheap", out[0].Name, "倍率低者在前")
	require.Equal(t, "a-standard", out[1].Name, "同倍率按名称")
	require.Equal(t, "b-standard", out[2].Name)
}

func TestListPlazaGroups_OfficialPricingFill(t *testing.T) {
	pricingSvc := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"claude-sonnet": {
			Mode:                                "chat",
			InputCostPerToken:                   3e-6,
			OutputCostPerToken:                  1.5e-5,
			CacheCreationInputTokenCost:         3.75e-6,
			CacheCreationInputTokenCostAbove1hr: 6e-6,
			CacheReadInputTokenCost:             3e-7,
		},
		"token-absent": {Mode: "image_generation", TokenPricingAbsent: true, OutputCostPerImage: 0.04},
	})
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10}, "anthropic", "claude-sonnet", "unknown-model", "token-absent"),
	}
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaService(channels, groups, pricingSvc)
	// 官方价与计费同源：需要计费服务与解析器（官方参考不查渠道，解析器无需渠道服务）。
	svc.billingService = NewBillingService(&config.Config{}, pricingSvc)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 3)

	byName := map[string]PlazaModel{}
	for _, m := range out[0].Models {
		byName[m.Name] = m
	}
	// 命中:填充完整官方价(含 1h 缓存写入)
	official := byName["claude-sonnet"].OfficialPricing
	require.NotNil(t, official)
	require.InDelta(t, 3e-6, *official.InputPrice, 1e-12)
	require.InDelta(t, 6e-6, *official.CacheWrite1hPrice, 1e-12)
	require.InDelta(t, 3e-7, *official.CacheReadPrice, 1e-12)
	// 未命中:nil(GetModelPricing 的 claude 系列模糊匹配对非 claude 名不生效)
	require.Nil(t, byName["unknown-model"].OfficialPricing)
	// TokenPricingAbsent 条目不作为官方 token 价展示
	require.Nil(t, byName["token-absent"].OfficialPricing)
}

func TestListPlazaGroups_GroupImagePriceOverridesChannelPricing(t *testing.T) {
	// 图片计费模型:档位价按实收口径合成(分组图片价 > 渠道档位价 > 渠道默认按次价),
	// 分组独立倍率字段透传;未配图片价的分组保持渠道定价原样。
	perReq := 0.2
	tier4K := 0.3
	imgPrice := 0.02
	channels := []Channel{{
		ID: 1, Name: "img-ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{{
			Platform:        "openai",
			Models:          []string{"gpt-image-2"},
			BillingMode:     BillingModeImage,
			PerRequestPrice: &perReq,
			Intervals:       []PricingInterval{{TierLabel: "4K", PerRequestPrice: &tier4K}},
		}},
	}}
	groups := []Group{
		{ID: 10, Name: "g-media", Platform: "openai", RateMultiplier: 1,
			ImagePrice1K: &imgPrice, ImageRateIndependent: true, ImageRateMultiplier: 1,
			VideoRateIndependent: true, VideoRateMultiplier: 0.7},
		{ID: 20, Name: "g-plain", Platform: "openai", RateMultiplier: 0.1},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string]PlazaGroup{}
	for _, g := range out {
		byName[g.Name] = g
	}

	media := byName["g-media"]
	require.True(t, media.ImageRateIndependent)
	require.True(t, media.VideoRateIndependent)
	require.InDelta(t, 0.7, media.VideoRateMultiplier, 1e-9)
	require.InDelta(t, 1.0, media.ImageRateMultiplier, 1e-9)
	require.Len(t, media.Models, 1)
	p := media.Models[0].Pricing
	require.NotNil(t, p)
	require.Len(t, p.Intervals, 3)
	tierPrices := map[string]float64{}
	for _, iv := range p.Intervals {
		require.NotNil(t, iv.PerRequestPrice)
		tierPrices[iv.TierLabel] = *iv.PerRequestPrice
	}
	require.InDelta(t, 0.02, tierPrices["1K"], 1e-9, "1K 用分组图片价")
	require.InDelta(t, 0.2, tierPrices["2K"], 1e-9, "2K 分组未配,回落渠道默认按次价")
	require.InDelta(t, 0.3, tierPrices["4K"], 1e-9, "4K 分组未配,回落渠道档位价")

	plain := byName["g-plain"]
	require.False(t, plain.ImageRateIndependent)
	require.False(t, plain.VideoRateIndependent)
	require.Len(t, plain.Models, 1)
	pp := plain.Models[0].Pricing
	require.NotNil(t, pp)
	require.Len(t, pp.Intervals, 1, "未配分组图片价:渠道定价原样")
	require.InDelta(t, 0.2, *pp.PerRequestPrice, 1e-9)

	// 合成为克隆,渠道原始定价不被修改
	require.Len(t, channels[0].ModelPricing[0].Intervals, 1)
}

func TestListPlazaGroups_GroupModelImagePricingWinsOverGroupImageTiers(t *testing.T) {
	groupImage1K, groupImage2K, groupImage4K := 0.05, 0.055, 0.06
	model1K, model2K, model4K := 0.05, 0.075, 0.1
	channels := []Channel{{
		ID: 1, Name: "img-ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform:        "openai",
			Models:          []string{"yeahzi-image"},
			BillingMode:     BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.2),
		}},
	}}
	groups := []Group{{
		ID: 10, Name: "g", Platform: "openai", RateMultiplier: 1,
		ImagePrice1K: &groupImage1K, ImagePrice2K: &groupImage2K, ImagePrice4K: &groupImage4K,
		ModelPricing: []ChannelModelPricing{{
			Platform:        "openai",
			Models:          []string{"yeahzi-image"},
			BillingMode:     BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.1),
			Intervals: []PricingInterval{
				{TierLabel: "1K", PerRequestPrice: &model1K},
				{TierLabel: "2K", PerRequestPrice: &model2K},
				{TierLabel: "4K", PerRequestPrice: &model4K},
			},
		}},
	}}

	out, err := newPlazaService(channels, groups, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	p := out[0].Models[0].Pricing
	require.Equal(t, BillingModeImage, p.BillingMode)
	require.Len(t, p.Intervals, 3)
	tierPrices := map[string]float64{}
	for _, iv := range p.Intervals {
		require.NotNil(t, iv.PerRequestPrice)
		tierPrices[iv.TierLabel] = *iv.PerRequestPrice
	}
	require.InDelta(t, 0.05, tierPrices["1K"], 1e-9)
	require.InDelta(t, 0.075, tierPrices["2K"], 1e-9)
	require.InDelta(t, 0.1, tierPrices["4K"], 1e-9)
}

func TestListPlazaGroups_GroupModelFlatImagePriceWinsOverGroupImageTiers(t *testing.T) {
	groupPrice := 0.2
	channels := []Channel{{
		ID: 1, Name: "img-ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: "openai", Models: []string{"gemini-3.1-flash-lite-image"},
			BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.2),
		}},
	}}
	groups := []Group{{
		ID: 10, Name: "g", Platform: "openai", RateMultiplier: 1,
		ImagePrice1K: &groupPrice,
		ModelPricing: []ChannelModelPricing{{
			Platform: "openai", Models: []string{"gemini-3.1-flash-lite-image"},
			BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.1),
		}},
	}}

	out, err := newPlazaService(channels, groups, nil).ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	p := out[0].Models[0].Pricing
	require.Equal(t, BillingModeImage, p.BillingMode)
	require.NotNil(t, p.PerRequestPrice)
	require.InDelta(t, 0.1, *p.PerRequestPrice, 1e-9)
	require.Empty(t, p.Intervals, "模型级固定价不应被分组图片档位覆盖")
}

func TestListPlazaGroups_GroupImagePriceIgnoredForNonImageModes(t *testing.T) {
	// token 模式定价不受分组图片价影响。
	imgPrice := 0.02
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, "openai", "gpt-5")}
	groups := []Group{{ID: 10, Name: "g", Platform: "openai", RateMultiplier: 1, ImagePrice1K: &imgPrice}}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	p := out[0].Models[0].Pricing
	require.NotNil(t, p)
	require.Empty(t, p.Intervals)
	require.NotNil(t, p.InputPrice)
	require.Nil(t, p.PerRequestPrice)
}

func TestListPlazaGroups_RepoErrorsPropagate(t *testing.T) {
	sentinel := errors.New("boom")
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, sentinel },
	}
	svc := NewModelPlazaService(repo, &stubGroupRepoForAvailable{}, nil, nil, nil)
	out, err := svc.ListGroups(context.Background())
	require.Nil(t, out)
	require.ErrorIs(t, err, sentinel)

	svc2 := NewModelPlazaService(
		&mockChannelRepository{listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, nil }},
		&stubGroupRepoForAvailable{listActiveErr: sentinel},
		nil, nil, nil,
	)
	out2, err2 := svc2.ListGroups(context.Background())
	require.Nil(t, out2)
	require.ErrorIs(t, err2, sentinel)
}

// newPlazaServiceWithBilling 构造接入计费服务与解析器的广场服务：解析器的渠道服务与广场共用同一份渠道数据。
func newPlazaServiceWithBilling(channels []Channel, groups []Group, groupPlatforms map[int64]string, catalog *PricingService) *ModelPlazaService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
		getGroupPlatformsFn: func(ctx context.Context, _ []int64) (map[int64]string, error) {
			return groupPlatforms, nil
		},
	}
	cs := NewChannelService(repo, nil, nil, nil, nil)
	bs := NewBillingService(&config.Config{}, catalog)
	return NewModelPlazaService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, catalog, bs, NewModelPricingResolver(cs, bs))
}

func plazaModelsByName(models []PlazaModel) map[string]PlazaModel {
	out := make(map[string]PlazaModel, len(models))
	for _, m := range models {
		out[m.Name] = m
	}
	return out
}

func TestListGroups_TokenLadderFollowsGroupToggle(t *testing.T) {
	// 同一渠道挂开启/关闭阶梯的两个分组：实付档位随分组开关，官方阶梯不受影响。
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken}},
	}}
	groups := []Group{
		{ID: 10, Name: "on", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true},
		{ID: 20, Name: "off", Platform: PlatformOpenAI, RateMultiplier: 2, LongContextPricingEnabled: false},
	}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI, 20: PlatformOpenAI},
		newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	on, off := out[0], out[1]
	require.True(t, on.LongContextPricingEnabled)
	require.False(t, off.LongContextPricingEnabled)

	onModel := on.Models[0]
	require.Equal(t, ContextPricingBasisWholeRequest, onModel.LongContextBasis)
	require.Len(t, onModel.Pricing.Intervals, 2)
	require.Equal(t, "≤272K", onModel.Pricing.Intervals[0].TierLabel)
	require.Equal(t, ">272K", onModel.Pricing.Intervals[1].TierLabel)
	require.InDelta(t, 2.5e-6, *onModel.Pricing.InputPrice, 1e-15)
	require.InDelta(t, 5e-6, *onModel.Pricing.Intervals[1].InputPrice, 1e-15)
	require.InDelta(t, 22.5e-6, *onModel.Pricing.Intervals[1].OutputPrice, 1e-15)
	require.InDelta(t, 5e-6, *onModel.Pricing.Intervals[1].CacheWritePrice, 1e-15)
	require.InDelta(t, 0.5e-6, *onModel.Pricing.Intervals[1].CacheReadPrice, 1e-15)

	offModel := off.Models[0]
	require.Empty(t, offModel.LongContextBasis)
	require.Empty(t, offModel.Pricing.Intervals)
	require.InDelta(t, 2.5e-6, *offModel.Pricing.InputPrice, 1e-15)

	for _, m := range []PlazaModel{onModel, offModel} {
		require.NotNil(t, m.OfficialPricing)
		require.Len(t, m.OfficialPricing.Intervals, 2, "官方阶梯不受分组开关影响")
		require.InDelta(t, 5e-6, *m.OfficialPricing.Intervals[1].InputPrice, 1e-15)
		require.InDelta(t, 2.5e-6, *m.OfficialPricing.InputPrice, 1e-15)
	}
}

func TestListGroups_GeminiCatalogLadderShownWholeRequest(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelMapping: map[string]map[string]string{PlatformGemini: {"gemini-2.5-pro": "gemini-2.5-pro"}},
	}}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformGemini, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformGemini},
		newStubPricingServiceFromJSON(t, geminiLadderCatalogJSON))
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	m := out[0].Models[0]
	require.Equal(t, ContextPricingBasisWholeRequest, m.LongContextBasis)
	require.Len(t, m.Pricing.Intervals, 2)
	require.Equal(t, "≤200K", m.Pricing.Intervals[0].TierLabel)
	require.Equal(t, ">200K", m.Pricing.Intervals[1].TierLabel)
	require.InDelta(t, 2.5e-6, *m.Pricing.Intervals[1].InputPrice, 1e-15)
	require.InDelta(t, 15e-6, *m.Pricing.Intervals[1].OutputPrice, 1e-15)
	// 官方参考价与实付同源：都来自目录数据的阶梯字段
	require.NotNil(t, m.OfficialPricing)
	require.Len(t, m.OfficialPricing.Intervals, 2)
}

func TestListGroups_GroupTokenCardOverridesChannelPricing(t *testing.T) {
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "claude-sonnet-4")}
	groups := []Group{{
		ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1, LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-*"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(1e-6)}},
	}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformAnthropic}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.InDelta(t, 1e-6, *m.Pricing.InputPrice, 1e-15, "分组价卡优先于渠道平价")
	require.InDelta(t, 15e-6, *m.Pricing.OutputPrice, 1e-15, "卡未配置的项回落目录价")
	require.Empty(t, m.Pricing.Intervals)
}

func TestListGroups_ImageModelKeepsTierSynthesisWithBilling(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformOpenAI, Models: []string{"gpt-image-2"}, BillingMode: BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.04),
		}},
	}}
	groups := []Group{{
		ID: 10, Name: "g", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true,
		ImagePrice1K: testPtrFloat64(0.02),
	}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.Equal(t, BillingModeImage, m.Pricing.BillingMode)
	require.Empty(t, m.LongContextBasis)
	require.Len(t, m.Pricing.Intervals, 3)
	require.InDelta(t, 0.02, *m.Pricing.Intervals[0].PerRequestPrice, 1e-12)
	require.InDelta(t, 0.04, *m.Pricing.Intervals[1].PerRequestPrice, 1e-12)
}

func TestListGroups_CatalogMissingStillShowsChannelFlatPricing(t *testing.T) {
	// 目录查不到的模型：计费按渠道平价（未配置项 $0），广场单档展示渠道平价，官方价为空。
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "unknown-model-xyz")}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformAnthropic}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.NotNil(t, m.Pricing)
	require.InDelta(t, 3e-6, *m.Pricing.InputPrice, 1e-15)
	require.Empty(t, m.Pricing.Intervals)
	require.Nil(t, m.Pricing.CacheWritePrice, "目录无价且渠道未配置 → 无价")
	require.Nil(t, m.OfficialPricing)
}

func TestListGroups_TimePricingPassthrough(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformDeepseek, Models: []string{"deepseek-chat"}, BillingMode: BillingModeToken,
			InputPrice: testPtrFloat64(0.28e-6), OutputPrice: testPtrFloat64(0.42e-6),
			TimePricing: &ChannelTimePricing{Timezone: "Asia/Shanghai", Periods: []ChannelTimePricingPeriod{
				{StartTime: "00:30", EndTime: "08:30", Multiplier: 0.5},
			}},
		}},
	}}
	groups := []Group{{ID: 10, Name: "cn", Platform: PlatformDeepseek, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformDeepseek}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.NotNil(t, m.TimePricing)
	require.Equal(t, "Asia/Shanghai", m.TimePricing.Timezone)
	require.Len(t, m.TimePricing.Periods, 1)
	require.InDelta(t, 0.5, m.TimePricing.Periods[0].Multiplier, 1e-12)
	// 展示单价为标准时段价
	require.InDelta(t, 0.28e-6, *m.Pricing.InputPrice, 1e-15)
}

// newPlazaCatalogService 构造一个接入目录价层的广场服务：override 文件由
// newPeakPricingService 写入，目录正文用 minimalCatalogJSON 作为基础价表。
// adminAuthoritative=false 时关闭目录价层开关，用于验证回退行为。
func newPlazaCatalogService(
	t *testing.T,
	overrideJSON string,
	channels []Channel,
	groups []Group,
	groupPlatforms map[int64]string,
	adminAuthoritative *bool,
) *ModelPlazaService {
	t.Helper()
	catalog := newPeakPricingService(t, overrideJSON)
	built, err := catalog.buildPricingData([]byte(minimalCatalogJSON))
	require.NoError(t, err)
	catalog.mu.Lock()
	catalog.pricingData = built.data
	catalog.pricingDataSources = built.sources
	catalog.overrideModels = built.overrideModels
	catalog.cnyModels = built.cnyModels
	catalog.peakConfigs = built.peakConfigs
	catalog.mu.Unlock()

	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
		getGroupPlatformsFn: func(ctx context.Context, _ []int64) (map[int64]string, error) {
			return groupPlatforms, nil
		},
	}
	cs := NewChannelService(repo, nil, nil, nil, nil)
	bs := NewBillingService(&config.Config{}, catalog)
	resolver := NewModelPricingResolver(cs, bs)
	admin := NewModelPricingAdminService(catalog.cfg, catalog)
	admin.SetCatalogPricingResolver(resolver)
	if adminAuthoritative != nil {
		admin.SetCatalogPricingAuthoritative(*adminAuthoritative)
	}
	return NewModelPlazaService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, catalog, bs, resolver)
}

// catalogPlazaChannels 造一个挂两分组的渠道，模型在渠道定价上也有价，
// 用于验证「目录价优先于渠道/分组价」。
func catalogPlazaChannels(platform, model string, groupIDs []int64) []Channel {
	return []Channel{{
		ID: 1, Name: "catalog-ch", Status: StatusActive, GroupIDs: groupIDs,
		ModelPricing: []ChannelModelPricing{{
			Platform: platform, Models: []string{model}, BillingMode: BillingModeToken,
			// 渠道价刻意与目录价不同（更贵），用来区分数据来源。
			InputPrice: testPtrFloat64(9e-6), OutputPrice: testPtrFloat64(9e-5),
		}},
	}}
}

// TestListGroups_CatalogPricingTakesPrecedence 覆盖 A10/§3.4：目录价层接管时，
// 广场的 pricing 与 catalog_pricing 都取目录价，而不是渠道/分组价。
func TestListPlazaGroups_CatalogPricingTakesPrecedence(t *testing.T) {
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1}}
	svc := newPlazaCatalogService(t, `{
		"claude-sonnet-4": {
			"input_cost_per_token": 1e-06,
			"output_cost_per_token": 5e-06,
			"_catalog": {"platform": "anthropic", "custom": true}
		}
	}`, catalogPlazaChannels(PlatformAnthropic, "claude-sonnet-4", []int64{10}),
		groups, map[int64]string{10: PlatformAnthropic}, nil)

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, 1, out[0].CatalogModelCount, "目录价接管应计入分组统计")
	m := out[0].Models[0]

	require.NotNil(t, m.CatalogPricing, "目录价层接管时必须给出 catalog_pricing")
	require.InDelta(t, 1e-6, *m.CatalogPricing.InputPrice, 1e-12)
	require.InDelta(t, 5e-6, *m.CatalogPricing.OutputPrice, 1e-12)
	// 展示单价同样取目录价（与计费同源），不是渠道的 9e-6。
	require.NotNil(t, m.Pricing)
	require.InDelta(t, 1e-6, *m.Pricing.InputPrice, 1e-12)
	require.InDelta(t, 5e-6, *m.Pricing.OutputPrice, 1e-12)
}

// TestListGroups_DiscountedPricingIsCatalogTimesGroupRate 覆盖 §4.3：
// 折后价 = 目录价 × 分组倍率。
func TestListPlazaGroups_DiscountedPricingIsCatalogTimesGroupRate(t *testing.T) {
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 0.8}}
	svc := newPlazaCatalogService(t, `{
		"claude-sonnet-4": {
			"input_cost_per_token": 4e-06,
			"output_cost_per_token": 1.6e-05,
			"_catalog": {"platform": "anthropic", "custom": true}
		}
	}`, catalogPlazaChannels(PlatformAnthropic, "claude-sonnet-4", []int64{10}),
		groups, map[int64]string{10: PlatformAnthropic}, nil)

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]

	require.NotNil(t, m.DiscountedPricing)
	require.InDelta(t, 4e-6*0.8, *m.DiscountedPricing.InputPrice, 1e-15)
	require.InDelta(t, 1.6e-5*0.8, *m.DiscountedPricing.OutputPrice, 1e-15)
	// 原始目录价不受折后计算影响（前端要同时展示两列）。
	require.InDelta(t, 4e-6, *m.CatalogPricing.InputPrice, 1e-15)
}

// TestListGroups_DifferentGroupRatesYieldDifferentDiscountedPricing 覆盖 A11：
// 同一模型挂在两个不同倍率的分组下，折后价必须不同且各自等于目录价 × 该组倍率。
func TestListPlazaGroups_DifferentGroupRatesYieldDifferentDiscountedPricing(t *testing.T) {
	groups := []Group{
		{ID: 10, Name: "standard", Platform: PlatformAnthropic, RateMultiplier: 1},
		{ID: 20, Name: "vip", Platform: PlatformAnthropic, RateMultiplier: 0.5},
	}
	svc := newPlazaCatalogService(t, `{
		"claude-sonnet-4": {
			"input_cost_per_token": 4e-06,
			"output_cost_per_token": 1.6e-05,
			"_catalog": {"platform": "anthropic", "custom": true}
		}
	}`, catalogPlazaChannels(PlatformAnthropic, "claude-sonnet-4", []int64{10, 20}),
		groups, map[int64]string{10: PlatformAnthropic, 20: PlatformAnthropic}, nil)

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := make(map[string]PlazaGroup, len(out))
	for _, g := range out {
		byName[g.Name] = g
	}

	standard := byName["standard"].Models[0]
	vip := byName["vip"].Models[0]
	require.InDelta(t, 4e-6, *standard.DiscountedPricing.InputPrice, 1e-15)
	require.InDelta(t, 2e-6, *vip.DiscountedPricing.InputPrice, 1e-15)
	require.NotEqual(t, *standard.DiscountedPricing.InputPrice, *vip.DiscountedPricing.InputPrice,
		"不同分组倍率下折后价必须不同")
	// 目录价本身与分组无关。
	require.InDelta(t, *standard.CatalogPricing.InputPrice, *vip.CatalogPricing.InputPrice, 1e-15)
}

// TestListGroups_CatalogPeakPricingExposed 覆盖 PlazaModel.PeakPricing：
// `_catalog.peak` 配置的高峰价按 peak 原样给出，不影响目录基础价。
func TestListPlazaGroups_CatalogPeakPricingExposed(t *testing.T) {
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformDeepseek, RateMultiplier: 1}}
	svc := newPlazaCatalogService(t, `{
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
	}`, catalogPlazaChannels(PlatformDeepseek, "deepseek-v4-flash", []int64{10}),
		groups, map[int64]string{10: PlatformDeepseek}, nil)

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]

	require.NotNil(t, m.PeakPricing, "启用自定义峰谷时应给出峰价")
	require.InDelta(t, 6e-7, *m.PeakPricing.InputPrice, 1e-15)
	require.InDelta(t, 2.4e-6, *m.PeakPricing.OutputPrice, 1e-15)
	// 基础目录价保持低谷价。
	require.InDelta(t, 3e-7, *m.CatalogPricing.InputPrice, 1e-15)
}

// TestListGroups_CatalogDisabledFallsBackToChannelPricing 覆盖 A9：
// catalog_pricing_authoritative=false 时不接管，广场回到渠道价且无 catalog 数据。
func TestListPlazaGroups_CatalogDisabledFallsBackToChannelPricing(t *testing.T) {
	disabled := false
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1}}
	svc := newPlazaCatalogService(t, `{
		"claude-sonnet-4": {
			"input_cost_per_token": 1e-06,
			"_catalog": {"platform": "anthropic", "custom": true}
		}
	}`, catalogPlazaChannels(PlatformAnthropic, "claude-sonnet-4", []int64{10}),
		groups, map[int64]string{10: PlatformAnthropic}, &disabled)

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]

	require.Nil(t, m.CatalogPricing, "开关关闭时不得接管目录价")
	require.Zero(t, out[0].CatalogModelCount)
	require.NotNil(t, m.Pricing)
	require.InDelta(t, 9e-6, *m.Pricing.InputPrice, 1e-15, "回退到渠道定价")
}

// TestPlazaGroupEffectiveRate_MediaIndependentRates 覆盖倍率口径：
// 图片/视频独立倍率开启时取独立倍率，否则用户专属倍率优先于分组默认倍率。
func TestPlazaGroupEffectiveRate_MediaIndependentRates(t *testing.T) {
	group := &PlazaGroup{
		RateMultiplier: 1.2, IsExclusive: false,
		ImageRateIndependent: true, ImageRateMultiplier: 1,
		VideoRateIndependent: true, VideoRateMultiplier: 0.7,
	}
	userRate := 0.5

	require.InDelta(t, 1.0, PlazaGroupEffectiveRate(group, BillingModeImage, &userRate), 1e-12,
		"图片独立倍率开启时不取分组/专属倍率")
	require.InDelta(t, 0.7, PlazaGroupEffectiveRate(group, BillingModeVideo, &userRate), 1e-12,
		"视频独立倍率开启时不取分组/专属倍率")
	require.InDelta(t, 0.5, PlazaGroupEffectiveRate(group, BillingModeToken, &userRate), 1e-12,
		"token 模型用户专属倍率优先")
	require.InDelta(t, 1.2, PlazaGroupEffectiveRate(group, BillingModeToken, nil), 1e-12,
		"无专属倍率时回落分组默认倍率")
}

// TestPlazaDiscountedPricing_ScalesEveryTier 覆盖折后价对阶梯逐档折算。
func TestPlazaDiscountedPricing_ScalesEveryTier(t *testing.T) {
	maxTokens := 272000
	pricing := &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(2e-6),
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: &maxTokens, TierLabel: "≤272K", InputPrice: testPtrFloat64(2e-6)},
			{MinTokens: 272000, TierLabel: ">272K", InputPrice: testPtrFloat64(4e-6)},
		},
	}

	out := PlazaDiscountedPricing(pricing, 0.5)
	require.NotNil(t, out)
	require.InDelta(t, 1e-6, *out.InputPrice, 1e-15)
	require.Len(t, out.Intervals, 2)
	require.InDelta(t, 1e-6, *out.Intervals[0].InputPrice, 1e-15)
	require.InDelta(t, 2e-6, *out.Intervals[1].InputPrice, 1e-15)
	// 入参不被修改。
	require.InDelta(t, 2e-6, *pricing.InputPrice, 1e-15)
	require.Nil(t, PlazaDiscountedPricing(nil, 0.5))
}
