import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import RateChangeCell from '../RateChangeCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

describe('opsmgmt RateChangeCell', () => {
  it('marks a rate increase in red with an up arrow, a text label and the grey previous value', () => {
    const wrapper = mount(RateChangeCell, {
      props: { current: 1.2, previous: 0.95, flag: 'changed', testId: 'rate-up' }
    })

    expect(wrapper.get('[data-testid="rate-current"]').text()).toBe('1.20x')
    expect(wrapper.get('[data-testid="rate-current"]').classes()).toContain('text-red-700')
    expect(wrapper.get('[data-testid="rate-direction-label"]').text()).toBe('admin.opsmgmt.rate.up')
    const previous = wrapper.get('[data-testid="rate-previous"]')
    expect(previous.text()).toBe('(0.95x)')
    // 原值必须是灰字（次文字令牌），不参与涨跌着色
    expect(previous.classes()).toContain('text-accent-600')
    expect(wrapper.attributes('data-direction')).toBe('up')
  })

  it('marks a rate decrease in green with a down arrow', () => {
    const wrapper = mount(RateChangeCell, {
      props: { current: 0.8, previous: 1.1, flag: 'changed', testId: 'rate-down' }
    })

    expect(wrapper.get('[data-testid="rate-current"]').text()).toBe('0.80x')
    expect(wrapper.get('[data-testid="rate-current"]').classes()).toContain('text-emerald-700')
    expect(wrapper.get('[data-testid="rate-direction-label"]').text()).toBe('admin.opsmgmt.rate.down')
    expect(wrapper.attributes('data-direction')).toBe('down')
  })

  it('hides the change markers for an unchanged rate', () => {
    const wrapper = mount(RateChangeCell, {
      props: { current: 0.85, previous: 0.85, testId: 'rate-flat' }
    })

    expect(wrapper.get('[data-testid="rate-current"]').text()).toBe('0.85x')
    expect(wrapper.find('[data-testid="rate-direction-label"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="rate-previous"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="rate-current"]').classes()).toContain('text-accent-900')
  })

  it('respects the explicit unchanged flag even when the values differ', () => {
    const wrapper = mount(RateChangeCell, {
      props: { current: 1.2, previous: 0.9, flag: 'unchanged', testId: 'rate-forced' }
    })

    expect(wrapper.find('[data-testid="rate-previous"]').exists()).toBe(false)
  })
})
