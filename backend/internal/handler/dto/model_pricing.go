package dto

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// UpdateModelPricingOverrideRequest 是逐模型 USD 覆盖的写入请求。
// 键为 USD 字段名（USD / 1M tokens），值为该字段的价格；值为 null 表示删除该字段的
// 覆盖（回退人民币层/目录价）。
// 支持的键：input_usd_per_mtok、output_usd_per_mtok、cache_read_usd_per_mtok、
// cache_write_usd_per_mtok、image_input_usd_per_mtok、priority_input_usd_per_mtok、
// priority_output_usd_per_mtok。
//
// 除价格键外还接受 `_catalog` 元数据（design.md §3.1）：
// platform（string）、custom（bool）、peak_enabled（bool）、
// peak / off_peak（嵌套对象，键与基础字段同名，单位 USD / 1M tokens）。
type UpdateModelPricingOverrideRequest map[string]json.RawMessage

// UpdateModelPricingOverrideBody 是解包后的请求体：价格键 + `_catalog` 元数据。
type UpdateModelPricingOverrideBody struct {
	Fields      map[string]*float64
	Platform    *string
	Custom      *bool
	PeakEnabled *bool
	Peak        map[string]*float64
	OffPeak     map[string]*float64
}

// ParseUpdateModelPricingOverride 解包 PUT overrides 请求体。
//
// 未知的顶层键直接报错（与既有「unknown model price field」语义一致），避免拼错的
// 字段被静默忽略。价格键值为 null 表示删除该字段覆盖。
func ParseUpdateModelPricingOverride(raw UpdateModelPricingOverrideRequest) (UpdateModelPricingOverrideBody, error) {
	body := UpdateModelPricingOverrideBody{Fields: make(map[string]*float64, len(raw))}
	for key, value := range raw {
		switch key {
		case "platform":
			var platform string
			if err := json.Unmarshal(value, &platform); err != nil {
				return body, service.NewInvalidModelPriceError("platform must be a string")
			}
			body.Platform = &platform
		case "custom":
			var custom *bool
			if err := json.Unmarshal(value, &custom); err != nil || custom == nil {
				return body, service.NewInvalidModelPriceError("custom must be a boolean")
			}
			body.Custom = custom
		case "peak_enabled":
			var enabled *bool
			if err := json.Unmarshal(value, &enabled); err != nil || enabled == nil {
				return body, service.NewInvalidModelPriceError("peak_enabled must be a boolean")
			}
			body.PeakEnabled = enabled
		case "peak":
			parsed, err := parsePeakRatesBody(value, "peak")
			if err != nil {
				return body, err
			}
			body.Peak = parsed
		case "off_peak":
			parsed, err := parsePeakRatesBody(value, "off_peak")
			if err != nil {
				return body, err
			}
			body.OffPeak = parsed
		default:
			var price *float64
			if err := json.Unmarshal(value, &price); err != nil {
				return body, service.NewInvalidModelPriceError("model price field " + key + " must be a number or null")
			}
			body.Fields[key] = price
		}
	}
	return body, nil
}

// parsePeakRatesBody 解包 peak / off_peak 嵌套对象：null 表示清空该组，
// 对象内的键为 USD 字段名、值为数字或 null。
func parsePeakRatesBody(raw json.RawMessage, group string) (map[string]*float64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var fields map[string]*float64
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, service.NewInvalidModelPriceError(group + " must be an object of USD per 1M token prices")
	}
	if fields == nil {
		return nil, nil
	}
	return fields, nil
}

// UpdateModelPricingPeakRates 是一组峰谷时段价（USD / 1M tokens）。
// 未提交的键表示该项回落到基础价（由 ResolvePeakPricing 的回落规则处理）。
type UpdateModelPricingPeakRates map[string]*float64

// ModelPricingPeakRates 是一组峰谷时段价视图（USD / 1M tokens，只读）。
type ModelPricingPeakRates struct {
	InputUSDPerMTok      float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok     float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok  float64 `json:"cache_read_usd_per_mtok"`
	CacheWriteUSDPerMTok float64 `json:"cache_write_usd_per_mtok"`
}

