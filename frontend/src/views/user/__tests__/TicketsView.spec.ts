import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'

import type { TicketBrief, TicketReply } from '@/api/tickets'
import TicketsView from '../TicketsView.vue'
import TicketDetailView from '../TicketDetailView.vue'

const { list, create, get, addReply, close, showError, showSuccess, routeParams } = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  get: vi.fn(),
  addReply: vi.fn(),
  close: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  routeParams: { id: '7' }
}))

// 部分 mock：新增的附件相关导出（常量/上传函数）仍需从真实模块透传。
vi.mock('@/api/tickets', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/tickets')>()
  return {
    ...actual,
    ticketsAPI: { list, create, get, addReply, close }
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showWarning: vi.fn() })
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => ({ params: routeParams })
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) =>
        key.replace(/\{(\w+)\}/g, (_match, token: string) => String(params?.[token] ?? `{${token}}`))
    })
  }
})

const SelectStub = {
  props: ['modelValue', 'options', 'placeholder', 'disabled'],
  emits: ['update:modelValue', 'change'],
  template: `
    <select
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value); $emit('change')"
    >
      <option v-for="option in options" :key="String(option.value)" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `
}

const BaseDialogStub = {
  props: ['show', 'title', 'width'],
  emits: ['close'],
  template: '<div v-if="show" data-testid="base-dialog"><slot /><slot name="footer" /></div>'
}

const PaginationStub = {
  props: ['page', 'total', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: `
    <div data-testid="pagination">
      <button data-testid="page-next" @click="$emit('update:page', 2)">next</button>
      <button data-testid="page-size-50" @click="$emit('update:pageSize', 50)">size</button>
    </div>
  `
}

const globalStubs = {
  AppLayout: { template: '<div><slot /></div>' },
  RouterLink: { props: ['to'], template: '<a><slot /></a>' },
  Select: SelectStub,
  BaseDialog: BaseDialogStub,
  Pagination: PaginationStub,
  Icon: true
}

function makeTicket(overrides: Partial<TicketBrief> = {}): TicketBrief {
  return {
    id: 12,
    subject: 'Cannot call gpt-4o',
    category: 'api',
    priority: 'high',
    status: 'open',
    reply_count: 2,
    last_reply_at: '2026-11-14T10:00:00Z',
    last_reply_by: 'admin',
    created_at: '2026-11-13T09:00:00Z',
    updated_at: '2026-11-14T10:00:00Z',
    ...overrides
  }
}

function makeReply(overrides: Partial<TicketReply> = {}): TicketReply {
  return {
    id: 1,
    author_id: 3,
    author_role: 'user',
    author_name: 'me',
    content: 'first line\nsecond line',
    email_notified: false,
    created_at: '2026-11-13T09:00:00Z',
    ...overrides
  }
}

function mountUserList() {
  return mount(TicketsView, { global: { stubs: globalStubs } })
}

function mountDetail(params: Record<string, string> = { id: '7' }) {
  routeParams.id = params.id
  return mount(TicketDetailView, {
    global: { stubs: { ...globalStubs, ConfirmDialog: ConfirmDialogStub } }
  })
}

const ConfirmDialogStub = {
  props: ['show', 'title', 'message', 'confirmText', 'danger'],
  emits: ['confirm', 'cancel'],
  template: '<div v-if="show" data-testid="confirm-dialog"><button data-testid="confirm-ok" @click="$emit(\'confirm\')">ok</button></div>'
}

describe('TicketsView (user ticket center)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    list.mockResolvedValue({
      items: [makeTicket()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    create.mockResolvedValue({ ticket: makeTicket({ id: 99, subject: 'New issue' }), reply: makeReply({ id: 5 }) })
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the ticket list with status, category, priority and reply metadata', async () => {
    const wrapper = mountUserList()
    await flushPromises()

    expect(list).toHaveBeenCalledTimes(1)
    expect(list).toHaveBeenCalledWith(expect.objectContaining({ page: 1, status: '' }))

    const row = wrapper.get('[data-testid="ticket-row-12"]')
    expect(row.text()).toContain('Cannot call gpt-4o')
    expect(row.text()).toContain('tickets.statusLabels.open')
    expect(row.text()).toContain('tickets.categoryLabels.api')
    expect(row.text()).toContain('tickets.priorityLabels.high')
    expect(row.text()).toContain('tickets.replyCount')
    expect(row.text()).toContain('#12')
    wrapper.unmount()
  })

  it('shows the empty state when the user has no tickets', async () => {
    list.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    const wrapper = mountUserList()
    await flushPromises()

    expect(wrapper.text()).toContain('tickets.empty.title')
    expect(wrapper.text()).toContain('tickets.empty.description')
    expect(wrapper.find('[data-testid="tickets-list"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pagination"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('reports load failures with an inline error and a toast', async () => {
    list.mockRejectedValue({ status: 500, message: 'boom' })
    const wrapper = mountUserList()
    await flushPromises()

    expect(wrapper.find('[data-testid="tickets-error"]').exists()).toBe(true)
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('refetches from page 1 when the status filter changes', async () => {
    const wrapper = mountUserList()
    await flushPromises()

    list.mockClear()
    await wrapper.get('select').setValue('open')
    await flushPromises()

    expect(list).toHaveBeenCalledWith(expect.objectContaining({ page: 1, status: 'open' }))
    wrapper.unmount()
  })

  it('requests the next page and a larger page size through the pagination contract', async () => {
    list.mockResolvedValue({ items: [makeTicket()], total: 45, page: 1, page_size: 20, pages: 3 })
    const wrapper = mountUserList()
    await flushPromises()

    list.mockClear()
    await wrapper.get('[data-testid="page-next"]').trigger('click')
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }))

    list.mockClear()
    await wrapper.get('[data-testid="page-size-50"]').trigger('click')
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, page_size: 50 }))
    wrapper.unmount()
  })

  it('validates the create form, then submits trimmed values and reloads the list', async () => {
    const wrapper = mountUserList()
    await flushPromises()

    const newTicketButton = wrapper.findAll('button').find((button) => button.text().includes('tickets.newTicket'))
    expect(newTicketButton).toBeTruthy()
    await newTicketButton!.trigger('click')

    const form = wrapper.get('#create-ticket-form')
    await form.trigger('submit')
    await flushPromises()
    expect(create).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('tickets.create.subjectRequired')
    expect(wrapper.text()).toContain('tickets.create.contentRequired')

    await wrapper.get('#ticket-subject').setValue('  New issue  ')
    await wrapper.get('#ticket-content').setValue('  details here  ')
    list.mockClear()
    await form.trigger('submit')
    await flushPromises()

    expect(create).toHaveBeenCalledTimes(1)
    expect(create).toHaveBeenCalledWith({
      subject: 'New issue',
      category: 'general',
      priority: 'normal',
      content: 'details here'
    })
    expect(showSuccess).toHaveBeenCalledWith('tickets.create.success')
    expect(list).toHaveBeenCalledWith(expect.objectContaining({ page: 1 }))
    wrapper.unmount()
  })

  it('keeps the dialog open and toasts when the create request fails', async () => {
    create.mockRejectedValue({ status: 400, message: 'invalid' })
    const wrapper = mountUserList()
    await flushPromises()

    await wrapper.findAll('button').find((button) => button.text().includes('tickets.newTicket'))!.trigger('click')
    await wrapper.get('#ticket-subject').setValue('Subject')
    await wrapper.get('#ticket-content').setValue('Body')
    await wrapper.get('#create-ticket-form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('invalid')
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="base-dialog"]').exists()).toBe(true)
    wrapper.unmount()
  })
})

