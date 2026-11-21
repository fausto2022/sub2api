package service

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
)

// PlazaOfficialPricing 模型广场展示用的官方参考价（USD per token），与计费同源：
// LiteLLM → 内置兜底价卡 → 模型策略。字段为 nil 表示该项缺失（0 视为未配置）。
type PlazaOfficialPricing struct {
	InputPrice         *float64
	OutputPrice        *float64
	CacheWritePrice    *float64 // 5m 缓存写入（= LiteLLM cache_creation）
	CacheWrite1hPrice  *float64 // 1h 缓存写入，仅计费会区分 5m/1h 时给出
	CacheWrite12hPrice *float64 // OpenAI 12h cache-write price
	CacheReadPrice     *float64
	// Intervals 官方长上下文阶梯（多档时给出），不受分组开关影响。
	Intervals []PricingInterval
}

// PlazaModel 模型广场中单个模型条目：按实收口径合成的展示定价 + 官方参考价。
type PlazaModel struct {
	Name     string
	Platform string
	// CatalogPricing 管理员在「模型价格」页配置的目录价（USD per token，未乘倍率）。
	// 仅当该模型被目录价层接管（override 条目带 `_catalog` 元数据且可计价）时给出；
	// 为 nil 表示该模型未纳入目录价，前端回落到 Pricing 展示。
	CatalogPricing *ChannelModelPricing
	// Pricing 实收单价（已按计费同源口径合成；目录价接管时即目录价，仍未乘分组倍率）。
	Pricing *ChannelModelPricing
	// DiscountedPricing 折后价 = Pricing × 生效分组倍率，仅 token 计费模式给出。
	// 分量与 Pricing 对应（输入/输出/缓存读写、阶梯逐档），前端可直接展示而无需再乘倍率。
	DiscountedPricing *ChannelModelPricing
	// PeakPricing 模型级自定义高峰价（USD per token，未乘倍率）；未启用峰谷时为 nil。
	PeakPricing     *PlazaOfficialPricing
	OfficialPricing *PlazaOfficialPricing
	// LongContextBasis 多档时的计价基准（整单 / 仅超出部分），单档为空。
	LongContextBasis ContextPricingBasis
	// TimePricing 计费会生效的分时倍率时段；无分时为 nil。
	TimePricing *TimePricingSchedule
}

// PlazaGroup 模型广场中以分组为顶层的条目。
//
// 与 AvailableGroupRef 相比多了 Description 与 Models；Models 来自该分组关联渠道的
// 支持模型（普通分组按分组平台隔离，Composite 分组展开关联渠道已配置的
// 具体平台），与「可用渠道」页口径一致。
type PlazaGroup struct {
	ID                 int64
	Name               string
	Description        string
	I18n               GroupI18n
	Platform           string
	SubscriptionType   string
	RateMultiplier     float64
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	IsExclusive        bool
	// 图片按次实付倍率：ImageRateIndependent 为 true 时，图片计费模型的实付
	// = 档位价 × ImageRateMultiplier，不乘分组/用户专属倍率（与计费口径一致）。
	ImageRateIndependent bool
	ImageRateMultiplier  float64
	// 视频独立倍率与图片独立倍率分别配置，开启时覆盖分组/用户专属倍率。
	VideoRateIndependent bool
	VideoRateMultiplier  float64
	// LongContextPricingEnabled 分组是否按上下文长度应用阶梯价；关闭时模型展示的是最低档。
	LongContextPricingEnabled bool
	// CatalogModelCount 该分组中价格来自模型目录价层（`_catalog` 接管）的模型数量。
	// 用于前端标注「本组 N 个模型按目录价计价」，未接管模型不计入。
	CatalogModelCount int
	Models            []PlazaModel
}

// ModelPlazaService 聚合模型广场数据。
//
// 模型枚举来自渠道配置；token 模型的展示单价与阶梯由 BillingService 的阶梯表
// 查询给出（与扣费走同一条解析链与计费函数），图片/按次模型沿用渠道/分组档位价。
type ModelPlazaService struct {
	channelRepo    ChannelRepository
	groupRepo      GroupRepository
	pricingService *PricingService
	billingService *BillingService
	resolver       *ModelPricingResolver
}

