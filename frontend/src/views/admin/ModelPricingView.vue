<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0">
            <h1 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('admin.modelPricing.title') }}
            </h1>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.modelPricing.description') }}
            </p>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <span
              v-if="meta"
              class="text-xs tabular-nums text-gray-500 dark:text-dark-400"
              data-testid="pricing-model-count"
            >
              {{ t('admin.modelPricing.installed.totalCount', { count: meta.model_count }) }}
            </span>
            <button
              type="button"
              class="btn btn-primary"
              :disabled="!canEdit"
              data-testid="pricing-add-model"
              @click="openCreate"
            >
              <Icon name="plus" size="sm" class="mr-1" />
              {{ t('admin.modelPricing.create.open') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary"
              data-testid="pricing-currency-settings"
              @click="openCurrencySettings"
            >
              <Icon name="dollar" size="sm" class="mr-1" />
              {{ t('admin.modelPricing.currencySettings.open') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary"
              data-testid="pricing-peak-settings"
              @click="openPeakSettings"
            >
              <Icon name="clock" size="sm" class="mr-1" />
              {{ t('admin.modelPricing.peakSettings.open') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="loading"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              data-testid="pricing-refresh"
              @click="loadInstalled"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </template>

      <template #filters>
        <div class="space-y-3">
          <div
            v-if="meta && !meta.editable"
            class="rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-200"
            data-testid="override-readonly-banner"
          >
            <Icon name="exclamationTriangle" size="sm" class="mr-1 inline" />
            {{ t('admin.modelPricing.installed.readonlyHint') }}
            <code v-if="meta.override_file" class="ml-1 font-mono text-xs">{{ meta.override_file }}</code>
          </div>
          <div
            v-else
            class="rounded-xl border border-gray-200 bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:border-dark-700 dark:bg-dark-800/60 dark:text-dark-300"
            data-testid="pricing-priority-hint"
          >
            {{ t('admin.modelPricing.installed.priorityHint') }}
          </div>

          <div
            v-if="meta && meta.catalog_authoritative === false"
            class="rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-200"
            data-testid="catalog-fallback-banner"
          >
            {{ t('admin.modelPricing.installed.catalogFallbackHint') }}
          </div>

          <div
            v-if="loadError"
            class="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
            data-testid="pricing-error"
          >
            {{ loadError }}
            <button type="button" class="ml-2 underline" @click="loadInstalled">
              {{ t('common.refresh') }}
            </button>
          </div>

          <div class="flex flex-wrap items-center gap-3">
            <div class="flex-1 sm:max-w-64">
              <input
                v-model="searchQuery"
                type="text"
                class="input"
                :placeholder="t('admin.modelPricing.installed.searchPlaceholder')"
                data-testid="pricing-search"
                @input="scheduleReload"
              />
            </div>
            <div class="w-44">
              <Select
                v-model="platformFilter"
                :options="platformOptions"
                :placeholder="t('admin.modelPricing.installed.platformFilter')"
                data-testid="pricing-platform-filter"
                @change="handleFilterChange"
              />
            </div>
            <div class="w-40">
              <Select
                v-model="sourceFilter"
                :options="sourceOptions"
                :placeholder="t('admin.modelPricing.installed.sourceFilter')"
                data-testid="pricing-source-filter"
                @change="handleFilterChange"
              />
            </div>
            <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                v-model="customOnly"
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
                data-testid="pricing-custom-only"
                @change="handleFilterChange"
              />
              {{ t('admin.modelPricing.installed.customOnly') }}
            </label>
            <span class="text-xs tabular-nums text-gray-500 dark:text-dark-400" data-testid="pricing-filtered-count">
              {{ t('admin.modelPricing.installed.filteredCount', { count: visibleItems.length, total: pagination.total }) }}
            </span>
          </div>
        </div>
      </template>

      <template #table>
        <ModelPriceTable
          :items="visibleItems"
          :loading="loading"
          :editable="canEdit"
          @edit="openEdit"
          @restore="requestRestore"
          @delete="requestDeleteModel"
        />
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <ModelPriceEditDialog
      :show="showEdit"
      :item="editingItem"
      :creating="creatingModel"
      :mode="meta?.cny_mode ?? 'convert'"
      :cny-per-usd="meta?.cny_per_usd ?? DEFAULT_CNY_PER_USD"
      :editable="canEdit"
      :saving="savingOverride"
      :error-text="overrideError"
      @close="closeEdit"
      @save="saveOverride"
      @create="createModel"
      @restore="requestRestore(editingItem)"
      @openPeakSettings="openPeakSettings"
    />

    <BaseDialog
      :show="showCurrencySettings"
      :title="t('admin.modelPricing.currencySettings.title')"
      width="extra-wide"
      @close="closeCurrencySettings"
    >
      <div class="space-y-4" data-testid="currency-settings-body">
        <div v-if="currencyLoading" class="space-y-3" data-testid="currency-loading">
          <Skeleton height="96px" />
          <Skeleton height="220px" />
        </div>

        <div v-else-if="currencyError" class="p-8 text-center" data-testid="currency-error">
          <Icon name="exclamationCircle" size="xl" class="mx-auto mb-3 text-red-500" />
          <p class="text-sm text-red-600 dark:text-red-400">{{ currencyError }}</p>
          <button type="button" class="btn btn-secondary mt-3" @click="loadCurrency(true)">
            {{ t('common.refresh') }}
          </button>
        </div>

        <template v-else-if="currency">
          <div
            v-if="!canEditCurrency"
            class="rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-200"
            data-testid="currency-readonly-banner"
          >
            {{ t('admin.modelPricing.currency.readonlyHint') }}
          </div>
          <p class="text-xs text-gray-500 dark:text-dark-400" data-testid="currency-file">
            {{ t('admin.modelPricing.currency.fileLabel') }}:
            <code class="font-mono">{{ currency.file || '—' }}</code>
          </p>

          <CurrencyModeCard
            :mode="currency.mode"
            :cny-per-usd="currency.cny_per_usd"
            :editable="canEditCurrency"
            :saving="savingMode"
            :sample="currencySample"
            @save="saveCurrencyMode"
          />

          <div class="rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
            <CnyPriceTable
              ref="cnyTableRef"
              :models="currency.models"
              :seeds="currency.seed_models"
              :mode="currency.mode"
              :cny-per-usd="currency.cny_per_usd"
              :editable="canEditCurrency"
              :saving="savingCnyModel"
              :error-text="cnyEditError"
              @save="saveCnyModel"
              @delete="requestDeleteCny"
            />
          </div>
        </template>
      </div>
    </BaseDialog>

    <PeakSettingsDialog
      :show="showPeakSettings"
      :settings="peakSettings"
      :editable="canEdit"
      :saving="savingPeakSettings"
      :error-text="peakSettingsError"
      @close="closePeakSettings"
      @save="savePeakSettings"
    />

    <ConfirmDialog
      :show="showRestoreConfirm"
      :title="t('admin.modelPricing.installed.restoreConfirmTitle')"
      :message="restoreConfirmMessage"
      :confirm-text="t('admin.modelPricing.installed.restore')"
      danger
      @confirm="confirmRestore"
      @cancel="cancelRestore"
    />

    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.modelPricing.installed.deleteConfirmTitle')"
      :message="deleteConfirmMessage"
      :confirm-text="t('admin.modelPricing.installed.delete')"
      danger
      @confirm="confirmDeleteModel"
      @cancel="cancelDeleteModel"
    />

    <ConfirmDialog
      :show="showCnyDeleteConfirm"
      :title="t('admin.modelPricing.currency.table.deleteConfirmTitle')"
      :message="cnyDeleteMessage"
      :confirm-text="t('admin.modelPricing.currency.table.delete')"
      danger
      @confirm="confirmDeleteCny"
      @cancel="cancelDeleteCny"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { extractApiErrorMessage, extractApiErrorCode } from '@/utils/apiError'
import {
  DEFAULT_CNY_PER_USD,
  MODEL_ALREADY_EXISTS,
  modelPricingAPI,
  type CreateModelPricingPayload,
  type ModelPricingCurrencyState,
  type ModelPricingCnyPayload,
  type ModelPricingItem,
  type ModelPricingMeta,
  type ModelPricingOverridePayload,
  type PeakPricingSettings,
  type UpdateModelPricingCurrencyPayload
} from '@/api/admin/modelPricing'
import { GROUP_PLATFORM_OPTIONS } from '@/constants/platforms'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelPriceTable from '@/components/admin/pricing/ModelPriceTable.vue'
import ModelPriceEditDialog from '@/components/admin/pricing/ModelPriceEditDialog.vue'
import CurrencyModeCard from '@/components/admin/pricing/CurrencyModeCard.vue'
import CnyPriceTable from '@/components/admin/pricing/CnyPriceTable.vue'
import PeakSettingsDialog from '@/components/admin/pricing/PeakSettingsDialog.vue'

const { t, te } = useI18n()
const appStore = useAppStore()

// ===== 单页列表 =====
const items = ref<ModelPricingItem[]>([])
const meta = ref<ModelPricingMeta | null>(null)
const loading = ref(false)
const loadError = ref('')
const searchQuery = ref('')
const platformFilter = ref('')
const sourceFilter = ref('')
const customOnly = ref(false)

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0
})

const canEdit = computed(() => meta.value?.editable === true)
const canEditCurrency = computed(() => meta.value?.cny_editable === true)

const platformOptions = computed(() => [
  { value: '', label: t('admin.modelPricing.installed.platformAll') },
  ...GROUP_PLATFORM_OPTIONS.map((option) => ({ value: option.value, label: option.label }))
])

const sourceOptions = computed(() => {
  const all = [
    { value: '', label: t('admin.modelPricing.installed.sourceAll') },
    { value: 'catalog', label: t('admin.modelPricing.sources.catalog') },
    { value: 'litellm', label: t('admin.modelPricing.sources.litellm') },
    { value: 'fallback', label: t('admin.modelPricing.sources.fallback') },
    { value: 'override', label: t('admin.modelPricing.sources.override') },
    { value: 'cny', label: t('admin.modelPricing.sources.cny') }
  ]
  // 来源键由服务端下发，用 te 判定避免命名空间缺键时报错。
  return all.filter((option) => !option.value || te(`admin.modelPricing.sources.${option.value}`))
})

/**
 * 行过滤：来源与平台都在本地过滤。
 *
 * 后端 `GET /admin/model-pricing?platform=` 只精确匹配 `_catalog.platform`，
 * 而未自定义的模型该字段是空串 —— 走服务端会把绝大多数模型筛掉。
 * 因此平台筛选在客户端做：按行 `platform`（`_catalog` 已接管时）
 * 或 `provider`（LiteLLM 目录的供应商，覆盖全量模型）任一命中即算匹配。
 * 269 个模型内存过滤无性能问题；若后端将来区分「目录价平台」与「供应商」两列，
 * 再切回服务端筛选。
 */
const visibleItems = computed(() =>
  items.value.filter((item) => {
    if (sourceFilter.value && (item.source || 'unknown') !== sourceFilter.value) return false
    if (platformFilter.value) {
      const wanted = platformFilter.value.toLowerCase()
      const candidates = [item.platform, item.provider]
        .filter((value): value is string => typeof value === 'string')
        .map((value) => value.toLowerCase())
      if (!candidates.includes(wanted)) return false
    }
    return true
  })
)

let listController: AbortController | null = null

async function loadInstalled(): Promise<void> {
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  const { signal } = controller

  loading.value = true
  loadError.value = ''

  try {
    const result = await modelPricingAPI.list({
      page: pagination.page,
      page_size: pagination.page_size,
      search: searchQuery.value.trim() || undefined,
      custom: customOnly.value ? true : undefined,
      signal
    })
    if (signal.aborted || listController !== controller) return

    items.value = result.items
    meta.value = result.meta
    pagination.total = result.total
    pagination.page = result.page
    pagination.page_size = result.page_size
    if (!peakSettings.value && result.meta.peak_settings) {
      peakSettings.value = result.meta.peak_settings
    }
  } catch (error) {
    if (
      signal.aborted ||
      listController !== controller ||
      (error as { code?: string })?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading model pricing:', error)
    loadError.value = extractApiErrorMessage(error, t('admin.modelPricing.installed.loadFailed'))
    appStore.showError(loadError.value)
  } finally {
    if (listController === controller) {
      loading.value = false
      listController = null
    }
  }
}

let searchTimer: number | null = null
function scheduleReload(): void {
  if (searchTimer) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    searchTimer = null
    pagination.page = 1
    void loadInstalled()
  }, 300)
}

function handleFilterChange(): void {
  pagination.page = 1
  void loadInstalled()
}

function handlePageChange(page: number): void {
  pagination.page = page
  void loadInstalled()
}

function handlePageSizeChange(pageSize: number): void {
  pagination.page_size = pageSize
  pagination.page = 1
  void loadInstalled()
}

// ===== 编辑 / 新增 =====
const showEdit = ref(false)
const editingItem = ref<ModelPricingItem | null>(null)
const creatingModel = ref(false)
const savingOverride = ref(false)
const overrideError = ref('')

function openEdit(item: ModelPricingItem): void {
  if (!canEdit.value) return
  editingItem.value = item
  creatingModel.value = false
  overrideError.value = ''
  showEdit.value = true
}

/** 新增模型：复用同一抽屉，以空表单打开。 */
function openCreate(): void {
  if (!canEdit.value) return
  editingItem.value = null
  creatingModel.value = true
  overrideError.value = ''
  showEdit.value = true
}

function closeEdit(): void {
  if (savingOverride.value) return
  showEdit.value = false
  editingItem.value = null
  creatingModel.value = false
  overrideError.value = ''
}

async function saveOverride(payload: ModelPricingOverridePayload): Promise<void> {
  if (!editingItem.value || savingOverride.value) return
  const model = editingItem.value.model
  savingOverride.value = true
  overrideError.value = ''
  try {
    editingItem.value = await modelPricingAPI.updateOverride(model, payload)
    appStore.showSuccess(t('admin.modelPricing.edit.saveSuccess', { model }))
    showEdit.value = false
    editingItem.value = null
    await loadInstalled()
  } catch (error) {
    console.error('Error saving model price override:', error)
    const message = extractApiErrorMessage(error, t('admin.modelPricing.edit.saveFailed'))
    overrideError.value = message
    appStore.showError(message)
  } finally {
    savingOverride.value = false
  }
}

async function createModel(payload: CreateModelPricingPayload): Promise<void> {
  if (savingOverride.value) return
  savingOverride.value = true
  overrideError.value = ''
  try {
    const created = await modelPricingAPI.createModel(payload)
    appStore.showSuccess(t('admin.modelPricing.create.created', { model: created.model }))
    showEdit.value = false
    creatingModel.value = false
    await loadInstalled()
  } catch (error) {
    console.error('Error creating model price:', error)
    const code = extractApiErrorCode(error)
    if (code === MODEL_ALREADY_EXISTS) {
      // 重名：转为对该模型的覆盖编辑（把刚填的价格带进抽屉，用户确认后再保存）。
      const target = payload.model
      creatingModel.value = false
      const existing =
        items.value.find((item) => item.model.toLowerCase() === target.toLowerCase()) ??
        modelFromCreatePayload(payload)
      editingItem.value = existing
      overrideError.value = t('admin.modelPricing.create.conflict', { model: target })
      appStore.showWarning(overrideError.value)
      return
    }
    const message = extractApiErrorMessage(error, t('admin.modelPricing.create.failed'))
    overrideError.value = message
    appStore.showError(message)
  } finally {
    savingOverride.value = false
  }
}

/** 重名转覆盖时，用刚填的新增表单构造一个临时条目，避免抽屉丢值。 */
function modelFromCreatePayload(payload: CreateModelPricingPayload): ModelPricingItem {
  return {
    model: payload.model,
    provider: payload.platform,
    platform: payload.platform,
    mode: '',
    input_usd_per_mtok: payload.input_usd_per_mtok ?? 0,
    output_usd_per_mtok: payload.output_usd_per_mtok ?? 0,
    cache_read_usd_per_mtok: payload.cache_read_usd_per_mtok ?? 0,
    cache_write_usd_per_mtok: payload.cache_write_usd_per_mtok ?? 0,
    image_input_usd_per_mtok: payload.image_input_usd_per_mtok ?? 0,
    priority_input_usd_per_mtok: payload.priority_input_usd_per_mtok ?? 0,
    priority_output_usd_per_mtok: payload.priority_output_usd_per_mtok ?? 0,
    source: 'override',
    overridden: false,
    custom: true,
    effective_updated_at: new Date().toISOString(),
    peak_enabled: payload.peak_enabled === true,
    peak: payload.peak ?? {},
    off_peak: payload.off_peak ?? {}
  }
}

// ===== 恢复内置 / 删除模型 =====
const showRestoreConfirm = ref(false)
const restoreTarget = ref<ModelPricingItem | null>(null)
const restoring = ref(false)

const restoreConfirmMessage = computed(() =>
  t('admin.modelPricing.installed.restoreConfirmMessage', {
    model: restoreTarget.value?.model ?? ''
  })
)

function requestRestore(item: ModelPricingItem | null): void {
  if (!item || !canEdit.value) return
  restoreTarget.value = item
  showEdit.value = false
  editingItem.value = null
  overrideError.value = ''
  showRestoreConfirm.value = true
}

function cancelRestore(): void {
  if (restoring.value) return
  showRestoreConfirm.value = false
  restoreTarget.value = null
}

async function confirmRestore(): Promise<void> {
  const target = restoreTarget.value
  if (!target || restoring.value) return
  restoring.value = true
  try {
    await modelPricingAPI.deleteOverride(target.model)
    appStore.showSuccess(t('admin.modelPricing.installed.restoreSuccess', { model: target.model }))
    showRestoreConfirm.value = false
    restoreTarget.value = null
    await loadInstalled()
  } catch (error) {
    console.error('Error restoring model pricing:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.modelPricing.installed.restoreFailed')))
  } finally {
    restoring.value = false
  }
}

const showDeleteConfirm = ref(false)
const deleteTarget = ref<ModelPricingItem | null>(null)
const deletingModel = ref(false)

const deleteConfirmMessage = computed(() =>
  t('admin.modelPricing.installed.deleteConfirmMessage', {
    model: deleteTarget.value?.model ?? ''
  })
)

function requestDeleteModel(item: ModelPricingItem | null): void {
  if (!item || !canEdit.value) return
  deleteTarget.value = item
  showDeleteConfirm.value = true
}

function cancelDeleteModel(): void {
  if (deletingModel.value) return
  showDeleteConfirm.value = false
  deleteTarget.value = null
}

async function confirmDeleteModel(): Promise<void> {
  const target = deleteTarget.value
  if (!target || deletingModel.value) return
  deletingModel.value = true
  try {
    await modelPricingAPI.deleteModel(target.model)
    appStore.showSuccess(t('admin.modelPricing.installed.deleteSuccess', { model: target.model }))
    showDeleteConfirm.value = false
    deleteTarget.value = null
    await loadInstalled()
  } catch (error) {
    console.error('Error deleting model:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.modelPricing.installed.deleteFailed')))
  } finally {
    deletingModel.value = false
  }
}

