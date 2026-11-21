<template>
  <BaseDialog :show="show" :title="dialogTitle" width="wide" @close="handleClose">
    <div class="space-y-5" data-testid="model-price-edit-body">
      <!-- 模型概要 / 新增模型的必填信息 -->
      <div v-if="isCreate" class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div class="space-y-1.5">
          <label :for="modelNameId" class="input-label block">
            {{ t('admin.modelPricing.create.modelLabel') }}
          </label>
          <input
            :id="modelNameId"
            v-model="modelName"
            type="text"
            class="input w-full font-mono"
            :disabled="saving"
            :placeholder="t('admin.modelPricing.create.modelPlaceholder')"
            data-testid="price-create-model"
            @input="modelNameError = ''"
          />
          <p v-if="modelNameError" class="input-error-text" data-testid="price-create-model-error">
            {{ modelNameError }}
          </p>
        </div>
        <div class="space-y-1.5">
          <label :for="platformId" class="input-label block">
            {{ t('admin.modelPricing.create.platformLabel') }}
          </label>
          <Select
            :id="platformId"
            v-model="platform"
            :options="platformChoices"
            :disabled="saving"
            :placeholder="t('admin.modelPricing.create.platformPlaceholder')"
            data-testid="price-create-platform"
          />
          <p v-if="platformError" class="input-error-text" data-testid="price-create-platform-error">
            {{ platformError }}
          </p>
        </div>
      </div>

      <div v-else class="flex flex-wrap items-center gap-2">        <span class="font-mono text-sm font-medium text-gray-900 dark:text-white">{{ item?.model }}</span>
        <span class="badge" :class="item?.overridden || item?.custom ? 'badge-warning' : 'badge-gray'">
          {{ item?.overridden || item?.custom ? t('admin.modelPricing.installed.custom') : t('admin.modelPricing.installed.builtin') }}
        </span>
        <span v-if="item?.platform || item?.provider" class="badge badge-gray">
          {{ item?.platform || item?.provider }}
        </span>
        <span v-if="item?.peak_enabled" class="badge badge-primary">
          {{ t('admin.modelPricing.edit.peak.enabledBadge') }}
        </span>
        <button
          v-if="item?.peak_enabled"
          type="button"
          class="ml-auto text-xs text-primary-600 underline disabled:cursor-not-allowed disabled:opacity-40 dark:text-primary-400"
          :disabled="!editable || saving"
          data-testid="price-open-peak-settings"
          @click="emit('openPeakSettings')"
        >
          {{ t('admin.modelPricing.edit.peak.settingsLink') }}
        </button>
      </div>

      <p v-if="!isCreate" class="text-xs text-gray-500 dark:text-dark-400">
        {{ t('admin.modelPricing.edit.subtitle') }}
      </p>

      <div
        v-if="!editable"
        class="rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-200"
        data-testid="price-edit-readonly"
      >
        {{ t('admin.modelPricing.installed.readonlyHint') }}
      </div>

      <div
        v-if="props.errorText"
        class="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
        data-testid="price-save-error"
      >
        {{ props.errorText }}
      </div>

      <p v-if="item?.cny" class="text-xs text-gray-500 dark:text-dark-400" data-testid="price-current-cny">
        {{ t('admin.modelPricing.edit.currentCny') }}:
        <span class="tabular-nums">
          ¥{{ formatCnyPerMTok(item.cny.input) }} / ¥{{ formatCnyPerMTok(item.cny.output) }}
          / ¥{{ formatCnyPerMTok(item.cny.cache_read) }}
        </span>
      </p>

      <!-- 基础 USD 字段（输入 / 输出 / 缓存读 / 缓存写） -->
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div v-for="field in BASIC_FIELDS" :key="field" class="space-y-1.5">
          <label :for="inputId(field)" class="input-label block">{{ t(FIELD_LABEL_KEYS[field]) }}</label>
          <div class="flex items-start gap-2">
            <div class="min-w-0 flex-1">
              <input
                :id="inputId(field)"
                v-model="values[field]"
                type="text"
                inputmode="decimal"
                class="input w-full tabular-nums"
                :disabled="!editable || saving"
                :data-testid="`price-input-${field}`"
                @input="clearResetFlag(field)"
              />
              <p
                class="mt-1 text-xs tabular-nums text-gray-500 dark:text-dark-400"
                :data-testid="`price-cny-preview-${field}`"
              >
                {{ t('admin.modelPricing.edit.cnyPreview') }} ¥{{ cnyPreview(field) }}
              </p>
              <p v-if="fieldErrors[field]" class="input-error-text mt-1">{{ fieldErrors[field] }}</p>
            </div>
            <button
              v-if="!isCreate"
              type="button"
              class="btn btn-secondary shrink-0 px-2 py-1.5 text-xs"
              :disabled="!editable || saving || resetFlags[field]"
              :title="t('admin.modelPricing.edit.resetField')"
              :data-testid="`price-reset-${field}`"
              @click="resetField(field)"
            >
              {{ t('admin.modelPricing.edit.resetField') }}
            </button>
          </div>
        </div>
      </div>

      <!-- 高峰 / 低谷双价（勾选式开关） -->
      <section
        class="rounded-xl border border-gray-200 dark:border-dark-700"
        data-testid="peak-panel"
      >
        <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-3">
          <label class="inline-flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-100">
            <input
              v-model="peakEnabled"
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
              :disabled="!editable || saving"
              data-testid="peak-enable"
              @change="peakEnabledChanged"
            />
            {{ t('admin.modelPricing.edit.peak.enable') }}
          </label>
          <button
            type="button"
            class="text-xs text-primary-600 underline disabled:cursor-not-allowed disabled:opacity-40 dark:text-primary-400"
            :disabled="!editable || saving"
            data-testid="peak-settings-link"
            @click="emit('openPeakSettings')"
          >
            {{ t('admin.modelPricing.edit.peak.settingsLink') }}
          </button>
        </div>

        <div v-if="peakEnabled" class="space-y-4 border-t border-gray-100 p-4 dark:border-dark-700">
          <div v-for="group in PEAK_GROUPS" :key="group.key">
            <h4 class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-400">
              {{ t(group.labelKey) }}
            </h4>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div v-for="field in PEAK_PRICE_FIELDS" :key="`${group.key}-${field}`" class="space-y-1">
                <label :for="peakInputId(group.key, field)" class="input-label block">
                  {{ t(FIELD_LABEL_KEYS[field]) }}
                </label>
                <input
                  :id="peakInputId(group.key, field)"
                  v-model="groupValues[group.key][field]"
                  type="text"
                  inputmode="decimal"
                  class="input w-full tabular-nums"
                  :disabled="!editable || saving"
                  :data-testid="`price-peak-${group.key}-${field}`"
                  @input="peakTouched = true"
                />
              </div>
            </div>
          </div>
          <p class="text-xs text-gray-500 dark:text-dark-400" data-testid="peak-fallback-hint">
            {{ t('admin.modelPricing.edit.peak.fallbackHint') }}
          </p>
          <p
            v-if="showOfficialMultiplierHint"
            class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-200"
            data-testid="peak-official-hint"
          >
            {{ t('admin.modelPricing.edit.peak.officialHint') }}
          </p>
          <p v-if="peakOrderWarning" class="input-error-text" data-testid="peak-order-warning">
            {{ t('admin.modelPricing.edit.peak.orderWarning') }}
          </p>
        </div>
      </section>

      <!-- 高级 USD 字段 -->
      <details class="rounded-xl border border-gray-200 dark:border-dark-700">
        <summary class="cursor-pointer px-4 py-3 text-sm font-medium text-gray-700 dark:text-gray-200">
          {{ t('admin.modelPricing.edit.advancedFields') }}
        </summary>
        <div class="grid grid-cols-1 gap-4 border-t border-gray-100 p-4 dark:border-dark-700 sm:grid-cols-2">
          <div v-for="field in ADVANCED_FIELDS" :key="field" class="space-y-1.5">
            <label :for="inputId(field)" class="input-label block">{{ t(FIELD_LABEL_KEYS[field]) }}</label>
            <div class="flex items-start gap-2">
              <div class="min-w-0 flex-1">
                <input
                  :id="inputId(field)"
                  v-model="values[field]"
                  type="text"
                  inputmode="decimal"
                  class="input w-full tabular-nums"
                  :disabled="!editable || saving"
                  :data-testid="`price-input-${field}`"
                  @input="clearResetFlag(field)"
                />
                <p
                  class="mt-1 text-xs tabular-nums text-gray-500 dark:text-dark-400"
                  :data-testid="`price-cny-preview-${field}`"
                >
                  {{ t('admin.modelPricing.edit.cnyPreview') }} ¥{{ cnyPreview(field) }}
                </p>
                <p v-if="fieldErrors[field]" class="input-error-text mt-1">{{ fieldErrors[field] }}</p>
              </div>
              <button
                v-if="!isCreate"
                type="button"
                class="btn btn-secondary shrink-0 px-2 py-1.5 text-xs"
                :disabled="!editable || saving || resetFlags[field]"
                :title="t('admin.modelPricing.edit.resetField')"
                :data-testid="`price-reset-${field}`"
                @click="resetField(field)"
              >
                {{ t('admin.modelPricing.edit.resetField') }}
              </button>
            </div>
          </div>
        </div>
      </details>

      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.modelPricing.edit.unitHint') }}</p>

      <div
        v-if="formError"
        class="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
        data-testid="price-form-error"
      >
        {{ formError }}
      </div>

      <!-- 一键还原人民币价：编辑抽屉底部、保存按钮上方 -->
      <section class="space-y-2 border-t border-gray-100 pt-4 dark:border-dark-700">
        <div
          v-if="restoreNotice"
          class="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-primary-200 bg-primary-50 px-3 py-2 text-sm text-primary-800 dark:border-primary-900/60 dark:bg-primary-900/20 dark:text-primary-200"
          data-testid="restore-notice"
        >
          <span>{{ t('admin.modelPricing.edit.restore.notice', { rate: restoreRateLabel }) }}</span>
          <button
            type="button"
            class="font-medium underline disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="!editable || saving"
            data-testid="restore-undo"
            @click="undoRestore"
          >
            {{ t('admin.modelPricing.edit.restore.undo') }}
          </button>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="restoreDisabled || restoreSnapshot !== null"
            :title="restoreSnapshot !== null ? t('admin.modelPricing.edit.restore.alreadyApplied') : restoreDisabledReason"
            data-testid="restore-cny"
            @click="applyRestore"
          >
            {{ t('admin.modelPricing.edit.restore.button', { rate: restoreRateLabel }) }}
          </button>
          <span
            v-if="isDirectMode"
            class="text-xs text-gray-500 dark:text-dark-400"
            data-testid="restore-disabled-hint"
          >
            {{ t('admin.modelPricing.edit.restore.directHint') }}
          </span>
          <span v-else class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.modelPricing.edit.restore.hint') }}
          </span>
        </div>
      </section>
    </div>

    <template #footer>
      <div class="flex w-full flex-wrap items-center justify-between gap-2">
        <button
          v-if="!isCreate && (item?.overridden || item?.custom)"
          type="button"
          class="btn btn-secondary text-amber-700 dark:text-amber-300"
          :disabled="!editable || saving"
          data-testid="price-restore-model"
          @click="emit('restore')"
        >
          {{ t('admin.modelPricing.edit.restoreModel') }}
        </button>
        <div class="ml-auto flex items-center gap-2">
          <button type="button" class="btn btn-secondary" :disabled="saving" @click="handleClose">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="!canSave"
            data-testid="price-save"
            @click="handleSave"
          >
            {{ saving ? t('admin.modelPricing.edit.saving') : saveLabel }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  MODEL_PRICE_USD_LIMIT,
  PEAK_PRICE_FIELDS,
  USD_PER_MTOK_ADVANCED_FIELDS,
  USD_PER_MTOK_BASIC_FIELDS,
  USD_PER_MTOK_FIELDS,
  type CreateModelPricingPayload,
  type CurrencyMode,
  type ModelPricingItem,
  type ModelPricingOverridePayload,
  type PeakPriceSet,
  type UsdPerMtokField
} from '@/api/admin/modelPricing'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import {
  cnyRestoreRate,
  formatCnyPerMTok,
  formatPriceInput,
  formatRate,
  restoreCnyPriceInput,
  usdToCnyValue
} from './priceFormat'

