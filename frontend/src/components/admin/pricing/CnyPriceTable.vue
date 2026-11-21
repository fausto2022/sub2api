<template>
  <div class="flex min-h-0 flex-col">
    <div class="flex flex-wrap items-center justify-between gap-2 px-4 pb-2 pt-4">
      <div>
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.modelPricing.currency.table.title') }}
        </h2>
        <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.currency.table.description') }}
        </p>
      </div>
      <button
        type="button"
        class="btn btn-primary text-sm"
        :disabled="!editable || saving || editing"
        data-testid="cny-add-model"
        @click="beginCreate"
      >
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('admin.modelPricing.currency.table.addModel') }}
      </button>
    </div>

    <!-- Seed models: 预填后由管理员确认保存，不自动写入 -->
    <div
      v-if="seeds.length > 0"
      class="mx-4 mb-3 rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-800/60"
      data-testid="cny-seeds"
    >
      <div class="flex flex-wrap items-center justify-between gap-1">
        <span class="text-xs font-medium text-gray-700 dark:text-gray-200">
          {{ t('admin.modelPricing.currency.seeds.title') }}
        </span>
        <span class="text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.currency.seeds.hint') }}
        </span>
      </div>
      <ul class="mt-2 grid gap-2 sm:grid-cols-3">
        <li
          v-for="seed in seeds"
          :key="seed.model"
          class="rounded-lg border border-gray-200 bg-white p-2.5 dark:border-dark-700 dark:bg-dark-900"
        >
          <div class="font-mono text-xs font-medium text-gray-900 dark:text-white">{{ seed.model }}</div>
          <div class="mt-1 text-xs tabular-nums text-gray-500 dark:text-dark-400">
            ¥{{ formatCnyPerMTok(seed.input_cny_per_mtok) }} / ¥{{ formatCnyPerMTok(seed.output_cny_per_mtok) }}
          </div>
          <button
            type="button"
            class="btn btn-secondary mt-2 w-full px-2 py-1 text-xs"
            :disabled="!editable || saving || isSeedRegistered(seed.model)"
            :data-testid="`cny-seed-import-${seed.model}`"
            @click="beginSeed(seed)"
          >
            {{
              isSeedRegistered(seed.model)
                ? t('admin.modelPricing.currency.seeds.registered')
                : t('admin.modelPricing.currency.seeds.import')
            }}
          </button>
        </li>
      </ul>
    </div>

    <!-- Inline editor -->
    <div
      v-if="editing"
      class="mx-4 mb-3 rounded-xl border border-primary-200 bg-primary-50/40 p-4 dark:border-primary-900/60 dark:bg-primary-900/10"
      data-testid="cny-price-editor"
    >
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{
          editingOriginal
            ? t('admin.modelPricing.currency.table.editTitle', { model: editingOriginal })
            : t('admin.modelPricing.currency.table.addTitle')
        }}
      </h3>
      <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div class="space-y-1">
          <label for="cny-model-name" class="input-label block">
            {{ t('admin.modelPricing.currency.table.modelLabel') }}
          </label>
          <input
            id="cny-model-name"
            v-model="modelInput"
            type="text"
            class="input w-full font-mono"
            :disabled="saving || !!editingOriginal"
            :placeholder="t('admin.modelPricing.currency.table.modelPlaceholder')"
            data-testid="cny-model-input"
            @input="modelError = ''"
          />
          <p v-if="modelError" class="input-error-text" data-testid="cny-model-error">{{ modelError }}</p>
        </div>
        <div v-for="field in CNY_FIELDS" :key="field" class="space-y-1">
          <label :for="`cny-input-${field}`" class="input-label block">
            {{ t(FIELD_LABEL_KEYS[field]) }}
          </label>
          <input
            :id="`cny-input-${field}`"
            v-model="inputs[field]"
            type="text"
            inputmode="decimal"
            class="input w-full tabular-nums"
            :disabled="saving"
            :data-testid="`cny-input-${field}`"
            @input="fieldErrors[field] = ''"
          />
          <p class="text-xs tabular-nums text-gray-500 dark:text-dark-400" :data-testid="`cny-usd-preview-${field}`">
            {{ t('admin.modelPricing.currency.table.usdPreview') }} ${{ usdPreview(field) }}
          </p>
          <p v-if="fieldErrors[field]" class="input-error-text">{{ fieldErrors[field] }}</p>
        </div>
      </div>

      <p v-if="formError" class="input-error-text mt-3" data-testid="cny-form-error">{{ formError }}</p>
      <p v-if="props.errorText" class="input-error-text mt-3" data-testid="cny-save-error">{{ props.errorText }}</p>

      <div class="mt-3 flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" :disabled="saving" data-testid="cny-cancel" @click="closeEditor">
          {{ t('common.cancel') }}
        </button>
        <button type="button" class="btn btn-primary" :disabled="saving" data-testid="cny-save" @click="handleSave">
          {{ saving ? t('admin.modelPricing.currency.table.saving') : t('admin.modelPricing.currency.table.save') }}
        </button>
      </div>
    </div>

    <p v-if="!editing && props.errorText" class="mx-4 mb-2 text-sm text-red-600 dark:text-red-400" data-testid="cny-save-error">
      {{ props.errorText }}
    </p>

    <DataTable :columns="columns" :data="models" :loading="loading" row-key="model">
      <template #cell-model="{ row }">
        <span class="font-mono text-sm font-medium text-gray-900 dark:text-white">{{ row.model }}</span>
      </template>
      <template #cell-input="{ row }">
        <span class="tabular-nums">{{ formatCnyPerMTok(row.input_cny_per_mtok) }}</span>
      </template>
      <template #cell-output="{ row }">
        <span class="tabular-nums">{{ formatCnyPerMTok(row.output_cny_per_mtok) }}</span>
      </template>
      <template #cell-cacheRead="{ row }">
        <span class="tabular-nums">{{ formatCnyPerMTok(row.cache_read_cny_per_mtok) }}</span>
      </template>
      <template #cell-cacheWrite="{ row }">
        <span class="tabular-nums">{{ formatCnyPerMTok(row.cache_write_cny_per_mtok) }}</span>
      </template>
      <template #cell-imageInput="{ row }">
        <span class="tabular-nums">{{ formatCnyPerMTok(row.image_input_cny_per_mtok) }}</span>
      </template>
      <template #cell-effectiveInput="{ row }">
        <span class="tabular-nums text-xs text-gray-600 dark:text-gray-300">
          {{ formatUsdPerMTok(row.effective_input_usd_per_mtok) }}
        </span>
      </template>
      <template #cell-effectiveOutput="{ row }">
        <span class="tabular-nums text-xs text-gray-600 dark:text-gray-300">
          {{ formatUsdPerMTok(row.effective_output_usd_per_mtok) }}
        </span>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1">
          <button
            type="button"
            class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-dark-700 dark:hover:text-gray-300"
            :disabled="!editable || saving"
            :title="t('admin.modelPricing.currency.table.edit')"
            :data-testid="`cny-edit-${row.model}`"
            @click="beginEdit(row)"
          >
            <Icon name="edit" size="sm" />
          </button>
          <button
            type="button"
            class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-red-900/20 dark:hover:text-red-400"
            :disabled="!editable || saving"
            :title="t('admin.modelPricing.currency.table.delete')"
            :data-testid="`cny-delete-${row.model}`"
            @click="emit('delete', row.model)"
          >
            <Icon name="trash" size="sm" />
          </button>
        </div>
      </template>
      <template #empty>
        <EmptyState
          :title="t('admin.modelPricing.currency.table.emptyTitle')"
          :description="t('admin.modelPricing.currency.table.emptyDescription')"
        />
      </template>
    </DataTable>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CNY_PER_MTOK_FIELDS,
  MODEL_PRICE_CNY_LIMIT,
  type CnyPerMtokField,
  type CurrencyMode,
  type ModelPricingCnyPayload,
  type ModelPricingCurrencyItem
} from '@/api/admin/modelPricing'
import type { Column } from '@/components/common/types'
import DataTable from '@/components/common/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  cnyToUsdRate,
  formatCnyPerMTok,
  formatPriceInput,
  formatUsdPerMTok
} from './priceFormat'