// ModelPricingCNY 是内置价格行附带的人民币登记价（原生 CNY / 1M tokens，只读）。
type ModelPricingCNY struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	ImageInput float64 `json:"image_input"`
}

// ModelPricingItem 是单个模型的有效价视图（USD / 1M tokens）。
type ModelPricingItem struct {
	Model                    string           `json:"model"`
	Provider                 string           `json:"provider"`
	Mode                     string           `json:"mode"`
	InputUSDPerMTok          float64          `json:"input_usd_per_mtok"`
	OutputUSDPerMTok         float64          `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok      float64          `json:"cache_read_usd_per_mtok"`
	CacheWriteUSDPerMTok     float64          `json:"cache_write_usd_per_mtok"`
	ImageInputUSDPerMTok     float64          `json:"image_input_usd_per_mtok"`
	PriorityInputUSDPerMTok  float64          `json:"priority_input_usd_per_mtok"`
	PriorityOutputUSDPerMTok float64          `json:"priority_output_usd_per_mtok"`
	Source                   string           `json:"source"`
	Overridden               bool             `json:"overridden"`
	CNY                      *ModelPricingCNY `json:"cny,omitempty"`
	EffectiveUpdatedAt       time.Time        `json:"effective_updated_at"`
	// `_catalog` 元数据视图，全部字段始终下发（未自定义时平台为空、峰谷为 0）。
	Platform    string                `json:"platform"`
	Custom      bool                  `json:"custom"`
	CNYSource   bool                  `json:"cny_source"`
	PeakEnabled bool                  `json:"peak_enabled"`
	Peak        ModelPricingPeakRates `json:"peak"`
	OffPeak     ModelPricingPeakRates `json:"off_peak"`
	UpdatedAt   string                `json:"updated_at,omitempty"`
	UpdatedBy   int64                 `json:"updated_by,omitempty"`
}

// ModelPricingMeta 是内置价格列表的价格层元信息。
type ModelPricingMeta struct {
	CNYMode          string    `json:"cny_mode"`
	CNYPerUSD        float64   `json:"cny_per_usd"`
	OverrideFile     string    `json:"override_file"`
	CNYFile          string    `json:"cny_file"`
	CatalogFile      string    `json:"catalog_file"`
	CatalogUpdatedAt time.Time `json:"catalog_updated_at"`
	ModelCount       int       `json:"model_count"`
	Editable         bool      `json:"editable"`
	CNYEditable      bool      `json:"cny_editable"`
	// CatalogAuthoritative 是「目录价层最高优先级」开关当前值（默认 true）。
	CatalogAuthoritative bool                    `json:"catalog_authoritative"`
	PeakSettings         PeakPricingSettingsView `json:"peak_settings"`
}

// PeakPricingSettingsView 是全局峰谷时段设置视图（= settings 的 peak_pricing_settings）。
type PeakPricingSettingsView struct {
	Timezone       string            `json:"timezone"`
	WeekdayOnly    bool              `json:"weekday_only"`
	WeekendOffPeak bool              `json:"weekend_off_peak"`
	Periods        []PeakPricingSlot `json:"periods"`
}

// PeakPricingSlot 是单个高峰时段（HH:mm，左闭右开）。
type PeakPricingSlot struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// UpdatePeakPricingSettingsRequest 是峰谷时段设置的写入请求（PUT 整体替换）。
type UpdatePeakPricingSettingsRequest struct {
	Timezone       string            `json:"timezone"`
	WeekdayOnly    bool              `json:"weekday_only"`
	WeekendOffPeak bool              `json:"weekend_off_peak"`
	Periods        []PeakPricingSlot `json:"periods"`
}

// PeakPricingSettingsResponse 是峰谷时段设置的读写响应。
type PeakPricingSettingsResponse struct {
	Timezone       string            `json:"timezone"`
	WeekdayOnly    bool              `json:"weekday_only"`
	WeekendOffPeak bool              `json:"weekend_off_peak"`
	Periods        []PeakPricingSlot `json:"periods"`
}

// CreateModelPricingRequest 是新增模型的写入请求。
//
// model 与 platform 必填；价格键为 USD / 1M tokens（≥0 且 < 1e4）。
// 允许目录外模型，但至少要提交一个价格字段或启用峰谷（design.md §2.2）。
type CreateModelPricingRequest struct {
	Model       string                      `json:"model"`
	Platform    string                      `json:"platform"`
	PeakEnabled bool                        `json:"peak_enabled"`
	Peak        UpdateModelPricingPeakRates `json:"peak"`
	OffPeak     UpdateModelPricingPeakRates `json:"off_peak"`
	// 基础价字段（USD / 1M tokens），平铺在请求体顶层。
	InputUSDPerMTok          *float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok         *float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok      *float64 `json:"cache_read_usd_per_mtok"`
	CacheWriteUSDPerMTok     *float64 `json:"cache_write_usd_per_mtok"`
	ImageInputUSDPerMTok     *float64 `json:"image_input_usd_per_mtok"`
	PriorityInputUSDPerMTok  *float64 `json:"priority_input_usd_per_mtok"`
	PriorityOutputUSDPerMTok *float64 `json:"priority_output_usd_per_mtok"`
}

// Fields 把顶层价格字段收敛成服务层的字段 map（未提交的字段不出现在 map 里）。
func (r CreateModelPricingRequest) Fields() map[string]*float64 {
	fields := make(map[string]*float64, 7)
	add := func(key string, value *float64) {
		if value != nil {
			fields[key] = value
		}
	}
	add("input_usd_per_mtok", r.InputUSDPerMTok)
	add("output_usd_per_mtok", r.OutputUSDPerMTok)
	add("cache_read_usd_per_mtok", r.CacheReadUSDPerMTok)
	add("cache_write_usd_per_mtok", r.CacheWriteUSDPerMTok)
	add("image_input_usd_per_mtok", r.ImageInputUSDPerMTok)
	add("priority_input_usd_per_mtok", r.PriorityInputUSDPerMTok)
	add("priority_output_usd_per_mtok", r.PriorityOutputUSDPerMTok)
	return fields
}

// ModelPricingListResponse 是内置价格列表响应（服务端分页）。
type ModelPricingListResponse struct {
	Items    []ModelPricingItem `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Meta     ModelPricingMeta   `json:"meta"`
}