const props = withDefaults(
  defineProps<{
    show: boolean
    item: ModelPricingItem | null
    mode: CurrencyMode
    cnyPerUsd: number
    editable: boolean
    saving?: boolean
    errorText?: string
    /** 新增模型：抽屉以空表单打开，模型名/平台必填。 */
    creating?: boolean
  }>(),
  {
    saving: false,
    errorText: '',
    creating: false
  }
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'save', payload: ModelPricingOverridePayload): void
  (e: 'create', payload: CreateModelPricingPayload): void
  (e: 'restore'): void
  (e: 'openPeakSettings'): void
}>()

const { t } = useI18n()
const uid = useId()

const BASIC_FIELDS = USD_PER_MTOK_BASIC_FIELDS
const ADVANCED_FIELDS = USD_PER_MTOK_ADVANCED_FIELDS

const FIELD_LABEL_KEYS: Record<UsdPerMtokField, string> = {
  input_usd_per_mtok: 'admin.modelPricing.edit.fields.input',
  output_usd_per_mtok: 'admin.modelPricing.edit.fields.output',
  cache_read_usd_per_mtok: 'admin.modelPricing.edit.fields.cacheRead',
  cache_write_usd_per_mtok: 'admin.modelPricing.edit.fields.cacheWrite',
  image_input_usd_per_mtok: 'admin.modelPricing.edit.fields.imageInput',
  priority_input_usd_per_mtok: 'admin.modelPricing.edit.fields.priorityInput',
  priority_output_usd_per_mtok: 'admin.modelPricing.edit.fields.priorityOutput'
}

