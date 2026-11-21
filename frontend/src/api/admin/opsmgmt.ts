/**
 * Admin Ops Management API endpoints（运营管理，design.md 第二部分 v3.0）。
 *
 * 前缀 /admin/opsmgmt/*：与既有可观测性模块的 /admin/ops/* 严格隔离
 * （后端 routes/admin.go registerOpsMgmtRoutes）。唯一例外是分组级最低利润率
 * 的别名 PUT /admin/groups/:id/opsmgmt-margin。
 *
 * 脱敏约定：站点密码 / access token / 上游 API Key 后端一律不返回，
 * 只下发 password_set / upstream_api_key_set 布尔。
 */

import { apiClient } from '../client'

// ==================== 类型 ====================

/** 站点状态：同步失败置 error，但保留旧快照。 */
export type OpsSiteStatus = 'active' | 'error'

/** 站点登录方式：账号密码登录（默认）/ 可选管理员 Key 只读。 */
export type OpsAuthMode = 'password' | 'api_key'

export interface OpsSite {
  id: number
  name: string
  base_url: string
  auth_mode: OpsAuthMode
  username: string
  password_set: boolean
  upstream_api_key_set: boolean
  status: OpsSiteStatus
  last_synced_at?: string
  last_error?: string
  sync_interval_minutes: number
  token_expires_at?: string
  created_at: string
  updated_at: string
}

/** 新增 / 编辑站点入参：password、upstream_api_key 留空表示保持不变。 */
export interface OpsSiteInput {
  name: string
  base_url: string
  auth_mode?: OpsAuthMode
  username?: string
  password?: string
  upstream_api_key?: string
  sync_interval_minutes?: number
  clear_upstream_api_key?: boolean
}

export interface OpsUpstreamGroup {
  id: number
  site_id: number
  remote_group_id: number
  name: string
  platform?: string
  ratio: number
  rate_multiplier: number
  peak_enabled: boolean
  peak_start?: string
  peak_end?: string
  peak_multiplier: number
  image_separate_rate: boolean
  video_separate_rate: boolean
  is_exclusive: boolean
  subscription_type?: string
  min_margin_basis_points: number | null
  synced_at: string
  /** 同步时算出的倍率变动标记（不落库）。 */
  rate_changed: boolean
  previous_rate_multiplier?: number | null
}

export interface OpsUpstreamAccount {
  id: number
  site_id: number
  remote_account_id: number
  name?: string
  platform?: string
  type?: string
  status?: string
  schedulable?: boolean
  rate_multiplier?: number | null
  group_ids: number[]
  synced_at: string
}

export interface OpsGroupProfitRow {
  group_id: number
  group_name: string
  revenue: number
  cost: number
  profit: number
  margin_basis_points: number
  requests: number
}

/** 运营看板数据（含保底收入）。Available=false 时不要当 0 展示。 */
export interface OpsProfitSummary {
  available: boolean
  range: string
  from: string
  to: string
  today_revenue: number
  today_guaranteed_revenue: number
  today_cost: number
  today_profit: number
  today_margin_basis_points: number
  seven_day_revenue: number
  seven_day_guaranteed_revenue: number
  seven_day_cost: number
  seven_day_profit: number
  seven_day_margin_basis_points: number
  sampled_days: number
  complete: boolean
  guaranteed_revenue_ratio_bp: number
  guaranteed_revenue_config_bp: number
  last_sampled_at?: string
  unavailable_reason?: string
  generated_at: string
  top_groups: OpsGroupProfitRow[]
}

/** 利润检查状态（对齐 relaydeck ProfitCheck）。 */
export type OpsProfitStatus = 'healthy' | 'risk' | 'unsupported' | 'unknown'