// ===== 峰谷全局时段 =====
const showPeakSettings = ref(false)
const peakSettings = ref<PeakPricingSettings | null>(null)
const savingPeakSettings = ref(false)
const peakSettingsError = ref('')

function openPeakSettings(): void {
  peakSettingsError.value = ''
  showPeakSettings.value = true
  if (!peakSettings.value && meta.value?.peak_settings) {
    peakSettings.value = meta.value.peak_settings
  }
  void loadPeakSettings()
}

function closePeakSettings(): void {
  if (savingPeakSettings.value) return
  showPeakSettings.value = false
  peakSettingsError.value = ''
}

async function loadPeakSettings(): Promise<void> {
  try {
    peakSettings.value = await modelPricingAPI.getPeakSettings()
  } catch (error) {
    console.error('Error loading peak settings:', error)
    // 读失败不阻断：列表 meta 已带一份时段，保存时会再次整体提交。
    peakSettingsError.value = extractApiErrorMessage(
      error,
      t('admin.modelPricing.peakSettings.loadFailed')
    )
  }
}

async function savePeakSettings(payload: PeakPricingSettings): Promise<void> {
  if (savingPeakSettings.value) return
  savingPeakSettings.value = true
  peakSettingsError.value = ''
  try {
    peakSettings.value = await modelPricingAPI.updatePeakSettings(payload)
    appStore.showSuccess(t('admin.modelPricing.peakSettings.saved'))
    showPeakSettings.value = false
    await loadInstalled()
  } catch (error) {
    console.error('Error saving peak settings:', error)
    const message = extractApiErrorMessage(error, t('admin.modelPricing.peakSettings.saveFailed'))
    peakSettingsError.value = message
    appStore.showError(message)
  } finally {
    savingPeakSettings.value = false
  }
}

