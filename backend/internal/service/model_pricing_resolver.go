package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
)

// PricingSource 定价来源标识
const (
	PricingSourceGroup    = "group"
	PricingSourceChannel  = "channel"
	PricingSourceLiteLLM  = "litellm"
	PricingSourceFallback = "fallback"
)

// PricingSourceCatalog 是「模型目录价」层的来源标识。
//
// 该层对应管理员在「模型价格」页显式接管（override 文件中带 `_catalog` 元数据）的模型，
// 按 design.md §3.3 排在 group > channel > litellm > fallback 之前。为了可回滚，
// settings.catalog_pricing_authoritative=false 时整层跳过，优先级退回旧口径。
const PricingSourceCatalog = "catalog"

const (
	// SettingKeyCatalogPricingAuthoritative 是「目录价层是否为最高优先级」的开关键。
	// 未配置或为空按默认 true 处理。
	SettingKeyCatalogPricingAuthoritative = "catalog_pricing_authoritative"
	// SettingKeyPeakPricingSettings 是全局峰谷时段设置的 settings 键
	// （JSON：PeakPricingSettings，见 design.md §3.2）。
	SettingKeyPeakPricingSettings = "peak_pricing_settings"
)

// ResolvedPricing 统一定价解析结果
type ResolvedPricing struct {
	// Mode 计费模式
	Mode BillingMode

	// Token 模式：基础定价（来自 LiteLLM 或 fallback）
	BasePricing *ModelPricing

	// Token 模式：区间定价列表（如有，覆盖 BasePricing 中的对应字段）
	Intervals []PricingInterval

	// 按次/图片模式：分层定价
	RequestTiers []PricingInterval

	// 按次/图片模式：默认价格（未命中层级时使用）
	DefaultPerRequestPrice float64

	// 来源标识
	Source string // "catalog", "group", "channel", "litellm", "fallback"

	// 是否支持缓存细分
	SupportsCacheBreakdown bool

	// 渠道定价原始配置（用于区间模式下获取 ImageOutputPrice）
	channelPricing *ChannelModelPricing

	// peakAware 是目录价层携带的模型级峰谷双价配置。
	//
	// 实际选峰/选谷在 BillingService 按请求时刻完成（经由 PricingService 的
	// peakConfigs，与 override 文件同源），这里保留一份用于诊断与测试断言。
	peakAware PeakAwarePricing

	longContextPricingEnabled bool
}

// ModelPricingResolver 统一模型定价解析器。
//
// 解析链：Catalog → Group → Channel → LiteLLM → Fallback。
// Catalog 层是管理员在「模型价格」页显式接管的目录价（override 文件中带 `_catalog`
// 元数据），优先级最高。settings.catalog_pricing_authoritative=false 时整层跳过，
// 退回旧的 Group → Channel → LiteLLM → Fallback 链。
type ModelPricingResolver struct {
	channelService *ChannelService
	billingService *BillingService
	// authoritativeCatalog 是目录价层开关的进程内快照（原子读写）。
	// nil 表示未安装（测试或旧调用路径），按默认 true 处理。
	authoritativeCatalog atomic.Pointer[bool]
	// catalogMu 保护 catalogOverrides 的换入换出。
	catalogMu sync.RWMutex
	// catalogOverrides 是 override 文件原始条目（含 `_catalog` 元数据），
	// 由 SetCatalogOverrideSource 安装，供目录价层解析。
	catalogOverrides func() map[string]json.RawMessage
}

// NewModelPricingResolver 创建定价解析器实例
func NewModelPricingResolver(channelService *ChannelService, billingService *BillingService) *ModelPricingResolver {
	return &ModelPricingResolver{
		channelService: channelService,
		billingService: billingService,
	}
}

// SetCatalogPricingAuthoritative 安装「目录价层是否最高优先级」开关。
//
// 由 ModelPricingAdminService 在保存 settings 的 catalog_pricing_authoritative 项后
// 调用（启动时由 LoadCatalogPricingAuthoritative 回填），热生效。nil 或未安装按默认 true。
func (r *ModelPricingResolver) SetCatalogPricingAuthoritative(enabled bool) {
	if r == nil {
		return
	}
	value := enabled
	r.authoritativeCatalog.Store(&value)
}

