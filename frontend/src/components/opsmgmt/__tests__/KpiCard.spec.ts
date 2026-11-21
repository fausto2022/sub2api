import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import KpiCard from '../KpiCard.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

describe('opsmgmt KpiCard', () => {
  it('renders the label, the big tabular number and the hint under it', () => {
    const wrapper = mount(KpiCard, {
      props: {
        label: 'admin.opsmgmt.dashboard.kpi.revenue',
        value: '$1,284.50',
        hint: 'admin.opsmgmt.dashboard.kpi.guaranteed',
        testId: 'kpi-test'
      }
    })

    expect(wrapper.get('[data-testid="kpi-test-label"]').text()).toBe('admin.opsmgmt.dashboard.kpi.revenue')
    const value = wrapper.get('[data-testid="kpi-test-value"]')
    expect(value.text()).toBe('$1,284.50')
    // 大号数字必须带 tabular-nums（MASTER §3.10）
    expect(value.classes()).toContain('tabular-nums')
    expect(value.classes().some((name) => name.startsWith('text-2xl'))).toBe(true)
    // 保底收入小字位于数值下方
    expect(wrapper.get('[data-testid="kpi-test-hint"]').text()).toBe('admin.opsmgmt.dashboard.kpi.guaranteed')
  })

  it('shows an arrow plus a text label so direction is never colour-only', () => {
    const wrapper = mount(KpiCard, {
      props: {
        label: 'rev',
        value: '$10',
        changeLabel: 'compare',
        changeValue: '+12.3%',
        trend: 'up',
        tone: 'good',
        testId: 'kpi-up'
      }
    })

    const change = wrapper.get('[data-testid="kpi-up-change"]')
    expect(change.attributes('data-trend')).toBe('up')
    expect(change.attributes('data-tone')).toBe('good')
    // 文字标签：i18n key（测试里 t 恒等返回 key）
    expect(change.text()).toContain('admin.opsmgmt.kpi.trendUp')
    expect(change.text()).toContain('+12.3%')
    expect(change.classes()).toContain('text-emerald-700')
  })

  it('renders the down direction with the danger tone for costs going up', () => {
    const wrapper = mount(KpiCard, {
      props: {
        label: 'cost',
        value: '$892.10',
        changeValue: '+8.1%',
        trend: 'up',
        tone: 'bad',
        testId: 'kpi-cost'
      }
    })

    const change = wrapper.get('[data-testid="kpi-cost-change"]')
    expect(change.attributes('data-tone')).toBe('bad')
    expect(change.classes()).toContain('text-red-700')
    expect(change.text()).toContain('admin.opsmgmt.kpi.trendUp')
  })

  it('omits the change row when no comparison value is supplied', () => {
    const wrapper = mount(KpiCard, {
      props: { label: 'rev', value: '—', testId: 'kpi-empty' }
    })

    expect(wrapper.find('[data-testid="kpi-empty-change"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="kpi-empty-value"]').text()).toBe('—')
  })
})
