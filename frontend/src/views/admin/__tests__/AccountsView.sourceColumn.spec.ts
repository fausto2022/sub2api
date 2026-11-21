import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import AccountsView from '../AccountsView.vue'

/**
 * V-01 补测：AccountsView 的「来源」列接线与来源筛选。
 *
 * 只在视图层断言既有实现（不改实现代码）：
 *   1. columns 里确实注册了 source 列（label 为「来源」）；
 *   2. 账号所属分组被运营管理渠道覆盖时渲染 SourceBadge（data-source=opsmgmt + 文本「运营管理」）；
 *   3. 来源筛选（全部 / 手工创建 / 运营管理）通过 displayAccounts computed 过滤当前页行。
 *
 * 账号没有 opsmgmt 列：来源由「所属分组是否被运营管理渠道覆盖」推断（控制对象是本地 Channel），
 * 因此这里用 auto-disabled 链接携带的 group_ids 驱动索引。
 */

const {
  listAccounts,
  listWithEtag,
  getById,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  refreshCredentials,
  showError,
  showWarning
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getById: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  refreshCredentials: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn()
}))

const { listSites, listAutoDisabledChannels, listTasks, listGuardLocks } = vi.hoisted(() => ({
  listSites: vi.fn(),
  listAutoDisabledChannels: vi.fn(),
  listTasks: vi.fn(),
  listGuardLocks: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getById,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn(),
      refreshCredentials
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

// 部分 mock：保留真实实现，只替换视图装配来源索引时用到的四个读通道。
vi.mock('@/api/admin/opsmgmt', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/admin/opsmgmt')>()
  return {
    ...actual,
    opsMgmtAPI: {
      ...actual.opsMgmtAPI,
      listSites,
      listAutoDisabledChannels,
      listTasks,
      listGuardLocks
    }
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showWarning, showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

// 部分 mock：只为运营管理文案提供真实中文，其余键照旧返回 key（与既有 spec 行为一致）。
const MESSAGES: Record<string, string> = {
  'admin.opsmgmt.source.column': '来源',
  'admin.opsmgmt.source.opsmgmt': '运营管理',
  'admin.opsmgmt.source.manual': '手工创建',
  'admin.opsmgmt.source.filterAll': '全部来源',
  'admin.opsmgmt.source.filterLabel': '按来源筛选',
  'admin.opsmgmt.source.reason.margin': '利润率不足'
}

function translate(key: string, params?: Record<string, unknown>): string {
  const template = MESSAGES[key]
  if (!template) return key
  if (!params) return template
  return template.replace(/\{(\w+)\}/g, (_match, name: string) => String(params[name] ?? ''))
}

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: translate })
  }
})

/** DataTable 桩：渲染真实 columns（断言「来源」表头）与 cell-source 插槽（走真实 SourceBadge）。 */
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array, default: () => [] },
    data: { type: Array, default: () => [] },
    loading: Boolean
  },
  template: `
    <div data-testid="accounts-table">
      <div data-testid="accounts-table-header">
        <span
          v-for="column in columns"
          :key="column.key"
          :data-testid="'column-' + column.key"
        >{{ column.label }}</span>
      </div>
      <div
        v-for="row in data"
        :key="row.id"
        :data-testid="'row-' + row.id"
        :data-account-name="row.name"
      >
        <slot name="cell-source" :row="row" />
      </div>
    </div>
  `
})

function mountView() {
  return mount(AccountsView, {
    attachTo: document.body,
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: DataTableStub,
        AccountTableActions: { template: '<div><slot name="after" /></div>' },
        AccountTableFilters: true,
        AccountBulkActionsBar: true,
        Pagination: true,
        ConfirmDialog: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        UpstreamBillingRateCell: true,
        HelpTooltip: true,
        Icon: true,
        Teleport: true
      }
    }
  })
}

function accountRow(overrides: Record<string, unknown> = {}) {
  return {
    id: 43,
    name: 'local-001',
    platform: 'openai',
    type: 'oauth',
    status: 'active',
    schedulable: true,
    concurrency: 2,
    priority: 1,
    group_ids: [7],
    extra: {},
    credentials: {},
    ...overrides
  }
}

const opsAccount = accountRow({ id: 42, name: 'ups-001', group_ids: [100] })
const manualAccount = accountRow({ id: 43, name: 'local-001', group_ids: [7] })

describe('admin AccountsView source column (V-01)', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({
      items: [opsAccount, manualAccount],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: 'etag', data: null })
    getById.mockReset().mockResolvedValue({ ...manualAccount, groups: [] })
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: false })
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([{ id: 7, name: 'pro', platform: 'openai' }])
    refreshCredentials.mockReset()
    showError.mockReset()
    showWarning.mockReset()

    listSites.mockReset().mockResolvedValue({ items: [{ id: 1, name: 'api.example.com' }], total: 1 })
    listAutoDisabledChannels.mockReset().mockResolvedValue({
      items: [
        {
          channel_id: 11,
          channel_name: 'ups-001',
          status: 'disabled',
          site_id: 1,
          remote_group_id: 5,
          auto_disabled_at: '2026-11-16T04:31:00Z',
          auto_disabled_reason: 'margin',
          group_ids: [100],
          created_at: '2026-11-10T00:00:00Z',
          updated_at: '2026-11-16T04:31:00Z'
        }
      ],
      total: 1
    })
    listTasks.mockReset().mockResolvedValue({ items: [], total: 0 })
    listGuardLocks.mockReset().mockResolvedValue({ items: [], total: 0 })
  })

  it('registers a source column labelled 来源', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="column-source"]').text()).toBe('来源')
    wrapper.unmount()
  })

  it('renders the ops-managed SourceBadge for an account covered by an ops channel group', async () => {
    const wrapper = mountView()
    await flushPromises()

    const badge = wrapper.get('[data-testid="account-source-42"]')
    expect(badge.attributes('data-source')).toBe('opsmgmt')
    expect(badge.text()).toContain('运营管理')
    // 账号来源是按「所属分组被运营渠道覆盖」推断的，不绑定某个具体站点，
    // 因此不渲染站点名小字（渠道行才有）。这里锁住该语义，避免日后误加。
    expect(wrapper.find('[data-testid="account-source-42-site"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps accounts outside the ops groups as manually created', async () => {
    const wrapper = mountView()
    await flushPromises()

    const badge = wrapper.get('[data-testid="account-source-43"]')
    expect(badge.attributes('data-source')).toBe('manual')
    expect(badge.text()).toBe('手工创建')
    wrapper.unmount()
  })

  it('filters the current page rows through the source filter (全部 / 手工创建 / 运营管理)', async () => {
    const wrapper = mountView()
    await flushPromises()

    const filter = wrapper.get('[data-testid="accounts-source-filter"]')
    expect(wrapper.find('[data-testid="row-42"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-43"]').exists()).toBe(true)

    await filter.setValue('opsmgmt')
    expect(wrapper.find('[data-testid="row-42"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-43"]').exists()).toBe(false)

    await filter.setValue('manual')
    expect(wrapper.find('[data-testid="row-42"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-43"]').exists()).toBe(true)

    await filter.setValue('')
    expect(wrapper.find('[data-testid="row-42"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-43"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('does not change the server-side total when the source filter narrows the page', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="accounts-source-filter"]').setValue('manual')

    // 来源筛选是客户端当前页过滤：只影响渲染行，不再触发列表请求。
    expect(wrapper.find('[data-testid="row-43"]').exists()).toBe(true)
    expect(listAccounts).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