// CatalogPricingAuthoritative 返回目录价层开关的当前值（默认 true）。
func (r *ModelPricingResolver) CatalogPricingAuthoritative() bool {
	if r == nil {
		return true
	}
	if value := r.authoritativeCatalog.Load(); value != nil {
		return *value
	}
	return true
}

// SetCatalogOverrideSource 安装目录价层的原始 override 条目读取函数。
//
// 传入 nil 表示未安装（测试/只有 LiteLLM 的路径），此时目录价层整体不可用。
// 读取函数由 PricingService 提供，保证与热重载同源。
func (r *ModelPricingResolver) SetCatalogOverrideSource(source func() map[string]json.RawMessage) {
	if r == nil {
		return
	}
	r.catalogMu.Lock()
	r.catalogOverrides = source
	r.catalogMu.Unlock()
}

// catalogOverrideEntries 返回目录价层的原始条目（未安装时 nil）。
//
// 未安装读取函数时回落到 PricingService 的 override 文件读取（同一 package，与
// 热重载同源），因此只要 wiring 了 billingService 就能工作。
func (r *ModelPricingResolver) catalogOverrideEntries() map[string]json.RawMessage {
	if r == nil {
		return nil
	}
	r.catalogMu.RLock()
	source := r.catalogOverrides
	r.catalogMu.RUnlock()
	if source != nil {
		return source()
	}
	if r.billingService == nil {
		return nil
	}
	pricingService := r.billingService.pricingService
	if pricingService == nil {
		return nil
	}
	return pricingService.loadPricingOverrideEntries()
}

// resolveCatalogPricing 尝试按目录价层解析。
//
// 只接管「管理员显式打了 `_catalog` 元数据」的模型（design.md §3.3）：普通 USD 覆盖
// 保持原有语义（进入 LiteLLM 层价格表），不会因为写了 override 就跳层。返回 nil 表示
// 该模型不由目录价层接管，调用方继续走旧链。
//
// 开关（settings.catalog_pricing_authoritative）由 ModelPricingAdminService 安装，
// 关闭时返回 nil，调用方自然回退到 group > channel > litellm > fallback。
func (r *ModelPricingResolver) resolveCatalogPricing(model string) *ResolvedPricing {
	if r == nil || !r.CatalogPricingAuthoritative() {
		return nil
	}
	entries := r.catalogOverrideEntries()
	if len(entries) == 0 {
		return nil
	}
	key, raw, ok := lookupCatalogOverrideEntry(entries, model)
	if !ok {
		return nil
	}
	meta, hasMeta := parseCatalogMeta(raw)
	if !hasMeta {
		return nil
	}
	pricing, ok := r.catalogBasePricing(key, raw, meta)
	if !ok {
		return nil
	}

	resolved := &ResolvedPricing{
		Mode:                   BillingModeToken,
		BasePricing:            pricing,
		Source:                 PricingSourceCatalog,
		SupportsCacheBreakdown: pricing.SupportsCacheBreakdown,
	}
	// 目录价层按模型级峰谷双价生效（时段判定在 BillingService 按请求时刻完成）。
	resolved.peakAware = peakAwareFromMeta(meta)
	return resolved
}

// catalogEntryPrices 是 override 条目自身的标准价格字段（per-token USD），
// 解析后用于构造目录价层的基础价。
type catalogEntryPrices struct {
	Input          *float64
	Output         *float64
	CacheRead      *float64
	CacheCreation  *float64
	InputPriority  *float64
	OutputPriority *float64
	ImageInput     *float64
}

