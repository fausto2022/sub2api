/**
 * Admin Model Pricing API endpoints
 *
 * 契约对齐 backend/internal/handler/dto/model_pricing.go 与
 * backend/internal/handler/admin/model_pricing_handler.go
 * （管理侧 /api/v1/admin/model-pricing）。
 *
 * 所有价格字段一律使用 **USD / 1M tokens**（人民币字段使用 CNY / 1M tokens），
 * 写接口中的 `null` 表示删除该字段的覆盖/登记，回退到下一层价格。
 *
 * 目录价层（design.md §3.1）：override 条目的 `_catalog` 元数据在管理端被拍平成
 * 顶层字段 `platform` / `custom` / `peak_enabled` / `peak` / `off_peak`，
 * 峰谷价同样使用 **USD / 1M tokens** 单位（与基础价同名同单位）。
 */

import { apiClient } from '../client'

/** 可写的 USD 覆盖字段（USD / 1M tokens）。 */
export const USD_PER_MTOK_FIELDS = [
  'input_usd_per_mtok',
  'output_usd_per_mtok',
  'cache_read_usd_per_mtok',
  'cache_write_usd_per_mtok',
  'image_input_usd_per_mtok',
  'priority_input_usd_per_mtok',
  'priority_output_usd_per_mtok'
] as const

export type UsdPerMtokField = (typeof USD_PER_MTOK_FIELDS)[number]

/** 首版 UI 直接展示的四个基础字段，其余作为高级字段折叠。 */
export const USD_PER_MTOK_BASIC_FIELDS = [
  'input_usd_per_mtok',
  'output_usd_per_mtok',
  'cache_read_usd_per_mtok',
  'cache_write_usd_per_mtok'
] as const satisfies readonly UsdPerMtokField[]

export const USD_PER_MTOK_ADVANCED_FIELDS = [
  'image_input_usd_per_mtok',
  'priority_input_usd_per_mtok',
  'priority_output_usd_per_mtok'
] as const satisfies readonly UsdPerMtokField[]

/**
 * 峰谷面板里的四个价格字段（USD / 1M tokens）。
 * 与基础字段同名同单位，便于「一键还原」按同一乘数换算。
 */
export const PEAK_PRICE_FIELDS = [
  'input_usd_per_mtok',
  'output_usd_per_mtok',
  'cache_read_usd_per_mtok',
  'cache_write_usd_per_mtok'
] as const satisfies readonly UsdPerMtokField[]

export type PeakPriceField = (typeof PEAK_PRICE_FIELDS)[number]

/** 可写的人民币登记字段（CNY / 1M tokens）。 */
export const CNY_PER_MTOK_FIELDS = [
  'input_cny_per_mtok',
  'output_cny_per_mtok',
  'cache_read_cny_per_mtok',
  'cache_write_cny_per_mtok',
  'image_input_cny_per_mtok'
] as const

export type CnyPerMtokField = (typeof CNY_PER_MTOK_FIELDS)[number]

/** 人民币口径：convert = 按汇率折算，direct = 人民币 1:1 当美元使用。 */
export type CurrencyMode = 'convert' | 'direct'

/** 服务端校验上限（对齐 service.ModelPriceUSDPerMTokLimit / ModelPriceCNYPerMTokLimit）。 */
export const MODEL_PRICE_USD_LIMIT = 1e4
export const MODEL_PRICE_CNY_LIMIT = 1e6
export const DEFAULT_CNY_PER_USD = 7.14

/** 新增模型时后端返回的冲突错误码（HTTP 409）。 */
export const MODEL_ALREADY_EXISTS = 'MODEL_ALREADY_EXISTS'

/** dto.ModelPricingCNY：内置价格行附带的人民币登记价（只读）。 */
export interface ModelPricingCNY {
  input: number
  output: number
  cache_read: number
  cache_write: number
  image_input: number
}

/** 一组峰谷价（USD / 1M tokens），未配置的键等同 0/缺失。 */
export type PeakPriceSet = Partial<Record<PeakPriceField, number>>

