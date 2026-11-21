package service

import (
	"encoding/json"
	"strings"
)

// CatalogMetaKey 是 override 文件里承载管理端元数据的命名空间键。
//
// 以 `_` 前缀命名，与 LiteLLM 的 snake_case 价格字段区分；解析器只认已知
// 价格字段，未知字段天然被忽略，因此该命名空间对既有解析零影响。
const CatalogMetaKey = "_catalog"

// catalogMeta 是 `_catalog` 的子结构，持久化在 override 文件中。
//
// 字段单位：价格类字段与 LiteLLM 一致，为 **per-token USD**；
// 仅 PerRequestPrice 为按次 USD。
type catalogMeta struct {
	// Platform 是模型所属平台（openai/anthropic/gemini/deepseek/...）。
	Platform string `json:"platform,omitempty"`
	// Custom 表示该模型由管理员在目录页定义或显式覆盖。
	Custom bool `json:"custom,omitempty"`
	// CNYSource 表示当前价来自人民币登记换算。
	CNYSource bool `json:"cny_source,omitempty"`
	// PeakEnabled 表示启用模型级自定义峰谷价。
	PeakEnabled bool `json:"peak_enabled,omitempty"`
	// Peak / OffPeak 是峰谷时段价格（per-token USD）。
	Peak    catalogPriceSet `json:"peak,omitempty"`
	OffPeak catalogPriceSet `json:"off_peak,omitempty"`
	// PerRequestPrice 是按次计费价（USD / 次），仅 per_request 模式使用。
	PerRequestPrice *float64 `json:"per_request_price,omitempty"`
	// UpdatedAt / UpdatedBy 记录最后一次管理端写入。
	UpdatedAt string `json:"updated_at,omitempty"`
	UpdatedBy int64  `json:"updated_by,omitempty"`
}

// catalogPriceSet 是一组时段价格（per-token USD）。
type catalogPriceSet struct {
	Input      float64 `json:"input_cost_per_token,omitempty"`
	Output     float64 `json:"output_cost_per_token,omitempty"`
	CacheRead  float64 `json:"cache_read_input_token_cost,omitempty"`
	CacheWrite float64 `json:"cache_creation_input_token_cost,omitempty"`
}

// isEmpty 报告该组价格是否完全未配置。
func (c catalogPriceSet) isEmpty() bool {
	return c.Input == 0 && c.Output == 0 && c.CacheRead == 0 && c.CacheWrite == 0
}

// toPeakAmounts 转换为计费侧结构。
func (c catalogPriceSet) toPeakAmounts() PeakPricingAmounts {
	return PeakPricingAmounts{
		Input:      c.Input,
		Output:     c.Output,
		CacheRead:  c.CacheRead,
		CacheWrite: c.CacheWrite,
	}
}

// parseCatalogMeta 从 override 条目的原始 JSON 中提取 `_catalog` 元数据。
//
// 返回 (meta, ok)；ok=false 表示该条目没有 `_catalog` 或格式不合法。
// 解析失败不报错：元数据坏掉时回落「无自定义峰谷」，不影响标准价格字段。
func parseCatalogMeta(raw json.RawMessage) (catalogMeta, bool) {
	if len(raw) == 0 {
		return catalogMeta{}, false
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		return catalogMeta{}, false
	}
	metaRaw, ok := entry[CatalogMetaKey]
	if !ok || len(metaRaw) == 0 {
		return catalogMeta{}, false
	}
	var meta catalogMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return catalogMeta{}, false
	}
	return meta, true
}

// peakAwareFromMeta 把 `_catalog` 元数据转换为计费侧的峰谷配置。
func peakAwareFromMeta(meta catalogMeta) PeakAwarePricing {
	return PeakAwarePricing{
		Enabled: meta.PeakEnabled,
		Peak:    meta.Peak.toPeakAmounts(),
		OffPeak: meta.OffPeak.toPeakAmounts(),
	}
}

// normalizeCatalogModel 归一化模型名（小写去空白），与 overrideModels 口径一致。
func normalizeCatalogModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}
