import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'

import type {
  ModelPricingCurrencyItem,
  ModelPricingCurrencyState,
  ModelPricingItem,
  ModelPricingMeta,
  PeakPricingSettings
} from '@/api/admin/modelPricing'
import ModelPricingView from '../ModelPricingView.vue'

const {
  list,
  updateOverride,
  deleteOverride,
  createModel,
  deleteModel,
  getPeakSettings,
  updatePeakSettings,
  getCurrency,
  updateCurrency,
  upsertCurrencyModel,
  deleteCurrencyModel,
  showError,
  showSuccess,
  showWarning
} = vi.hoisted(() => ({
  list: vi.fn(),
  updateOverride: vi.fn(),
  deleteOverride: vi.fn(),
  createModel: vi.fn(),
  deleteModel: vi.fn(),
  getPeakSettings: vi.fn(),
  updatePeakSettings: vi.fn(),
  getCurrency: vi.fn(),
  updateCurrency: vi.fn(),
  upsertCurrencyModel: vi.fn(),
  deleteCurrencyModel: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('@/api/admin/modelPricing', () => ({
  modelPricingAPI: {
    list,
    updateOverride,
    deleteOverride,
    createModel,
    deleteModel,
    getPeakSettings,
    updatePeakSettings,
    getCurrency,
    updateCurrency,
    upsertCurrencyModel,
    deleteCurrencyModel
  },
  USD_PER_MTOK_FIELDS: [
    'input_usd_per_mtok',
    'output_usd_per_mtok',
    'cache_read_usd_per_mtok',
    'cache_write_usd_per_mtok',
    'image_input_usd_per_mtok',
    'priority_input_usd_per_mtok',
    'priority_output_usd_per_mtok'
  ],
  USD_PER_MTOK_BASIC_FIELDS: [
    'input_usd_per_mtok',
    'output_usd_per_mtok',
    'cache_read_usd_per_mtok',
    'cache_write_usd_per_mtok'
  ],
  USD_PER_MTOK_ADVANCED_FIELDS: [
    'image_input_usd_per_mtok',
    'priority_input_usd_per_mtok',
    'priority_output_usd_per_mtok'
  ],
  PEAK_PRICE_FIELDS: [
    'input_usd_per_mtok',
    'output_usd_per_mtok',
    'cache_read_usd_per_mtok',
    'cache_write_usd_per_mtok'
  ],
  CNY_PER_MTOK_FIELDS: [
    'input_cny_per_mtok',
    'output_cny_per_mtok',
    'cache_read_cny_per_mtok',
    'cache_write_cny_per_mtok',
    'image_input_cny_per_mtok'
  ],
  MODEL_PRICE_USD_LIMIT: 1e4,
  MODEL_PRICE_CNY_LIMIT: 1e6,
  DEFAULT_CNY_PER_USD: 7.14,
  MODEL_ALREADY_EXISTS: 'MODEL_ALREADY_EXISTS'
}))

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
      },
      te: (key: string) => key.startsWith('admin.modelPricing.sources.') || key.startsWith('admin.modelPricing.create.')
    })
  }
})

vi.mock('@/constants/platforms', () => ({
  CONCRETE_PLATFORM_OPTIONS: [
    { value: 'openai', label: 'OpenAI' },
    { value: 'deepseek', label: 'DeepSeek' },
    { value: 'moonshot', label: 'Moonshot' }
  ],
  GROUP_PLATFORM_OPTIONS: [
    { value: 'openai', label: 'OpenAI' },
    { value: 'deepseek', label: 'DeepSeek' },
    { value: 'moonshot', label: 'Moonshot' }
  ]
}))

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
      <div v-if="!data || data.length === 0" data-testid="table-empty"><slot name="empty" /></div>
      <div v-for="row in data" :key="row.model" :data-testid="'row-' + row.model">
        <slot name="cell-model" :row="row" />
        <slot name="cell-input" :row="row" />
        <slot name="cell-output" :row="row" />
        <slot name="cell-cacheRead" :row="row" />
        <slot name="cell-cacheWrite" :row="row" />
        <slot name="cell-peak" :row="row" />
        <slot name="cell-cny" :row="row" />
        <slot name="cell-source" :row="row" />
        <slot name="cell-status" :row="row" />
        <slot name="cell-imageInput" :row="row" />
        <slot name="cell-effectiveInput" :row="row" />
        <slot name="cell-effectiveOutput" :row="row" />
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
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

