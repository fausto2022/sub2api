import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import SourceBadge from '../SourceBadge.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

describe('opsmgmt SourceBadge', () => {
  it('renders the hexagon badge with the site name and a hover detail for ops-managed rows', () => {
    const wrapper = mount(SourceBadge, {
      props: { kind: 'opsmgmt', siteName: 'api.example.com', siteId: 7, remoteGroupId: 42, testId: 'src-ops' }
    })

    const badge = wrapper.get('[data-testid="src-ops"]')
    expect(badge.attributes('data-source')).toBe('opsmgmt')
    expect(badge.text()).toContain('admin.opsmgmt.source.opsmgmt')
    // ⬢ 用内联 SVG，不是 emoji
    expect(badge.find('svg').exists()).toBe(true)
    expect(wrapper.get('[data-testid="src-ops-site"]').text()).toBe('api.example.com')

    const title = badge.attributes('title') || ''
    expect(title).toContain('api.example.com')
    expect(title).toContain('42')
  })

  it('renders the manual label without any ops badge', () => {
    const wrapper = mount(SourceBadge, {
      props: { kind: 'manual', testId: 'src-manual' }
    })

    const badge = wrapper.get('[data-testid="src-manual"]')
    expect(badge.attributes('data-source')).toBe('manual')
    expect(badge.text()).toBe('admin.opsmgmt.source.manual')
    expect(badge.find('svg').exists()).toBe(false)
  })

  it('shows a danger badge with the auto-disabled reason', () => {
    const wrapper = mount(SourceBadge, {
      props: {
        kind: 'opsmgmt',
        siteName: 'api.example.com',
        autoDisabledReason: 'margin',
        autoDisabledAt: '2026-11-16 12:31',
        testId: 'src-disabled'
      }
    })

    const reason = wrapper.get('[data-testid="src-disabled-reason"]')
    expect(reason.text()).toBe('admin.opsmgmt.source.reason.margin')
    expect(reason.classes()).toContain('badge-danger')
    expect(wrapper.get('[data-testid="src-disabled"]').attributes('title')).toContain('2026-11-16 12:31')
  })

  it('falls back to the raw reason for unknown backend codes', () => {
    const wrapper = mount(SourceBadge, {
      props: { kind: 'opsmgmt', autoDisabledReason: 'quota', testId: 'src-unknown' }
    })

    expect(wrapper.get('[data-testid="src-unknown-reason"]').text()).toBe('quota')
  })
})