const props = withDefaults(
  defineProps<{
    models: ModelPricingCurrencyItem[]
    seeds: ModelPricingCurrencyItem[]
    mode: CurrencyMode
    cnyPerUsd: number
    editable: boolean
    loading?: boolean
    saving?: boolean
    errorText?: string
  }>(),
  {
    loading: false,
    saving: false,
    errorText: ''
  }
)

const emit = defineEmits<{
  (e: 'save', model: string, payload: ModelPricingCnyPayload): void
  (e: 'delete', model: string): void
}>()

const { t } = useI18n()

const CNY_FIELDS = CNY_PER_MTOK_FIELDS

const FIELD_LABEL_KEYS: Record<CnyPerMtokField, string> = {
  input_cny_per_mtok: 'admin.modelPricing.currency.table.fields.input',
  output_cny_per_mtok: 'admin.modelPricing.currency.table.fields.output',
  cache_read_cny_per_mtok: 'admin.modelPricing.currency.table.fields.cacheRead',
  cache_write_cny_per_mtok: 'admin.modelPricing.currency.table.fields.cacheWrite',
  image_input_cny_per_mtok: 'admin.modelPricing.currency.table.fields.imageInput'
}

const columns = computed<Column[]>(() => [
  { key: 'model', label: t('admin.modelPricing.currency.table.columns.model') },
  { key: 'input', label: t('admin.modelPricing.currency.table.columns.input'), class: 'text-right' },
  { key: 'output', label: t('admin.modelPricing.currency.table.columns.output'), class: 'text-right' },
  { key: 'cacheRead', label: t('admin.modelPricing.currency.table.columns.cacheRead'), class: 'text-right' },
  { key: 'cacheWrite', label: t('admin.modelPricing.currency.table.columns.cacheWrite'), class: 'text-right' },
  { key: 'imageInput', label: t('admin.modelPricing.currency.table.columns.imageInput'), class: 'text-right' },
  { key: 'effectiveInput', label: t('admin.modelPricing.currency.table.columns.effectiveInput'), class: 'text-right' },
  { key: 'effectiveOutput', label: t('admin.modelPricing.currency.table.columns.effectiveOutput'), class: 'text-right' },
  { key: 'actions', label: t('admin.modelPricing.currency.table.columns.actions'), class: 'text-right' }
])

