import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import ChannelsView from '../ChannelsView.vue'

/**
 * V-01 补测：ChannelsView 的「来源」列接线。
 *
 * 该 spec 只在视图层断言既有实现（不改实现代码）：
 *   1. columns 里确实注册了 source 列（label 为「来源」）；
 *   2. 运营管理来源的渠道行渲染出 SourceBadge（data-source=opsmgmt + 文本「运营管理」+ 站点名）；
 *   3. 被自动停用的渠道额外显示停用原因（margin → 「利润率不足」）；
 *   4. 来源筛选（全部 / 手工创建 / 运营管理）走 displayedChannels 过滤当前页行。
 */

const { listChannels, getGroups, getWebSearchEmulationConfig, showSuccess, showError } = vi.hoisted(() => ({
  listChannels: vi.fn(),
  getGroups: vi.fn(),
  getWebSearchEmulationConfig: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

const { listSites, listAutoDisabledChannels, listTasks, listGuardLocks } = vi.hoisted(() => ({
  listSites: vi.fn(),
  listAutoDisabledChannels: vi.fn(),
  listTasks: vi.fn(),
  listGuardLocks: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channels: { list: listChannels, syncPricingModels: vi.fn() },
    groups: { getAll: getGroups },
    settings: { getWebSearchEmulationConfig }
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

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))

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

/** DataTable 桩：渲染真实 columns（便于断言「来源」表头）与 cell-source 插槽（走真实 SourceBadge）。 */
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array, default: () => [] },
    data: { type: Array, default: () => [] },
    loading: Boolean
  },
  template: `
    <div data-testid="channels-table">
      <div data-testid="channels-table-header">
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
        :data-channel-name="row.name"
      >
        <slot name="cell-source" :row="row" :value="row.source" />
      </div>
    </div>
  `
})

function mountView() {
  return mount(ChannelsView, {
    global: {
      stubs: {
        AppLayout: defineComponent({ template: '<main><slot /></main>' }),
        TablePageLayout: defineComponent({
          template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
        }),
        DataTable: DataTableStub,
        BaseDialog: true,
        ConfirmDialog: true,
        EmptyState: true,
        Select: defineComponent({
          props: ['modelValue', 'options'],
          template: '<select><option v-for="option in (options || [])" :key="option.value">{{ option.label }}</option></select>'
        }),
        Pagination: true,
        Icon: true,
        PlatformIcon: true,
        Toggle: true,
        PricingEntryCard: true
      }
    }
  })
}

function channelRow(overrides: Record<string, unknown> = {}) {
  return {
    id: 22,
    name: 'local-001',
    description: '',
    status: 'active',
    billing_model_source: 'channel_mapped',
    restrict_models: false,
    group_ids: [7],
    model_pricing: [],
    model_mapping: {},
    apply_pricing_to_account_stats: false,
    account_stats_pricing_rules: [],
    created_at: '2026-11-10T00:00:00Z',
    updated_at: '2026-11-10T00:00:00Z',
    ...overrides
  }
}

const opsChannel = channelRow({ id: 11, name: 'ups-001', group_ids: [100] })
const manualChannel = channelRow({ id: 22, name: 'local-001', group_ids: [7] })

describe('admin ChannelsView source column (V-01)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    listChannels.mockResolvedValue({ items: [opsChannel, manualChannel], total: 2 })
    getGroups.mockResolvedValue([])
    getWebSearchEmulationConfig.mockResolvedValue({ enabled: false })
    listSites.mockResolvedValue({ items: [{ id: 1, name: 'api.example.com' }], total: 1 })
    listAutoDisabledChannels.mockResolvedValue({
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
    listTasks.mockResolvedValue({ items: [], total: 0 })
    listGuardLocks.mockResolvedValue({ items: [], total: 0 })
  })

  it('registers a source column labelled 来源', async () => {
    const wrapper = mountView()
    await flushPromises()

    const header = wrapper.get('[data-testid="column-source"]')
    expect(header.text()).toBe('来源')
    wrapper.unmount()
  })

  it('renders the ops-managed SourceBadge with the site name for an ops channel', async () => {
    const wrapper = mountView()
    await flushPromises()

    const badge = wrapper.get('[data-testid="channel-source-11"]')
    expect(badge.attributes('data-source')).toBe('opsmgmt')
    expect(badge.text()).toContain('运营管理')
    expect(badge.text()).toContain('api.example.com')
    wrapper.unmount()
  })

  it('shows the auto-disabled reason on the ops channel row only', async () => {
    const wrapper = mountView()
    await flushPromises()

    const reason = wrapper.get('[data-testid="channel-source-11-reason"]')
    expect(reason.text()).toBe('利润率不足')
    expect(reason.classes()).toContain('badge-danger')
    expect(wrapper.find('[data-testid="channel-source-22-reason"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('marks a channel that carries no ops tag as manually created', async () => {
    const wrapper = mountView()
    await flushPromises()

    const badge = wrapper.get('[data-testid="channel-source-22"]')
    expect(badge.attributes('data-source')).toBe('manual')
    expect(badge.text()).toBe('手工创建')
    wrapper.unmount()
  })

  it('filters the current page rows through the source filter (全部 / 手工创建 / 运营管理)', async () => {
    const wrapper = mountView()
    await flushPromises()

    const filter = wrapper.get('[data-testid="channels-source-filter"]')
    expect(wrapper.find('[data-testid="row-11"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-22"]').exists()).toBe(true)

    await filter.setValue('opsmgmt')
    expect(wrapper.find('[data-testid="row-11"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-22"]').exists()).toBe(false)

    await filter.setValue('manual')
    expect(wrapper.find('[data-testid="row-11"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-22"]').exists()).toBe(true)

    await filter.setValue('')
    expect(wrapper.find('[data-testid="row-11"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="row-22"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
