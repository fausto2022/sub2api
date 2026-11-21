<template>
  <section
    class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900"
    data-testid="currency-mode-card"
  >
    <div class="flex flex-wrap items-start justify-between gap-2">
      <div>
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.modelPricing.currency.mode.title') }}
        </h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.currency.mode.description') }}
        </p>
      </div>
      <span class="badge badge-gray" data-testid="currency-current-mode">
        {{ t('admin.modelPricing.currency.mode.current', { mode: modeLabel(props.mode) }) }}
      </span>
    </div>

    <div class="mt-4 grid gap-3 sm:grid-cols-2" role="radiogroup" :aria-label="t('admin.modelPricing.currency.mode.title')">
      <button
        type="button"
        role="radio"
        :aria-checked="draftMode === 'direct'"
        :disabled="!editable || saving"
        class="rounded-xl border p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60"
        :class="modeButtonClass('direct')"
        data-testid="currency-mode-direct"
        @click="draftMode = 'direct'"
      >
        <span class="block text-sm font-medium">{{ t('admin.modelPricing.currency.mode.direct') }}</span>
        <span class="mt-1 block text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.currency.mode.directHint') }}
        </span>
      </button>
      <button
        type="button"
        role="radio"
        :aria-checked="draftMode === 'convert'"
        :disabled="!editable || saving"
        class="rounded-xl border p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60"
        :class="modeButtonClass('convert')"
        data-testid="currency-mode-convert"
        @click="draftMode = 'convert'"
      >
        <span class="block text-sm font-medium">{{ t('admin.modelPricing.currency.mode.convert') }}</span>
        <span class="mt-1 block text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.currency.mode.convertHint') }}
        </span>
      </button>
    </div>

    <div class="mt-4 grid grid-cols-1 items-start gap-4 sm:grid-cols-2">
      <div>
        <label for="currency-rate" class="input-label mb-1.5 block">
          {{ t('admin.modelPricing.currency.mode.rateLabel') }}
        </label>
        <input
          id="currency-rate"
          v-model="rateInput"
          type="text"
          inputmode="decimal"
          class="input w-full tabular-nums"
          :disabled="!editable || saving || draftMode !== 'convert'"
          :aria-invalid="!!rateError"
          data-testid="currency-rate-input"
        />
        <p v-if="rateError" class="input-error-text mt-1" data-testid="currency-rate-error">
          {{ rateError }}
        </p>
        <p v-else class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.currency.mode.rateHint') }}
        </p>
      </div>

      <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-800/60" data-testid="currency-preview">
        <p class="text-xs font-medium text-gray-700 dark:text-gray-200">
          {{ t('admin.modelPricing.currency.preview.title') }}
        </p>
        <template v-if="sample">
          <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.modelPricing.currency.preview.sample', { model: sample.model, cny: formatCnyPerMTok(sample.inputCny) }) }}
          </p>
          <ul class="mt-2 space-y-1">
            <li
              v-for="row in previewRows"
              :key="row.key"
              class="flex flex-wrap items-center justify-between gap-2 text-xs tabular-nums"
              :data-testid="`currency-preview-${row.key}`"
            >
              <span class="badge" :class="row.key === 'direct' ? 'badge-gray' : 'badge-primary'">{{ row.label }}</span>
              <span class="text-gray-700 dark:text-gray-300">${{ row.input }} / ${{ row.output }}</span>
            </li>
          </ul>
        </template>
        <p v-else class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.currency.preview.empty') }}
        </p>
      </div>
    </div>

    <div class="mt-4 flex justify-end">
      <button
        type="button"
        class="btn btn-primary"
        :disabled="!canSave"
        data-testid="currency-save"
        @click="handleSave"
      >
        {{ saving ? t('admin.modelPricing.currency.mode.saving') : t('admin.modelPricing.currency.mode.save') }}
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  CurrencyMode,
  UpdateModelPricingCurrencyPayload
} from '@/api/admin/modelPricing'
import { cnyToUsdRate, formatCnyPerMTok, formatUsdPerMTok } from './priceFormat'

export interface CurrencyPreviewSample {
  model: string
  inputCny: number
  outputCny: number
}

const props = withDefaults(
  defineProps<{
    mode: CurrencyMode
    cnyPerUsd: number
    editable: boolean
    saving?: boolean
    sample?: CurrencyPreviewSample | null
  }>(),
  {
    saving: false,
    sample: null
  }
)

const emit = defineEmits<{
  (e: 'save', payload: UpdateModelPricingCurrencyPayload): void
}>()

const { t } = useI18n()

const draftMode = ref<CurrencyMode>(props.mode)
const rateInput = ref(String(props.cnyPerUsd))

watch(
  () => props.mode,
  (mode) => {
    draftMode.value = mode
  }
)

watch(
  () => props.cnyPerUsd,
  (rate) => {
    rateInput.value = String(rate)
  }
)

function modeLabel(mode: CurrencyMode): string {
  return mode === 'direct'
    ? t('admin.modelPricing.currency.mode.direct')
    : t('admin.modelPricing.currency.mode.convert')
}

function modeButtonClass(mode: CurrencyMode): string {
  const selected = draftMode.value === mode
  return selected
    ? 'border-primary-500 bg-primary-50 ring-1 ring-primary-500/40 dark:border-primary-500 dark:bg-primary-900/20'
    : 'border-gray-200 hover:border-primary-300 dark:border-dark-700 dark:hover:border-primary-700'
}

const rateError = computed(() => {
  if (draftMode.value !== 'convert') return ''
  const raw = rateInput.value.trim()
  if (!raw) return t('admin.modelPricing.currency.mode.rateRequired')
  const value = Number(raw)
  if (!Number.isFinite(value) || value <= 0) {
    return t('admin.modelPricing.currency.mode.rateInvalid')
  }
  return ''
})

const effectiveRate = computed(() =>
  cnyToUsdRate('convert', Number(rateInput.value) || props.cnyPerUsd)
)

const dirty = computed(() => {
  if (draftMode.value !== props.mode) return true
  if (draftMode.value !== 'convert') return false
  return Number(rateInput.value) !== props.cnyPerUsd
})

const canSave = computed(
  () => props.editable && !props.saving && dirty.value && !rateError.value
)

const previewRows = computed(() => {
  if (!props.sample) return []
  const rate = effectiveRate.value
  return [
    {
      key: 'direct' as const,
      label: t('admin.modelPricing.currency.preview.direct'),
      input: formatUsdPerMTok(props.sample.inputCny),
      output: formatUsdPerMTok(props.sample.outputCny)
    },
    {
      key: 'convert' as const,
      label: t('admin.modelPricing.currency.preview.convert', { rate }),
      input: formatUsdPerMTok(props.sample.inputCny / rate),
      output: formatUsdPerMTok(props.sample.outputCny / rate)
    }
  ]
})

function handleSave() {
  if (!canSave.value) return
  const payload: UpdateModelPricingCurrencyPayload = {
    mode: draftMode.value,
    cny_per_usd:
      draftMode.value === 'convert' ? Number(rateInput.value) : props.cnyPerUsd
  }
  emit('save', payload)
}
</script>
