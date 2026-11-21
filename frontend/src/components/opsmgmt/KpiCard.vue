<template>
  <div class="card p-5" :data-testid="testId">
    <p class="text-xs font-medium text-accent-600 dark:text-dark-300" :data-testid="`${testId}-label`">
      {{ label }}
    </p>

    <p class="mt-1.5 text-2xl font-bold tabular-nums text-accent-900 dark:text-dark-50" :data-testid="`${testId}-value`">
      {{ value }}
    </p>

    <!-- 保底收入等补充说明：小字，位于数值下方（design §2.4.2） -->
    <p
      v-if="hint"
      class="mt-1 text-xs tabular-nums text-accent-600 dark:text-dark-300"
      :data-testid="`${testId}-hint`"
    >
      {{ hint }}
    </p>

    <!--
      环比：箭头 + 文字方向标签 + 数值。方向与好坏都由文字表达，
      颜色只是强化（MASTER §3.5 / color-not-decorative-only）。
    -->
    <p
      v-if="changeValue"
      class="mt-1.5 flex flex-wrap items-center gap-1 text-xs font-medium"
      :class="toneClass"
      :data-testid="`${testId}-change`"
      :data-trend="trend"
      :data-tone="tone"
    >
      <Icon v-if="trend !== 'flat'" :name="trend === 'up' ? 'arrowUp' : 'arrowDown'" size="xs" aria-hidden="true" />
      <span v-if="changeLabel" class="text-accent-600 dark:text-dark-300">{{ changeLabel }}</span>
      <span class="tabular-nums">{{ directionText }}</span>
      <span class="tabular-nums" :data-testid="`${testId}-change-value`">{{ changeValue }}</span>
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { ChangeTrend } from './format'
import type { KpiTone } from './types'

const props = withDefaults(
  defineProps<{
    /** 指标名（例如「今日收入」）。 */
    label: string
    /** 已格式化好的数值（含货币符号 / 百分号）。 */
    value: string
    /** 环比说明，例如「较 7 日均值」。 */
    changeLabel?: string
    /** 已格式化好的环比数值，例如「+12.3%」。 */
    changeValue?: string
    trend?: ChangeTrend
    /** 色彩语义：good=获利方向（绿）、bad=不利（红）、neutral=中性。 */
    tone?: KpiTone
    /** 数值下方小字（今日收入的保底收入就放这里）。 */
    hint?: string
    testId?: string
  }>(),
  {
    changeLabel: '',
    changeValue: '',
    trend: 'flat',
    tone: 'neutral',
    hint: '',
    testId: 'kpi'
  }
)

const { t } = useI18n()

const directionText = computed(() => {
  if (props.trend === 'up') return t('admin.opsmgmt.kpi.trendUp')
  if (props.trend === 'down') return t('admin.opsmgmt.kpi.trendDown')
  return t('admin.opsmgmt.kpi.trendFlat')
})

const toneClass = computed(() => {
  switch (props.tone) {
    case 'good':
      return 'text-emerald-700 dark:text-emerald-300'
    case 'bad':
      return 'text-red-700 dark:text-red-300'
    default:
      return 'text-accent-600 dark:text-dark-300'
  }
})
</script>