type PeakGroupKey = 'peak' | 'off_peak'

const PEAK_GROUPS: ReadonlyArray<{ key: PeakGroupKey; labelKey: string }> = [
  { key: 'peak', labelKey: 'admin.modelPricing.edit.peak.peakTitle' },
  { key: 'off_peak', labelKey: 'admin.modelPricing.edit.peak.offPeakTitle' }
]

/** 内置平台兜底：平台目录未加载时仍能选择常见平台。 */
const FALLBACK_PLATFORMS = [
  'openai',
  'anthropic',
  'gemini',
  'deepseek',
  'moonshot',
  'zhipu',
  'qwen',
  'volcengine',
  'xai',
  'mistral',
  'meta',
  'cohere'
]

const isCreate = computed(() => props.creating)

const modelNameId = `model-price-name-${uid}`
const platformId = `model-price-platform-${uid}`

function emptyRecord<T>(factory: () => T): Record<UsdPerMtokField, T> {
  return Object.fromEntries(USD_PER_MTOK_FIELDS.map((field) => [field, factory()])) as Record<
    UsdPerMtokField,
    T
  >
}

function emptyPeakSet(): Record<string, string> {
  return Object.fromEntries(PEAK_PRICE_FIELDS.map((field) => [field, '']))
}

const values = reactive<Record<UsdPerMtokField, string>>(emptyRecord(() => ''))
const initialStrings = reactive<Record<UsdPerMtokField, string>>(emptyRecord(() => ''))
const initialNumbers = reactive<Record<UsdPerMtokField, number>>(emptyRecord(() => 0))
const resetFlags = reactive<Record<UsdPerMtokField, boolean>>(emptyRecord(() => false))
const fieldErrors = reactive<Record<UsdPerMtokField, string>>(emptyRecord(() => ''))