// parseCatalogEntryPrices 从 override 条目原始 JSON 解析标准价格字段。
// 与 LiteLLM 字段同名字段直接沿用；单条解析失败不报错，返回零值（全部 nil）。
func parseCatalogEntryPrices(raw json.RawMessage) catalogEntryPrices {
	var prices catalogEntryPrices
	if len(raw) == 0 {
		return prices
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		return prices
	}
	read := func(key string) *float64 {
		body, ok := entry[key]
		if !ok {
			return nil
		}
		var value float64
		if err := json.Unmarshal(body, &value); err != nil {
			return nil
		}
		return &value
	}
	prices.Input = read("input_cost_per_token")
	prices.Output = read("output_cost_per_token")
	prices.CacheRead = read("cache_read_input_token_cost")
	prices.CacheCreation = read("cache_creation_input_token_cost")
	prices.InputPriority = read("input_cost_per_token_priority")
	prices.OutputPriority = read("output_cost_per_token_priority")
	prices.ImageInput = read("input_cost_per_image_token")
	return prices
}

// hasAny 报告条目是否至少配置了一项可计价的价格字段。
func (p catalogEntryPrices) hasAny() bool {
	return p.Input != nil || p.Output != nil || p.CacheRead != nil || p.CacheCreation != nil ||
		p.InputPriority != nil || p.OutputPriority != nil || p.ImageInput != nil
}

// catalogBasePricing 从目录价层条目构造 ModelPricing。
//
// 基础价以条目自身的价格字段为准（管理员显式价），未提交的字段回落到基础定价表
// （LiteLLM/fallback）里的同名价。完全没有可计价字段时返回 ok=false，避免目录价层
// 把模型接管成 $0 —— 此时调用方继续走旧链，行为与未接管一致。
func (r *ModelPricingResolver) catalogBasePricing(model string, raw json.RawMessage, meta catalogMeta) (*ModelPricing, bool) {
	base, _ := r.resolveBasePricing(model)
	pricing := ModelPricing{}
	if base != nil {
		pricing = *base
	}
	own := parseCatalogEntryPrices(raw)
	if own.Input != nil {
		pricing.InputPricePerToken = *own.Input
	}
	if own.Output != nil {
		pricing.OutputPricePerToken = *own.Output
	}
	if own.CacheRead != nil {
		pricing.CacheReadPricePerToken = *own.CacheRead
	}
	if own.CacheCreation != nil {
		pricing.CacheCreationPricePerToken = *own.CacheCreation
		pricing.CacheCreationPriceExplicit = true
	}
	if own.InputPriority != nil {
		pricing.InputPricePerTokenPriority = *own.InputPriority
	}
	if own.OutputPriority != nil {
		pricing.OutputPricePerTokenPriority = *own.OutputPriority
	}
	if own.ImageInput != nil {
		pricing.ImageInputPricePerToken = *own.ImageInput
	}

	// 可计价判定：条目自带价、基础价表命中、或（目录外自定义模型的）峰谷/按次价。
	if own.hasAny() || base != nil {
		return &pricing, true
	}
	if meta.PeakEnabled {
		peak := peakAwareFromMeta(meta)
		if !peak.Peak.IsZero() || !peak.OffPeak.IsZero() {
			return &pricing, true
		}
	}
	if meta.PerRequestPrice != nil && *meta.PerRequestPrice > 0 {
		return &pricing, true
	}
	return nil, false
}

// lookupCatalogOverrideEntry 在 override 条目里按模型名查找（大小写不敏感）。
// 先精确匹配，再匹配小写键，最后匹配模型名去掉日期版本后缀的键。
func lookupCatalogOverrideEntry(entries map[string]json.RawMessage, model string) (string, json.RawMessage, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", nil, false
	}
	if raw, ok := entries[model]; ok {
		return model, raw, true
	}
	lower := strings.ToLower(model)
	if raw, ok := entries[lower]; ok {
		return lower, raw, true
	}
	for name, raw := range entries {
		if strings.EqualFold(name, model) {
			return name, raw, true
		}
	}
	for name, raw := range entries {
		if normalizeKnownOpenAICodexModel(name) == normalizeKnownOpenAICodexModel(model) && normalizeKnownOpenAICodexModel(model) != "" {
			return name, raw, true
		}
	}
	return "", nil, false
}