export interface OpsProfitCheck {
  id: number
  site_id: number
  remote_group_id: number
  group_name?: string
  channel_ref?: string
  sale_multiplier_micros: number
  cost_multiplier_micros: number
  revenue_micros: number
  cost_micros: number
  margin_basis_points: number
  status: OpsProfitStatus
  cost_source?: string
  cost_observed_at?: string
  cost_reliable: boolean
  cost_reason?: string
  created_at: string
}

export interface OpsGuardLock {
  id: number
  site_id: number
  target_ref: string
  lock_type: string
  active: boolean
  reason?: string
  metadata?: Record<string, unknown>
  activated_at: string
  cleared_at?: string
}

/** 本地 Channel 上的运营标签投影（channels.opsmgmt_* 扩展列）。 */
export interface OpsChannelLink {
  channel_id: number
  channel_name: string
  status: string
  site_id?: number | null
  remote_group_id?: number | null
  upstream_key_hash?: string
  auto_disabled_at?: string | null
  auto_disabled_reason?: string
  group_ids: number[]
  created_at: string
  updated_at: string
}

/** 本地分组的最小投影（含分组级最低利润率与生效值来源）。 */
export interface OpsLocalGroup {
  id: number
  name: string
  platform?: string
  rate_multiplier: number
  opsmgmt_min_margin_basis_points: number | null
  effective_min_margin_basis_points: number
  min_margin_source: string
}

export interface OpsMgmtSettings {
  auto_margin_protection: boolean
  auto_recovery: boolean
  minimum_margin_basis_points: number
  risk_confirmations: number
  cost_max_age_minutes: number
  guaranteed_revenue_ratio_bp: number
  sync_interval_minutes: number
  profit_window_hours: number
}

/** 全局配置局部更新：缺省字段保持不变。 */
export type OpsMgmtSettingsPatch = Partial<OpsMgmtSettings>

export type OpsTaskKind = 'sync' | 'expand' | 'evaluate'
export type OpsTaskStatus = 'pending' | 'running' | 'succeeded' | 'failed'
/** 批量任务单条状态：skipped 表示去重命中（已对接过）。 */
export type OpsTaskItemStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'skipped'

export interface OpsTaskItem {
  ref: string
  status: OpsTaskItemStatus
  message?: string
  channel_id?: number
}

export interface OpsTask {
  id: string
  kind: OpsTaskKind
  site_id: number
  status: OpsTaskStatus
  progress: number
  total: number
  message?: string
  error?: string
  result?: { created?: number; skipped?: number; failed?: number }
  items: OpsTaskItem[]
  created_at: string
  updated_at: string
  finished_at?: string
}

/** 单条对接（扩池 = 对接渠道）请求。 */
export interface OpsExpandItem {
  site_id: number
  remote_group_id: number
  upstream_api_key: string
  target_group_id: number
  channel_name?: string
  description?: string
  rate_multiplier?: number
}

export interface OpsExpandResult {
  channel_id: number
  channel_name: string
  site_id: number
  remote_group_id: string
  created: boolean
  skipped: boolean
  message?: string
}

export interface OpsSiteTestResult {
  ok: boolean
  auth_mode: OpsAuthMode
  latency_ms: number
  group_count: number
  account_count: number
  profile_email?: string
  balance?: number
  currency?: string
  error?: string
  tested_at: string
}

export interface OpsProfitCheckQuery {
  site_id?: number
  remote_group_id?: number
  channel_ref?: string
  status?: string
  page?: number
  page_size?: number
}

interface OpsItemsResponse<T> {
  items: T[]
  total: number
}

interface OpsPaginatedResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

// ==================== 运营看板 ====================

/** 运营看板：今日 / 近 7 日收入-成本-利润-利润率 + 分组盈利榜。 */
export async function getDashboard(
  range: 'today' | '7d' = 'today',
  options?: { signal?: AbortSignal }
): Promise<OpsProfitSummary> {
  const { data } = await apiClient.get<OpsProfitSummary>('/admin/opsmgmt/dashboard', {
    params: { range },
    signal: options?.signal
  })
  return data
}

// ==================== 上游站点 ====================