const groupValues = reactive<Record<PeakGroupKey, Record<string, string>>>({
  peak: emptyPeakSet(),
  off_peak: emptyPeakSet()
})
const initialPeak = reactive<Record<PeakGroupKey, Record<string, string>>>({
  peak: emptyPeakSet(),
  off_peak: emptyPeakSet()
})

const peakEnabled = ref(false)
const initialPeakEnabled = ref(false)
const peakTouched = ref(false)

const modelName = ref('')
const platform = ref('')
const modelNameError = ref('')
const platformError = ref('')

const formError = ref('')

/** 一键还原的快照与提示状态（一次性动作，不做任何持久化）。 */
const restoreSnapshot = ref<{
  values: Record<UsdPerMtokField, string>
  peak: Record<PeakGroupKey, Record<string, string>>
} | null>(null)
const restoreNotice = ref(false)

const dialogTitle = computed(() =>
  isCreate.value
    ? t('admin.modelPricing.create.title')
    : t('admin.modelPricing.edit.title')
)

const saveLabel = computed(() =>
  isCreate.value ? t('admin.modelPricing.create.save') : t('admin.modelPricing.edit.save')
)

const platformChoices = computed(() => {
  const seen = new Set<string>()
  const options: Array<{ value: string; label: string }> = []
  for (const option of CONCRETE_PLATFORM_OPTIONS) {
    if (seen.has(option.value)) continue
    seen.add(option.value)
    options.push({ value: option.value, label: option.label })
  }
  for (const fallback of FALLBACK_PLATFORMS) {
    if (seen.has(fallback)) continue
    seen.add(fallback)
    options.push({ value: fallback, label: fallback })
  }
  return options
})