// PricingInput 定价解析输入
type PricingInput struct {
	Model   string
	GroupID *int64 // nil 表示不检查渠道
	Group   *Group
}

// Resolve 解析模型定价。
// 1. 目录价层（管理员显式接管，可用 settings 开关回退）
// 2. 分组价 → 渠道价 → LiteLLM → Fallback
func (r *ModelPricingResolver) Resolve(ctx context.Context, input PricingInput) *ResolvedPricing {
	longContextPricingEnabled := input.Group == nil || input.Group.LongContextPricingEnabled
	// 目录价层优先级最高（design.md §3.3）；开关关闭时整层跳过，退回旧口径。
	if catalog := r.resolveCatalogPricing(input.Model); catalog != nil {
		catalog.longContextPricingEnabled = longContextPricingEnabled
		return catalog
	}
	if groupPricing := matchGroupModelPricing(input.Group, input.Model); groupPricing != nil {
		// Group token cards only override the first-tier / flat rates.
		// Long-context ladders come from official presets, gated by the checkbox.
		if groupPricing.BillingMode == "" || groupPricing.BillingMode == BillingModeToken {
			stripped := groupPricing.Clone()
			stripped.Intervals = nil
			groupPricing = &stripped
		}
		resolved := r.resolveConfiguredPricing(groupPricing, input.Model, PricingSourceGroup)
		resolved.longContextPricingEnabled = longContextPricingEnabled
		return resolved
	}

	var chPricing *ChannelModelPricing
	if input.GroupID != nil && r.channelService != nil {
		chPricing = r.lookupChannelPricingNormalized(ctx, *input.GroupID, input.Model)
		if chPricing != nil {
			mode := chPricing.BillingMode
			if mode == "" {
				mode = BillingModeToken
			}
			if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
				resolved := &ResolvedPricing{
					Mode:           mode,
					Source:         PricingSourceChannel,
					channelPricing: chPricing,
				}
				resolved.longContextPricingEnabled = longContextPricingEnabled
				r.applyRequestTierOverrides(chPricing, resolved)
				return resolved
			}
		}
	}

	// 1. 获取基础定价
	basePricing, source := r.resolveBasePricing(input.Model)

	resolved := &ResolvedPricing{
		Mode:                   BillingModeToken,
		BasePricing:            basePricing,
		Source:                 source,
		SupportsCacheBreakdown: basePricing != nil && basePricing.SupportsCacheBreakdown,
	}
	resolved.longContextPricingEnabled = longContextPricingEnabled

	// 2. 如果有 GroupID，尝试渠道覆盖
	if chPricing != nil {
		resolved.Source = PricingSourceChannel
		resolved.channelPricing = chPricing
		r.applyTokenOverrides(chPricing, resolved)
	} else if input.GroupID != nil && r.channelService != nil {
		r.applyChannelOverrides(ctx, *input.GroupID, input.Model, resolved)
	}

	return resolved
}

func (r *ModelPricingResolver) resolveConfiguredPricing(config *ChannelModelPricing, model, source string) *ResolvedPricing {
	mode := config.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	resolved := &ResolvedPricing{Mode: mode, Source: source, channelPricing: config}
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		r.applyRequestTierOverrides(config, resolved)
		return resolved
	}
	resolved.BasePricing, _ = r.resolveBasePricing(model)
	resolved.SupportsCacheBreakdown = resolved.BasePricing != nil && resolved.BasePricing.SupportsCacheBreakdown
	r.applyTokenOverrides(config, resolved)
	return resolved
}

func matchGroupModelPricing(group *Group, model string) *ChannelModelPricing {
	if group == nil {
		return nil
	}
	model = normalizeChannelPricingModelName(model)
	var wildcard *ChannelModelPricing
	for i := range group.ModelPricing {
		entry := &group.ModelPricing[i]
		for _, pattern := range entry.Models {
			normalized := normalizeChannelPricingModelName(pattern)
			if normalized == model {
				cp := entry.Clone()
				return &cp
			}
			if strings.HasSuffix(normalized, "*") && strings.HasPrefix(model, strings.TrimSuffix(normalized, "*")) && wildcard == nil {
				cp := entry.Clone()
				wildcard = &cp
			}
		}
	}
	return wildcard
}

