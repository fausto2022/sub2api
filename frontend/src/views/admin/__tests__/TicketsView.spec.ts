import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'

import type { TicketReply } from '@/api/tickets'
import type { AdminTicketBrief } from '@/api/admin/tickets'
import TicketsView from '../TicketsView.vue'

const { list, get, reply, update, stats, showError, showSuccess, showWarning } = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
  reply: vi.fn(),
  update: vi.fn(),
  stats: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('@/api/admin/tickets', () => ({
  adminTicketsAPI: { list, get, reply, update, stats }
}))

// 部分 mock：新增的附件相关导出（常量/上传函数）仍需从真实模块透传。
vi.mock('@/api/tickets', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/tickets')>()
  return {
    ...actual,
    ticketsAPI: {}
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showWarning })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (!params) return key
        return `${key}|${Object.values(params).map((value) => String(value)).join(',')}`
      }
    })
  }
})

const SelectStub = {
  props: ['modelValue', 'options', 'placeholder', 'disabled'],
  emits: ['update:modelValue', 'change'],
  template: `
    <select :value="modelValue" @change="$emit('update:modelValue', $event.target.value); $emit('change')">
      <option v-for="option in options" :key="String(option.value)" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `
}

const DataTableStub = {
  props: ['columns', 'data', 'loading'],
  template: `
    <div data-testid="data-table">
      <div v-if="data.length === 0" data-testid="table-empty"><slot name="empty" /></div>
      <div v-for="row in data" :key="row.id" :data-testid="'row-' + row.id">
        <slot name="cell-subject" :row="row" />
        <slot name="cell-user" :row="row" />
        <slot name="cell-status" :row="row" />
        <slot name="cell-priority" :row="row" />
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
}

const PaginationStub = {
  props: ['page', 'total', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: '<button data-testid="page-next" @click="$emit(\'update:page\', 2)">next</button>'
}

const BaseDialogStub = {
  props: ['show', 'title', 'width'],
  emits: ['close'],
  template:
    '<div v-if="show" data-testid="ticket-detail-dialog"><span data-testid="ticket-detail-dialog-title">{{ title }}</span><slot /><slot name="footer" /></div>'
}

const globalStubs = {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: {
    template:
      '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
  },
  DataTable: DataTableStub,
  Pagination: PaginationStub,
  BaseDialog: BaseDialogStub,
  Select: SelectStub,
  Icon: true
}

function makeAdminTicket(overrides: Partial<AdminTicketBrief> = {}): AdminTicketBrief {
  return {
    id: 3,
    subject: 'Billing mismatch',
    category: 'billing',
    priority: 'high',
    status: 'open',
    reply_count: 1,
    last_reply_at: null,
    last_reply_by: '',
    created_at: '2026-11-12T08:00:00Z',
    updated_at: '2026-11-13T08:00:00Z',
    user_id: 42,
    user_email: 'user@example.com',
    user_name: 'Alice',
    ...overrides
  }
}

function makeReply(overrides: Partial<TicketReply> = {}): TicketReply {
  return {
    id: 11,
    author_id: 42,
    author_role: 'admin',
    author_name: 'admin',
    content: 'we are on it',
    email_notified: false,
    created_at: '2026-11-13T09:00:00Z',
    ...overrides
  }
}

function mountView(): VueWrapper {
  return mount(TicketsView, { global: { stubs: globalStubs } })
}

async function openDetail(wrapper: VueWrapper) {
  await wrapper.get('[data-testid="row-3"] button').trigger('click')
  await flushPromises()
}

describe('admin TicketsView (ticket workbench)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    list.mockResolvedValue({
      items: [makeAdminTicket()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    stats.mockResolvedValue({ open: 4, pending: 2, resolved: 9, closed: 3, total: 18 })
    get.mockResolvedValue({
      ticket: makeAdminTicket(),
      replies: [makeReply({ id: 1, author_role: 'user', author_name: 'Alice', content: 'first' })],
      user: { id: 42, email: 'user@example.com', username: 'Alice' }
    })
    reply.mockResolvedValue({
      ticket: makeAdminTicket({ status: 'pending', reply_count: 2 }),
      reply: makeReply(),
      email_sent: true,
      email_error: ''
    })
    update.mockResolvedValue({ ticket: makeAdminTicket({ status: 'resolved', priority: 'urgent' }) })
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loads the list and the status counters on mount', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(list).toHaveBeenCalledWith(
      expect.objectContaining({ page: 1, status: '', priority: '', search: '' })
    )
    expect(stats).toHaveBeenCalledTimes(1)

    const chips = wrapper.get('[data-testid="ticket-stats"]').text()
    expect(chips).toContain('admin.tickets.stats.open')
    expect(chips).toContain('4')
    expect(chips).toContain('18')

    const row = wrapper.get('[data-testid="row-3"]')
    expect(row.text()).toContain('Billing mismatch')
    expect(row.text()).toContain('Alice')
    expect(row.text()).toContain('user@example.com')
    expect(row.text()).toContain('tickets.statusLabels.open')
    expect(row.text()).toContain('tickets.priorityLabels.high')
    wrapper.unmount()
  })

  it('filters by status, priority and keyword', async () => {
    const wrapper = mountView()
    await flushPromises()

    const selects = wrapper.findAll('select')
    list.mockClear()
    await selects[0].setValue('pending')
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ status: 'pending', page: 1 }))

    list.mockClear()
    await selects[1].setValue('urgent')
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ priority: 'urgent', page: 1 }))

    list.mockClear()
    await wrapper.get('input[type="text"]').setValue('alice@example.com')
    await new Promise((resolve) => setTimeout(resolve, 350))
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(
      expect.objectContaining({ search: 'alice@example.com', page: 1 })
    )
    wrapper.unmount()
  })

  it('opens the detail dialog with the submitter and conversation', async () => {
    const wrapper = mountView()
    await flushPromises()

    await openDetail(wrapper)

    expect(get).toHaveBeenCalledWith(3, expect.objectContaining({ signal: expect.anything() }))
    const dialog = wrapper.get('[data-testid="ticket-detail-dialog"]')
    expect(dialog.text()).toContain('admin.tickets.detailTitle')
    expect(dialog.text()).toContain('Billing mismatch')
    expect(dialog.text()).toContain('Alice')
    expect(dialog.text()).toContain('first')
    wrapper.unmount()
  })

  it('passes notify_email through on admin replies and can turn it off', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openDetail(wrapper)

    // 默认勾选邮件通知
    await wrapper.get('textarea').setValue('hello there')
    await wrapper.get('[data-testid="ticket-composer"]').trigger('submit')
    await flushPromises()

    expect(reply).toHaveBeenLastCalledWith(3, { content: 'hello there', notify_email: true })
    expect(showSuccess).toHaveBeenCalledWith('admin.tickets.replyEmailSuccess')

    // 关闭通知后再回复
    reply.mockClear()
    showSuccess.mockClear()
    reply.mockResolvedValue({
      ticket: makeAdminTicket({ status: 'pending', reply_count: 3 }),
      reply: makeReply({ id: 12 }),
      email_sent: false
    })
    await wrapper.get('[data-testid="admin-ticket-notify-toggle"]').trigger('click')
    await wrapper.get('textarea').setValue('no email this time')
    await wrapper.get('[data-testid="ticket-composer"]').trigger('submit')
    await flushPromises()

    expect(reply).toHaveBeenLastCalledWith(3, { content: 'no email this time', notify_email: false })
    expect(showSuccess).toHaveBeenLastCalledWith('admin.tickets.replySuccess')
    wrapper.unmount()
  })

  it('warns when the reply is stored but the email notification fails', async () => {
    reply.mockResolvedValue({
      ticket: makeAdminTicket({ status: 'pending', reply_count: 2 }),
      reply: makeReply(),
      email_sent: false,
      email_error: 'smtp down'
    })
    const wrapper = mountView()
    await flushPromises()
    await openDetail(wrapper)

    await wrapper.get('textarea').setValue('reply')
    await wrapper.get('[data-testid="ticket-composer"]').trigger('submit')
    await flushPromises()

    expect(showWarning).toHaveBeenCalledTimes(1)
    expect(String(showWarning.mock.calls[0][0])).toContain('admin.tickets.replyEmailFailed')
    expect(String(showWarning.mock.calls[0][0])).toContain('smtp down')
    wrapper.unmount()
  })

  it('saves status and priority changes and refreshes the counters', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openDetail(wrapper)

    const selects = wrapper.get('[data-testid="ticket-detail-dialog"]').findAll('select')
    const statusSelect = selects[selects.length - 2]
    const prioritySelect = selects[selects.length - 1]
    await statusSelect.setValue('resolved')
    await prioritySelect.setValue('urgent')

    stats.mockClear()
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.tickets.saveChanges')!
      .trigger('click')
    await flushPromises()

    expect(update).toHaveBeenCalledWith(3, { status: 'resolved', priority: 'urgent' })
    expect(showSuccess).toHaveBeenCalledWith('admin.tickets.updateSuccess')
    expect(stats).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('shows the empty state and reports list failures', async () => {
    list.mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="table-empty"]').exists()).toBe(true)

    list.mockRejectedValueOnce({ status: 500, message: 'list failed' })
    await wrapper.findAll('button').find((button) => button.attributes('aria-label') === 'common.refresh')!.trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('list failed')
    wrapper.unmount()
  })
})