// UpdateModelPricingCurrencyRequest 是人民币口径写入请求：mode 必填
// （convert/direct），cny_per_usd 仅在 convert 模式下需要且必须 > 0。
type UpdateModelPricingCurrencyRequest struct {
	Mode      string   `json:"mode"`
	CNYPerUSD *float64 `json:"cny_per_usd"`
}

// UpdateModelPricingCNYModelRequest 是逐模型人民币登记价写入请求（CNY / 1M tokens）。
// 0 或 null 表示该字段不登记。
// 支持的键：input_cny_per_mtok、output_cny_per_mtok、cache_read_cny_per_mtok、
// cache_write_cny_per_mtok、image_input_cny_per_mtok。
type UpdateModelPricingCNYModelRequest map[string]*float64

// ModelPricingCurrencyItem 是人民币层的单模型视图：原生人民币价 + 当前口径折算的 USD 价。
type ModelPricingCurrencyItem struct {
	Model                         string  `json:"model"`
	InputCNYPerMTok               float64 `json:"input_cny_per_mtok"`
	OutputCNYPerMTok              float64 `json:"output_cny_per_mtok"`
	CacheReadCNYPerMTok           float64 `json:"cache_read_cny_per_mtok"`
	CacheWriteCNYPerMTok          float64 `json:"cache_write_cny_per_mtok"`
	ImageInputCNYPerMTok          float64 `json:"image_input_cny_per_mtok"`
	EffectiveInputUSDPerMTok      float64 `json:"effective_input_usd_per_mtok"`
	EffectiveOutputUSDPerMTok     float64 `json:"effective_output_usd_per_mtok"`
	EffectiveCacheReadUSDPerMTok  float64 `json:"effective_cache_read_usd_per_mtok"`
	EffectiveCacheWriteUSDPerMTok float64 `json:"effective_cache_write_usd_per_mtok"`
	EffectiveImageInputUSDPerMTok float64 `json:"effective_image_input_usd_per_mtok"`
	Overridden                    bool    `json:"overridden"`
	Source                        string  `json:"source"`
}