// resolveBasePricing 从 LiteLLM 或 Fallback 获取基础定价
func (r *ModelPricingResolver) resolveBasePricing(model string) (*ModelPricing, string) {
	pricing, err := r.billingService.GetModelPricing(model)
	if err != nil {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback",
			"model", model, "error", err)
		return nil, PricingSourceFallback
	}
	return pricing, PricingSourceLiteLLM
}

// lookupChannelPricingNormalized 查找渠道定价：先用字面模型名做精确/通配匹配，
// 未命中时用与官方兜底价一致的归一化模型名再查一次。
//
// 官方兜底价对 OpenAI/Codex 族会把 gpt-5.6-luna-high 这类变体名归一化到基名
// （billing_service.go 的 normalizeKnownOpenAICodexModel 分支），而渠道定价此前
// 只认字面名。两者不对称导致：管理员只配基名、请求模型带 effort 后缀时，渠道定价
// 未命中而官方兜底命中，计费候选循环首个成功即返回，渠道定价永远轮不到（issue #5256）。
//
// 字面名优先，保证管理员对具体变体的显式配价不被基名覆盖；非 OpenAI 模型
// normalizeKnownOpenAICodexModel 返回空串，此处天然 no-op。
func (r *ModelPricingResolver) lookupChannelPricingNormalized(ctx context.Context, groupID int64, model string) *ChannelModelPricing {
	if r.channelService == nil {
		return nil
	}
	if pricing := r.channelService.GetChannelModelPricing(ctx, groupID, model); pricing != nil {
		return pricing
	}
	normalized := normalizeKnownOpenAICodexModel(model)
	if normalized == "" || strings.EqualFold(normalized, strings.TrimSpace(model)) {
		return nil
	}
	return r.channelService.GetChannelModelPricing(ctx, groupID, normalized)
}

// applyChannelOverrides 应用渠道定价覆盖
func (r *ModelPricingResolver) applyChannelOverrides(ctx context.Context, groupID int64, model string, resolved *ResolvedPricing) {
	chPricing := r.lookupChannelPricingNormalized(ctx, groupID, model)
	if chPricing == nil {
		return
	}

	resolved.Source = PricingSourceChannel
	resolved.channelPricing = chPricing
	resolved.Mode = chPricing.BillingMode
	if resolved.Mode == "" {
		resolved.Mode = BillingModeToken
	}

	switch resolved.Mode {
	case BillingModeToken:
		r.applyTokenOverrides(chPricing, resolved)
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		r.applyRequestTierOverrides(chPricing, resolved)
	}
}

// applyTokenOverrides 应用 token 模式的渠道覆盖
func (r *ModelPricingResolver) applyTokenOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	if resolved.BasePricing == nil {
		resolved.BasePricing = &ModelPricing{}
	} else {
		// 防止修改 fallbackPrices 中的共享指针
		cloned := *resolved.BasePricing
		resolved.BasePricing = &cloned
	}

	applyChannelTokenPriceOverrides(resolved.BasePricing, chPricing)
	if chPricing.CacheWrite1hPrice != nil {
		resolved.SupportsCacheBreakdown = true
		resolved.BasePricing.SupportsCacheBreakdown = true
	}
	for i := range chPricing.Intervals {
		if chPricing.Intervals[i].CacheWrite1hPrice != nil {
			resolved.SupportsCacheBreakdown = true
			resolved.BasePricing.SupportsCacheBreakdown = true
			break
		}
	}
	resolved.BasePricing.FastMultiplier = chPricing.FastMultiplier
	resolved.BasePricing.FlexMultiplier = chPricing.FlexMultiplier
	resolved.BasePricing.ReasoningEffortMultipliers = maps.Clone(chPricing.ReasoningEffortMultipliers)
	applyChannelImagePriceOverrides(chPricing, resolved.BasePricing)

	// 区间未命中时回退到上面已经应用渠道覆盖的基础价。
	resolved.Intervals = filterValidIntervals(chPricing.Intervals)
}

