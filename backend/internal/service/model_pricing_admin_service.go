package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// ModelPriceUSDPerMTokLimit 是管理端 USD 覆盖价的上限（USD / 1M tokens）。
	ModelPriceUSDPerMTokLimit = 1e4
	// ModelPriceCNYPerMTokLimit 是人民币登记价的上限（CNY / 1M tokens）。
	ModelPriceCNYPerMTokLimit = 1e6
	// defaultModelPricingPageSize 是内置价格列表的默认分页大小。
	defaultModelPricingPageSize = 20
	// modelPricingPageSizeLimit 是内置价格列表的分页大小上限。
	modelPricingPageSizeLimit = 100
)

// 管理端错误码。非法值统一返回 400 INVALID_MODEL_PRICE / INVALID_CNY_*，
// 与 design.md §2.3/§3.3 的契约一致。
var (
	ErrModelPricingOverrideFileNotConfigured = infraerrors.BadRequest(
		"PRICING_OVERRIDE_FILE_NOT_CONFIGURED",
		"pricing.override_file is not configured; model price overrides are disabled",
	)
	ErrModelPricingOverrideFileInvalid = infraerrors.BadRequest(
		"PRICING_OVERRIDE_FILE_INVALID",
		"pricing override file is not a valid JSON object",
	)
	ErrModelPricingCNYFileNotConfigured = infraerrors.BadRequest(
		"PRICING_CNY_FILE_NOT_CONFIGURED",
		"pricing.cny_override_file is not configured; CNY pricing is disabled",
	)
	ErrInvalidModelPrice = infraerrors.BadRequest(
		"INVALID_MODEL_PRICE",
		"model price is invalid",
	)
	ErrInvalidCNYPrice = infraerrors.BadRequest(
		"INVALID_CNY_PRICE",
		"CNY model price is invalid",
	)
	ErrInvalidCNYRate = infraerrors.BadRequest(
		"INVALID_CNY_RATE",
		"cny_per_usd must be greater than 0 in convert mode",
	)
	ErrInvalidCNYMode = infraerrors.BadRequest(
		"INVALID_CNY_MODE",
		"currency mode must be convert or direct",
	)
	ErrModelPricingNotFound = infraerrors.NotFound(
		"MODEL_PRICING_NOT_FOUND",
		"model pricing not found",
	)
	ErrModelPricingWriteFailed = infraerrors.InternalServer(
		"PRICING_FILE_WRITE_FAILED",
		"failed to write pricing file",
	)
	ErrModelPricingReloadFailed = infraerrors.InternalServer(
		"PRICING_RELOAD_FAILED",
		"failed to reload pricing after write",
	)
	// ErrModelAlreadyExists 是新增模型与目录/自定义条目重名的冲突错误（409）。
	// 前端据此提示「已存在，是否转为覆盖？」并切到编辑态（design.md §2.2）。
	ErrModelAlreadyExists = infraerrors.Conflict(
		"MODEL_ALREADY_EXISTS",
		"model already exists in the pricing catalog; edit it instead of creating a new one",
	)
	// ErrModelDeleteBuiltinRejected 是尝试删除目录内置模型时的拒绝错误：
	// 删除仅对管理员自定义的模型开放，内置模型请用「恢复内置」清理覆盖。
	ErrModelDeleteBuiltinRejected = infraerrors.BadRequest(
		"MODEL_DELETE_BUILTIN_REJECTED",
		"only custom models can be deleted; use the override reset for built-in models",
	)
	// ErrPeakPricingSettingsInvalid 是峰谷时段设置非法（如时区无法解析）的错误。
	ErrPeakPricingSettingsInvalid = infraerrors.BadRequest(
		"INVALID_PEAK_PRICING_SETTINGS",
		"peak pricing settings is invalid",
	)
)

// modelPricingUSDFieldToLiteLLM 是管理端 USD 字段名到 LiteLLM 原始字段名的映射。
// API 一律使用 USD / 1M tokens，写盘时换算成 per-token。
var modelPricingUSDFieldToLiteLLM = map[string]string{
	"input_usd_per_mtok":           "input_cost_per_token",
	"output_usd_per_mtok":          "output_cost_per_token",
	"cache_read_usd_per_mtok":      "cache_read_input_token_cost",
	"cache_write_usd_per_mtok":     "cache_creation_input_token_cost",
	"image_input_usd_per_mtok":     "input_cost_per_image_token",
	"priority_input_usd_per_mtok":  "input_cost_per_token_priority",
	"priority_output_usd_per_mtok": "output_cost_per_token_priority",
}

// modelPricingCNYFields 是管理端可写的人民币字段名（与 cny-overrides.json 一致）。
var modelPricingCNYFields = map[string]struct{}{
	"input_cny_per_mtok":       {},
	"output_cny_per_mtok":      {},
	"cache_read_cny_per_mtok":  {},
	"cache_write_cny_per_mtok": {},
	"image_input_cny_per_mtok": {},
}

// ModelPricingCNYSeed 是"导入已知人民币口径模型"的预填项。
type ModelPricingCNYSeed struct {
	Model  string
	Prices LiteLLMCNYModelPricing
}

// ModelPricingCNYSeeds 返回已知人民币口径模型的预填值，数值来自 billing_service.go
// 中 Kimi / 豆包官方人民币价注释（元/百万 tokens）。管理端确认后再写入，不自动创建文件。
func ModelPricingCNYSeeds() []ModelPricingCNYSeed {
	ptr := func(v float64) *float64 { return &v }
	return []ModelPricingCNYSeed{
		{Model: "kimi-k2", Prices: LiteLLMCNYModelPricing{
			InputCNYPerMTok: ptr(4), OutputCNYPerMTok: ptr(16), CacheReadCNYPerMTok: ptr(1),
		}},
		{Model: "kimi-k2-thinking", Prices: LiteLLMCNYModelPricing{
			InputCNYPerMTok: ptr(4), OutputCNYPerMTok: ptr(16), CacheReadCNYPerMTok: ptr(1),
		}},
		{Model: "doubao-embedding-vision", Prices: LiteLLMCNYModelPricing{
			InputCNYPerMTok: ptr(0.7), ImageInputCNYPerMTok: ptr(1.8),
		}},
	}
}

// ModelPricingAdminService 负责内置模型价格的查看与覆盖管理，以及人民币计费口径的
// 读写。所有写操作都走"读取 JSON → 修改 → 临时文件 + rename 原子替换 → JSON 校验 →
// 热重载"，不新增数据库表。
type ModelPricingAdminService struct {
	cfg     *config.Config
	pricing *PricingService
	// settingRepo 用于持久化全局峰谷时段设置（settings 表，key =
	// peak_pricing_settings）与目录价层开关（catalog_pricing_authoritative）。
	// 为 nil 时峰谷时段仅内存生效（测试/未接线场景）。
	settingRepo SettingRepository
	// resolver 用于把目录价层开关推给计费链路（design.md §3.3 的可回滚开关）。
	// 为 nil 时只影响管理端自身的读取口径。
	resolver *ModelPricingResolver
	// catalogAuthoritative 是目录价层开关的进程内快照（原子读写）。
	// nil 表示尚未从 settings 装载，按默认 true 处理。
	catalogAuthoritative atomic.Pointer[bool]
	// mu 串行化文件读改写，避免并发覆盖写互相丢失条目。
	mu sync.Mutex
}

// NewModelPricingAdminService 创建模型价格管理服务。
func NewModelPricingAdminService(cfg *config.Config, pricing *PricingService) *ModelPricingAdminService {
	return &ModelPricingAdminService{cfg: cfg, pricing: pricing}
}

