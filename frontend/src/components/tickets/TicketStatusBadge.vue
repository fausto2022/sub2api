<template>
  <span :class="['badge', variantClass]" :data-status="status">
    {{ label }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TicketStatus } from '@/api/tickets'

const props = defineProps<{
  status: TicketStatus
}>()

const { t } = useI18n()

const variantClass = computed(() => {
  switch (props.status) {
    case 'open':
      return 'badge-warning'
    case 'pending':
      return 'badge-primary'
    case 'resolved':
      return 'badge-success'
    case 'closed':
      return 'badge-gray'
    default:
      return 'badge-gray'
  }
})

const label = computed(() => {
  switch (props.status) {
    case 'open':
      return t('tickets.statusLabels.open')
    case 'pending':
      return t('tickets.statusLabels.pending')
    case 'resolved':
      return t('tickets.statusLabels.resolved')
    case 'closed':
      return t('tickets.statusLabels.closed')
    default:
      return props.status
  }
})
</script>