// applyChannelImagePriceOverrides 应用渠道图片输入/输出价，规则与其他 token 字段一致：
//   - 渠道填写了（含 0）：覆盖目录价。图片输出价同时标记 ImageOutputPriceExplicit，
//     显式 0 表示图片输出免费，computeTokenBreakdown 不再回退文本输出价。
//   - 渠道留空（nil）：沿用目录价（LiteLLM 价格表或内置兜底价）。目录也没有图片价时，
//     computeTokenBreakdown 分别回退文本输入/输出价。
//
// 此前留空会被归零，图片输出还被标成显式 0：只建了空价卡的图片模型，图片输出
// 一律按 0 计费，图片输入按文本价计费。
func applyChannelImagePriceOverrides(chPricing *ChannelModelPricing, pricing *ModelPricing) {
	if chPricing == nil || pricing == nil {
		return
	}
	if chPricing.ImageOutputPrice != nil {
		pricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
		pricing.ImageOutputPriceExplicit = true
	}
	if chPricing.ImageInputPrice != nil {
		pricing.ImageInputPricePerToken = *chPricing.ImageInputPrice
	}
}

// applyRequestTierOverrides 应用按次/图片模式的渠道覆盖
func (r *ModelPricingResolver) applyRequestTierOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	resolved.RequestTiers = filterValidIntervals(chPricing.Intervals)
	if chPricing.PerRequestPrice != nil {
		resolved.DefaultPerRequestPrice = *chPricing.PerRequestPrice
	}
}

// filterValidIntervals 过滤掉所有价格字段都为空的无效 interval。
// 前端可能创建了只有 min/max 但无价格的空 interval。
func filterValidIntervals(intervals []PricingInterval) []PricingInterval {
	var valid []PricingInterval
	for _, iv := range intervals {
		if iv.InputPrice != nil || iv.OutputPrice != nil ||
			iv.CacheWritePrice != nil || iv.CacheWrite1hPrice != nil || iv.CacheWrite12hPrice != nil || iv.CacheReadPrice != nil ||
			iv.PerRequestPrice != nil || iv.InputMultiplier != nil ||
			iv.OutputMultiplier != nil || iv.CacheWriteMultiplier != nil ||
			iv.CacheReadMultiplier != nil {
			valid = append(valid, iv)
		}
	}
	return valid
}

// GetIntervalPricing 根据 context token 数获取区间定价。
// 如果有区间列表，找到匹配区间并构造 ModelPricing；否则直接返回 BasePricing。
func (r *ModelPricingResolver) GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	if len(resolved.Intervals) == 0 {
		return resolved.BasePricing
	}

	iv := FindMatchingInterval(resolved.Intervals, totalContextTokens)
	if iv == nil {
		return resolved.BasePricing
	}

	pricing := intervalToModelPricing(iv, resolved.BasePricing, resolved.channelPricing)
	// BasePricing 为 nil（仅配置区间）时拷贝不到该标志，从 resolved 回填，
	// 保证 computeCacheCreationCost 的 5m/1h 分档判断不被区间路径吞掉。
	pricing.SupportsCacheBreakdown = resolved.SupportsCacheBreakdown
	return pricing
}