export async function listSites(options?: { signal?: AbortSignal }): Promise<OpsItemsResponse<OpsSite>> {
  const { data } = await apiClient.get<OpsItemsResponse<OpsSite>>('/admin/opsmgmt/sites', {
    signal: options?.signal
  })
  return data
}

export async function createSite(input: OpsSiteInput): Promise<OpsSite> {
  const { data } = await apiClient.post<OpsSite>('/admin/opsmgmt/sites', input)
  return data
}

export async function getSite(id: number): Promise<OpsSite> {
  const { data } = await apiClient.get<OpsSite>(`/admin/opsmgmt/sites/${id}`)
  return data
}

export async function updateSite(id: number, input: OpsSiteInput): Promise<OpsSite> {
  const { data } = await apiClient.put<OpsSite>(`/admin/opsmgmt/sites/${id}`, input)
  return data
}

export async function deleteSite(id: number): Promise<{ deleted: boolean }> {
  const { data } = await apiClient.delete<{ deleted: boolean }>(`/admin/opsmgmt/sites/${id}`)
  return data
}

/** 登录连通性测试（POST /auth/login），返回延迟 / 分组数 / 余额。 */
export async function testSite(id: number): Promise<OpsSiteTestResult> {
  const { data } = await apiClient.post<OpsSiteTestResult>(`/admin/opsmgmt/sites/${id}/test`)
  return data
}

/** 手动同步（异步任务）：返回 202 + task。 */
export async function syncSite(id: number): Promise<OpsTask> {
  const { data } = await apiClient.post<OpsTask>(`/admin/opsmgmt/sites/${id}/sync`)
  return data
}

/** 上游分组快照（含倍率与变动标记）。 */
export async function listSiteGroups(
  id: number,
  options?: { signal?: AbortSignal }
): Promise<OpsItemsResponse<OpsUpstreamGroup>> {
  const { data } = await apiClient.get<OpsItemsResponse<OpsUpstreamGroup>>(
    `/admin/opsmgmt/sites/${id}/groups`,
    { signal: options?.signal }
  )
  return data
}

/** 上游账号快照（password 模式为空）。 */
export async function listSiteAccounts(
  id: number,
  options?: { signal?: AbortSignal }
): Promise<OpsItemsResponse<OpsUpstreamAccount>> {
  const { data } = await apiClient.get<OpsItemsResponse<OpsUpstreamAccount>>(
    `/admin/opsmgmt/sites/${id}/accounts`,
    { signal: options?.signal }
  )
  return data
}

/** 上游分组级最低利润率（basis_points=null 表示回落全局）。 */
export async function setSiteGroupMargin(
  siteId: number,
  remoteGroupId: number,
  basisPoints: number | null
): Promise<{ site_id: number; remote_group_id: number; min_margin_basis_points: number | null }> {
  const { data } = await apiClient.put(
    `/admin/opsmgmt/sites/${siteId}/groups/${remoteGroupId}/margin`,
    { basis_points: basisPoints }
  )
  return data
}

// ==================== 扩池 = 对接渠道 ====================

/** 单条对接：同步创建（或复用）本地 Channel。 */
export async function expand(item: OpsExpandItem): Promise<OpsExpandResult> {
  const { data } = await apiClient.post<OpsExpandResult>('/admin/opsmgmt/expand', item)
  return data
}

/** 批量对接：异步任务（202 + task）。 */
export async function expandBatch(items: OpsExpandItem[]): Promise<OpsTask> {
  const { data } = await apiClient.post<OpsTask>('/admin/opsmgmt/expand', { items })
  return data
}

export async function listTasks(limit = 20, options?: { signal?: AbortSignal }): Promise<OpsItemsResponse<OpsTask>> {
  const { data } = await apiClient.get<OpsItemsResponse<OpsTask>>('/admin/opsmgmt/tasks', {
    params: { limit },
    signal: options?.signal
  })
  return data
}