function inputId(field: UsdPerMtokField): string {
  return `model-price-${uid}-${field}`
}

function peakInputId(group: PeakGroupKey, field: string): string {
  return `model-price-${uid}-${group}-${field}`
}

function inputValue(item: ModelPricingItem | null, field: UsdPerMtokField): string {
  if (!item) return ''
  return formatPriceInput(item[field])
}

function peakSetValue(item: ModelPricingItem | null, group: PeakGroupKey, field: string): string {
  const set = item?.[group] as PeakPriceSet | undefined
  const value = set?.[field as keyof PeakPriceSet]
  return typeof value === 'number' ? formatPriceInput(value) : ''
}

function resetForm() {
  for (const field of USD_PER_MTOK_FIELDS) {
    values[field] = inputValue(props.item, field)
    initialStrings[field] = values[field]
    initialNumbers[field] = props.item ? props.item[field] : 0
    resetFlags[field] = false
    fieldErrors[field] = ''
  }
  for (const group of PEAK_GROUPS) {
    for (const field of PEAK_PRICE_FIELDS) {
      const value = peakSetValue(props.item, group.key, field)
      groupValues[group.key][field] = value
      initialPeak[group.key][field] = value
    }
  }
  peakEnabled.value = props.item?.peak_enabled === true
  initialPeakEnabled.value = peakEnabled.value
  peakTouched.value = false
  modelName.value = props.item?.model ?? ''
  platform.value = props.item?.platform ?? props.item?.provider ?? ''
  modelNameError.value = ''
  platformError.value = ''
  formError.value = ''
  restoreSnapshot.value = null
  restoreNotice.value = false
}

watch(
  [() => props.show, () => props.item, () => props.creating],
  ([show]) => {
    if (show) resetForm()
  },
  { immediate: true }
)

function clearResetFlag(field: UsdPerMtokField) {
  resetFlags[field] = false
  // 用户手动改过价格后，之前的还原快照已经不对应当前值，撤销会写回旧价：
  // 因此同时清掉快照与提示条，解除「已还原」锁，允许对新值重新执行还原。
  if (restoreSnapshot.value !== null) {
    restoreSnapshot.value = null
    restoreNotice.value = false
  }
}

function resetField(field: UsdPerMtokField) {
  values[field] = ''
  resetFlags[field] = true
  fieldErrors[field] = ''
}

function peakEnabledChanged() {
  peakTouched.value = true
}

function cnyPreview(field: UsdPerMtokField): string {
  const raw = values[field].trim()
  if (!raw) return '—'
  const value = Number(raw)
  if (!Number.isFinite(value)) return '—'
  return usdToCnyValue(value, props.mode, props.cnyPerUsd)
}

function parseNullable(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '') return null
  const value = Number(trimmed)
  return Number.isFinite(value) ? value : null
}

