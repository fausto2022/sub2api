<template>
  <DataTable :columns="columns" :data="items" :loading="loading" row-key="model">
    <template #cell-model="{ row }">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-1.5">
          <span class="font-mono text-sm font-medium text-gray-900 dark:text-white">
            {{ row.model }}
          </span>
          <span v-if="row.custom || row.overridden" class="badge badge-warning" :data-testid="`price-badge-custom-${row.model}`">
            {{ t('admin.modelPricing.installed.custom') }}
          </span>
          <span v-if="row.cny_source" class="badge badge-success" :data-testid="`price-badge-cny-${row.model}`">
            {{ t('admin.modelPricing.installed.cnySource') }}
          </span>
        </div>
        <div class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
          <span>{{ row.platform || row.provider || '—' }}</span>
          <template v-if="row.mode">
            <span class="mx-1 text-gray-300 dark:text-dark-600">·</span>
            <span>{{ row.mode }}</span>
          </template>
        </div>
      </div>
    </template>

    <template #cell-input="{ row }">
      <span class="tabular-nums" data-testid="price-usd-input">{{ formatUsdPerMTok(row.input_usd_per_mtok) }}</span>
    </template>
    <template #cell-output="{ row }">
      <span class="tabular-nums" data-testid="price-usd-output">{{ formatUsdPerMTok(row.output_usd_per_mtok) }}</span>
    </template>
    <template #cell-cacheRead="{ row }">
      <span class="tabular-nums">{{ formatUsdPerMTok(row.cache_read_usd_per_mtok) }}</span>
    </template>
    <template #cell-cacheWrite="{ row }">
      <span class="tabular-nums">{{ formatUsdPerMTok(row.cache_write_usd_per_mtok) }}</span>
    </template>

    <template #cell-peak="{ row }">
      <div v-if="row.peak_enabled" class="text-xs tabular-nums leading-5" :data-testid="`price-peak-${row.model}`">
        <div class="text-amber-700 dark:text-amber-300">
          峰 ${{ formatUsdPerMTok(row.peak?.input_usd_per_mtok) }} / ${{ formatUsdPerMTok(row.peak?.output_usd_per_mtok) }}
        </div>
        <div class="text-emerald-700 dark:text-emerald-300">
          谷 ${{ formatUsdPerMTok(row.off_peak?.input_usd_per_mtok) }} / ${{ formatUsdPerMTok(row.off_peak?.output_usd_per_mtok) }}
        </div>
      </div>
      <span v-else class="text-xs text-gray-400 dark:text-dark-500">—</span>
    </template>

    <template #cell-source="{ row }">
      <span class="badge" :class="sourceClass(row.source)" :data-testid="`price-source-${row.model}`">
        {{ sourceLabel(row.source) }}
      </span>
    </template>

    <template #cell-actions="{ row }">
      <div class="flex items-center justify-end gap-1">
        <button
          type="button"
          class="flex items-center gap-1 rounded-lg px-2 py-1 text-sm text-primary-600 transition-colors hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-40 dark:text-primary-400 dark:hover:bg-primary-900/20"
          :disabled="!editable"
          :title="t('admin.modelPricing.installed.edit')"
          :data-testid="`price-edit-${row.model}`"
          @click="emit('edit', row)"
        >
          <Icon name="edit" size="sm" />
          {{ t('admin.modelPricing.installed.edit') }}
        </button>
        <button
          v-if="row.overridden"
          type="button"
          class="flex items-center gap-1 rounded-lg px-2 py-1 text-sm text-amber-600 transition-colors hover:bg-amber-50 disabled:cursor-not-allowed disabled:opacity-40 dark:text-amber-400 dark:hover:bg-amber-900/20"
          :disabled="!editable"
          :title="t('admin.modelPricing.installed.restore')"
          :data-testid="`price-restore-${row.model}`"
          @click="emit('restore', row)"
        >
          <Icon name="refresh" size="sm" />
          {{ t('admin.modelPricing.installed.restore') }}
        </button>
        <button
          v-if="row.custom"
          type="button"
          class="flex items-center gap-1 rounded-lg px-2 py-1 text-sm text-red-600 transition-colors hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-40 dark:text-red-400 dark:hover:bg-red-900/20"
          :disabled="!editable"
          :title="t('admin.modelPricing.installed.delete')"
          :data-testid="`price-delete-${row.model}`"
          @click="emit('delete', row)"
        >
          <Icon name="trash" size="sm" />
          {{ t('admin.modelPricing.installed.delete') }}
        </button>
      </div>
    </template>

    <template #empty>
      <EmptyState
        :title="t('admin.modelPricing.installed.emptyTitle')"
        :description="t('admin.modelPricing.installed.emptyDescription')"
      />
    </template>
  </DataTable>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Column } from '@/components/common/types'
import type { ModelPricingItem } from '@/api/admin/modelPricing'
import DataTable from '@/components/common/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatUsdPerMTok } from './priceFormat'

defineProps<{
  items: ModelPricingItem[]
  loading: boolean
  editable: boolean
}>()

const emit = defineEmits<{
  (e: 'edit', item: ModelPricingItem): void
  (e: 'restore', item: ModelPricingItem): void
  (e: 'delete', item: ModelPricingItem): void
}>()

const { t, te } = useI18n()

const columns = computed<Column[]>(() => [
  { key: 'model', label: t('admin.modelPricing.installed.columns.model') },
  { key: 'input', label: t('admin.modelPricing.installed.columns.input'), class: 'text-right' },
  { key: 'output', label: t('admin.modelPricing.installed.columns.output'), class: 'text-right' },
  { key: 'cacheRead', label: t('admin.modelPricing.installed.columns.cacheRead'), class: 'text-right' },
  { key: 'cacheWrite', label: t('admin.modelPricing.installed.columns.cacheWrite'), class: 'text-right' },
  { key: 'peak', label: t('admin.modelPricing.installed.columns.peak'), class: 'text-right' },
  { key: 'source', label: t('admin.modelPricing.installed.columns.source') },
  { key: 'actions', label: t('admin.modelPricing.installed.columns.actions'), class: 'text-right' }
])

function sourceLabel(source: string): string {
  const key = `admin.modelPricing.sources.${source || 'unknown'}`
  return te(key) ? t(key) : t('admin.modelPricing.sources.unknown')
}

function sourceClass(source: string): string {
  switch (source) {
    case 'litellm':
      return 'badge-primary'
    case 'catalog':
      return 'badge-purple'
    case 'cny':
      return 'badge-success'
    case 'override':
      return 'badge-warning'
    default:
      return 'badge-gray'
  }
}
</script>
