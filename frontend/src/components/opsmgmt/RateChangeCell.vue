<template>
  <span class="inline-flex flex-wrap items-center gap-1 tabular-nums" :data-testid="testId" :data-direction="direction">
    <span class="font-medium" :class="valueClass" data-testid="rate-current">{{ formatMultiplier(current, digits) }}</span>

    <template v-if="changed">
      <Icon :name="direction === 'down' ? 'arrowDown' : 'arrowUp'" size="xs" :class="valueClass" aria-hidden="true" />
      <!-- 文字方向标签：不允许只靠颜色传达涨跌（MASTER §3.5） -->
      <span class="text-xs font-medium" :class="valueClass" data-testid="rate-direction-label">{{ directionLabel }}</span>
      <span class="text-xs text-accent-600 dark:text-dark-300" data-testid="rate-previous">
        ({{ formatMultiplier(previous, digits) }})
      </span>
    </template>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatMultiplier } from './format'

const props = withDefaults(
  defineProps<{
    /** 当前倍率（例如 1.2）。 */
    current: number | null | undefined
    /** 变动前的倍率（同步快照里保留的上一次值）。 */
    previous?: number | null
    /**
     * 变动判定：'auto'（按 current/previous 差值判断，默认）|
     * 'changed'（后端 rate_changed=true）| 'unchanged'（强制不显示变动）。
     * 用字符串而不是布尔，避免 Vue 布尔 prop 缺省被强转为 false。
     */
    flag?: 'auto' | 'changed' | 'unchanged'
    digits?: number
    testId?: string
  }>(),
  {
    previous: null,
    flag: 'auto',
    digits: 2,
    testId: 'rate-change'
  }
)

const { t } = useI18n()

const currentValue = computed(() => (typeof props.current === 'number' ? props.current : null))

const hasPrevious = computed(() => typeof props.previous === 'number')

const direction = computed<'up' | 'down' | 'flat'>(() => {
  if (currentValue.value === null || !hasPrevious.value) return 'flat'
  const delta = currentValue.value - (props.previous as number)
  if (delta > 1e-9) return 'up'
  if (delta < -1e-9) return 'down'
  return 'flat'
})

const changed = computed(() => {
  if (props.flag === 'unchanged') return false
  if (props.flag === 'changed') return hasPrevious.value && direction.value !== 'flat'
  return direction.value !== 'flat'
})

const directionLabel = computed(() => {
  if (direction.value === 'up') return t('admin.opsmgmt.rate.up')
  if (direction.value === 'down') return t('admin.opsmgmt.rate.down')
  return t('admin.opsmgmt.rate.flat')
})

/** 上涨 = 成本变高 = 危险（红）；下降 = 变便宜 = 成功（绿）。 */
const valueClass = computed(() => {
  if (direction.value === 'up') return 'text-red-700 dark:text-red-300'
  if (direction.value === 'down') return 'text-emerald-700 dark:text-emerald-300'
  return 'text-accent-900 dark:text-dark-100'
})
</script>