const BaseDialogStub = {
  props: ['show', 'title', 'width'],
  emits: ['close'],
  template:
    '<div v-if="show" data-testid="model-price-edit-dialog"><span data-testid="model-price-edit-dialog-title">{{ title }}</span><slot /><slot name="footer" /></div>'
}

const ConfirmDialogStub = {
  props: ['show', 'title', 'message', 'confirmText', 'cancelText', 'danger'],
  emits: ['confirm', 'cancel'],
  template: `
    <div v-if="show" data-testid="confirm-dialog">
      <span data-testid="confirm-dialog-title">{{ title }}</span>
      <span data-testid="confirm-dialog-message">{{ message }}</span>
      <button data-testid="confirm-ok" @click="$emit('confirm')">ok</button>
      <button data-testid="confirm-cancel" @click="$emit('cancel')">cancel</button>
    </div>
  `
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
  ConfirmDialog: ConfirmDialogStub,
  Select: SelectStub,
  Skeleton: true,
  Icon: true
}

function makeItem(overrides: Partial<ModelPricingItem> = {}): ModelPricingItem {
  return {
    model: 'deepseek-v4-flash',
    provider: 'deepseek',
    platform: 'deepseek',
    mode: 'chat',
    input_usd_per_mtok: 0.3,
    output_usd_per_mtok: 1.2,
    cache_read_usd_per_mtok: 0.006,
    cache_write_usd_per_mtok: 0,
    image_input_usd_per_mtok: 0,
    priority_input_usd_per_mtok: 0,
    priority_output_usd_per_mtok: 0,
    source: 'catalog',
    overridden: false,
    custom: true,
    cny: { input: 2, output: 8, cache_read: 0.04, cache_write: 0, image_input: 0 },
    effective_updated_at: '2026-11-15T12:00:00Z',
    peak_enabled: false,
    peak: {
      input_usd_per_mtok: 0,
      output_usd_per_mtok: 0,
      cache_read_usd_per_mtok: 0,
      cache_write_usd_per_mtok: 0
    },
    off_peak: {
      input_usd_per_mtok: 0,
      output_usd_per_mtok: 0,
      cache_read_usd_per_mtok: 0,
      cache_write_usd_per_mtok: 0
    },
    ...overrides
  }
}

function makeMeta(overrides: Partial<ModelPricingMeta> = {}): ModelPricingMeta {
  return {
    cny_mode: 'convert',
    cny_per_usd: 7.14,
    override_file: '/data/pricing-overrides.json',
    cny_file: '/data/cny-overrides.json',
    catalog_file: '/data/pricing-overrides.json',
    catalog_updated_at: '2026-11-15T10:00:00Z',
    model_count: 269,
    editable: true,
    cny_editable: true,
    catalog_authoritative: true,
    ...overrides
  }
}

const PEAK_SETTINGS: PeakPricingSettings = {
  timezone: 'Asia/Shanghai',
  weekday_only: true,
  weekend_off_peak: true,
  periods: [{ start: '09:00', end: '12:00' }]
}

function makeCnyItem(overrides: Partial<ModelPricingCurrencyItem> = {}): ModelPricingCurrencyItem {
  return {
    model: 'deepseek-v4-flash',
    input_cny_per_mtok: 2,
    output_cny_per_mtok: 8,
    cache_read_cny_per_mtok: 0.04,
    cache_write_cny_per_mtok: 0,
    image_input_cny_per_mtok: 0,
    effective_input_usd_per_mtok: 0.28,
    effective_output_usd_per_mtok: 1.12,
    effective_cache_read_usd_per_mtok: 0.006,
    effective_cache_write_usd_per_mtok: 0,
    effective_image_input_usd_per_mtok: 0,
    overridden: false,
    source: 'cny',
    ...overrides
  }
}

function makeCurrencyState(
  overrides: Partial<ModelPricingCurrencyState> = {}
): ModelPricingCurrencyState {
  return {
    mode: 'convert',
    cny_per_usd: 7.14,
    file: '/data/cny-overrides.json',
    models: [makeCnyItem()],
    seed_models: [
      makeCnyItem({
        model: 'kimi-k2',
        input_cny_per_mtok: 4,
        output_cny_per_mtok: 16,
        cache_read_cny_per_mtok: 1
      })
    ],
    ...overrides
  }
}

function mountView(): VueWrapper {
  return mount(ModelPricingView, { global: { stubs: globalStubs } })
}