// ===== 人民币口径设置（小窗） =====
const showCurrencySettings = ref(false)
const currency = ref<ModelPricingCurrencyState | null>(null)
const currencyLoading = ref(false)
const currencyError = ref('')
const currencyLoaded = ref(false)
const savingMode = ref(false)
const savingCnyModel = ref(false)
const cnyEditError = ref('')
const cnyTableRef = ref<InstanceType<typeof CnyPriceTable> | null>(null)

let currencyController: AbortController | null = null

async function loadCurrency(force = false): Promise<void> {
  if (currencyLoaded.value && !force) return
  currencyController?.abort()
  const controller = new AbortController()
  currencyController = controller
  const { signal } = controller

  currencyLoading.value = true
  currencyError.value = ''

  try {
    const result = await modelPricingAPI.getCurrency({ signal })
    if (signal.aborted || currencyController !== controller) return

    currency.value = result
    currencyLoaded.value = true
  } catch (error) {
    if (
      signal.aborted ||
      currencyController !== controller ||
      (error as { code?: string })?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading model pricing currency:', error)
    currencyError.value = extractApiErrorMessage(
      error,
      t('admin.modelPricing.currency.loadFailed')
    )
    appStore.showError(currencyError.value)
  } finally {
    if (currencyController === controller) {
      currencyLoading.value = false
      currencyController = null
    }
  }
}

function openCurrencySettings(): void {
  showCurrencySettings.value = true
  cnyEditError.value = ''
  void loadCurrency()
}

function closeCurrencySettings(): void {
  if (savingMode.value || savingCnyModel.value) return
  showCurrencySettings.value = false
  cnyEditError.value = ''
}

const currencySample = computed(() => {
  const list = currency.value?.models ?? []
  const seeds = currency.value?.seed_models ?? []
  const sample =
    list.find((item) => item.model === 'kimi-k2') ??
    seeds.find((item) => item.model === 'kimi-k2') ??
    list[0] ??
    seeds[0] ??
    null
  if (!sample) return null
  return {
    model: sample.model,
    inputCny: sample.input_cny_per_mtok,
    outputCny: sample.output_cny_per_mtok
  }
})

async function saveCurrencyMode(payload: UpdateModelPricingCurrencyPayload): Promise<void> {
  if (!canEditCurrency.value || savingMode.value) return
  savingMode.value = true
  try {
    currency.value = await modelPricingAPI.updateCurrency(payload)
    // 口径变化会影响抽屉里的「≈¥」预览与一键还原乘数，刷新列表 meta。
    await loadInstalled()
    appStore.showSuccess(t('admin.modelPricing.currency.mode.saved'))
  } catch (error) {
    console.error('Error updating model pricing currency:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.modelPricing.currency.mode.saveFailed')))
  } finally {
    savingMode.value = false
  }
}