// NewModelPricingAdminServiceWithSettings 创建带 settings 仓储的模型价格管理服务，
// 使峰谷时段设置可持久化到 settings 的 peak_pricing_settings 键并在启动时回填。
func NewModelPricingAdminServiceWithSettings(
	cfg *config.Config,
	pricing *PricingService,
	settingRepo SettingRepository,
) *ModelPricingAdminService {
	return &ModelPricingAdminService{cfg: cfg, pricing: pricing, settingRepo: settingRepo}
}

// SetCatalogPricingResolver 安装目录价层开关的下游消费方（ModelPricingResolver），
// 使 settings 里的 catalog_pricing_authoritative 变化后计费链路立即生效。
func (s *ModelPricingAdminService) SetCatalogPricingResolver(resolver *ModelPricingResolver) {
	if s == nil {
		return
	}
	s.resolver = resolver
	if value := s.catalogAuthoritative.Load(); value != nil {
		resolver.SetCatalogPricingAuthoritative(*value)
	}
}

// CatalogPricingAuthoritative 返回目录价层开关的当前值（默认 true）。
func (s *ModelPricingAdminService) CatalogPricingAuthoritative() bool {
	if s == nil {
		return true
	}
	if value := s.catalogAuthoritative.Load(); value != nil {
		return *value
	}
	return true
}

// SetCatalogPricingAuthoritative 设置目录价层开关并下推给计费链路（热生效，不落盘）。
// 需要持久化时用 UpdateCatalogPricingAuthoritative。
func (s *ModelPricingAdminService) SetCatalogPricingAuthoritative(enabled bool) {
	if s == nil {
		return
	}
	value := enabled
	s.catalogAuthoritative.Store(&value)
	if s.resolver != nil {
		s.resolver.SetCatalogPricingAuthoritative(enabled)
	}
}

// LoadCatalogPricingAuthoritative 从 settings 读回目录价层开关并注入计费链路
// （服务启动时调用）。未配置或无法解析时按默认 true（design.md §3.3）。
func (s *ModelPricingAdminService) LoadCatalogPricingAuthoritative(ctx context.Context) bool {
	enabled := true
	if s != nil && s.settingRepo != nil {
		if value, err := s.settingRepo.GetValue(ctx, SettingKeyCatalogPricingAuthoritative); err == nil {
			if parsed, ok := parseBoolSetting(value); ok {
				enabled = parsed
			}
		}
	}
	if s != nil {
		s.SetCatalogPricingAuthoritative(enabled)
	}
	return enabled
}

// UpdateCatalogPricingAuthoritative 持久化目录价层开关（settings key
// catalog_pricing_authoritative）并热生效。
func (s *ModelPricingAdminService) UpdateCatalogPricingAuthoritative(ctx context.Context, enabled bool) error {
	if s != nil && s.settingRepo != nil {
		value := "false"
		if enabled {
			value = "true"
		}
		if err := s.settingRepo.Set(ctx, SettingKeyCatalogPricingAuthoritative, value); err != nil {
			return ErrModelPricingWriteFailed.WithCause(err)
		}
	}
	if s != nil {
		s.SetCatalogPricingAuthoritative(enabled)
	}
	return nil
}

// parseBoolSetting 解析 settings 里的布尔值（大小写不敏感，空值视为未配置）。
func parseBoolSetting(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// ModelPricingCNYEquivalent 是内置价格行里附带的人民币登记价（原生 CNY / 1M tokens）。
type ModelPricingCNYEquivalent struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	ImageInput float64 `json:"image_input"`
}