const editing = ref(false)
const editingOriginal = ref<string | null>(null)
const modelInput = ref('')
const modelError = ref('')
const formError = ref('')
const inputs = reactive<Record<CnyPerMtokField, string>>(
  Object.fromEntries(CNY_FIELDS.map((field) => [field, ''])) as Record<CnyPerMtokField, string>
)
const fieldErrors = reactive<Record<CnyPerMtokField, string>>(
  Object.fromEntries(CNY_FIELDS.map((field) => [field, ''])) as Record<CnyPerMtokField, string>
)

function clearInputs() {
  for (const field of CNY_FIELDS) {
    inputs[field] = ''
    fieldErrors[field] = ''
  }
  modelError.value = ''
  formError.value = ''
}

function beginCreate() {
  editing.value = true
  editingOriginal.value = null
  modelInput.value = ''
  clearInputs()
}

function beginEdit(item: ModelPricingCurrencyItem) {
  editing.value = true
  editingOriginal.value = item.model
  modelInput.value = item.model
  clearInputs()
  for (const field of CNY_FIELDS) {
    inputs[field] = formatPriceInput(item[field])
  }
}

function beginSeed(seed: ModelPricingCurrencyItem) {
  editing.value = true
  editingOriginal.value = null
  modelInput.value = seed.model
  clearInputs()
  for (const field of CNY_FIELDS) {
    inputs[field] = formatPriceInput(seed[field])
  }
}

function closeEditor() {
  editing.value = false
  editingOriginal.value = null
  modelInput.value = ''
  clearInputs()
}

function isSeedRegistered(model: string): boolean {
  return props.models.some((item) => item.model === model)
}

function usdPreview(field: CnyPerMtokField): string {
  const raw = inputs[field].trim()
  if (!raw) return '—'
  const value = Number(raw)
  if (!Number.isFinite(value)) return '—'
  const rate = cnyToUsdRate(props.mode, props.cnyPerUsd)
  return formatUsdPerMTok(value / rate)
}

function handleSave() {
  formError.value = ''
  modelError.value = ''
  for (const field of CNY_FIELDS) fieldErrors[field] = ''

  const model = modelInput.value.trim().toLowerCase()
  if (!model) {
    modelError.value = t('admin.modelPricing.currency.table.modelRequired')
    return
  }

  const payload: ModelPricingCnyPayload = {}
  let invalid = false
  let hasPositive = false
  for (const field of CNY_FIELDS) {
    const raw = inputs[field].trim()
    if (raw === '') {
      payload[field] = 0
      continue
    }
    const value = Number(raw)
    if (!Number.isFinite(value)) {
      fieldErrors[field] = t('admin.modelPricing.currency.table.invalidNumber')
      invalid = true
      continue
    }
    if (value < 0 || value >= MODEL_PRICE_CNY_LIMIT) {
      fieldErrors[field] = t('admin.modelPricing.currency.table.outOfRange', {
        max: MODEL_PRICE_CNY_LIMIT
      })
      invalid = true
      continue
    }
    payload[field] = value
    if (value > 0) hasPositive = true
  }

  if (invalid) return
  if (!hasPositive) {
    formError.value = t('admin.modelPricing.currency.table.allEmpty')
    return
  }
  emit('save', model, payload)
}

defineExpose({ closeEditor, beginCreate })
</script>