// intervalToModelPricing 将区间定价转换为 ModelPricing
func intervalToModelPricing(iv *PricingInterval, base *ModelPricing, chPricing *ChannelModelPricing) *ModelPricing {
	pricing := &ModelPricing{}
	if base != nil {
		*pricing = *base
	}
	applyMultiplier := func(value float64, multiplier *float64) float64 {
		if multiplier == nil {
			return value
		}
		return value * *multiplier
	}
	if iv.InputPrice != nil {
		pricing.InputPricePerTokenPriority = channelTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *iv.InputPrice)
		pricing.InputPricePerToken = *iv.InputPrice
	} else if iv.InputMultiplier != nil {
		pricing.InputPricePerToken = applyMultiplier(pricing.InputPricePerToken, iv.InputMultiplier)
		pricing.InputPricePerTokenPriority = applyMultiplier(pricing.InputPricePerTokenPriority, iv.InputMultiplier)
	}
	if iv.OutputPrice != nil {
		pricing.OutputPricePerTokenPriority = channelTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *iv.OutputPrice)
		pricing.OutputPricePerToken = *iv.OutputPrice
	} else if iv.OutputMultiplier != nil {
		pricing.OutputPricePerToken = applyMultiplier(pricing.OutputPricePerToken, iv.OutputMultiplier)
		pricing.OutputPricePerTokenPriority = applyMultiplier(pricing.OutputPricePerTokenPriority, iv.OutputMultiplier)
	}
	if iv.CacheWritePrice != nil {
		pricing.CacheCreationPricePerTokenPriority = channelTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *iv.CacheWritePrice)
		pricing.CacheCreationPricePerToken = *iv.CacheWritePrice
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreation5mPrice = *iv.CacheWritePrice
		if iv.CacheWrite1hPrice == nil {
			pricing.CacheCreation1hPrice = *iv.CacheWritePrice
		}
	} else if iv.CacheWriteMultiplier != nil {
		pricing.CacheCreationPricePerToken = applyMultiplier(pricing.CacheCreationPricePerToken, iv.CacheWriteMultiplier)
		pricing.CacheCreationPricePerTokenPriority = applyMultiplier(pricing.CacheCreationPricePerTokenPriority, iv.CacheWriteMultiplier)
		pricing.CacheCreation5mPrice = applyMultiplier(pricing.CacheCreation5mPrice, iv.CacheWriteMultiplier)
		pricing.CacheCreation1hPrice = applyMultiplier(pricing.CacheCreation1hPrice, iv.CacheWriteMultiplier)
		if pricing.CacheCreation12hPricePerToken != nil {
			price := applyMultiplier(*pricing.CacheCreation12hPricePerToken, iv.CacheWriteMultiplier)
			pricing.CacheCreation12hPricePerToken = &price
		}
	}
	if iv.CacheWrite12hPrice != nil {
		pricing.CacheCreation12hPricePerToken = iv.CacheWrite12hPrice
	} else if iv.CacheWritePrice != nil {
		pricing.CacheCreation12hPricePerToken = nil
	}
	if iv.CacheWrite1hPrice != nil {
		pricing.CacheCreation1hPrice = *iv.CacheWrite1hPrice
		pricing.SupportsCacheBreakdown = true
	}
	if iv.CacheReadPrice != nil {
		pricing.CacheReadPricePerTokenPriority = channelTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *iv.CacheReadPrice)
		pricing.CacheReadPricePerToken = *iv.CacheReadPrice
	} else if iv.CacheReadMultiplier != nil {
		pricing.CacheReadPricePerToken = applyMultiplier(pricing.CacheReadPricePerToken, iv.CacheReadMultiplier)
		pricing.CacheReadPricePerTokenPriority = applyMultiplier(pricing.CacheReadPricePerTokenPriority, iv.CacheReadMultiplier)
	}
	// 区间不携带图片价，沿用渠道级配置；渠道留空时保留基础价里的目录图片价。
	applyChannelImagePriceOverrides(chPricing, pricing)
	return pricing
}

// GetRequestTierPrice 根据层级标签获取按次价格
func (r *ModelPricingResolver) GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	for _, tier := range resolved.RequestTiers {
		if tier.TierLabel == tierLabel && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice
		}
	}
	return 0
}

// GetRequestTierPriceByContext 根据 context token 数获取按次价格
func (r *ModelPricingResolver) GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	iv := FindMatchingInterval(resolved.RequestTiers, totalContextTokens)
	if iv != nil && iv.PerRequestPrice != nil {
		return *iv.PerRequestPrice
	}
	return 0
}