async function saveCnyModel(model: string, payload: ModelPricingCnyPayload): Promise<void> {
  if (!canEditCurrency.value || savingCnyModel.value) return
  savingCnyModel.value = true
  cnyEditError.value = ''
  try {
    currency.value = await modelPricingAPI.upsertCurrencyModel(model, payload)
    cnyTableRef.value?.closeEditor()
    appStore.showSuccess(t('admin.modelPricing.currency.table.saveSuccess', { model }))
  } catch (error) {
    console.error('Error saving model CNY price:', error)
    const message = extractApiErrorMessage(error, t('admin.modelPricing.currency.table.saveFailed'))
    cnyEditError.value = message
    appStore.showError(message)
  } finally {
    savingCnyModel.value = false
  }
}

const showCnyDeleteConfirm = ref(false)
const cnyDeleteTarget = ref('')
const deletingCny = ref(false)

const cnyDeleteMessage = computed(() =>
  t('admin.modelPricing.currency.table.deleteConfirmMessage', {
    model: cnyDeleteTarget.value
  })
)

function requestDeleteCny(model: string): void {
  if (!canEditCurrency.value) return
  cnyDeleteTarget.value = model
  showCnyDeleteConfirm.value = true
}

function cancelDeleteCny(): void {
  if (deletingCny.value) return
  showCnyDeleteConfirm.value = false
  cnyDeleteTarget.value = ''
}

async function confirmDeleteCny(): Promise<void> {
  const model = cnyDeleteTarget.value
  if (!model || deletingCny.value) return
  deletingCny.value = true
  try {
    currency.value = await modelPricingAPI.deleteCurrencyModel(model)
    appStore.showSuccess(t('admin.modelPricing.currency.table.deleteSuccess', { model }))
    showCnyDeleteConfirm.value = false
    cnyDeleteTarget.value = ''
  } catch (error) {
    console.error('Error deleting model CNY price:', error)
    appStore.showError(
      extractApiErrorMessage(error, t('admin.modelPricing.currency.table.deleteFailed'))
    )
  } finally {
    deletingCny.value = false
  }
}

onMounted(() => {
  void loadInstalled()
})

onUnmounted(() => {
  listController?.abort()
  currencyController?.abort()
  if (searchTimer) window.clearTimeout(searchTimer)
})
</script>
