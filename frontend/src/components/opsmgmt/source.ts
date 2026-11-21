/**
 * 「来源」标识（运营管理 / 手工创建）的数据装配与筛选。
 *
 * 后端只有三条运营标签的读通道：
 *   1. GET /admin/opsmgmt/channels/auto-disabled —— 被自动停用的本地 Channel
 *      （带 site_id / remote_group_id / group_ids / 原因 / 时间，最完整）；
 *   2. GET /admin/opsmgmt/tasks —— 批量对接任务的 items（带 channel_id + ref），
 *      覆盖「已对接但未被停用」的 Channel（进程内 24h 有效）；
 *   3. GET /admin/opsmgmt/guard-locks —— active 守护锁，target_ref = "channel:<id>"。
 * 三者按 channel_id 合并，再用站点列表把 site_id 翻成站点名（悬浮提示用）。
 *
 * 账号没有 opsmgmt 列：本仓库的控制对象是本地 Channel（design §2.6），
 * 因此账号来源按「所属分组是否被运营管理渠道覆盖」推断（后端返回值中
 * channels/channel-groups 的 group_ids 即覆盖关系）。
 */

import type { OpsChannelLink, OpsGuardLock, OpsSite, OpsTask } from '@/api/admin/opsmgmt'

export type OpsSourceKind = 'opsmgmt' | 'manual'

export interface OpsChannelSource {
  kind: OpsSourceKind
  siteId?: number | null
  siteName?: string
  remoteGroupId?: number | null
  autoDisabledReason?: string
  autoDisabledAt?: string | null
}

export interface OpsSourceIndex {
  /** channel_id → 运营管理来源信息。 */
  channels: Map<number, OpsChannelSource>
  /** 被运营管理渠道覆盖的本地分组 id（账号来源据此推断）。 */
  groupIds: Set<number>
  /** site_id → 站点名。 */
  siteNames: Map<number, string>
}

export function emptySourceIndex(): OpsSourceIndex {
  return { channels: new Map(), groupIds: new Set(), siteNames: new Map() }
}

/** target_ref 形如 `channel:42`（service.OpsChannelRef）。 */
export function parseChannelRef(targetRef: string | null | undefined): number | null {
  if (!targetRef) return null
  const matched = /^channel:(\d+)$/.exec(targetRef.trim())
  if (!matched) return null
  const id = Number(matched[1])
  return Number.isFinite(id) && id > 0 ? id : null
}

interface BuildSourceIndexInput {
  sites?: Pick<OpsSite, 'id' | 'name'>[]
  autoDisabled?: Pick<OpsChannelLink, 'channel_id' | 'site_id' | 'remote_group_id' | 'group_ids' | 'auto_disabled_at' | 'auto_disabled_reason'>[]
  tasks?: Pick<OpsTask, 'site_id' | 'items'>[]
  guardLocks?: Pick<OpsGuardLock, 'site_id' | 'target_ref' | 'active'>[]
}

function mergeSource(
  index: OpsSourceIndex,
  channelId: number,
  source: OpsChannelSource
): void {
  const existing = index.channels.get(channelId)
  if (!existing) {
    index.channels.set(channelId, { ...source })
    return
  }
  // 自动停用信息优先（含原因/时间），其余字段互补。
  index.channels.set(channelId, {
    ...existing,
    ...source,
    kind: 'opsmgmt',
    siteId: source.siteId ?? existing.siteId ?? null,
    remoteGroupId: source.remoteGroupId ?? existing.remoteGroupId ?? null,
    autoDisabledReason: source.autoDisabledReason ?? existing.autoDisabledReason,
    autoDisabledAt: source.autoDisabledAt ?? existing.autoDisabledAt
  })
}

/** 把三条读通道装配成一个来源索引。任一入参缺失都安全。 */
export function buildOpsSourceIndex(input: BuildSourceIndexInput = {}): OpsSourceIndex {
  const index = emptySourceIndex()

  for (const site of input.sites ?? []) {
    if (!site || typeof site.id !== 'number') continue
    index.siteNames.set(site.id, site.name)
  }

  for (const link of input.autoDisabled ?? []) {
    if (!link || typeof link.channel_id !== 'number') continue
    mergeSource(index, link.channel_id, {
      kind: 'opsmgmt',
      siteId: link.site_id ?? null,
      remoteGroupId: link.remote_group_id ?? null,
      autoDisabledReason: link.auto_disabled_reason,
      autoDisabledAt: link.auto_disabled_at ?? null
    })
    for (const groupId of link.group_ids ?? []) {
      if (typeof groupId === 'number') index.groupIds.add(groupId)
    }
  }

  for (const task of input.tasks ?? []) {
    for (const item of task?.items ?? []) {
      if (!item || typeof item.channel_id !== 'number' || item.channel_id <= 0) continue
      mergeSource(index, item.channel_id, {
        kind: 'opsmgmt',
        siteId: task.site_id ?? null,
        remoteGroupId: null
      })
    }
  }

  for (const lock of input.guardLocks ?? []) {
    if (!lock?.active) continue
    const channelId = parseChannelRef(lock.target_ref)
    if (channelId === null) continue
    mergeSource(index, channelId, {
      kind: 'opsmgmt',
      siteId: lock.site_id ?? null,
      remoteGroupId: null
    })
  }

  // 站点名回填（index 内 siteId 已知但名字未知时）。
  for (const [channelId, source] of index.channels.entries()) {
    if (source.siteName) continue
    const name = source.siteId != null ? index.siteNames.get(source.siteId) : undefined
    if (name) index.channels.set(channelId, { ...source, siteName: name })
  }

  return index
}

const MANUAL_SOURCE: OpsChannelSource = { kind: 'manual' }

/** 渠道来源：命中运营管理标签 → opsmgmt，否则手工创建。 */
export function resolveChannelSource(
  channel: { id?: number; group_ids?: number[] } | null | undefined,
  index: OpsSourceIndex
): OpsChannelSource {
  if (!channel) return MANUAL_SOURCE
  if (typeof channel.id === 'number') {
    const direct = index.channels.get(channel.id)
    if (direct) {
      return {
        ...direct,
        kind: 'opsmgmt',
        siteName: direct.siteName ?? (direct.siteId != null ? index.siteNames.get(direct.siteId) : undefined)
      }
    }
  }
  // 兜底：渠道绑定的分组被运营管理覆盖时同样标记（手工创建的分组通常不重叠）。
  for (const groupId of channel.group_ids ?? []) {
    if (index.groupIds.has(groupId)) return { kind: 'opsmgmt' }
  }
  return MANUAL_SOURCE
}

/** 账号来源：所属分组被运营管理渠道覆盖即视为运营管理来源。 */
export function resolveAccountSource(
  account: { group_ids?: number[] } | null | undefined,
  index: OpsSourceIndex
): OpsChannelSource {
  for (const groupId of account?.group_ids ?? []) {
    if (index.groupIds.has(groupId)) return { kind: 'opsmgmt' }
  }
  return MANUAL_SOURCE
}

export type OpsSourceFilter = '' | 'opsmgmt' | 'manual'

/** 按来源筛选（空字符串 = 全部）。 */
export function filterBySource<T>(
  items: T[],
  filter: OpsSourceFilter,
  resolve: (item: T) => OpsChannelSource
): T[] {
  if (!filter) return items
  return items.filter((item) => resolve(item).kind === filter)
}