/**
 * 峰谷分组 → payload。
 *
 * 只提交「用户确实填了值」的键：空输入与「仍为 0 且原本就是 0/空」都视为未配置，
 * 直接省略（后端语义：缺省键回落到基础价）。用户把原本有值的项改成 0 时仍然提交，
 * 因为那是一次真实修改。
 */
function buildPeakSet(group: PeakGroupKey): PeakPriceSet {
  const out: PeakPriceSet = {}
  for (const field of PEAK_PRICE_FIELDS) {
    const value = parseNullable(groupValues[group][field])
    if (value === null) continue
    const initial = parseNullable(initialPeak[group][field])
    if (value === 0 && (initial === null || initial === 0)) continue
    out[field] = value
  }
  return out
}

const peakDirty = computed(() => {
  if (peakEnabled.value !== initialPeakEnabled.value) return true
  if (peakTouched.value) return true
  return PEAK_GROUPS.some((group) =>
    PEAK_PRICE_FIELDS.some(
      (field) => groupValues[group.key][field].trim() !== initialPeak[group.key][field].trim()
    )
  )
})

function buildEditPayload(): ModelPricingOverridePayload {
  const payload: ModelPricingOverridePayload = {}
  for (const field of USD_PER_MTOK_FIELDS) {
    if (resetFlags[field]) {
      payload[field] = null
      continue
    }
    const raw = values[field].trim()
    if (raw === '') {
      if (initialStrings[field] !== '') payload[field] = null
      continue
    }
    const value = Number(raw)
    if (!Number.isFinite(value)) continue
    if (value !== initialNumbers[field]) payload[field] = value
  }
  if (peakDirty.value) {
    payload.peak_enabled = peakEnabled.value
    if (peakEnabled.value) {
      payload.peak = buildPeakSet('peak')
      payload.off_peak = buildPeakSet('off_peak')
    }
  }
  return payload
}

function buildCreatePayload(): CreateModelPricingPayload {
  const payload: CreateModelPricingPayload = {
    model: modelName.value.trim().toLowerCase(),
    platform: platform.value.trim()
  }
  for (const field of USD_PER_MTOK_FIELDS) {
    const value = parseNullable(values[field])
    if (value !== null) payload[field] = value
  }
  if (peakEnabled.value) {
    payload.peak_enabled = true
    payload.peak = buildPeakSet('peak')
    payload.off_peak = buildPeakSet('off_peak')
  }
  return payload
}

const hasChanges = computed(() => {
  if (isCreate.value) return true
  return USD_PER_MTOK_FIELDS.some((field) => {
    if (resetFlags[field]) return true
    const raw = values[field].trim()
    if (raw === '') return initialStrings[field] !== ''
    const value = Number(raw)
    if (!Number.isFinite(value)) return true
    return value !== initialNumbers[field]
  }) || peakDirty.value
})

const canSave = computed(
  () => props.editable && !props.saving && (isCreate.value || props.item !== null) && hasChanges.value
)

// ===== 一键还原人民币价 =====
const restoreMultiplier = computed(() => cnyRestoreRate(props.mode, props.cnyPerUsd))
const restoreRateLabel = computed(() => formatRate(restoreMultiplier.value))
const isDirectMode = computed(() => props.mode === 'direct')
const restoreDisabled = computed(() => !props.editable || props.saving || isDirectMode.value)
const restoreDisabledReason = computed(() =>
  isDirectMode.value
    ? t('admin.modelPricing.edit.restore.directHint')
    : t('admin.modelPricing.edit.restore.hint')
)

function takeSnapshot() {
  return {
    values: Object.fromEntries(USD_PER_MTOK_FIELDS.map((f) => [f, values[f]])) as Record<
      UsdPerMtokField,
      string
    >,
    peak: {
      peak: { ...groupValues.peak },
      off_peak: { ...groupValues.off_peak }
    } as Record<PeakGroupKey, Record<string, string>>
  }
}