async function openEditDialog(wrapper: VueWrapper, model = 'deepseek-v4-flash') {
  await wrapper.get(`[data-testid="price-edit-${model}"]`).trigger('click')
  await flushPromises()
}

function inputValue(wrapper: VueWrapper, field: string): string {
  return (wrapper.get(`[data-testid="price-input-${field}"]`).element as HTMLInputElement).value
}

async function clickRestore(wrapper: VueWrapper) {
  const button = wrapper.get('[data-testid="restore-cny"]')
  expect(button.attributes('disabled')).toBeUndefined()
  await button.trigger('click')
  await flushPromises()
}

describe('admin ModelPricingView (single page, catalog price, CNY restore, peak)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    list.mockResolvedValue({
      items: [makeItem()],
      total: 1,
      page: 1,
      page_size: 20,
      meta: makeMeta({ peak_settings: PEAK_SETTINGS })
    })
    updateOverride.mockResolvedValue(makeItem({ overridden: true }))
    deleteOverride.mockResolvedValue(makeItem())
    createModel.mockResolvedValue(makeItem({ model: 'glm-5', provider: 'zhipu' }))
    deleteModel.mockResolvedValue(undefined)
    getPeakSettings.mockResolvedValue(PEAK_SETTINGS)
    updatePeakSettings.mockResolvedValue(PEAK_SETTINGS)
    getCurrency.mockResolvedValue(makeCurrencyState())
    updateCurrency.mockResolvedValue(makeCurrencyState({ mode: 'direct' }))
    upsertCurrencyModel.mockResolvedValue(makeCurrencyState())
    deleteCurrencyModel.mockResolvedValue(makeCurrencyState({ models: [] }))
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  // ===== 单页 / 无 Tab =====
  it('renders a single page without tabs and lists catalog prices', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[role="tablist"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="tab-installed"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="tab-currency"]').exists()).toBe(false)

    expect(list).toHaveBeenCalledWith(expect.objectContaining({ page: 1, page_size: 20 }))

    const row = wrapper.get('[data-testid="row-deepseek-v4-flash"]')
    expect(row.text()).toContain('deepseek-v4-flash')
    expect(row.find('[data-testid="price-usd-input"]').text()).toBe('0.3000')
    expect(row.find('[data-testid="price-usd-output"]').text()).toBe('1.2000')
    expect(row.find('[data-testid="price-source-deepseek-v4-flash"]').text()).toBe(
      'admin.modelPricing.sources.catalog'
    )
    expect(wrapper.get('[data-testid="pricing-model-count"]').text()).toContain('269')
    wrapper.unmount()
  })

  it('sends search, custom and pagination state to the API', async () => {
    const wrapper = mountView()
    await flushPromises()

    list.mockClear()
    await wrapper.get('[data-testid="pricing-search"]').setValue('deepseek')
    await new Promise((resolve) => setTimeout(resolve, 350))
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'deepseek', page: 1 }))

    list.mockClear()
    await wrapper.get('[data-testid="pricing-custom-only"]').setValue(true)
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith(expect.objectContaining({ custom: true, page: 1 }))

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

  it('filters rows client-side by platform', async () => {
    list.mockResolvedValue({
      items: [makeItem(), makeItem({ model: 'gpt-5.4', source: 'litellm', platform: 'openai', provider: 'openai' })],
      total: 2,
      page: 1,
      page_size: 20,
      meta: makeMeta()
    })
    const wrapper = mountView()
    await flushPromises()

    const selects = wrapper.findAll('select')
    await selects[0].setValue('openai')
    await flushPromises()

    expect(wrapper.find('[data-testid="row-deepseek-v4-flash"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-gpt-5.4"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('filters rows client-side by source', async () => {
    list.mockResolvedValue({
      items: [makeItem(), makeItem({ model: 'gpt-5.4', source: 'litellm', platform: 'openai', provider: 'openai' })],
      total: 2,
      page: 1,
      page_size: 20,
      meta: makeMeta()
    })
    const wrapper = mountView()
    await flushPromises()

    const selects = wrapper.findAll('select')
    await selects[1].setValue('litellm')
    await flushPromises()

    expect(wrapper.find('[data-testid="row-deepseek-v4-flash"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-gpt-5.4"]').exists()).toBe(true)
    wrapper.unmount()
  })

  // ===== 一键还原人民币价 =====
  it('restores the CNY price by multiplying the rate (0.3 → 2.142)', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('0.3')
    // 只读灰字预览保留
    expect(wrapper.get('[data-testid="price-cny-preview-input_usd_per_mtok"]').text()).toContain('¥2.14')

    await clickRestore(wrapper)

    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('2.142')
    expect(inputValue(wrapper, 'output_usd_per_mtok')).toBe('8.568')
    // 0.006 × 7.14 = 0.04284
    expect(inputValue(wrapper, 'cache_read_usd_per_mtok')).toBe('0.04284')
    // 按钮文案带动态汇率
    expect(wrapper.get('[data-testid="restore-cny"]').text()).toContain('7.14')
    wrapper.unmount()
  })

  // 回归防护：连续点击还原按钮不得把价格反复 ×汇率（曾经的 bug）
  it('applies the CNY restore only once per edit and disables the button afterwards', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('0.3')

    // 第一次：0.3 → 2.142
    await clickRestore(wrapper)
    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('2.142')

    // 再次点击（按钮应已 disabled）：价格必须保持 2.142，绝不能再乘一次变成 15.29…
    const button = wrapper.get('[data-testid="restore-cny"]')
    expect(button.attributes('disabled')).toBeDefined()
    await button.trigger('click')
    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('2.142')
    expect(inputValue(wrapper, 'output_usd_per_mtok')).toBe('8.568')

    // 撤销后可再次还原（快照清空 → 锁解除）
    await wrapper.get('[data-testid="restore-undo"]').trigger('click')
    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('0.3')
    expect(wrapper.get('[data-testid="restore-cny"]').attributes('disabled')).toBeUndefined()

    await clickRestore(wrapper)
    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('2.142')

    wrapper.unmount()
  })

  it('keeps zero values at 0 and never blanks them', async () => {    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    expect(inputValue(wrapper, 'cache_write_usd_per_mtok')).toBe('0')
    await clickRestore(wrapper)
    expect(inputValue(wrapper, 'cache_write_usd_per_mtok')).toBe('0')
    wrapper.unmount()
  })

  it('shows the restore notice and undoes back to the original values', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    expect(wrapper.find('[data-testid="restore-notice"]').exists()).toBe(false)

    await clickRestore(wrapper)
    const notice = wrapper.get('[data-testid="restore-notice"]')
    expect(notice.text()).toContain('admin.modelPricing.edit.restore.notice')

    await wrapper.get('[data-testid="restore-undo"]').trigger('click')
    await flushPromises()

    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('0.3')
    expect(inputValue(wrapper, 'output_usd_per_mtok')).toBe('1.2')
    expect(wrapper.find('[data-testid="restore-notice"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('disables the restore button under the direct basis', async () => {
    list.mockResolvedValue({
      items: [makeItem()],
      total: 1,
      page: 1,
      page_size: 20,
      meta: makeMeta({ cny_mode: 'direct' })
    })
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    expect(wrapper.get('[data-testid="restore-cny"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="restore-disabled-hint"]').text()).toContain(
      'admin.modelPricing.edit.restore.directHint'
    )

    await wrapper.get('[data-testid="restore-cny"]').trigger('click')
    await flushPromises()
    // 置灰时点击不改值
    expect(inputValue(wrapper, 'input_usd_per_mtok')).toBe('0.3')
    wrapper.unmount()
  })

  it('submits the restored values in the save payload', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    await clickRestore(wrapper)
    await wrapper.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()

    expect(updateOverride).toHaveBeenCalledWith('deepseek-v4-flash', {
      input_usd_per_mtok: 2.142,
      output_usd_per_mtok: 8.568,
      cache_read_usd_per_mtok: 0.04284
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.modelPricing.edit.saveSuccess|deepseek-v4-flash')
    wrapper.unmount()
  })

  it('also restores the peak / off-peak groups when the panel is open', async () => {
    list.mockResolvedValue({
      items: [
        makeItem({
          peak_enabled: true,
          peak: {
            input_usd_per_mtok: 0.6,
            output_usd_per_mtok: 2.4,
            cache_read_usd_per_mtok: 0.012,
            cache_write_usd_per_mtok: 0
          },
          off_peak: {
            input_usd_per_mtok: 0.3,
            output_usd_per_mtok: 1.2,
            cache_read_usd_per_mtok: 0.006,
            cache_write_usd_per_mtok: 0
          }
        })
      ],
      total: 1,
      page: 1,
      page_size: 20,
      meta: makeMeta()
    })
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    expect(
      (wrapper.get('[data-testid="price-peak-peak-input_usd_per_mtok"]').element as HTMLInputElement).value
    ).toBe('0.6')

    await clickRestore(wrapper)

    expect(
      (wrapper.get('[data-testid="price-peak-peak-input_usd_per_mtok"]').element as HTMLInputElement).value
    ).toBe('4.284')
    expect(
      (wrapper.get('[data-testid="price-peak-off_peak-input_usd_per_mtok"]').element as HTMLInputElement).value
    ).toBe('2.142')
    wrapper.unmount()
  })

  it('keeps the read-only CNY preview as reference only and never submits it', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    await wrapper.get('[data-testid="price-input-input_usd_per_mtok"]').setValue('1.2')
    await flushPromises()
    expect(wrapper.get('[data-testid="price-cny-preview-input_usd_per_mtok"]').text()).toContain('¥8.57')

    await wrapper.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()

    expect(updateOverride).toHaveBeenCalledWith('deepseek-v4-flash', { input_usd_per_mtok: 1.2 })
    wrapper.unmount()
  })

  // ===== 峰谷勾选 =====
  it('reveals the peak groups only after ticking the peak checkbox', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    expect(wrapper.find('[data-testid="price-peak-peak-input_usd_per_mtok"]').exists()).toBe(false)

    await wrapper.get('[data-testid="peak-enable"]').setValue(true)
    await flushPromises()

    expect(wrapper.find('[data-testid="price-peak-peak-input_usd_per_mtok"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="price-peak-off_peak-output_usd_per_mtok"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="peak-fallback-hint"]').text()).toContain(
      'admin.modelPricing.edit.peak.fallbackHint'
    )
    wrapper.unmount()
  })

  it('submits peak_enabled with the peak and off_peak groups', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    await wrapper.get('[data-testid="peak-enable"]').setValue(true)
    await wrapper.get('[data-testid="price-peak-peak-input_usd_per_mtok"]').setValue('3')
    await wrapper.get('[data-testid="price-peak-off_peak-input_usd_per_mtok"]').setValue('1.5')
    await wrapper.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()

    expect(updateOverride).toHaveBeenCalledWith('deepseek-v4-flash', {
      peak_enabled: true,
      peak: { input_usd_per_mtok: 3 },
      off_peak: { input_usd_per_mtok: 1.5 }
    })
    wrapper.unmount()
  })

  // ===== 新增模型 =====
  it('creates a model with the required name and platform', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="pricing-add-model"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.get('[data-testid="model-price-edit-dialog"]')
    expect(dialog.text()).toContain('admin.modelPricing.create.title')

    // 必填校验：空表单不能提交
    await wrapper.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="price-create-model-error"]').text()).toContain(
      'admin.modelPricing.create.modelRequired'
    )
    expect(createModel).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="price-create-model"]').setValue('glm-5')
    await wrapper.get('[data-testid="price-create-platform"]').setValue('moonshot')
    await wrapper.get('[data-testid="price-input-input_usd_per_mtok"]').setValue('2.142')
    await wrapper.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()

    expect(createModel).toHaveBeenCalledWith({
      model: 'glm-5',
      platform: 'moonshot',
      input_usd_per_mtok: 2.142
    })
    wrapper.unmount()
  })

  it('falls back to override editing when the model already exists', async () => {
    createModel.mockRejectedValue({ status: 409, reason: 'MODEL_ALREADY_EXISTS', message: 'exists' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="pricing-add-model"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="price-create-model"]').setValue('deepseek-v4-flash')
    await wrapper.get('[data-testid="price-create-platform"]').setValue('deepseek')
    await wrapper.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()

    expect(createModel).toHaveBeenCalled()
    expect(showWarning).toHaveBeenCalledWith(
      'admin.modelPricing.create.conflict|deepseek-v4-flash'
    )
    // 转为覆盖编辑：新增表单消失，出现该模型的编辑态
    expect(wrapper.find('[data-testid="price-create-model"]').exists()).toBe(false)
    expect((wrapper.get('[data-testid="price-input-input_usd_per_mtok"]').element as HTMLInputElement).value).toBe('0.3')
    wrapper.unmount()
  })

  // ===== 删除自定义模型 =====
  it('deletes a custom model after confirmation', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="price-delete-deepseek-v4-flash"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="confirm-dialog"]').text()).toContain(
      'admin.modelPricing.installed.deleteConfirmTitle'
    )

    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()
    expect(deleteModel).toHaveBeenCalledWith('deepseek-v4-flash')
    expect(showSuccess).toHaveBeenCalledWith(
      'admin.modelPricing.installed.deleteSuccess|deepseek-v4-flash'
    )
    wrapper.unmount()
  })

  // ===== 口径设置 / 峰谷时段 =====
  it('opens the CNY basis settings dialog lazily', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(getCurrency).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="pricing-currency-settings"]').trigger('click')
    await flushPromises()
    expect(getCurrency).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="currency-file"]').text()).toContain('/data/cny-overrides.json')

    const rateInput = wrapper.get('[data-testid="currency-rate-input"]')
    expect((rateInput.element as HTMLInputElement).value).toBe('7.14')

    await wrapper.get('[data-testid="currency-mode-direct"]').trigger('click')
    expect(rateInput.attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="currency-save"]').trigger('click')
    await flushPromises()
    expect(updateCurrency).toHaveBeenCalledWith({ mode: 'direct', cny_per_usd: 7.14 })
    expect(showSuccess).toHaveBeenCalledWith('admin.modelPricing.currency.mode.saved')
    wrapper.unmount()
  })

  it('loads and saves the global peak schedule', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="pricing-peak-settings"]').trigger('click')
    await flushPromises()

    expect(getPeakSettings).toHaveBeenCalledTimes(1)
    expect(
      (wrapper.get('[data-testid="peak-settings-timezone"]').element as HTMLInputElement).value
    ).toBe('Asia/Shanghai')
    expect(
      (wrapper.get('[data-testid="peak-settings-period-start-0"]').element as HTMLInputElement).value
    ).toBe('09:00')

    await wrapper.get('[data-testid="peak-settings-add-period"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="peak-settings-period-start-1"]').setValue('14:00')
    await wrapper.get('[data-testid="peak-settings-period-end-1"]').setValue('18:00')
    await wrapper.get('[data-testid="peak-settings-save"]').trigger('click')
    await flushPromises()

    expect(updatePeakSettings).toHaveBeenCalledWith({
      timezone: 'Asia/Shanghai',
      weekday_only: true,
      weekend_off_peak: true,
      periods: [
        { start: '09:00', end: '12:00' },
        { start: '14:00', end: '18:00' }
      ]
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.modelPricing.peakSettings.saved')
    wrapper.unmount()
  })

  // ===== 只读 / 错误 =====
  it('renders the read-only banner and disables mutations when the file is not writable', async () => {
    list.mockResolvedValue({
      items: [makeItem({ overridden: true })],
      total: 1,
      page: 1,
      page_size: 20,
      meta: makeMeta({ editable: false, cny_editable: false, override_file: '' })
    })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="override-readonly-banner"]').text()).toContain(
      'admin.modelPricing.installed.readonlyHint'
    )
    expect(wrapper.get('[data-testid="pricing-add-model"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="price-edit-deepseek-v4-flash"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="price-restore-deepseek-v4-flash"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="price-delete-deepseek-v4-flash"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('shows the catalog fallback banner when the catalog layer is disabled', async () => {
    list.mockResolvedValue({
      items: [makeItem()],
      total: 1,
      page: 1,
      page_size: 20,
      meta: makeMeta({ catalog_authoritative: false })
    })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="catalog-fallback-banner"]').text()).toContain(
      'admin.modelPricing.installed.catalogFallbackHint'
    )
    wrapper.unmount()
  })

  it('shows an inline error and toast when the list fails', async () => {
    list.mockRejectedValue({ status: 500, message: 'list down' })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="pricing-error"]').text()).toContain('list down')
    expect(showError).toHaveBeenCalledWith('list down')
    wrapper.unmount()
  })

  it('keeps the edit dialog open and surfaces backend validation errors', async () => {
    updateOverride.mockRejectedValue({ status: 400, message: 'INVALID_MODEL_PRICE: bad price' })
    const wrapper = mountView()
    await flushPromises()
    await openEditDialog(wrapper)

    await wrapper.get('[data-testid="price-input-input_usd_per_mtok"]').setValue('2')
    await wrapper.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="model-price-edit-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="price-save-error"]').text()).toContain(
      'INVALID_MODEL_PRICE: bad price'
    )
    expect(showError).toHaveBeenCalledWith('INVALID_MODEL_PRICE: bad price')
    wrapper.unmount()
  })
})