/** dto.ModelPricingItem：单个模型的有效价（USD / 1M tokens）。 */
export interface ModelPricingItem {
  model: string
  provider: string
  mode: string
  input_usd_per_mtok: number
  output_usd_per_mtok: number
  cache_read_usd_per_mtok: number
  cache_write_usd_per_mtok: number
  image_input_usd_per_mtok: number
  priority_input_usd_per_mtok: number
  priority_output_usd_per_mtok: number
  source: string
  overridden: boolean
  cny?: ModelPricingCNY
  effective_updated_at: string
  /** 平台（来自 `_catalog.platform`，未配置时为空串）。 */
  platform?: string
  /** 是否由管理员在目录页定义/覆盖（`_catalog.custom`）。 */
  custom?: boolean
  /** 当前价是否来自人民币登记的换算（`_catalog.cny_source`）。 */
  cny_source?: boolean
  /** 是否启用模型级自定义峰谷价。 */
  peak_enabled?: boolean
  /** 高峰价（USD / 1M）；后端始终下发，未启用时是 4 个 0 值。 */
  peak?: PeakPriceSet
  /** 低谷价（USD / 1M）；缺省键回落基础价。 */
  off_peak?: PeakPriceSet
  /** 最后一次管理端写入时间（`_catalog.updated_at`，未自定义时缺省）。 */
  updated_at?: string
  /** 最后一次管理端写入者用户 ID（`_catalog.updated_by`，未自定义时缺省）。 */
  updated_by?: number
}

/** 峰谷全局时段（settings.peak_pricing_settings）。 */
export interface PeakPricingPeriod {
  start: string
  end: string
}

export interface PeakPricingSettings {
  timezone: string
  weekday_only: boolean
  weekend_off_peak: boolean
  periods: PeakPricingPeriod[]
}

/** dto.ModelPricingMeta：价格层元信息。 */
export interface ModelPricingMeta {
  cny_mode: CurrencyMode
  cny_per_usd: number
  override_file: string
  cny_file: string
  catalog_file: string
  catalog_updated_at: string
  model_count: number
  editable: boolean
  cny_editable: boolean
  /** `catalog_pricing_authoritative` 开关当前值（默认 true）。 */
  catalog_authoritative?: boolean
  /** 当前峰谷全局时段（与 peak-settings 接口同结构）。 */
  peak_settings?: PeakPricingSettings
}

/** dto.ModelPricingListResponse */
export interface ModelPricingListResponse {
  items: ModelPricingItem[]
  total: number
  page: number
  page_size: number
  meta: ModelPricingMeta
}

export interface ModelPricingListParams {
  page?: number
  page_size?: number
  search?: string
  provider?: string
  platform?: string
  overridden?: boolean
  custom?: boolean
  peak_enabled?: boolean
  signal?: AbortSignal
}

/** PUT /overrides/:model 请求体：字段值 null = 删除该字段覆盖。 */
export type ModelPricingOverridePayload = Partial<Record<UsdPerMtokField, number | null>> & {
  platform?: string
  custom?: boolean
  peak_enabled?: boolean
  peak?: PeakPriceSet | null
  off_peak?: PeakPriceSet | null
}

/** POST /models 请求体：新增目录外/自定义模型。 */
export interface CreateModelPricingPayload {
  model: string
  platform: string
  input_usd_per_mtok?: number
  output_usd_per_mtok?: number
  cache_read_usd_per_mtok?: number
  cache_write_usd_per_mtok?: number
  image_input_usd_per_mtok?: number
  priority_input_usd_per_mtok?: number
  priority_output_usd_per_mtok?: number
  peak_enabled?: boolean
  peak?: PeakPriceSet
  off_peak?: PeakPriceSet
}

/** dto.ModelPricingCurrencyItem：人民币层单模型视图（原生 CNY + 当前口径折算 USD）。 */
export interface ModelPricingCurrencyItem {
  model: string
  input_cny_per_mtok: number
  output_cny_per_mtok: number
  cache_read_cny_per_mtok: number
  cache_write_cny_per_mtok: number
  image_input_cny_per_mtok: number
  effective_input_usd_per_mtok: number
  effective_output_usd_per_mtok: number
  effective_cache_read_usd_per_mtok: number
  effective_cache_write_usd_per_mtok: number
  effective_image_input_usd_per_mtok: number
  overridden: boolean
  source: string
}

/** dto.ModelPricingCurrencyResponse */
export interface ModelPricingCurrencyState {
  mode: CurrencyMode
  cny_per_usd: number
  file: string
  models: ModelPricingCurrencyItem[]
  seed_models: ModelPricingCurrencyItem[]
}

export interface UpdateModelPricingCurrencyPayload {
  mode: CurrencyMode
  cny_per_usd?: number
}

/** PUT /currency/models/:model 请求体：0 或 null = 该字段不登记。 */
export type ModelPricingCnyPayload = Partial<Record<CnyPerMtokField, number | null>>

function encodeModel(model: string): string {
  return encodeURIComponent(model)
}