function applyRestore() {
  if (restoreDisabled.value) return
  // 幂等保护：一次编辑内只允许还原一次，避免连续点击把价格反复 ×汇率。
  // 需要再还原时先点「撤销还原」，或关闭后重新打开抽屉。
  if (restoreSnapshot.value !== null) return
  const rate = restoreMultiplier.value
  restoreSnapshot.value = takeSnapshot()

  for (const field of USD_PER_MTOK_FIELDS) {
    values[field] = restoreCnyPriceInput(values[field], rate)
  }
  if (peakEnabled.value) {
    for (const group of PEAK_GROUPS) {
      for (const field of PEAK_PRICE_FIELDS) {
        groupValues[group.key][field] = restoreCnyPriceInput(groupValues[group.key][field], rate)
      }
    }
    peakTouched.value = true
  }
  for (const field of USD_PER_MTOK_FIELDS) fieldErrors[field] = ''
  formError.value = ''
  restoreNotice.value = true
}

function undoRestore() {
  const snapshot = restoreSnapshot.value
  if (!snapshot) return
  for (const field of USD_PER_MTOK_FIELDS) values[field] = snapshot.values[field]
  groupValues.peak = { ...snapshot.peak.peak }
  groupValues.off_peak = { ...snapshot.peak.off_peak }
  restoreSnapshot.value = null
  restoreNotice.value = false
}

// ===== 峰谷回落提示 =====
const showOfficialMultiplierHint = computed(() => {
  if (!peakEnabled.value) return false
  const model = (props.item?.model ?? modelName.value).toLowerCase()
  return model.includes('deepseek')
})

const peakOrderWarning = computed(() => {
  if (!peakEnabled.value) return false
  const peakInput = parseNullable(groupValues.peak.input_usd_per_mtok)
  const offPeakInput = parseNullable(groupValues.off_peak.input_usd_per_mtok)
  if (peakInput === null || offPeakInput === null) return false
  return peakInput < offPeakInput
})

function validate(): boolean {
  let valid = true
  for (const field of USD_PER_MTOK_FIELDS) {
    fieldErrors[field] = ''
    if (resetFlags[field]) continue
    const raw = values[field].trim()
    if (raw === '') continue
    const value = Number(raw)
    if (!Number.isFinite(value)) {
      fieldErrors[field] = t('admin.modelPricing.edit.invalidNumber')
      valid = false
      continue
    }
    if (value < 0 || value >= MODEL_PRICE_USD_LIMIT) {
      fieldErrors[field] = t('admin.modelPricing.edit.outOfRange', {
        max: MODEL_PRICE_USD_LIMIT
      })
      valid = false
    }
  }
  if (peakEnabled.value) {
    for (const group of PEAK_GROUPS) {
      for (const field of PEAK_PRICE_FIELDS) {
        const raw = groupValues[group.key][field].trim()
        if (raw === '') continue
        const value = Number(raw)
        if (!Number.isFinite(value) || value < 0 || value >= MODEL_PRICE_USD_LIMIT) {
          valid = false
        }
      }
    }
  }
  return valid
}

function validateCreate(): boolean {
  modelNameError.value = ''
  platformError.value = ''
  if (!modelName.value.trim()) {
    modelNameError.value = t('admin.modelPricing.create.modelRequired')
    return false
  }
  if (!platform.value.trim()) {
    platformError.value = t('admin.modelPricing.create.platformRequired')
    return false
  }
  return true
}

function handleSave() {
  if (!props.editable || props.saving) return
  formError.value = ''
  if (isCreate.value) {
    if (!validateCreate() || !validate()) {
      if (!formError.value) formError.value = t('admin.modelPricing.edit.fixErrors')
      return
    }
    emit('create', buildCreatePayload())
    return
  }
  if (!props.item) return
  if (!validate()) {
    formError.value = t('admin.modelPricing.edit.fixErrors')
    return
  }
  const payload = buildEditPayload()
  if (Object.keys(payload).length === 0) {
    formError.value = t('admin.modelPricing.edit.noChanges')
    return
  }
  emit('save', payload)
}

function handleClose() {
  if (props.saving) return
  emit('close')
}

defineExpose({ applyRestore, undoRestore })
</script>