// ModelPricingCurrencyResponse 是人民币口径读数/写入后的响应。
// SeedModels 是"导入已知人民币口径模型"的预填项（不由后端自动写入）。
type ModelPricingCurrencyResponse struct {
	Mode       string                     `json:"mode"`
	CNYPerUSD  float64                    `json:"cny_per_usd"`
	File       string                     `json:"file"`
	Models     []ModelPricingCurrencyItem `json:"models"`
	SeedModels []ModelPricingCurrencyItem `json:"seed_models"`
}

// ModelPricingItemFromService 把服务层条目转换成 DTO。
func ModelPricingItemFromService(entry *service.ModelPricingEntry) ModelPricingItem {
	if entry == nil {
		return ModelPricingItem{}
	}
	item := ModelPricingItem{
		Model:                    entry.Model,
		Provider:                 entry.Provider,
		Mode:                     entry.Mode,
		InputUSDPerMTok:          entry.InputUSDPerMTok,
		OutputUSDPerMTok:         entry.OutputUSDPerMTok,
		CacheReadUSDPerMTok:      entry.CacheReadUSDPerMTok,
		CacheWriteUSDPerMTok:     entry.CacheWriteUSDPerMTok,
		ImageInputUSDPerMTok:     entry.ImageInputUSDPerMTok,
		PriorityInputUSDPerMTok:  entry.PriorityInputUSDPerMTok,
		PriorityOutputUSDPerMTok: entry.PriorityOutputUSDPerMTok,
		Source:                   entry.Source,
		Overridden:               entry.Overridden,
		EffectiveUpdatedAt:       entry.EffectiveUpdatedAt,
		Platform:                 entry.Platform,
		Custom:                   entry.Custom,
		CNYSource:                entry.CNYSource,
		PeakEnabled:              entry.PeakEnabled,
		Peak:                     modelPricingPeakRatesFromService(entry.Peak),
		OffPeak:                  modelPricingPeakRatesFromService(entry.OffPeak),
		UpdatedAt:                entry.UpdatedAt,
		UpdatedBy:                entry.UpdatedBy,
	}
	if entry.CNY != nil {
		item.CNY = &ModelPricingCNY{
			Input:      entry.CNY.Input,
			Output:     entry.CNY.Output,
			CacheRead:  entry.CNY.CacheRead,
			CacheWrite: entry.CNY.CacheWrite,
			ImageInput: entry.CNY.ImageInput,
		}
	}
	return item
}

// modelPricingPeakRatesFromService 转换峰谷价（USD / 1M tokens，原样透传）。
func modelPricingPeakRatesFromService(rates service.ModelPricingPeakRates) ModelPricingPeakRates {
	return ModelPricingPeakRates{
		InputUSDPerMTok:      rates.InputUSDPerMTok,
		OutputUSDPerMTok:     rates.OutputUSDPerMTok,
		CacheReadUSDPerMTok:  rates.CacheReadUSDPerMTok,
		CacheWriteUSDPerMTok: rates.CacheWriteUSDPerMTok,
	}
}