/** GET /admin/model-pricing：分页 + 搜索 + 供应商/平台/覆盖/自定义筛选。 */
export async function listModelPricing(
  params: ModelPricingListParams = {}
): Promise<ModelPricingListResponse> {
  const {
    page = 1,
    page_size = 20,
    search,
    provider,
    platform,
    overridden,
    custom,
    peak_enabled,
    signal
  } = params
  const { data } = await apiClient.get<ModelPricingListResponse>('/admin/model-pricing', {
    params: {
      page,
      page_size,
      ...(search ? { search } : {}),
      ...(provider ? { provider } : {}),
      ...(platform ? { platform } : {}),
      ...(overridden !== undefined ? { overridden } : {}),
      ...(custom !== undefined ? { custom } : {}),
      ...(peak_enabled !== undefined ? { peak_enabled } : {})
    },
    signal
  })
  return data
}

/** PUT /admin/model-pricing/overrides/:model：写入字段级 USD 覆盖（含峰谷与平台）。 */
export async function updateModelPricingOverride(
  model: string,
  payload: ModelPricingOverridePayload
): Promise<ModelPricingItem> {
  const { data } = await apiClient.put<ModelPricingItem>(
    `/admin/model-pricing/overrides/${encodeModel(model)}`,
    payload
  )
  return data
}

/** DELETE /admin/model-pricing/overrides/:model：整模型恢复内置价。 */
export async function deleteModelPricingOverride(model: string): Promise<ModelPricingItem> {
  const { data } = await apiClient.delete<ModelPricingItem>(
    `/admin/model-pricing/overrides/${encodeModel(model)}`
  )
  return data
}

/** POST /admin/model-pricing/models：新增模型（目录外可创建，重名返回 409）。 */
export async function createModelPricing(
  payload: CreateModelPricingPayload
): Promise<ModelPricingItem> {
  const { data } = await apiClient.post<ModelPricingItem>('/admin/model-pricing/models', payload)
  return data
}

/** DELETE /admin/model-pricing/models/:model：删除自定义模型。 */
export async function deleteModelPricing(model: string): Promise<void> {
  await apiClient.delete(`/admin/model-pricing/models/${encodeModel(model)}`)
}

/** GET /admin/model-pricing/peak-settings：读取全局峰谷时段。 */
export async function getPeakPricingSettings(
  options: { signal?: AbortSignal } = {}
): Promise<PeakPricingSettings> {
  const { data } = await apiClient.get<PeakPricingSettings>(
    '/admin/model-pricing/peak-settings',
    { signal: options.signal }
  )
  return data
}

/** PUT /admin/model-pricing/peak-settings：整体替换全局峰谷时段（热生效）。 */
export async function updatePeakPricingSettings(
  payload: PeakPricingSettings
): Promise<PeakPricingSettings> {
  const { data } = await apiClient.put<PeakPricingSettings>(
    '/admin/model-pricing/peak-settings',
    payload
  )
  return data
}

/** GET /admin/model-pricing/currency：人民币口径配置与逐模型人民币价。 */
export async function getModelPricingCurrency(
  options: { signal?: AbortSignal } = {}
): Promise<ModelPricingCurrencyState> {
  const { data } = await apiClient.get<ModelPricingCurrencyState>('/admin/model-pricing/currency', {
    signal: options.signal
  })
  return data
}

/** PUT /admin/model-pricing/currency：切换口径 / 更新汇率。 */
export async function updateModelPricingCurrency(
  payload: UpdateModelPricingCurrencyPayload
): Promise<ModelPricingCurrencyState> {
  const { data } = await apiClient.put<ModelPricingCurrencyState>(
    '/admin/model-pricing/currency',
    payload
  )
  return data
}

/** PUT /admin/model-pricing/currency/models/:model：登记单模型人民币价。 */
export async function upsertModelPricingCurrencyModel(
  model: string,
  payload: ModelPricingCnyPayload
): Promise<ModelPricingCurrencyState> {
  const { data } = await apiClient.put<ModelPricingCurrencyState>(
    `/admin/model-pricing/currency/models/${encodeModel(model)}`,
    payload
  )
  return data
}

/** DELETE /admin/model-pricing/currency/models/:model：删除人民币登记。 */
export async function deleteModelPricingCurrencyModel(
  model: string
): Promise<ModelPricingCurrencyState> {
  const { data } = await apiClient.delete<ModelPricingCurrencyState>(
    `/admin/model-pricing/currency/models/${encodeModel(model)}`
  )
  return data
}

export const modelPricingAPI = {
  list: listModelPricing,
  updateOverride: updateModelPricingOverride,
  deleteOverride: deleteModelPricingOverride,
  createModel: createModelPricing,
  deleteModel: deleteModelPricing,
  getPeakSettings: getPeakPricingSettings,
  updatePeakSettings: updatePeakPricingSettings,
  getCurrency: getModelPricingCurrency,
  updateCurrency: updateModelPricingCurrency,
  upsertCurrencyModel: upsertModelPricingCurrencyModel,
  deleteCurrencyModel: deleteModelPricingCurrencyModel
}

export default modelPricingAPI