export async function getTask(id: string): Promise<OpsTask> {
  const { data } = await apiClient.get<OpsTask>(`/admin/opsmgmt/tasks/${id}`)
  return data
}

// ==================== 利润管理 ====================

export async function listProfitChecks(
  query: OpsProfitCheckQuery = {},
  options?: { signal?: AbortSignal }
): Promise<OpsPaginatedResponse<OpsProfitCheck>> {
  const { data } = await apiClient.get<OpsPaginatedResponse<OpsProfitCheck>>(
    '/admin/opsmgmt/profit/checks',
    { params: query, signal: options?.signal }
  )
  return data
}

/** 手动触发利润评估（异步任务，灰度观察用）。 */
export async function evaluateProfit(siteId?: number): Promise<OpsTask> {
  const { data } = await apiClient.post<OpsTask>(
    '/admin/opsmgmt/profit/evaluate',
    siteId ? { site_id: siteId } : undefined
  )
  return data
}

export async function listGuardLocks(siteId?: number): Promise<OpsItemsResponse<OpsGuardLock>> {
  const { data } = await apiClient.get<OpsItemsResponse<OpsGuardLock>>('/admin/opsmgmt/guard-locks', {
    params: siteId ? { site_id: siteId } : undefined
  })
  return data
}

/** 被运营管理自动停用的本地 Channel（可一键恢复）。 */
export async function listAutoDisabledChannels(
  options?: { signal?: AbortSignal }
): Promise<OpsItemsResponse<OpsChannelLink>> {
  const { data } = await apiClient.get<OpsItemsResponse<OpsChannelLink>>(
    '/admin/opsmgmt/channels/auto-disabled',
    { signal: options?.signal }
  )
  return data
}

/** 恢复被自动停用的 Channel（清守护锁 + 置回 active）。 */
export async function restoreChannel(channelId: number): Promise<OpsChannelLink> {
  const { data } = await apiClient.post<OpsChannelLink>(`/admin/opsmgmt/channels/${channelId}/restore`)
  return data
}

// ==================== 配置 ====================

export async function getSettings(options?: { signal?: AbortSignal }): Promise<OpsMgmtSettings> {
  const { data } = await apiClient.get<OpsMgmtSettings>('/admin/opsmgmt/settings', {
    signal: options?.signal
  })
  return data
}

export async function updateSettings(patch: OpsMgmtSettingsPatch): Promise<OpsMgmtSettings> {
  const { data } = await apiClient.put<OpsMgmtSettings>('/admin/opsmgmt/settings', patch)
  return data
}

/** 本地分组最低利润率列表（含生效值与来源）。 */
export async function listGroupMargins(
  options?: { signal?: AbortSignal }
): Promise<OpsItemsResponse<OpsLocalGroup>> {
  const { data } = await apiClient.get<OpsItemsResponse<OpsLocalGroup>>('/admin/opsmgmt/group-margins', {
    signal: options?.signal
  })
  return data
}

/** 设置本地分组最低利润率；basisPoints=null 表示继承全局。 */
export async function setGroupMargin(groupId: number, basisPoints: number | null): Promise<OpsLocalGroup> {
  const { data } = await apiClient.put<OpsLocalGroup>(`/admin/opsmgmt/group-margins/${groupId}`, {
    basis_points: basisPoints
  })
  return data
}

export const opsMgmtAPI = {
  getDashboard,
  listSites,
  createSite,
  getSite,
  updateSite,
  deleteSite,
  testSite,
  syncSite,
  listSiteGroups,
  listSiteAccounts,
  setSiteGroupMargin,
  expand,
  expandBatch,
  listTasks,
  getTask,
  listProfitChecks,
  evaluateProfit,
  listGuardLocks,
  listAutoDisabledChannels,
  restoreChannel,
  getSettings,
  updateSettings,
  listGroupMargins,
  setGroupMargin
}

export default opsMgmtAPI