// PeakPricingSettingsResponseFromService 转换峰谷时段设置为响应视图。
func PeakPricingSettingsResponseFromService(settings service.PeakPricingSettings) PeakPricingSettingsResponse {
	periods := make([]PeakPricingSlot, 0, len(settings.Periods))
	for _, period := range settings.Periods {
		periods = append(periods, PeakPricingSlot{Start: period.Start, End: period.End})
	}
	return PeakPricingSettingsResponse{
		Timezone:       settings.Timezone,
		WeekdayOnly:    settings.WeekdayOnly,
		WeekendOffPeak: settings.WeekendOffPeak,
		Periods:        periods,
	}
}

// ModelPricingItemsFromService 批量转换列表条目。
func ModelPricingItemsFromService(entries []service.ModelPricingEntry) []ModelPricingItem {
	items := make([]ModelPricingItem, 0, len(entries))
	for i := range entries {
		items = append(items, ModelPricingItemFromService(&entries[i]))
	}
	return items
}

// ModelPricingListFromService 组装内置价格列表响应。
func ModelPricingListFromService(result service.ModelPricingListResult) ModelPricingListResponse {
	return ModelPricingListResponse{
		Items:    ModelPricingItemsFromService(result.Items),
		Total:    result.Total,
		Page:     result.Page,
		PageSize: result.PageSize,
		Meta:     ModelPricingMetaFromService(result.Meta),
	}
}

// ModelPricingMetaFromService 转换元信息。
func ModelPricingMetaFromService(meta service.ModelPricingMeta) ModelPricingMeta {
	view := PeakPricingSettingsResponseFromService(meta.PeakSettings)
	return ModelPricingMeta{
		CNYMode:              meta.CNYMode,
		CNYPerUSD:            meta.CNYPerUSD,
		OverrideFile:         meta.OverrideFile,
		CNYFile:              meta.CNYFile,
		CatalogFile:          meta.CatalogFile,
		CatalogUpdatedAt:     meta.CatalogUpdatedAt,
		ModelCount:           meta.ModelCount,
		Editable:             meta.Editable,
		CNYEditable:          meta.CNYEditable,
		CatalogAuthoritative: meta.CatalogAuthoritative,
		PeakSettings: PeakPricingSettingsView{
			Timezone:       view.Timezone,
			WeekdayOnly:    view.WeekdayOnly,
			WeekendOffPeak: view.WeekendOffPeak,
			Periods:        view.Periods,
		},
	}
}

// ModelPricingCurrencyFromService 组装人民币口径响应。
func ModelPricingCurrencyFromService(state service.ModelPricingCurrencyState) ModelPricingCurrencyResponse {
	return ModelPricingCurrencyResponse{
		Mode:       state.Mode,
		CNYPerUSD:  state.CNYPerUSD,
		File:       state.File,
		Models:     modelPricingCurrencyItemsFromService(state.Models),
		SeedModels: modelPricingCurrencyItemsFromService(state.SeedModels),
	}
}

func modelPricingCurrencyItemsFromService(entries []service.ModelPricingCNYEntry) []ModelPricingCurrencyItem {
	items := make([]ModelPricingCurrencyItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, ModelPricingCurrencyItem{
			Model:                         entry.Model,
			InputCNYPerMTok:               entry.InputCNYPerMTok,
			OutputCNYPerMTok:              entry.OutputCNYPerMTok,
			CacheReadCNYPerMTok:           entry.CacheReadCNYPerMTok,
			CacheWriteCNYPerMTok:          entry.CacheWriteCNYPerMTok,
			ImageInputCNYPerMTok:          entry.ImageInputCNYPerMTok,
			EffectiveInputUSDPerMTok:      entry.EffectiveInputUSDPerMTok,
			EffectiveOutputUSDPerMTok:     entry.EffectiveOutputUSDPerMTok,
			EffectiveCacheReadUSDPerMTok:  entry.EffectiveCacheReadUSDPerMTok,
			EffectiveCacheWriteUSDPerMTok: entry.EffectiveCacheWriteUSDPerMTok,
			EffectiveImageInputUSDPerMTok: entry.EffectiveImageInputUSDPerMTok,
			Overridden:                    entry.Overridden,
			Source:                        entry.Source,
		})
	}
	return items
}
