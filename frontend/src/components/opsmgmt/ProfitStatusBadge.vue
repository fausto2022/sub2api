<template>
  <span class="badge" :class="badgeClass" :data-testid="testId" :data-status="status">
    {{ label }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpsProfitStatus } from '@/api/admin/opsmgmt'

const props = withDefaults(
  defineProps<{
    status: OpsProfitStatus
    testId?: string
  }>(),
  {
    testId: 'profit-status-badge'
  }
)

const { t } = useI18n()

const badgeClass = computed(() => {
  switch (props.status) {
    case 'healthy':
      return 'badge-success'
    case 'risk':
      return 'badge-danger'
    case 'unsupported':
      return 'badge-warning'
    default:
      return 'badge-gray'
  }
})

const label = computed(() => {
  switch (props.status) {
    case 'healthy':
      return t('admin.opsmgmt.profit.status.healthy')
    case 'risk':
      return t('admin.opsmgmt.profit.status.risk')
    case 'unsupported':
      return t('admin.opsmgmt.profit.status.unsupported')
    default:
      return t('admin.opsmgmt.profit.status.unknown')
  }
})
</script>
