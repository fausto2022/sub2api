<template>
  <span
    class="inline-flex min-w-0 flex-col gap-0.5"
    :data-testid="testId"
    :data-source="kind"
    :title="tooltip"
  >
    <span v-if="kind === 'opsmgmt'" class="badge badge-primary max-w-full">
      <!-- ⬢ 六边形：内联 SVG（MASTER 禁止 emoji 当图标） -->
      <svg class="mr-1 h-3 w-3 shrink-0" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path
          d="M8 1.5 13.6 4.75v6.5L8 14.5 2.4 11.25v-6.5L8 1.5Z"
          stroke="currentColor"
          stroke-width="1.5"
          stroke-linejoin="round"
        />
      </svg>
      <span class="truncate">{{ t('admin.opsmgmt.source.opsmgmt') }}</span>
    </span>
    <span v-else class="text-xs text-accent-600 dark:text-dark-300">
      {{ t('admin.opsmgmt.source.manual') }}
    </span>

    <span
      v-if="kind === 'opsmgmt' && siteName"
      class="max-w-[12rem] truncate text-[11px] text-accent-600 dark:text-dark-300"
      :data-testid="`${testId}-site`"
    >
      {{ siteName }}
    </span>

    <span
      v-if="autoDisabledReasonText"
      class="badge badge-danger max-w-full"
      :data-testid="`${testId}-reason`"
    >
      {{ autoDisabledReasonText }}
    </span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SourceKind } from './types'

const props = withDefaults(
  defineProps<{
    kind: SourceKind
    siteName?: string
    siteId?: number | null
    remoteGroupId?: number | null
    autoDisabledReason?: string
    autoDisabledAt?: string | null
    testId?: string
  }>(),
  {
    siteName: '',
    siteId: null,
    remoteGroupId: null,
    autoDisabledReason: '',
    autoDisabledAt: null,
    testId: 'source-badge'
  }
)

const { t } = useI18n()

/** 停用原因：已知枚举走 i18n，未知值原样显示（后端 reason 是 varchar(64)）。 */
const autoDisabledReasonText = computed(() => {
  const reason = (props.autoDisabledReason || '').trim()
  if (!reason) return ''
  if (reason === 'margin') return t('admin.opsmgmt.source.reason.margin')
  return reason
})

const tooltip = computed(() => {
  if (props.kind !== 'opsmgmt') return t('admin.opsmgmt.source.manualHint')
  const parts: string[] = [t('admin.opsmgmt.source.opsmgmt')]
  if (props.siteName) parts.push(`${t('admin.opsmgmt.source.site')}: ${props.siteName}`)
  else if (props.siteId != null) parts.push(`${t('admin.opsmgmt.source.siteId')}: ${props.siteId}`)
  if (props.remoteGroupId != null) {
    parts.push(`${t('admin.opsmgmt.source.remoteGroup')}: ${props.remoteGroupId}`)
  }
  if (autoDisabledReasonText.value) {
    parts.push(`${t('admin.opsmgmt.source.disabledReason')}: ${autoDisabledReasonText.value}`)
  }
  if (props.autoDisabledAt) parts.push(`${t('admin.opsmgmt.source.disabledAt')}: ${props.autoDisabledAt}`)
  return parts.join(' · ')
})
</script>