// NewModelPlazaService 创建模型广场服务。
func NewModelPlazaService(
	channelRepo ChannelRepository,
	groupRepo GroupRepository,
	pricingService *PricingService,
	billingService *BillingService,
	resolver *ModelPricingResolver,
) *ModelPlazaService {
	return &ModelPlazaService{
		channelRepo:    channelRepo,
		groupRepo:      groupRepo,
		pricingService: pricingService,
		billingService: billingService,
		resolver:       resolver,
	}
}

// ListGroups 返回模型广场数据：每个活跃分组附带其可用模型与定价。
//
// 模型枚举口径与 ListAvailable 一致（Active 渠道、SupportedModels ∪ 全局定价回落、
// 平台隔离），仅把顶层从渠道换成分组：
//   - 渠道按 lower(name) 排序后遍历，保证同名模型去重结果确定；
//   - 同分组同名模型「先见者胜」，仅当已存条目无定价而新条目有定价时升级替换；
//   - token 模型的单价与阶梯按实收口径合成（见 ResolveContextPricingSchedule），
//     图片计费模型的档位价按实收口径合成（见 plazaImageDisplayPricing）；
//   - 每个模型附带官方参考价（查不到为 nil）；
//   - 只返回 Models 非空的分组；分组按 RateMultiplier 升序（同倍率按名称），
//     组内模型按名称排序。
//
// 可见性过滤（专属分组）不在此层做，由 handler 按登录态裁剪。
func (s *ModelPlazaService) ListGroups(ctx context.Context) ([]PlazaGroup, error) {
	channels, err := s.channelRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	sort.SliceStable(channels, func(i, j int) bool {
		return strings.ToLower(channels[i].Name) < strings.ToLower(channels[j].Name)
	})

	byGroup := make(map[int64]*PlazaGroup, len(groups))
	groupEnt := make(map[int64]*Group, len(groups))
	order := make([]int64, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		byGroup[g.ID] = &PlazaGroup{
			ID:                        g.ID,
			Name:                      g.Name,
			Description:               g.Description,
			I18n:                      g.I18n,
			Platform:                  g.Platform,
			SubscriptionType:          g.SubscriptionType,
			RateMultiplier:            g.RateMultiplier,
			PeakRateEnabled:           g.PeakRateEnabled,
			PeakStart:                 g.PeakStart,
			PeakEnd:                   g.PeakEnd,
			PeakRateMultiplier:        g.PeakRateMultiplier,
			IsExclusive:               g.IsExclusive,
			ImageRateIndependent:      g.ImageRateIndependent,
			ImageRateMultiplier:       g.ImageRateMultiplier,
			VideoRateIndependent:      g.VideoRateIndependent,
			VideoRateMultiplier:       g.VideoRateMultiplier,
			LongContextPricingEnabled: g.LongContextPricingEnabled,
		}
		groupEnt[g.ID] = g
		order = append(order, g.ID)
	}

	type modelKey struct {
		platform string
		name     string
	}
	// modelIdx[groupID][platform+modelName] = index into byGroup[groupID].Models
	modelIdx := make(map[int64]map[modelKey]int, len(groups))
	for i := range channels {
		ch := &channels[i]
		if ch.Status != StatusActive {
			continue
		}
		ch.normalizeBillingModelSource()
		supported := ch.SupportedModels()
		fillGlobalPricingFallback(s.pricingService, supported)

		for _, gid := range ch.GroupIDs {
			pg, ok := byGroup[gid]
			if !ok {
				continue
			}
			idx := modelIdx[gid]
			if idx == nil {
				idx = make(map[modelKey]int, len(supported))
				modelIdx[gid] = idx
			}
			for j := range supported {
				m := supported[j]
				if pg.Platform == PlatformComposite {
					if !isConcreteRequestPlatform(m.Platform) {
						continue
					}
				} else if m.Platform != pg.Platform {
					continue
				}
				key := modelKey{platform: m.Platform, name: m.Name}
				if at, seen := idx[key]; seen {
					// 先见者胜；仅当已存条目无定价而新条目有定价时升级。
					if pg.Models[at].Pricing == nil && m.Pricing != nil {
						pg.Models[at].Pricing = m.Pricing
					}
					continue
				}
				idx[key] = len(pg.Models)
				pg.Models = append(pg.Models, PlazaModel{
					Name:     m.Name,
					Platform: m.Platform,
					Pricing:  m.Pricing,
				})
			}
		}
	}

	officialMemo := make(map[string]*PlazaOfficialPricing)
	out := make([]PlazaGroup, 0, len(order))
	for _, gid := range order {
		pg := byGroup[gid]
		if len(pg.Models) == 0 {
			continue
		}
		sort.SliceStable(pg.Models, func(i, j int) bool {
			if pg.Models[i].Name != pg.Models[j].Name {
				return pg.Models[i].Name < pg.Models[j].Name
			}
			return pg.Models[i].Platform < pg.Models[j].Platform
		})
		g := groupEnt[gid]
		for j := range pg.Models {
			s.fillDisplayPricing(ctx, &pg.Models[j], g)
			pg.Models[j].OfficialPricing = s.lookupOfficialPricing(ctx, pg.Models[j].Name, officialMemo)
			// 折后价 = 展示单价 × 分组默认生效倍率（design.md §4.3）。
			// 用户专属倍率在 handler 层按登录态覆盖，见 PlazaDiscountedPricing。
			pg.Models[j].DiscountedPricing = PlazaDiscountedPricing(
				pg.Models[j].Pricing,
				PlazaGroupEffectiveRate(pg, plazaBillingModeOf(pg.Models[j].Pricing), nil),
			)
			if pg.Models[j].CatalogPricing != nil {
				pg.CatalogModelCount++
			}
		}
		out = append(out, *pg)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RateMultiplier != out[j].RateMultiplier {
			return out[i].RateMultiplier < out[j].RateMultiplier
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// fillDisplayPricing 把模型的展示定价换成实收口径：
// token 模型取计费阶梯表（单价与档位均由真实计费函数得出），
// 图片/按次模型（或阶梯表不可用时）沿用渠道定价与分组图片档位价。
//
// 目录价优先（design.md §3.3/§3.4）：模型被「模型价格」页的目录价层接管时，
// 展示价与计费同源直接取目录价，分组模型级固定价不再参与（resolver 的目录价层
// 优先级最高，计费同样会跳过分组卡）。目录价层开关关闭时不接管，回退旧口径。
func (s *ModelPlazaService) fillDisplayPricing(ctx context.Context, m *PlazaModel, g *Group) {
	catalogPricing, catalogPeak := s.catalogDisplayPricing(m.Name)
	m.CatalogPricing = catalogPricing
	m.PeakPricing = catalogPeak

	if catalogPricing != nil {
		m.Pricing = catalogPricing
	} else if groupPricing := matchGroupModelPricing(g, m.Name); groupPricing != nil {
		m.Pricing = groupPricing
		// 分组模型级固定价就是实收价，不能再被分组图片兜底档位覆盖。
		// 否则模型广场会显示 image_price_1k/2k/4k，而实际按 model_pricing 扣费。
		if groupPricing.BillingMode == BillingModeImage || groupPricing.BillingMode == BillingModePerRequest {
			return
		}
	}
	if catalogPricing != nil && catalogPricing.BillingMode != BillingModeToken {
		// 目录价给出的是按次/图片档位价，阶梯探针不适用。
		return
	}
	if s.billingService != nil && s.resolver != nil {
		sched, err := s.billingService.ResolveContextPricingSchedule(ctx, s.resolver, ContextPricingScheduleInput{
			Model:    m.Name,
			Group:    g,
			Platform: m.Platform,
		})
		if err == nil && sched != nil && len(sched.Tiers) > 0 {
			m.Pricing = plazaPricingFromSchedule(m.Pricing, sched)
			if len(sched.Tiers) > 1 {
				m.LongContextBasis = sched.Basis
			}
			m.TimePricing = sched.TimePricing
			return
		}
	}
	if catalogPricing != nil {
		// 目录价已给出单价，不再套用分组图片兜底档位。
		return
	}
	m.Pricing = plazaImageDisplayPricing(m.Pricing, g)
}

// catalogDisplayPricing 返回模型在目录价层的展示价与高峰价。
//
// 与计费同源：直接复用 resolver 的目录价层解析（`_catalog` 元数据 + 目录价层开关 +
// 基础价表回退），因此广场展示的目录价与实际扣费价一致。返回 nil 表示该模型未被
// 目录价层接管（未配置 `_catalog`、开关关闭、或条目没有任何可计价字段）。
func (s *ModelPlazaService) catalogDisplayPricing(model string) (*ChannelModelPricing, *PlazaOfficialPricing) {
	if s == nil || s.resolver == nil || s.resolver.billingService == nil || model == "" {
		return nil, nil
	}
	resolved := s.resolver.resolveCatalogPricing(model)
	if resolved == nil || resolved.BasePricing == nil {
		return nil, nil
	}
	pricing := plazaPricingFromModelPricing(resolved.BasePricing)
	if !plazaHasTokenPrice(pricing) {
		pricing = nil
	}
	peak := plazaPeakPricingFromAware(resolved.peakAware, resolved.BasePricing)
	if pricing == nil && peak == nil {
		return nil, nil
	}
	return pricing, peak
}

// plazaPricingFromModelPricing 把计费侧的 per-token 价格转换成广场展示价；
// 0 值按「未配置」处理（与官方参考价口径一致）。
func plazaPricingFromModelPricing(mp *ModelPricing) *ChannelModelPricing {
	if mp == nil {
		return nil
	}
	out := &ChannelModelPricing{
		BillingMode:                BillingModeToken,
		InputPrice:                 nonZeroPtr(mp.InputPricePerToken),
		OutputPrice:                nonZeroPtr(mp.OutputPricePerToken),
		CacheWritePrice:            nonZeroPtr(mp.CacheCreationPricePerToken),
		CacheReadPrice:             nonZeroPtr(mp.CacheReadPricePerToken),
		ImageInputPrice:            nonZeroPtr(mp.ImageInputPricePerToken),
		ReasoningEffortMultipliers: maps.Clone(mp.ReasoningEffortMultipliers),
	}
	// 与官方参考价一致：只有支持 5m/1h 分档时才展示 1h 价。
	if mp.SupportsCacheBreakdown {
		out.CacheWrite1hPrice = nonZeroPtr(mp.CacheCreation1hPrice)
	}
	if mp.CacheCreation12hPricePerToken != nil {
		out.CacheWrite12hPrice = nonZeroPtr(*mp.CacheCreation12hPricePerToken)
	}
	return out
}

// plazaHasTokenPrice 报告展示价是否至少有一个可展示的 token 单价。
func plazaHasTokenPrice(p *ChannelModelPricing) bool {
	if p == nil {
		return false
	}
	return p.InputPrice != nil || p.OutputPrice != nil || p.CacheWritePrice != nil ||
		p.CacheWrite1hPrice != nil || p.CacheWrite12hPrice != nil || p.CacheReadPrice != nil
}

// plazaPeakPricingFromAware 把模型级峰谷配置转换成广场展示用的高峰价。
//
// 回落规则与计费侧 ResolvePeakPricing 一致（design.md §0.5）：
//   - 只填峰价 → 峰价生效；
//   - 只填谷价 → 峰价 = 谷价 × 2（保持官方倍率语义）；
//   - 谷价留空 → 谷价回落基础价，峰价缺省时同样按谷价 × 2 推导。
//
// 未启用峰谷或推导不出任何峰价时返回 nil（前端隐藏该列）。
func plazaPeakPricingFromAware(peak PeakAwarePricing, base *ModelPricing) *PlazaOfficialPricing {
	if !peak.Enabled {
		return nil
	}
	offPeak := peak.OffPeak
	if offPeak.IsZero() {
		if base == nil {
			return nil
		}
		offPeak = PeakPricingAmounts{
			Input:      base.InputPricePerToken,
			Output:     base.OutputPricePerToken,
			CacheRead:  base.CacheReadPricePerToken,
			CacheWrite: base.CacheCreationPricePerToken,
		}
	}
	high := peak.Peak
	if high.IsZero() {
		high = PeakPricingAmounts{
			Input:      offPeak.Input * 2,
			Output:     offPeak.Output * 2,
			CacheRead:  offPeak.CacheRead * 2,
			CacheWrite: offPeak.CacheWrite * 2,
		}
	}
	if high.IsZero() {
		return nil
	}
	return &PlazaOfficialPricing{
		InputPrice:      nonZeroPtr(high.Input),
		OutputPrice:     nonZeroPtr(high.Output),
		CacheWritePrice: nonZeroPtr(high.CacheWrite),
		CacheReadPrice:  nonZeroPtr(high.CacheRead),
	}
}

// PlazaGroupEffectiveRate 返回模型在该分组下实际生效的展示倍率。
//
// 与计费口径一致：图片/视频独立倍率开启时取独立倍率（不乘分组/用户专属倍率），
// 否则用户专属倍率优先，未配置时回退分组默认倍率。
func PlazaGroupEffectiveRate(g *PlazaGroup, mode BillingMode, userRate *float64) float64 {
	if g == nil {
		return 1
	}
	switch mode {
	case BillingModeImage:
		if g.ImageRateIndependent {
			return g.ImageRateMultiplier
		}
	case BillingModeVideo:
		if g.VideoRateIndependent {
			return g.VideoRateMultiplier
		}
	}
	if userRate != nil {
		return *userRate
	}
	return g.RateMultiplier
}

// PlazaDiscountedPricing 返回折后价 = 展示单价 × 生效倍率（design.md §4.3）。
//
// 入参与返回都是未乘倍率的展示口径之外的新结构，不修改入参。目录价/实收价、
// 阶梯逐档单价全部按同一倍率折算，便于前端直接展示「折后价」列。
func PlazaDiscountedPricing(p *ChannelModelPricing, rate float64) *ChannelModelPricing {
	if p == nil {
		return nil
	}
	out := p.Clone()
	out.InputPrice = scalePricePtr(out.InputPrice, rate)
	out.OutputPrice = scalePricePtr(out.OutputPrice, rate)
	out.CacheWritePrice = scalePricePtr(out.CacheWritePrice, rate)
	out.CacheWrite1hPrice = scalePricePtr(out.CacheWrite1hPrice, rate)
	out.CacheWrite12hPrice = scalePricePtr(out.CacheWrite12hPrice, rate)
	out.CacheReadPrice = scalePricePtr(out.CacheReadPrice, rate)
	out.ImageInputPrice = scalePricePtr(out.ImageInputPrice, rate)
	out.ImageOutputPrice = scalePricePtr(out.ImageOutputPrice, rate)
	out.PerRequestPrice = scalePricePtr(out.PerRequestPrice, rate)
	for i := range out.Intervals {
		out.Intervals[i].InputPrice = scalePricePtr(out.Intervals[i].InputPrice, rate)
		out.Intervals[i].OutputPrice = scalePricePtr(out.Intervals[i].OutputPrice, rate)
		out.Intervals[i].CacheWritePrice = scalePricePtr(out.Intervals[i].CacheWritePrice, rate)
		out.Intervals[i].CacheWrite1hPrice = scalePricePtr(out.Intervals[i].CacheWrite1hPrice, rate)
		out.Intervals[i].CacheWrite12hPrice = scalePricePtr(out.Intervals[i].CacheWrite12hPrice, rate)
		out.Intervals[i].CacheReadPrice = scalePricePtr(out.Intervals[i].CacheReadPrice, rate)
		out.Intervals[i].PerRequestPrice = scalePricePtr(out.Intervals[i].PerRequestPrice, rate)
	}
	return &out
}

// scalePricePtr 按倍率折算单价；nil 透传（未配置保持未配置）。
func scalePricePtr(p *float64, rate float64) *float64 {
	if p == nil {
		return nil
	}
	value := *p * rate
	return &value
}

// plazaBillingModeOf 返回展示定价的计费模式，空值按 token 处理。
func plazaBillingModeOf(p *ChannelModelPricing) BillingMode {
	if p == nil || p.BillingMode == "" {
		return BillingModeToken
	}
	return p.BillingMode
}

// plazaPricingFromSchedule 把阶梯表压成展示用的 ChannelModelPricing：
// 平价取首档单价，多档时 Intervals 逐档给出绝对单价；图片/按次字段沿用原始定价。
func plazaPricingFromSchedule(raw *ChannelModelPricing, sched *ContextPricingSchedule) *ChannelModelPricing {
	out := &ChannelModelPricing{BillingMode: BillingModeToken}
	if raw != nil {
		out.ImageInputPrice = raw.ImageInputPrice
		out.ImageOutputPrice = raw.ImageOutputPrice
		out.PerRequestPrice = raw.PerRequestPrice
		out.ReasoningEffortMultipliers = reasoningEffortMultipliersFromPricing(raw)
	}
	first := sched.Tiers[0]
	out.InputPrice = first.Input
	out.OutputPrice = first.Output
	out.CacheWritePrice = first.CacheWrite
	out.CacheWrite1hPrice = first.CacheWrite1h
	out.CacheWrite12hPrice = first.CacheWrite12h
	out.CacheReadPrice = first.CacheRead
	if len(sched.Tiers) > 1 {
		out.Intervals = plazaIntervalsFromTiers(sched.Tiers)
	}
	return out
}

func plazaIntervalsFromTiers(tiers []ContextPricingTier) []PricingInterval {
	intervals := make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		intervals = append(intervals, PricingInterval{
			MinTokens:          t.MinTokens,
			MaxTokens:          t.MaxTokens,
			TierLabel:          t.Label,
			InputPrice:         t.Input,
			OutputPrice:        t.Output,
			CacheWritePrice:    t.CacheWrite,
			CacheWrite1hPrice:  t.CacheWrite1h,
			CacheWrite12hPrice: t.CacheWrite12h,
			CacheReadPrice:     t.CacheRead,
			SortOrder:          i,
		})
	}
	return intervals
}

// plazaImageDisplayPricing 为图片计费模型合成展示定价，使档位价与实收口径一致：
// 每档（1K/2K/4K）单价 = 分组图片价 > 渠道同档位价 > 渠道默认按次价，无价的档不展示。
// 分组未配任何图片价、或定价非图片模式时原样返回。返回克隆，不修改入参
// （渠道定价指针指向缓存共享数据）。
func plazaImageDisplayPricing(p *ChannelModelPricing, g *Group) *ChannelModelPricing {
	if p == nil || g == nil || p.BillingMode != BillingModeImage {
		return p
	}
	if g.ImagePrice1K == nil && g.ImagePrice2K == nil && g.ImagePrice4K == nil {
		return p
	}
	channelTierPrice := func(label string) *float64 {
		for i := range p.Intervals {
			if p.Intervals[i].TierLabel == label && p.Intervals[i].PerRequestPrice != nil {
				return p.Intervals[i].PerRequestPrice
			}
		}
		return p.PerRequestPrice
	}
	tiers := []struct {
		label      string
		groupPrice *float64
	}{
		{"1K", g.ImagePrice1K},
		{"2K", g.ImagePrice2K},
		{"4K", g.ImagePrice4K},
	}
	clone := *p
	clone.Intervals = make([]PricingInterval, 0, len(tiers))
	for i, t := range tiers {
		price := t.groupPrice
		if price == nil {
			price = channelTierPrice(t.label)
		}
		if price == nil {
			continue
		}
		v := *price
		clone.Intervals = append(clone.Intervals, PricingInterval{
			TierLabel:       t.label,
			PerRequestPrice: &v,
			SortOrder:       i,
		})
	}
	return &clone
}

// lookupOfficialPricing 查询模型的官方参考价（与计费同源：LiteLLM → 内置兜底 → 模型策略），
// 带 memo 避免同名模型重复解析。官方阶梯按无分组、无渠道的口径查阶梯表。
// billingService 为 nil（测试场景）或查不到时返回 nil。
func (s *ModelPlazaService) lookupOfficialPricing(ctx context.Context, modelName string, memo map[string]*PlazaOfficialPricing) *PlazaOfficialPricing {
	if s.billingService == nil {
		return nil
	}
	if cached, ok := memo[modelName]; ok {
		return cached
	}
	var result *PlazaOfficialPricing
	if mp, err := s.billingService.GetModelPricing(modelName); err == nil && mp != nil {
		result = &PlazaOfficialPricing{
			InputPrice:      nonZeroPtr(mp.InputPricePerToken),
			OutputPrice:     nonZeroPtr(mp.OutputPricePerToken),
			CacheWritePrice: nonZeroPtr(mp.CacheCreationPricePerToken),
			CacheReadPrice:  nonZeroPtr(mp.CacheReadPricePerToken),
		}
		// 计费只在支持 5m/1h 分档时使用 1h 价，其余情况 1h 价对用户无意义。
		if mp.SupportsCacheBreakdown {
			result.CacheWrite1hPrice = nonZeroPtr(mp.CacheCreation1hPrice)
		}
		result.CacheWrite12hPrice = mp.CacheCreation12hPricePerToken
		if s.resolver != nil {
			sched, schedErr := s.billingService.ResolveContextPricingSchedule(ctx, s.resolver, ContextPricingScheduleInput{Model: modelName})
			if schedErr == nil && sched != nil && len(sched.Tiers) > 1 {
				result.Intervals = plazaIntervalsFromTiers(sched.Tiers)
			}
		}
		if result.InputPrice == nil && result.OutputPrice == nil && result.CacheWritePrice == nil &&
			result.CacheWrite1hPrice == nil && result.CacheWrite12hPrice == nil && result.CacheReadPrice == nil && len(result.Intervals) == 0 {
			result = nil
		}
	}
	memo[modelName] = result
	return result
}
