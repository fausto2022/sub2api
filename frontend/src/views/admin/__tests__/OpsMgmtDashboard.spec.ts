import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { OpsProfitSummary } from '@/api/admin/opsmgmt'
import OpsMgmtDashboardView from '../opsmgmt/DashboardView.vue'

const { getDashboard, getUserUsageTrend } = vi.hoisted(() => ({
  getDashboard: vi.fn(),
  getUserUsageTrend: vi.fn()
}))

vi.mock('@/api/admin/opsmgmt', () => ({
  opsMgmtAPI: {
    getDashboard
  }
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getUserUsageTrend
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key}|${JSON.stringify(params)}` : key)
    })
  }
})

const stubs = {
  AppLayout: { template: '<div><slot /></div>' }
}

function makeSummary(overrides: Partial<OpsProfitSummary> = {}): OpsProfitSummary {
  return {
    available: true,
    range: 'today',
    from: '2026-11-16T00:00:00Z',
    to: '2026-11-17T00:00:00Z',
    today_revenue: 1284.5,
    today_guaranteed_revenue: 1156.05,
    today_cost: 892.1,
    today_profit: 392.4,
    today_margin_basis_points: 3055,
    seven_day_revenue: 8400,
    seven_day_guaranteed_revenue: 7560,
    seven_day_cost: 5880,
    seven_day_profit: 2520,
    seven_day_margin_basis_points: 3000,
    sampled_days: 7,
    complete: true,
    guaranteed_revenue_ratio_bp: 9000,
    guaranteed_revenue_config_bp: 9000,
    generated_at: '2026-11-16T12:00:00Z',
    top_groups: [
      { group_id: 1, group_name: 'claude-pro', revenue: 820, cost: 557.6, profit: 262.4, margin_basis_points: 3200, requests: 1200 },
      { group_id: 2, group_name: 'gpt-plus', revenue: 464, cost: 380.48, profit: 83.52, margin_basis_points: 1800, requests: 640 },
      { group_id: 3, group_name: 'gemini-max', revenue: 120, cost: 124.8, profit: -4.8, margin_basis_points: -400, requests: 80 }
    ],
    ...overrides
  }
}

function mountView() {
  return mount(OpsMgmtDashboardView, { global: { stubs } })
}

describe('admin OpsMgmt DashboardView', () => {
  beforeEach(() => {
    getDashboard.mockReset()
    getUserUsageTrend.mockReset()
    getDashboard.mockResolvedValue(makeSummary())
    getUserUsageTrend.mockResolvedValue({ trend: [], start_date: '', end_date: '', granularity: 'day' })
  })

  it('renders the four KPI cards with formatted values and the guaranteed revenue hint', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="opsmgmt-kpi-revenue-value"]').text()).toBe('$1,284.50')
    expect(wrapper.get('[data-testid="opsmgmt-kpi-cost-value"]').text()).toBe('$892.10')
    expect(wrapper.get('[data-testid="opsmgmt-kpi-profit-value"]').text()).toBe('$392.40')
    expect(wrapper.get('[data-testid="opsmgmt-kpi-margin-value"]').text()).toBe('30.55%')

    // 今日收入卡片下方小字：保底 $1156.05（90%）
    const hint = wrapper.get('[data-testid="opsmgmt-kpi-revenue-hint"]').text()
    expect(hint).toContain('admin.opsmgmt.dashboard.kpi.guaranteed')
    expect(hint).toContain('$1,156.05')
    expect(hint).toContain('90')

    // 环比箭头 + 文字标签（不只靠颜色）
    const change = wrapper.get('[data-testid="opsmgmt-kpi-revenue-change"]')
    expect(change.attributes('data-trend')).toBe('up')
    expect(change.text()).toContain('admin.opsmgmt.kpi.trendUp')
    wrapper.unmount()
  })

  it('renders the 7-day trend chart with revenue / cost lines and a profit area', async () => {
    getUserUsageTrend.mockResolvedValue({
      trend: [
        { date: '2026-11-10', user_id: 1, email: 'a@b.c', username: 'a', requests: 1, tokens: 10, cost: 100, actual_cost: 140 },
        { date: '2026-11-11', user_id: 1, email: 'a@b.c', username: 'a', requests: 1, tokens: 10, cost: 120, actual_cost: 160 }
      ],
      start_date: '2026-11-10',
      end_date: '2026-11-16',
      granularity: 'day'
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="trend-svg"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="trend-revenue-line"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="trend-cost-line"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="trend-profit-area"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="trend-legend-revenue"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="opsmgmt-trend-derived"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('falls back to the aggregate points when the daily series is unavailable', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="trend-svg"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="opsmgmt-trend-derived"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('marks loss rows with a tinted background and a loss badge', async () => {
    const wrapper = mountView()
    await flushPromises()

    const lossRow = wrapper.get('[data-testid="opsmgmt-group-row-3"]')
    expect(lossRow.attributes('data-loss')).toBe('true')
    expect(lossRow.classes()).toContain('bg-red-50')
    expect(lossRow.find('[data-testid="opsmgmt-group-loss-badge"]').exists()).toBe(true)

    const profitRow = wrapper.get('[data-testid="opsmgmt-group-row-1"]')
    expect(profitRow.attributes('data-loss')).toBe('false')
    expect(profitRow.find('[data-testid="opsmgmt-group-loss-badge"]').exists()).toBe(false)

    // 盈利榜按利润降序
    const rows = wrapper.findAll('[data-testid^="opsmgmt-group-row-"]')
    expect(rows.map((row) => row.attributes('data-testid'))).toEqual([
      'opsmgmt-group-row-1',
      'opsmgmt-group-row-2',
      'opsmgmt-group-row-3'
    ])
    wrapper.unmount()
  })

  it('shows an unavailable banner and dashes instead of zeros when the cost data is missing', async () => {
    getDashboard.mockResolvedValue(
      makeSummary({
        available: false,
        today_revenue: 0,
        today_cost: 0,
        today_profit: 0,
        today_margin_basis_points: 0,
        unavailable_reason: 'usage logs unavailable',
        top_groups: []
      })
    )

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="opsmgmt-dashboard-unavailable"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="opsmgmt-dashboard-unavailable"]').text()).toContain(
      'admin.opsmgmt.dashboard.unavailable.title'
    )
    // 关键：不把「查不到」渲染成 0
    expect(wrapper.get('[data-testid="opsmgmt-kpi-revenue-value"]').text()).toBe('—')
    expect(wrapper.get('[data-testid="opsmgmt-kpi-cost-value"]').text()).toBe('—')
    expect(wrapper.get('[data-testid="opsmgmt-kpi-margin-value"]').text()).toBe('—')
    expect(wrapper.find('[data-testid="opsmgmt-kpi-revenue-hint"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="trend-unavailable"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('surfaces a load error without crashing the page shell', async () => {
    getDashboard.mockRejectedValue({ status: 500, message: 'dashboard down' })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="opsmgmt-dashboard-error"]').text()).toContain('dashboard down')
    expect(wrapper.find('[data-testid="opsmgmt-dashboard"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
