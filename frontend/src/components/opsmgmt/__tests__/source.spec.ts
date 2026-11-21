import { describe, expect, it } from 'vitest'

import {
  buildOpsSourceIndex,
  emptySourceIndex,
  filterBySource,
  parseChannelRef,
  resolveAccountSource,
  resolveChannelSource
} from '../source'
import { changePercent, changeTrend, formatBasisPoints, formatChangePercent, formatUsd, isLossRow } from '../format'

describe('opsmgmt source index', () => {
  it('parses guard-lock target refs', () => {
    expect(parseChannelRef('channel:42')).toBe(42)
    expect(parseChannelRef(' channel:7 ')).toBe(7)
    expect(parseChannelRef('channel:')).toBeNull()
    expect(parseChannelRef('account:3')).toBeNull()
    expect(parseChannelRef('')).toBeNull()
  })

  it('merges auto-disabled channels, expand tasks, guard locks and site names', () => {
    const index = buildOpsSourceIndex({
      sites: [{ id: 1, name: 'api.example.com' }],
      autoDisabled: [
        {
          channel_id: 11,
          site_id: 1,
          remote_group_id: 5,
          group_ids: [100, 101],
          auto_disabled_at: '2026-11-16T04:31:00Z',
          auto_disabled_reason: 'margin'
        }
      ],
      tasks: [{ site_id: 2, items: [{ ref: '9', status: 'succeeded', channel_id: 12 }] }],
      guardLocks: [{ site_id: 3, target_ref: 'channel:13', active: true }]
    })

    expect(index.channels.get(11)?.siteName).toBe('api.example.com')
    expect(index.channels.get(11)?.autoDisabledReason).toBe('margin')
    expect(index.channels.has(12)).toBe(true)
    expect(index.channels.get(12)?.siteId).toBe(2)
    expect(index.channels.has(13)).toBe(true)
    expect([...index.groupIds]).toEqual([100, 101])
  })

  it('ignores inactive guard locks and malformed rows', () => {
    const index = buildOpsSourceIndex({
      guardLocks: [{ site_id: 3, target_ref: 'channel:13', active: false }]
    })

    expect(index.channels.size).toBe(0)
  })

  it('resolves a channel to ops-managed when its id is tagged, and to manual otherwise', () => {
    const index = buildOpsSourceIndex({
      sites: [{ id: 1, name: 'api.example.com' }],
      autoDisabled: [
        { channel_id: 11, site_id: 1, remote_group_id: 5, group_ids: [100], auto_disabled_at: null, auto_disabled_reason: '' }
      ]
    })

    expect(resolveChannelSource({ id: 11, group_ids: [] }, index).kind).toBe('opsmgmt')
    expect(resolveChannelSource({ id: 11, group_ids: [] }, index).siteName).toBe('api.example.com')
    expect(resolveChannelSource({ id: 99, group_ids: [999] }, index).kind).toBe('manual')
    // 分组覆盖的兜底推断
    expect(resolveChannelSource({ id: 98, group_ids: [100] }, index).kind).toBe('opsmgmt')
  })

  it('resolves an account by the groups covered by ops channels', () => {
    const index = buildOpsSourceIndex({
      autoDisabled: [
        { channel_id: 11, site_id: 1, remote_group_id: 5, group_ids: [100], auto_disabled_at: null, auto_disabled_reason: '' }
      ]
    })

    expect(resolveAccountSource({ group_ids: [100] }, index).kind).toBe('opsmgmt')
    expect(resolveAccountSource({ group_ids: [7] }, index).kind).toBe('manual')
    expect(resolveAccountSource({}, emptySourceIndex()).kind).toBe('manual')
  })

  it('filters rows by source while keeping order', () => {
    const index = buildOpsSourceIndex({
      autoDisabled: [
        { channel_id: 11, site_id: 1, remote_group_id: 5, group_ids: [], auto_disabled_at: null, auto_disabled_reason: '' }
      ]
    })
    const rows = [{ id: 11 }, { id: 1 }, { id: 2 }]
    const resolve = (row: { id: number }) => resolveChannelSource(row, index)

    expect(filterBySource(rows, '', resolve).map((row) => row.id)).toEqual([11, 1, 2])
    expect(filterBySource(rows, 'opsmgmt', resolve).map((row) => row.id)).toEqual([11])
    expect(filterBySource(rows, 'manual', resolve).map((row) => row.id)).toEqual([1, 2])
  })
})

describe('opsmgmt formatting helpers', () => {
  it('formats USD with grouping and basis points as percentages', () => {
    expect(formatUsd(1284.5)).toBe('$1,284.50')
    expect(formatUsd(-12.4)).toBe('$-12.40')
    expect(formatUsd(null)).toBe('—')
    expect(formatBasisPoints(3055)).toBe('30.55%')
    expect(formatBasisPoints(-420)).toBe('-4.20%')
    expect(formatBasisPoints(null)).toBe('—')
  })

  it('never fabricates a change percentage without a baseline', () => {
    expect(changePercent(100, 50)).toBe(100)
    expect(changePercent(100, 0)).toBeNull()
    expect(changePercent(100, null)).toBeNull()
    expect(formatChangePercent(12.34)).toBe('+12.3%')
    expect(formatChangePercent(-4.2)).toBe('-4.2%')
    expect(formatChangePercent(null)).toBe('—')
    expect(changeTrend(1.2)).toBe('up')
    expect(changeTrend(-1.2)).toBe('down')
    expect(changeTrend(0)).toBe('flat')
    expect(changeTrend(null)).toBe('flat')
  })

  it('detects loss rows from profit or margin', () => {
    expect(isLossRow({ profit: -1 })).toBe(true)
    expect(isLossRow({ margin_basis_points: -4 })).toBe(true)
    expect(isLossRow({ profit: 1, margin_basis_points: 4 })).toBe(false)
  })
})
