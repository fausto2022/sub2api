import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'

import TicketThread from '../TicketThread.vue'
import type { TicketAttachment, TicketReply } from '@/api/tickets'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key
    })
  }
})

let wrapper: VueWrapper | null = null

function makeAttachment(id: number, name: string): TicketAttachment {
  return {
    id,
    url: `/api/v1/tickets/attachments/${id}`,
    original_name: name,
    content_type: 'image/png',
    byte_size: 2048,
    width: 800,
    height: 600
  }
}

function makeReply(overrides: Partial<TicketReply> = {}): TicketReply {
  return {
    id: 1,
    author_id: 2,
    author_role: 'user',
    author_name: 'Alice',
    content: 'Here is a screenshot',
    email_notified: false,
    created_at: '2026-11-15T10:00:00Z',
    attachments: [],
    ...overrides
  }
}

function mountThread(replies: TicketReply[]): VueWrapper {
  wrapper = mount(TicketThread, { props: { replies } })
  return wrapper
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

describe('TicketThread attachments', () => {
  it('renders a thumbnail for every attachment in a reply', () => {
    const vm = mountThread([
      makeReply({
        attachments: [makeAttachment(11, 'first.png'), makeAttachment(12, 'second.png')]
      })
    ])

    const thumbs = vm.findAll('[data-testid="ticket-attachment-thumb"]')
    expect(thumbs).toHaveLength(2)

    const images = vm.findAll('[data-testid="ticket-reply-attachments"] img')
    expect(images.map((image) => image.attributes('src'))).toEqual([
      '/api/v1/tickets/attachments/11',
      '/api/v1/tickets/attachments/12'
    ])
    expect(images.map((image) => image.attributes('alt'))).toEqual(['first.png', 'second.png'])
  })

  it('does not render an attachment list when a reply has none', () => {
    const vm = mountThread([makeReply()])

    expect(vm.find('[data-testid="ticket-reply"]').exists()).toBe(true)
    expect(vm.find('[data-testid="ticket-reply-attachments"]').exists()).toBe(false)
  })

  it('opens the original image in a lightbox and closes it again', async () => {
    const vm = mountThread([makeReply({ attachments: [makeAttachment(7, 'detail.png')] })])

    expect(vm.find('[data-testid="ticket-attachment-lightbox"]').exists()).toBe(false)

    await vm.get('[data-testid="ticket-attachment-thumb"]').trigger('click')

    const lightbox = vm.get('[data-testid="ticket-attachment-lightbox"]')
    expect(lightbox.attributes('role')).toBe('dialog')
    expect(vm.get('[data-testid="ticket-attachment-lightbox-image"]').attributes('src')).toBe(
      '/api/v1/tickets/attachments/7'
    )
    expect(vm.get('[data-testid="ticket-attachment-open-original"]').attributes('href')).toBe(
      '/api/v1/tickets/attachments/7'
    )

    await vm.get('[data-testid="ticket-attachment-lightbox-close"]').trigger('click')
    expect(vm.find('[data-testid="ticket-attachment-lightbox"]').exists()).toBe(false)
  })

  it('closes the lightbox on Escape', async () => {
    const vm = mountThread([makeReply({ attachments: [makeAttachment(8, 'esc.png')] })])

    await vm.get('[data-testid="ticket-attachment-thumb"]').trigger('click')
    expect(vm.find('[data-testid="ticket-attachment-lightbox"]').exists()).toBe(true)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await vm.vm.$nextTick()

    expect(vm.find('[data-testid="ticket-attachment-lightbox"]').exists()).toBe(false)
  })

  it('renders reply content as text only (no v-html)', () => {
    const payload = '<img src=x onerror="window.__xss=1"> hello'
    const vm = mountThread([makeReply({ content: payload })])

    const reply = vm.get('[data-testid="ticket-reply"]')
    expect(reply.text()).toContain(payload)
    // 内容中的标签不会被解析成元素：回复气泡里只应有附件缩略图（本用例为 0 张）。
    expect(reply.findAll('img')).toHaveLength(0)
    expect((window as unknown as { __xss?: number }).__xss).toBeUndefined()
  })

  it('shows the empty state when there are no replies', () => {
    const vm = mountThread([])

    expect(vm.text()).toContain('tickets.detail.emptyReplies')
    expect(vm.findAll('[data-testid="ticket-reply"]')).toHaveLength(0)
  })
})