// ModelPricingEntry 是单个模型的有效价视图（USD / 1M tokens）。
type ModelPricingEntry struct {
	Model                    string                     `json:"model"`
	Provider                 string                     `json:"provider"`
	Mode                     string                     `json:"mode"`
	InputUSDPerMTok          float64                    `json:"input_usd_per_mtok"`
	OutputUSDPerMTok         float64                    `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok      float64                    `json:"cache_read_usd_per_mtok"`
	CacheWriteUSDPerMTok     float64                    `json:"cache_write_usd_per_mtok"`
	ImageInputUSDPerMTok     float64                    `json:"image_input_usd_per_mtok,omitempty"`
	PriorityInputUSDPerMTok  float64                    `json:"priority_input_usd_per_mtok,omitempty"`
	PriorityOutputUSDPerMTok float64                    `json:"priority_output_usd_per_mtok,omitempty"`
	Source                   string                     `json:"source"`
	Overridden               bool                       `json:"overridden"`
	CNY                      *ModelPricingCNYEquivalent `json:"cny,omitempty"`
	EffectiveUpdatedAt       time.Time                  `json:"effective_updated_at"`
	// 以下是 `_catalog` 元数据视图（design.md §3.1/§4.1）。全部字段始终下发，
	// 未自定义时 Platform 为空、Custom=false、PeakEnabled=false、峰谷为 4 个 0。
	Platform    string                `json:"platform"`
	Custom      bool                  `json:"custom"`
	CNYSource   bool                  `json:"cny_source"`
	PeakEnabled bool                  `json:"peak_enabled"`
	Peak        ModelPricingPeakRates `json:"peak"`
	OffPeak     ModelPricingPeakRates `json:"off_peak"`
	UpdatedAt   string                `json:"updated_at,omitempty"`
	UpdatedBy   int64                 `json:"updated_by,omitempty"`
}

// ModelPricingPeakRates 是一组峰谷时段价格（USD / 1M tokens，前端只做展示与输入）。
type ModelPricingPeakRates struct {
	InputUSDPerMTok      float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok     float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok  float64 `json:"cache_read_usd_per_mtok"`
	CacheWriteUSDPerMTok float64 `json:"cache_write_usd_per_mtok"`
}

// isEmpty 报告该组价格是否完全未配置（用于回落语义判定）。
func (r ModelPricingPeakRates) isEmpty() bool {
	return r.InputUSDPerMTok == 0 && r.OutputUSDPerMTok == 0 &&
		r.CacheReadUSDPerMTok == 0 && r.CacheWriteUSDPerMTok == 0
}

// ModelPricingMeta 是列表响应附带的价格层元信息。
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
	// CatalogAuthoritative 是「目录价层最高优先级」开关的当前值（默认 true）。
	// false 时前端应提示目录价层已回退到旧优先级。
	CatalogAuthoritative bool                `json:"catalog_authoritative"`
	PeakSettings         PeakPricingSettings `json:"peak_settings"`
}

// ModelPricingListFilter 是内置价格列表的查询条件。
type ModelPricingListFilter struct {
	Page       int
	PageSize   int
	Search     string
	Provider   string
	Overridden *bool
	// Custom 筛选由管理员在目录页自定义（`_catalog.custom`）的模型。
	Custom *bool
	// PeakEnabled 筛选启用了自定义峰谷价的模型（`_catalog.peak_enabled`）。
	PeakEnabled *bool
	// Platform 按模型所属平台筛选（`_catalog.platform`，大小写不敏感）。
	// 未自定义的模型没有平台，可用 Provider 列筛选其定价供应商。
	Platform string
}

// ModelPricingListResult 是内置价格列表结果（服务端分页）。
type ModelPricingListResult struct {
	Items    []ModelPricingEntry
	Total    int
	Page     int
	PageSize int
	Meta     ModelPricingMeta
}

// ModelPricingOverrideInput 是逐模型 USD 覆盖的写入输入。value 为 nil 表示删除该字段
// 的覆盖（回退人民币层/目录价）。
//
// 除 7 个 USD 价格键外，还支持 `_catalog` 元数据：Platform / Custom / PeakEnabled /
// Peak / OffPeak。任一 Catalog 字段非 nil 时同时写入 `_catalog` 元数据
// （design.md §3.1）。
type ModelPricingOverrideInput struct {
	Model  string
	Fields map[string]*float64
	// Platform 是模型所属平台（openai/anthropic/deepseek/...）；nil 表示不改。
	Platform *string
	// Custom 标记录管理器自定义；nil 表示不改。
	Custom *bool
	// PeakEnabled 是模型级峰谷开关；nil 表示不改。
	PeakEnabled *bool
	// Peak / OffPeak 是峰谷时段价（USD / 1M tokens，提交后落盘为 per-token）。
	// 字段为 nil 表示该键不改；空 map 表示该组价格整体不变。
	Peak    map[string]*float64
	OffPeak map[string]*float64
	// UpdatedBy 记录最后一次管理端写入的操作者（管理员用户 ID）。
	UpdatedBy int64
}

// hasCatalogFields 报告这次写入是否需要同时更新 `_catalog` 元数据。
func (i ModelPricingOverrideInput) hasCatalogFields() bool {
	return i.Platform != nil || i.Custom != nil || i.PeakEnabled != nil ||
		len(i.Peak) > 0 || len(i.OffPeak) > 0
}

// ModelPricingCreateInput 是新增模型的写入输入。
//
// 允许目录外模型：只要自带价格字段（或峰谷价）即可独立成条
// （design.md §3.1 最后一条）。重名（目录或已有自定义）返回 MODEL_ALREADY_EXISTS。
type ModelPricingCreateInput struct {
	Model    string
	Platform string
	// Fields 是基础价（7 个 USD 键，USD / 1M tokens）；nil 值按未提交处理。
	Fields map[string]*float64
	// PeakEnabled / Peak / OffPeak 是峰谷配置（可选）。
	PeakEnabled bool
	Peak        map[string]*float64
	OffPeak     map[string]*float64
	// UpdatedBy 是操作者管理员用户 ID。
	UpdatedBy int64
}

// ModelPricingCNYEntry 是人民币层的单模型视图：原生人民币价 + 当前口径下的折算 USD 价。
type ModelPricingCNYEntry struct {
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

// ModelPricingCurrencyState 是人民币口径配置的读写视图。
type ModelPricingCurrencyState struct {
	Mode       string                 `json:"mode"`
	CNYPerUSD  float64                `json:"cny_per_usd"`
	File       string                 `json:"file"`
	Models     []ModelPricingCNYEntry `json:"models"`
	SeedModels []ModelPricingCNYEntry `json:"seed_models"`
}

// List 返回内置价格列表：按 overridden desc, model asc 排序，服务端分页。
func (s *ModelPricingAdminService) List(filter ModelPricingListFilter) ModelPricingListResult {
	page, pageSize := normalizeModelPricingPagination(filter.Page, filter.PageSize)
	result := ModelPricingListResult{Page: page, PageSize: pageSize}
	if s == nil || s.pricing == nil {
		return result
	}

	snapshot := s.pricing.Snapshot()
	cnyPrices := s.pricing.CurrentCNYModelPrices()
	catalogMetas := s.catalogMetaIndex()
	entries := make([]ModelPricingEntry, 0, len(snapshot.Models))
	for name, pricing := range snapshot.Models {
		entries = append(entries, s.entryFromSnapshot(name, pricing, snapshot, cnyPrices, catalogMetas))
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Overridden != entries[j].Overridden {
			return entries[i].Overridden
		}
		return entries[i].Model < entries[j].Model
	})

	search := strings.ToLower(strings.TrimSpace(filter.Search))
	provider := strings.ToLower(strings.TrimSpace(filter.Provider))
	platform := strings.ToLower(strings.TrimSpace(filter.Platform))
	filtered := entries[:0]
	for _, entry := range entries {
		if search != "" &&
			!strings.Contains(strings.ToLower(entry.Model), search) &&
			!strings.Contains(strings.ToLower(entry.Provider), search) {
			continue
		}
		if provider != "" && strings.ToLower(entry.Provider) != provider {
			continue
		}
		// 平台筛选只看 `_catalog.platform`（未自定义的模型没有平台，需用 provider 筛）。
		if platform != "" && strings.ToLower(entry.Platform) != platform {
			continue
		}
		if filter.Overridden != nil && entry.Overridden != *filter.Overridden {
			continue
		}
		if filter.Custom != nil && entry.Custom != *filter.Custom {
			continue
		}
		if filter.PeakEnabled != nil && entry.PeakEnabled != *filter.PeakEnabled {
			continue
		}
		filtered = append(filtered, entry)
	}

	result.Total = len(filtered)
	result.Items = paginateModelPricingEntries(filtered, page, pageSize)
	result.Meta = s.metaFromSnapshot(snapshot)
	return result
}

// catalogMetaIndex 返回 override 文件里 `_catalog` 元数据的小写模型名索引。
// 未配置 override 文件或读取失败时返回空 map（列表降级为「无自定义」）。
func (s *ModelPricingAdminService) catalogMetaIndex() map[string]catalogMeta {
	if s == nil || s.pricing == nil {
		return nil
	}
	entries := s.pricing.loadPricingOverrideEntries()
	if len(entries) == 0 {
		return nil
	}
	index := make(map[string]catalogMeta, len(entries))
	for name, raw := range entries {
		meta, ok := parseCatalogMeta(raw)
		if !ok {
			continue
		}
		index[normalizeCatalogModel(name)] = meta
	}
	return index
}

// UpdateOverride 写入单个模型的 USD 覆盖：只写显式提交的字段，nil 表示删除该字段的
// 覆盖；写入后热重载并返回最新有效价。
//
// 提交 `_catalog` 相关字段（platform/custom/peak_enabled/peak/off_peak）时，同时把
// 元数据写进 override 条目的 `_catalog` 命名空间（design.md §3.1），并自动填充
// updated_at / updated_by。
func (s *ModelPricingAdminService) UpdateOverride(input ModelPricingOverrideInput) (*ModelPricingEntry, error) {
	path := s.overrideFilePath()
	if path == "" {
		return nil, ErrModelPricingOverrideFileNotConfigured
	}
	model := normalizeModelName(input.Model)
	if model == "" {
		return nil, invalidModelPriceError("model is required")
	}

	// 先整体校验，非法输入不落盘。
	updates := make(map[string]*float64, len(input.Fields))
	for key, value := range input.Fields {
		field, ok := modelPricingUSDFieldToLiteLLM[key]
		if !ok {
			return nil, invalidModelPriceError(fmt.Sprintf("unknown model price field %q", key))
		}
		if value != nil && !validUSDPerMTok(*value) {
			return nil, invalidModelPriceError(fmt.Sprintf("%s must be >= 0 and < %g (USD per 1M tokens)", key, float64(ModelPriceUSDPerMTokLimit)))
		}
		updates[field] = value
	}
	peakUpdates, err := normalizePeakRateFields(input.Peak, "peak")
	if err != nil {
		return nil, err
	}
	offPeakUpdates, err := normalizePeakRateFields(input.OffPeak, "off_peak")
	if err != nil {
		return nil, err
	}
	if input.Platform != nil {
		if err := validateModelPlatform(*input.Platform); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := readJSONObjectFile(path)
	if err != nil {
		return nil, ErrModelPricingOverrideFileInvalid.WithCause(err)
	}
	patch := make(map[string]any)
	if raw, ok := entries[model]; ok {
		if err := json.Unmarshal(raw, &patch); err != nil {
			return nil, ErrModelPricingOverrideFileInvalid.WithCause(err)
		}
	}
	for field, value := range updates {
		if value == nil {
			delete(patch, field)
			continue
		}
		patch[field] = *value / 1e6
	}

	// `_catalog` 元数据：与标准价格字段分开维护，互不干扰。
	if input.hasCatalogFields() {
		meta := catalogMeta{}
		if raw, ok := patch[CatalogMetaKey]; ok {
			if body, err := json.Marshal(raw); err == nil {
				if err := json.Unmarshal(body, &meta); err != nil {
					meta = catalogMeta{}
				}
			}
		}
		if input.Platform != nil {
			meta.Platform = strings.TrimSpace(*input.Platform)
		}
		if input.Custom != nil {
			meta.Custom = *input.Custom
		}
		if input.PeakEnabled != nil {
			meta.PeakEnabled = *input.PeakEnabled
		}
		if len(input.Peak) > 0 {
			meta.Peak = mergePeakRateSet(meta.Peak, peakUpdates)
		}
		if len(input.OffPeak) > 0 {
			meta.OffPeak = mergePeakRateSet(meta.OffPeak, offPeakUpdates)
		}
		meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		meta.UpdatedBy = input.UpdatedBy
		if catalogMetaEmpty(meta) {
			delete(patch, CatalogMetaKey)
		} else {
			patch[CatalogMetaKey] = meta
		}
	}

	if len(patch) == 0 {
		delete(entries, model)
	} else {
		body, err := json.Marshal(patch)
		if err != nil {
			return nil, ErrModelPricingWriteFailed.WithCause(err)
		}
		entries[model] = body
	}
	if err := writeJSONObjectFileAtomic(path, entries); err != nil {
		return nil, ErrModelPricingWriteFailed.WithCause(err)
	}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s.currentEntry(model)
}

// normalizePeakRateFields 校验并归一化峰谷价字段（USD / 1M tokens，键与基础字段同名）。
// 返回 per-token 值；nil 值表示删除该键（回落规则由 ResolvePeakPricing 处理）。
func normalizePeakRateFields(fields map[string]*float64, group string) (map[string]*float64, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	out := make(map[string]*float64, len(fields))
	for key, value := range fields {
		field, ok := modelPricingUSDFieldToLiteLLM[key]
		if !ok {
			return nil, invalidModelPriceError(fmt.Sprintf("unknown %s price field %q", group, key))
		}
		switch field {
		case "input_cost_per_token", "output_cost_per_token",
			"cache_read_input_token_cost", "cache_creation_input_token_cost":
		default:
			return nil, invalidModelPriceError(fmt.Sprintf("%s only supports input/output/cache_read/cache_write", group))
		}
		if value == nil {
			out[field] = nil
			continue
		}
		if !validUSDPerMTok(*value) {
			return nil, invalidModelPriceError(fmt.Sprintf("%s.%s must be >= 0 and < %g (USD per 1M tokens)", group, key, float64(ModelPriceUSDPerMTokLimit)))
		}
		rate := *value
		out[field] = &rate
	}
	return out, nil
}

// mergePeakRateSet 把字段级更新并入一组峰谷价（per-token）。nil 值删除该键。
func mergePeakRateSet(current catalogPriceSet, updates map[string]*float64) catalogPriceSet {
	for field, value := range updates {
		slot := catalogPriceSetField(&current, field)
		if slot == nil {
			continue
		}
		if value == nil {
			*slot = 0
			continue
		}
		*slot = *value / 1e6
	}
	return current
}

// catalogPriceSetField 返回峰谷价结构里对应 LiteLLM 字段的槽位。
func catalogPriceSetField(set *catalogPriceSet, field string) *float64 {
	if set == nil {
		return nil
	}
	switch field {
	case "input_cost_per_token":
		return &set.Input
	case "output_cost_per_token":
		return &set.Output
	case "cache_read_input_token_cost":
		return &set.CacheRead
	case "cache_creation_input_token_cost":
		return &set.CacheWrite
	default:
		return nil
	}
}

// catalogMetaEmpty 报告元数据是否已无任何内容（此时应移除 `_catalog` 键）。
func catalogMetaEmpty(meta catalogMeta) bool {
	return meta.Platform == "" && !meta.Custom && !meta.CNYSource && !meta.PeakEnabled &&
		meta.Peak.isEmpty() && meta.OffPeak.isEmpty() && meta.PerRequestPrice == nil
}

// modelPricingAllowedPlatforms 返回新增/编辑模型时可接受的平台标识集合。
//
// 同时接受两类写法（design.md §2.2「平台必选」，下拉来自前端平台清单）：
//   - domain 平台 ID（openai / anthropic / gemini / deepseek / kimi / ...），
//   - 定价目录的 provider 名（moonshot / zhipu / xai / ...，即列表里的 provider 列）。
//
// 两者都来自同一份 domain 平台清单，因此新增平台时无需改这里。
func modelPricingAllowedPlatforms() map[string]struct{} {
	specs := domain.Platforms()
	allowed := make(map[string]struct{}, len(specs)*2)
	for _, spec := range specs {
		allowed[strings.ToLower(strings.TrimSpace(spec.ID))] = struct{}{}
		if provider := strings.ToLower(strings.TrimSpace(spec.LiteLLMProvider)); provider != "" {
			allowed[provider] = struct{}{}
		}
	}
	return allowed
}

// validateModelPlatform 校验平台白名单：空值合法（表示不改/未指定），
// 非空时必须是已登记的平台 ID 或定价目录 provider 名。
func validateModelPlatform(platform string) error {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return nil
	}
	if _, ok := modelPricingAllowedPlatforms()[platform]; ok {
		return nil
	}
	return invalidModelPriceError(fmt.Sprintf("platform %q is not a recognized platform", platform))
}

// DeleteOverride 删除该模型的 override 条目（恢复内置），写入后热重载。
func (s *ModelPricingAdminService) DeleteOverride(model string) (*ModelPricingEntry, error) {
	path := s.overrideFilePath()
	if path == "" {
		return nil, ErrModelPricingOverrideFileNotConfigured
	}
	model = normalizeModelName(model)
	if model == "" {
		return nil, invalidModelPriceError("model is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := readJSONObjectFile(path)
	if err != nil {
		return nil, ErrModelPricingOverrideFileInvalid.WithCause(err)
	}
	if _, ok := entries[model]; ok {
		delete(entries, model)
		if err := writeJSONObjectFileAtomic(path, entries); err != nil {
			return nil, ErrModelPricingWriteFailed.WithCause(err)
		}
		if err := s.reload(); err != nil {
			return nil, err
		}
	}
	return s.currentEntry(model)
}

// CreateModel 新增一个模型（允许目录外，design.md §2.2/§3.1）。
//
// 校验：模型名格式、平台白名单、价格范围（USD / 1M，≥0 且 < 1e4）；与目录内置模型
// 或已有自定义条目重名时返回 409 MODEL_ALREADY_EXISTS，前端据此提示「转为覆盖」。
// 写入 override_file 并打 `_catalog`（custom=true），随后热重载。
func (s *ModelPricingAdminService) CreateModel(input ModelPricingCreateInput) (*ModelPricingEntry, error) {
	path := s.overrideFilePath()
	if path == "" {
		return nil, ErrModelPricingOverrideFileNotConfigured
	}
	model := normalizeModelName(input.Model)
	if err := validateNewModelName(model); err != nil {
		return nil, err
	}
	platform := strings.TrimSpace(input.Platform)
	if platform == "" {
		return nil, invalidModelPriceError("platform is required")
	}
	if err := validateModelPlatform(platform); err != nil {
		return nil, err
	}

	updates := make(map[string]float64, len(input.Fields))
	for key, value := range input.Fields {
		field, ok := modelPricingUSDFieldToLiteLLM[key]
		if !ok {
			return nil, invalidModelPriceError(fmt.Sprintf("unknown model price field %q", key))
		}
		if value == nil {
			continue
		}
		if !validUSDPerMTok(*value) {
			return nil, invalidModelPriceError(fmt.Sprintf("%s must be >= 0 and < %g (USD per 1M tokens)", key, float64(ModelPriceUSDPerMTokLimit)))
		}
		updates[field] = *value
	}
	peakUpdates, err := normalizePeakRateFields(input.Peak, "peak")
	if err != nil {
		return nil, err
	}
	offPeakUpdates, err := normalizePeakRateFields(input.OffPeak, "off_peak")
	if err != nil {
		return nil, err
	}
	if len(updates) == 0 && !input.PeakEnabled {
		return nil, invalidModelPriceError("at least one price field or peak_enabled is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 重名检查同时覆盖目录内置模型与已有自定义条目。
	if _, exists := s.pricing.Snapshot().Models[model]; exists {
		return nil, ErrModelAlreadyExists
	}
	entries, err := readJSONObjectFile(path)
	if err != nil {
		return nil, ErrModelPricingOverrideFileInvalid.WithCause(err)
	}
	for name := range entries {
		if normalizeModelName(name) == model {
			return nil, ErrModelAlreadyExists
		}
	}

	patch := make(map[string]any, len(updates)+1)
	for field, value := range updates {
		patch[field] = value / 1e6
	}
	meta := catalogMeta{
		Platform:    platform,
		Custom:      true,
		PeakEnabled: input.PeakEnabled,
		Peak:        mergePeakRateSet(catalogPriceSet{}, peakUpdates),
		OffPeak:     mergePeakRateSet(catalogPriceSet{}, offPeakUpdates),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedBy:   input.UpdatedBy,
	}
	patch[CatalogMetaKey] = meta

	body, err := json.Marshal(patch)
	if err != nil {
		return nil, ErrModelPricingWriteFailed.WithCause(err)
	}
	entries[model] = body
	if err := writeJSONObjectFileAtomic(path, entries); err != nil {
		return nil, ErrModelPricingWriteFailed.WithCause(err)
	}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s.currentEntry(model)
}

// DeleteModel 删除管理员自定义的模型（`_catalog.custom` 的条目），随后热重载。
//
// 目录内置模型（无 `_catalog` 或 custom=false）不允许删除：那会删掉运营者的覆盖
// 配置却留下目录价，语义上应走「恢复内置」。删除后模型从价格表消失。
func (s *ModelPricingAdminService) DeleteModel(model string) error {
	path := s.overrideFilePath()
	if path == "" {
		return ErrModelPricingOverrideFileNotConfigured
	}
	model = normalizeModelName(model)
	if model == "" {
		return invalidModelPriceError("model is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := readJSONObjectFile(path)
	if err != nil {
		return ErrModelPricingOverrideFileInvalid.WithCause(err)
	}
	key := ""
	for name, raw := range entries {
		if normalizeModelName(name) != model {
			continue
		}
		meta, hasMeta := parseCatalogMeta(raw)
		if !hasMeta || !meta.Custom {
			return ErrModelDeleteBuiltinRejected
		}
		key = name
		break
	}
	if key == "" {
		return ErrModelPricingNotFound
	}
	delete(entries, key)
	if err := writeJSONObjectFileAtomic(path, entries); err != nil {
		return ErrModelPricingWriteFailed.WithCause(err)
	}
	return s.reload()
}

// validateNewModelName 校验新增模型的名称格式：非空、≤128 字符、只允许字母数字与
// `-._/:@` 等常见模型名分隔符（与上游模型 ID 的实际字符集一致）。
func validateNewModelName(model string) error {
	if model == "" {
		return invalidModelPriceError("model is required")
	}
	if len(model) > 128 {
		return invalidModelPriceError("model name must be at most 128 characters")
	}
	if strings.HasPrefix(model, "_") {
		return invalidModelPriceError("model name cannot start with an underscore (reserved for metadata)")
	}
	for _, r := range model {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/', r == ':', r == '@', r == '+':
		default:
			return invalidModelPriceError(fmt.Sprintf("model name contains an invalid character %q", r))
		}
	}
	return nil
}

// PeakSettings 返回全局峰谷时段设置的持久化状态（未配置时按官方口径）。
func (s *ModelPricingAdminService) PeakSettings(ctx context.Context) PeakPricingSettings {
	if s != nil && s.pricing != nil {
		return s.pricing.PeakPricingSettings()
	}
	return defaultPeakPricingSettings()
}

// UpdatePeakSettings 保存全局峰谷时段设置：先归一化校验，再持久化到 settings 的
// peak_pricing_settings 键，最后注入 PricingService 热生效（design.md §3.2）。
func (s *ModelPricingAdminService) UpdatePeakSettings(ctx context.Context, settings PeakPricingSettings) (PeakPricingSettings, error) {
	if err := validatePeakPricingSettings(settings); err != nil {
		return PeakPricingSettings{}, err
	}
	normalized := settings.Normalize()
	if s != nil && s.settingRepo != nil {
		body, err := json.Marshal(normalized)
		if err != nil {
			return PeakPricingSettings{}, ErrModelPricingWriteFailed.WithCause(err)
		}
		if err := s.settingRepo.Set(ctx, SettingKeyPeakPricingSettings, string(body)); err != nil {
			return PeakPricingSettings{}, ErrModelPricingWriteFailed.WithCause(err)
		}
	}
	if s != nil && s.pricing != nil {
		s.pricing.SetPeakPricingSettings(normalized)
	}
	return normalized, nil
}

// LoadPeakSettings 从 settings 读取峰谷时段并注入 PricingService（服务启动时调用）。
//
// 未配置、为空或解析失败时回落内置官方口径；返回值仅用于日志/诊断。
func (s *ModelPricingAdminService) LoadPeakSettings(ctx context.Context) PeakPricingSettings {
	settings := defaultPeakPricingSettings()
	if s != nil && s.settingRepo != nil {
		if value, err := s.settingRepo.GetValue(ctx, SettingKeyPeakPricingSettings); err == nil {
			if raw := strings.TrimSpace(value); raw != "" {
				parsed := PeakPricingSettings{}
				if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
					settings = parsed
				}
			}
		}
	}
	normalized := settings.Normalize()
	if s != nil && s.pricing != nil {
		s.pricing.SetPeakPricingSettings(normalized)
	}
	return normalized
}

// validatePeakPricingSettings 校验用户提交的峰谷时段设置：时区必须可解析，
// 每个时段的 HH:mm 必须合法且起止不同。空 periods 合法（回落官方口径）。
func validatePeakPricingSettings(settings PeakPricingSettings) error {
	timezone := strings.TrimSpace(settings.Timezone)
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return ErrPeakPricingSettingsInvalid.WithCause(fmt.Errorf("unknown timezone %q: %w", timezone, err))
		}
	}
	for _, period := range settings.Periods {
		start, okStart := parseClockMinutes(period.Start)
		end, okEnd := parseClockMinutes(period.End)
		if !okStart || !okEnd {
			return ErrPeakPricingSettingsInvalid.WithCause(fmt.Errorf("period %q-%q must use HH:mm format", period.Start, period.End))
		}
		if start == end {
			return ErrPeakPricingSettingsInvalid.WithCause(fmt.Errorf("period %q-%q must not have equal start and end", period.Start, period.End))
		}
	}
	return nil
}

// Currency 返回当前人民币口径配置与逐模型的人民币价。
func (s *ModelPricingAdminService) Currency() ModelPricingCurrencyState {
	state := ModelPricingCurrencyState{Mode: CNYModeConvert, CNYPerUSD: defaultCNYPerUSD}
	if s == nil || s.pricing == nil {
		state.Models = []ModelPricingCNYEntry{}
		state.SeedModels = s.seedEntries(state)
		return state
	}
	settings := s.pricing.CurrentCNYSettings()
	snapshot := s.pricing.Snapshot()
	prices := s.pricing.CurrentCNYModelPrices()

	state.Mode = settings.NormalizedMode()
	state.CNYPerUSD = settings.CNYPerUSD
	state.File = settings.File
	state.Models = make([]ModelPricingCNYEntry, 0, len(prices))
	for name, price := range prices {
		state.Models = append(state.Models, cnyEntryFromPrice(name, price, settings, snapshot))
	}
	sort.Slice(state.Models, func(i, j int) bool { return state.Models[i].Model < state.Models[j].Model })
	state.SeedModels = s.seedEntries(state)
	return state
}

// UpdateCurrency 更新人民币口径（mode 与 cny_per_usd），写入后热重载。
func (s *ModelPricingAdminService) UpdateCurrency(mode string, cnyPerUSD *float64) (ModelPricingCurrencyState, error) {
	path := s.cnyFilePath()
	if path == "" {
		return ModelPricingCurrencyState{}, ErrModelPricingCNYFileNotConfigured
	}
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	if normalizedMode != CNYModeConvert && normalizedMode != CNYModeDirect {
		return ModelPricingCurrencyState{}, ErrInvalidCNYMode
	}
	if cnyPerUSD != nil && (math.IsNaN(*cnyPerUSD) || math.IsInf(*cnyPerUSD, 0) || *cnyPerUSD <= 0) {
		return ModelPricingCurrencyState{}, ErrInvalidCNYRate
	}

	s.mu.Lock()
	file, err := s.readCNYFile(path)
	if err != nil {
		s.mu.Unlock()
		return ModelPricingCurrencyState{}, err
	}
	if file.Version == 0 {
		file.Version = 1
	}
	file.Mode = normalizedMode
	if cnyPerUSD != nil {
		rate := *cnyPerUSD
		file.CNYPerUSD = &rate
	} else if file.CNYPerUSD == nil || *file.CNYPerUSD <= 0 {
		rate := defaultCNYPerUSD
		file.CNYPerUSD = &rate
	}
	if err := writeJSONObjectFileAtomic(path, file); err != nil {
		s.mu.Unlock()
		return ModelPricingCurrencyState{}, ErrModelPricingWriteFailed.WithCause(err)
	}
	reloadErr := s.reload()
	s.mu.Unlock()
	if reloadErr != nil {
		return ModelPricingCurrencyState{}, reloadErr
	}
	return s.Currency(), nil
}

// UpsertCurrencyModel 写入单个模型的人民币原生价：0 或 nil 表示该字段不登记；
// 全部字段都不登记时删除该模型条目。
func (s *ModelPricingAdminService) UpsertCurrencyModel(model string, fields map[string]*float64) (ModelPricingCurrencyState, error) {
	path := s.cnyFilePath()
	if path == "" {
		return ModelPricingCurrencyState{}, ErrModelPricingCNYFileNotConfigured
	}
	model = normalizeModelName(model)
	if model == "" {
		return ModelPricingCurrencyState{}, invalidCNYPriceError("model is required")
	}

	// 先整体校验，非法输入不落盘。
	for key, value := range fields {
		if _, ok := modelPricingCNYFields[key]; !ok {
			return ModelPricingCurrencyState{}, invalidCNYPriceError(fmt.Sprintf("unknown CNY price field %q", key))
		}
		if value != nil && !validCNYPerMTok(*value) {
			return ModelPricingCurrencyState{}, invalidCNYPriceError(fmt.Sprintf("%s must be >= 0 and < %g (CNY per 1M tokens)", key, float64(ModelPriceCNYPerMTokLimit)))
		}
	}

	s.mu.Lock()
	file, err := s.readCNYFile(path)
	if err != nil {
		s.mu.Unlock()
		return ModelPricingCurrencyState{}, err
	}
	if file.Models == nil {
		file.Models = make(map[string]LiteLLMCNYModelPricing)
	}
	price := file.Models[model]
	for key, value := range fields {
		field := cnyPriceFieldPointer(&price, key)
		if field == nil {
			continue
		}
		if value == nil || *value <= 0 {
			*field = nil
			continue
		}
		v := *value
		*field = &v
	}
	if cnyModelPricingEmpty(price) {
		delete(file.Models, model)
	} else {
		file.Models[model] = price
	}
	if err := writeJSONObjectFileAtomic(path, file); err != nil {
		s.mu.Unlock()
		return ModelPricingCurrencyState{}, ErrModelPricingWriteFailed.WithCause(err)
	}
	reloadErr := s.reload()
	s.mu.Unlock()
	if reloadErr != nil {
		return ModelPricingCurrencyState{}, reloadErr
	}
	return s.Currency(), nil
}

// DeleteCurrencyModel 删除该模型的人民币登记（恢复目录/兜底价），写入后热重载。
func (s *ModelPricingAdminService) DeleteCurrencyModel(model string) (ModelPricingCurrencyState, error) {
	path := s.cnyFilePath()
	if path == "" {
		return ModelPricingCurrencyState{}, ErrModelPricingCNYFileNotConfigured
	}
	model = normalizeModelName(model)
	if model == "" {
		return ModelPricingCurrencyState{}, invalidCNYPriceError("model is required")
	}

	s.mu.Lock()
	file, err := s.readCNYFile(path)
	if err != nil {
		s.mu.Unlock()
		return ModelPricingCurrencyState{}, err
	}
	if _, ok := file.Models[model]; ok {
		delete(file.Models, model)
		if err := writeJSONObjectFileAtomic(path, file); err != nil {
			s.mu.Unlock()
			return ModelPricingCurrencyState{}, ErrModelPricingWriteFailed.WithCause(err)
		}
		reloadErr := s.reload()
		s.mu.Unlock()
		if reloadErr != nil {
			return ModelPricingCurrencyState{}, reloadErr
		}
	} else {
		s.mu.Unlock()
	}
	return s.Currency(), nil
}

// overrideFilePath 返回 override 文件路径；未配置时为空串（编辑禁用）。
func (s *ModelPricingAdminService) overrideFilePath() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.Pricing.OverrideFile)
}

// cnyFilePath 返回人民币层文件的有效路径（配置或推导）。
func (s *ModelPricingAdminService) cnyFilePath() string {
	if s == nil || s.pricing == nil {
		return ""
	}
	return s.pricing.EffectiveCNYOverrideFile()
}

// reload 触发价格服务热重载，写成后立即生效。
func (s *ModelPricingAdminService) reload() error {
	if s == nil || s.pricing == nil {
		return ErrModelPricingReloadFailed
	}
	if err := s.pricing.ReloadCustomPricingLayers(); err != nil {
		return ErrModelPricingReloadFailed.WithCause(err)
	}
	return nil
}

// currentEntry 返回单个模型的最新有效价；模型不存在时返回 404 语义错误。
func (s *ModelPricingAdminService) currentEntry(model string) (*ModelPricingEntry, error) {
	snapshot := s.pricing.Snapshot()
	pricing, ok := snapshot.Models[model]
	if !ok {
		return nil, ErrModelPricingNotFound
	}
	entry := s.entryFromSnapshot(model, pricing, snapshot, s.pricing.CurrentCNYModelPrices(), s.catalogMetaIndex())
	return &entry, nil
}

// entryFromSnapshot 把快照中的一条价格转换成管理端视图。
func (s *ModelPricingAdminService) entryFromSnapshot(
	model string,
	pricing LiteLLMModelPricing,
	snapshot PricingSnapshot,
	cnyPrices map[string]LiteLLMCNYModelPricing,
	catalogMetas map[string]catalogMeta,
) ModelPricingEntry {
	source := snapshot.Sources[model]
	if source == "" {
		source = "litellm"
	}
	entry := ModelPricingEntry{
		Model:                    model,
		Provider:                 pricing.LiteLLMProvider,
		Mode:                     pricing.Mode,
		InputUSDPerMTok:          pricing.InputCostPerToken * 1e6,
		OutputUSDPerMTok:         pricing.OutputCostPerToken * 1e6,
		CacheReadUSDPerMTok:      pricing.CacheReadInputTokenCost * 1e6,
		CacheWriteUSDPerMTok:     pricing.CacheCreationInputTokenCost * 1e6,
		ImageInputUSDPerMTok:     pricing.InputCostPerImageToken * 1e6,
		PriorityInputUSDPerMTok:  pricing.InputCostPerTokenPriority * 1e6,
		PriorityOutputUSDPerMTok: pricing.OutputCostPerTokenPriority * 1e6,
		Source:                   source,
		Overridden:               hasKey(snapshot.OverriddenModels, model),
		EffectiveUpdatedAt:       snapshot.CatalogUpdatedAt,
	}
	if price, ok := cnyPrices[model]; ok {
		entry.CNY = &ModelPricingCNYEquivalent{
			Input:      cnyValue(price.InputCNYPerMTok),
			Output:     cnyValue(price.OutputCNYPerMTok),
			CacheRead:  cnyValue(price.CacheReadCNYPerMTok),
			CacheWrite: cnyValue(price.CacheWriteCNYPerMTok),
			ImageInput: cnyValue(price.ImageInputCNYPerMTok),
		}
	}
	if meta, ok := catalogMetas[normalizeCatalogModel(model)]; ok {
		entry.Platform = meta.Platform
		entry.Custom = meta.Custom
		entry.CNYSource = meta.CNYSource
		entry.PeakEnabled = meta.PeakEnabled
		entry.Peak = modelPricingPeakRatesFromCatalog(meta.Peak)
		entry.OffPeak = modelPricingPeakRatesFromCatalog(meta.OffPeak)
		entry.UpdatedAt = meta.UpdatedAt
		entry.UpdatedBy = meta.UpdatedBy
	}
	// 目录价层由 resolver 接管，来源标注为 catalog（design.md §4.1「来源」列）。
	if entry.Custom && s.catalogApplied(pricing, snapshot, metaOf(catalogMetas, model)) {
		entry.Source = PricingSourceCatalog
	}
	return entry
}

// metaOf 读取模型对应的 `_catalog` 元数据（缺失返回零值）。
func metaOf(metas map[string]catalogMeta, model string) catalogMeta {
	if metas == nil {
		return catalogMeta{}
	}
	return metas[normalizeCatalogModel(model)]
}

// catalogApplied 报告该模型的当前有效价是否由目录价层接管。
//
// 判定与 model_pricing_resolver.resolveCatalogPricing 一致：开关打开（默认 true）、
// 条目带 `_catalog` 元数据、且至少有可计价字段。列表里的「来源」列据此显示 catalog，
// 与实际计费路径保持同源。
func (s *ModelPricingAdminService) catalogApplied(pricing LiteLLMModelPricing, snapshot PricingSnapshot, meta catalogMeta) bool {
	if s == nil || s.pricing == nil {
		return false
	}
	if !s.CatalogPricingAuthoritative() {
		return false
	}
	if meta.Platform == "" && !meta.Custom && !meta.PeakEnabled && !meta.CNYSource && meta.PerRequestPrice == nil {
		// 完全没有元数据内容，等同于未接管。
		return false
	}
	// 价格字段存在（override 合并后进入快照）或峰谷可计价，都视为目录价层接管。
	if pricing.InputCostPerToken > 0 || pricing.OutputCostPerToken > 0 ||
		pricing.CacheReadInputTokenCost > 0 || pricing.CacheCreationInputTokenCost > 0 {
		return true
	}
	return meta.PeakEnabled
}

// modelPricingPeakRatesFromCatalog 把 `_catalog` 里的 per-token 峰谷价转成 USD / 1M 视图。
func modelPricingPeakRatesFromCatalog(set catalogPriceSet) ModelPricingPeakRates {
	return ModelPricingPeakRates{
		InputUSDPerMTok:      set.Input * 1e6,
		OutputUSDPerMTok:     set.Output * 1e6,
		CacheReadUSDPerMTok:  set.CacheRead * 1e6,
		CacheWriteUSDPerMTok: set.CacheWrite * 1e6,
	}
}

// modelPricingPeakRatesToCatalog 把 USD / 1M 的峰谷价转回落盘的 per-token 结构。
// 全 0 视为未配置，返回零值（JSON omitempty 时不落盘）。
func modelPricingPeakRatesToCatalog(rates ModelPricingPeakRates) catalogPriceSet {
	return catalogPriceSet{
		Input:      rates.InputUSDPerMTok / 1e6,
		Output:     rates.OutputUSDPerMTok / 1e6,
		CacheRead:  rates.CacheReadUSDPerMTok / 1e6,
		CacheWrite: rates.CacheWriteUSDPerMTok / 1e6,
	}
}

// metaFromSnapshot 组装列表响应的元信息。
func (s *ModelPricingAdminService) metaFromSnapshot(snapshot PricingSnapshot) ModelPricingMeta {
	meta := ModelPricingMeta{
		CNYMode:          snapshot.CNYSettings.NormalizedMode(),
		CNYPerUSD:        snapshot.CNYSettings.CNYPerUSD,
		OverrideFile:     snapshot.OverrideFile,
		CNYFile:          snapshot.CNYOverrideFile,
		CatalogFile:      snapshot.CatalogFile,
		CatalogUpdatedAt: snapshot.CatalogUpdatedAt,
		ModelCount:       snapshot.ModelCount,
		Editable:         snapshot.OverrideFile != "",
		CNYEditable:      snapshot.CNYOverrideFile != "",
	}
	if s != nil && s.pricing != nil {
		meta.CatalogAuthoritative = s.CatalogPricingAuthoritative()
		meta.PeakSettings = s.pricing.PeakPricingSettings()
	} else {
		meta.CatalogAuthoritative = true
		meta.PeakSettings = defaultPeakPricingSettings()
	}
	return meta
}

// seedEntries 把种子模型按当前口径折算成视图。
func (s *ModelPricingAdminService) seedEntries(state ModelPricingCurrencyState) []ModelPricingCNYEntry {
	settings := CNYSettings{Mode: state.Mode, CNYPerUSD: state.CNYPerUSD}
	seeds := ModelPricingCNYSeeds()
	entries := make([]ModelPricingCNYEntry, 0, len(seeds))
	for _, seed := range seeds {
		entries = append(entries, cnyEntryFromPrice(seed.Model, seed.Prices, settings, PricingSnapshot{}))
	}
	return entries
}

// readCNYFile 读取人民币层文件；文件缺失时返回默认口径的空配置。
func (s *ModelPricingAdminService) readCNYFile(path string) (*LiteLLMCNYOverrideFile, error) {
	file := &LiteLLMCNYOverrideFile{Version: 1, Mode: CNYModeConvert, Models: map[string]LiteLLMCNYModelPricing{}}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		rate := defaultCNYPerUSD
		file.CNYPerUSD = &rate
		return file, nil
	}
	if err != nil {
		return nil, ErrModelPricingCNYFileNotConfigured.WithCause(err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		rate := defaultCNYPerUSD
		file.CNYPerUSD = &rate
		return file, nil
	}
	if err := json.Unmarshal(body, file); err != nil {
		return nil, ErrModelPricingOverrideFileInvalid.WithCause(err)
	}
	if file.Models == nil {
		file.Models = map[string]LiteLLMCNYModelPricing{}
	}
	return file, nil
}

// cnyEntryFromPrice 把单模型人民币登记价换算成视图（含当前口径下的折算 USD 价）。
func cnyEntryFromPrice(model string, price LiteLLMCNYModelPricing, settings CNYSettings, snapshot PricingSnapshot) ModelPricingCNYEntry {
	rate := settings.ConversionRate()
	if rate <= 0 {
		rate = defaultCNYPerUSD
	}
	entry := ModelPricingCNYEntry{
		Model:                model,
		InputCNYPerMTok:      cnyValue(price.InputCNYPerMTok),
		OutputCNYPerMTok:     cnyValue(price.OutputCNYPerMTok),
		CacheReadCNYPerMTok:  cnyValue(price.CacheReadCNYPerMTok),
		CacheWriteCNYPerMTok: cnyValue(price.CacheWriteCNYPerMTok),
		ImageInputCNYPerMTok: cnyValue(price.ImageInputCNYPerMTok),
		Overridden:           hasKey(snapshot.OverriddenModels, model),
	}
	entry.Source = snapshot.Sources[model]
	if entry.Source == "" {
		entry.Source = "cny"
	}
	entry.EffectiveInputUSDPerMTok = entry.InputCNYPerMTok / rate
	entry.EffectiveOutputUSDPerMTok = entry.OutputCNYPerMTok / rate
	entry.EffectiveCacheReadUSDPerMTok = entry.CacheReadCNYPerMTok / rate
	entry.EffectiveCacheWriteUSDPerMTok = entry.CacheWriteCNYPerMTok / rate
	entry.EffectiveImageInputUSDPerMTok = entry.ImageInputCNYPerMTok / rate
	return entry
}

// cnyValue 解引用人民币登记价指针；缺失或非正值按 0 显示。
func cnyValue(value *float64) float64 {
	if value == nil || *value <= 0 {
		return 0
	}
	return *value
}

// cnyPriceFieldPointer 返回人民币登记价中对应 API 字段的指针槽位。
func cnyPriceFieldPointer(price *LiteLLMCNYModelPricing, field string) **float64 {
	switch field {
	case "input_cny_per_mtok":
		return &price.InputCNYPerMTok
	case "output_cny_per_mtok":
		return &price.OutputCNYPerMTok
	case "cache_read_cny_per_mtok":
		return &price.CacheReadCNYPerMTok
	case "cache_write_cny_per_mtok":
		return &price.CacheWriteCNYPerMTok
	case "image_input_cny_per_mtok":
		return &price.ImageInputCNYPerMTok
	default:
		return nil
	}
}

// cnyModelPricingEmpty 报告人民币登记价是否已无任何字段。
func cnyModelPricingEmpty(price LiteLLMCNYModelPricing) bool {
	return price.InputCNYPerMTok == nil &&
		price.OutputCNYPerMTok == nil &&
		price.CacheReadCNYPerMTok == nil &&
		price.CacheWriteCNYPerMTok == nil &&
		price.ImageInputCNYPerMTok == nil
}

// normalizeModelName 统一模型名大小写与空白（价格表按小写键查找）。
func normalizeModelName(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// normalizeModelPricingPagination 归一化分页参数：page ≥ 1，1 ≤ page_size ≤ 100。
func normalizeModelPricingPagination(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultModelPricingPageSize
	}
	if pageSize > modelPricingPageSizeLimit {
		pageSize = modelPricingPageSizeLimit
	}
	return page, pageSize
}

// paginateModelPricingEntries 按 1 基页码切片。
func paginateModelPricingEntries(entries []ModelPricingEntry, page, pageSize int) []ModelPricingEntry {
	start := (page - 1) * pageSize
	if start >= len(entries) {
		return []ModelPricingEntry{}
	}
	end := start + pageSize
	if end > len(entries) {
		end = len(entries)
	}
	return entries[start:end]
}

// validUSDPerMTok 校验 USD / 1M tokens 取值范围：≥0 且 < 1e4。
func validUSDPerMTok(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < ModelPriceUSDPerMTokLimit
}

// validCNYPerMTok 校验 CNY / 1M tokens 取值范围：≥0 且 < 1e6。
func validCNYPerMTok(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value < ModelPriceCNYPerMTokLimit
}

func invalidModelPriceError(message string) *infraerrors.ApplicationError {
	return infraerrors.BadRequest("INVALID_MODEL_PRICE", message)
}

func invalidCNYPriceError(message string) *infraerrors.ApplicationError {
	return infraerrors.BadRequest("INVALID_CNY_PRICE", message)
}

// NewInvalidModelPriceError 构造 400 INVALID_MODEL_PRICE，供 handler 在请求体绑定
// 失败时复用同一错误码。
func NewInvalidModelPriceError(message string) *infraerrors.ApplicationError {
	return invalidModelPriceError(message)
}

// NewInvalidCNYPriceError 构造 400 INVALID_CNY_PRICE。
func NewInvalidCNYPriceError(message string) *infraerrors.ApplicationError {
	return invalidCNYPriceError(message)
}

// NewInvalidCNYModeError 构造 400 INVALID_CNY_MODE。
func NewInvalidCNYModeError(message string) *infraerrors.ApplicationError {
	return infraerrors.BadRequest("INVALID_CNY_MODE", message)
}

// NewInvalidPeakPricingSettingsError 构造 400 INVALID_PEAK_PRICING_SETTINGS，
// 供 handler 在请求体绑定失败时复用同一错误码。
func NewInvalidPeakPricingSettingsError(message string) *infraerrors.ApplicationError {
	return infraerrors.BadRequest("INVALID_PEAK_PRICING_SETTINGS", message)
}

// readJSONObjectFile 读取 JSON 对象文件为原始条目 map；文件缺失按空对象处理。
func readJSONObjectFile(path string) (map[string]json.RawMessage, error) {
	entries := map[string]json.RawMessage{}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return entries, nil
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = map[string]json.RawMessage{}
	}
	return entries, nil
}

// writeJSONObjectFileAtomic 以"临时文件 + rename"原子替换 JSON 文件，并在写盘前后校验
// JSON 合法性；失败时清理临时文件，目标文件保持原样。
func writeJSONObjectFileAtomic(path string, payload any) error {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pricing payload: %w", err)
	}
	body = append(body, '\n')
	var precheck map[string]json.RawMessage
	if err := json.Unmarshal(body, &precheck); err != nil {
		return fmt.Errorf("generated pricing payload is not a JSON object: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create pricing directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp pricing file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("write temp pricing file: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("chmod temp pricing file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("sync temp pricing file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeTemp()
		return fmt.Errorf("close temp pricing file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		removeTemp()
		return fmt.Errorf("replace pricing file: %w", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("verify pricing file: %w", err)
	}
	var postcheck map[string]json.RawMessage
	if err := json.Unmarshal(saved, &postcheck); err != nil {
		return fmt.Errorf("written pricing file is not a JSON object: %w", err)
	}
	return nil
}