describe('TicketDetailView (user ticket detail)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({
      ticket: makeTicket({ id: 7, status: 'pending', priority: 'normal', reply_count: 2 }),
      replies: [makeReply({ id: 1, author_role: 'user' }), makeReply({ id: 2, author_role: 'admin', author_name: 'Support', email_notified: true })]
    })
    addReply.mockResolvedValue({
      ticket: makeTicket({ id: 7, status: 'open', reply_count: 3 }),
      reply: makeReply({ id: 3, content: 'thanks' })
    })
    close.mockResolvedValue({ ticket: makeTicket({ id: 7, status: 'closed' }) })
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loads the conversation and renders replies as plain text', async () => {
    const wrapper = mountDetail()
    await flushPromises()

    expect(get).toHaveBeenCalledWith(7, expect.objectContaining({ signal: expect.anything() }))
    const replies = wrapper.findAll('[data-testid="ticket-reply"]')
    expect(replies).toHaveLength(2)
    expect(replies[0].text()).toContain('tickets.detail.authorUser')
    expect(replies[1].text()).toContain('tickets.detail.authorSupport')
    expect(replies[1].text()).toContain('tickets.detail.emailNotified')

    const content = replies[0].get('.whitespace-pre-wrap')
    expect(content.text()).toBe('first line\nsecond line')
    expect(content.html()).not.toContain('<br')
    wrapper.unmount()
  })

  it('appends a reply, resets the composer and toasts on success', async () => {
    const wrapper = mountDetail()
    await flushPromises()

    const textarea = wrapper.get('textarea')
    await textarea.setValue('  thanks  ')
    await wrapper.get('[data-testid="ticket-composer"]').trigger('submit')
    await flushPromises()

    // 第三个参数是工单附件 ID 列表（本次未上传图片，故为空数组）。
    expect(addReply).toHaveBeenCalledWith(7, 'thanks', [])
    expect(showSuccess).toHaveBeenCalledWith('tickets.detail.replySuccess')
    expect(wrapper.findAll('[data-testid="ticket-reply"]')).toHaveLength(3)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('')
    wrapper.unmount()
  })

  it('rejects an empty reply without calling the API', async () => {
    const wrapper = mountDetail()
    await flushPromises()

    await wrapper.get('[data-testid="ticket-composer"]').trigger('submit')
    await flushPromises()

    expect(addReply).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('tickets.detail.replyRequired')
    wrapper.unmount()
  })

  it('closes the ticket after confirmation and reflects the closed state', async () => {
    const wrapper = mountDetail()
    await flushPromises()

    await wrapper.findAll('button').find((button) => button.text() === 'tickets.detail.close')!.trigger('click')
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(close).toHaveBeenCalledWith(7)
    expect(showSuccess).toHaveBeenCalledWith('tickets.detail.closeSuccess')
    expect(wrapper.find('[data-testid="ticket-closed-notice"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="ticket-composer"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('surfaces an inline error and toast when the ticket cannot be loaded', async () => {
    get.mockRejectedValue({ status: 404, message: 'ticket-not-found' })
    const wrapper = mountDetail()
    await flushPromises()

    expect(wrapper.find('[data-testid="ticket-detail-error"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('ticket-not-found')
    expect(showError).toHaveBeenCalledWith('ticket-not-found')
    wrapper.unmount()
  })
})