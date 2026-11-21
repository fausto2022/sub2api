<template>
  <BaseDialog
    :show="show"
    :title="t('admin.modelPricing.peakSettings.title')"
    width="normal"
    @close="handleClose"
  >
    <div class="space-y-4" data-testid="peak-settings-body">
      <p class="text-xs text-gray-500 dark:text-dark-400">
        {{ t('admin.modelPricing.peakSettings.description') }}
      </p>

      <div
        v-if="props.errorText"
        class="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
        data-testid="peak-settings-error"
      >
        {{ props.errorText }}
      </div>

      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div class="space-y-1.5">
          <label :for="timezoneId" class="input-label block">
            {{ t('admin.modelPricing.peakSettings.timezone') }}
          </label>
          <input
            :id="timezoneId"
            v-model="timezone"
            type="text"
            class="input w-full"
            :disabled="saving || !editable"
            data-testid="peak-settings-timezone"
          />
        </div>
        <div class="space-y-2 pt-6">
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input
              v-model="weekdayOnly"
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
              :disabled="saving || !editable"
              data-testid="peak-settings-weekday-only"
            />
            {{ t('admin.modelPricing.peakSettings.weekdayOnly') }}
          </label>
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input
              v-model="weekendOffPeak"
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
              :disabled="saving || !editable"
              data-testid="peak-settings-weekend-off-peak"
            />
            {{ t('admin.modelPricing.peakSettings.weekendOffPeak') }}
          </label>
        </div>
      </div>

      <div>
        <div class="mb-2 flex items-center justify-between">
          <span class="text-sm font-medium text-gray-800 dark:text-gray-100">
            {{ t('admin.modelPricing.peakSettings.periodsTitle') }}
          </span>
          <button
            type="button"
            class="btn btn-secondary px-2 py-1 text-xs"
            :disabled="saving || !editable"
            data-testid="peak-settings-add-period"
            @click="addPeriod"
          >
            <Icon name="plus" size="sm" class="mr-1" />
            {{ t('admin.modelPricing.peakSettings.addPeriod') }}
          </button>
        </div>

        <div v-if="periods.length === 0" class="rounded-xl bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:bg-dark-800/60 dark:text-dark-400" data-testid="peak-settings-empty">
          {{ t('admin.modelPricing.peakSettings.emptyHint') }}
        </div>

        <ul class="space-y-2">
          <li
            v-for="(period, index) in periods"
            :key="index"
            class="flex flex-wrap items-center gap-2"
            :data-testid="`peak-settings-period-${index}`"
          >
            <input
              v-model="period.start"
              type="text"
              inputmode="numeric"
              class="input w-24 tabular-nums"
              :placeholder="t('admin.modelPricing.peakSettings.startPlaceholder')"
              :disabled="saving || !editable"
              :data-testid="`peak-settings-period-start-${index}`"
            />
            <span class="text-gray-400">—</span>
            <input
              v-model="period.end"
              type="text"
              inputmode="numeric"
              class="input w-24 tabular-nums"
              :placeholder="t('admin.modelPricing.peakSettings.endPlaceholder')"
              :disabled="saving || !editable"
              :data-testid="`peak-settings-period-end-${index}`"
            />
            <button
              type="button"
              class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-red-900/20"
              :disabled="saving || !editable"
              :title="t('admin.modelPricing.peakSettings.removePeriod')"
              :data-testid="`peak-settings-remove-period-${index}`"
              @click="removePeriod(index)"
            >
              <Icon name="trash" size="sm" />
            </button>
          </li>
        </ul>
        <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.modelPricing.peakSettings.periodHint') }}
        </p>
      </div>
    </div>

    <template #footer>
      <div class="ml-auto flex items-center gap-2">
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="!canSave"
          data-testid="peak-settings-save"
          @click="handleSave"
        >
          {{ saving ? t('admin.modelPricing.peakSettings.saving') : t('admin.modelPricing.peakSettings.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PeakPricingPeriod, PeakPricingSettings } from '@/api/admin/modelPricing'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(
  defineProps<{
    show: boolean
    settings: PeakPricingSettings | null
    editable: boolean
    saving?: boolean
    errorText?: string
  }>(),
  {
    saving: false,
    errorText: ''
  }
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'save', payload: PeakPricingSettings): void
}>()

const { t } = useI18n()
const uid = useId()
const timezoneId = `peak-timezone-${uid}`

const timezone = ref('Asia/Shanghai')
const weekdayOnly = ref(true)
const weekendOffPeak = ref(true)
const periods = reactive<PeakPricingPeriod[]>([])

function resetForm() {
  const settings = props.settings
  timezone.value = settings?.timezone || 'Asia/Shanghai'
  weekdayOnly.value = settings?.weekday_only ?? true
  weekendOffPeak.value = settings?.weekend_off_peak ?? true
  periods.splice(0, periods.length)
  for (const period of settings?.periods ?? []) {
    periods.push({ start: period.start, end: period.end })
  }
}

watch(
  [() => props.show, () => props.settings],
  ([show]) => {
    if (show) resetForm()
  },
  { immediate: true }
)

function addPeriod() {
  periods.push({ start: '09:00', end: '12:00' })
}

function removePeriod(index: number) {
  periods.splice(index, 1)
}

const canSave = computed(() => props.editable && !props.saving)

function handleSave() {
  if (!canSave.value) return
  emit('save', {
    timezone: timezone.value.trim() || 'Asia/Shanghai',
    weekday_only: weekdayOnly.value,
    weekend_off_peak: weekendOffPeak.value,
    periods: periods.map((period) => ({
      start: period.start.trim(),
      end: period.end.trim()
    }))
  })
}

function handleClose() {
  if (props.saving) return
  emit('close')
}
</script>
