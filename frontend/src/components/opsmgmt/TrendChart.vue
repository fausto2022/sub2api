<template>
  <div class="card p-5" :data-testid="testId">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">{{ title }}</h2>
        <p v-if="subtitle" class="mt-0.5 text-xs text-accent-600 dark:text-dark-300">{{ subtitle }}</p>
      </div>
      <!-- 图例：颜色 + 文字双通道（不允许只靠颜色区分系列） -->
      <ul class="flex flex-wrap items-center gap-3 text-xs text-accent-600 dark:text-dark-300">
        <li class="flex items-center gap-1.5" data-testid="trend-legend-revenue">
          <span class="h-0.5 w-4 rounded-full bg-primary-600 dark:bg-primary-400" aria-hidden="true" />
          {{ t('admin.opsmgmt.dashboard.trend.revenue') }}
        </li>
        <li class="flex items-center gap-1.5" data-testid="trend-legend-cost">
          <span class="h-0.5 w-4 rounded-full bg-accent-600 dark:bg-dark-300" aria-hidden="true" />
          {{ t('admin.opsmgmt.dashboard.trend.cost') }}
        </li>
        <li class="flex items-center gap-1.5" data-testid="trend-legend-profit">
          <span class="h-2 w-4 rounded-sm bg-cyan-500/25 dark:bg-cyan-400/30" aria-hidden="true" />
          {{ t('admin.opsmgmt.dashboard.trend.profit') }}
        </li>
      </ul>
    </div>

    <div v-if="loading" class="mt-4 space-y-2" data-testid="trend-loading">
      <div v-for="i in 4" :key="i" class="h-8 animate-pulse rounded-lg bg-accent-100 dark:bg-dark-700" />
    </div>

    <p
      v-else-if="unavailable || !points.length"
      class="mt-4 flex items-center gap-2 rounded-xl bg-accent-100 px-3 py-6 text-sm text-accent-600 dark:bg-dark-900 dark:text-dark-300"
      data-testid="trend-unavailable"
    >
      <Icon name="exclamationCircle" size="md" aria-hidden="true" />
      {{ unavailableText }}
    </p>

    <svg
      v-else
      class="mt-3 w-full"
      :viewBox="`0 0 ${WIDTH} ${HEIGHT}`"
      role="img"
      :aria-label="ariaLabel"
      data-testid="trend-svg"
    >
      <!-- 网格与纵轴刻度 -->
      <g>
        <line
          v-for="tick in ticks"
          :key="`grid-${tick.value}`"
          :x1="PAD_LEFT"
          :x2="WIDTH - PAD_RIGHT"
          :y1="tick.y"
          :y2="tick.y"
          class="stroke-accent-200 dark:stroke-dark-700"
          stroke-width="1"
          stroke-dasharray="3 4"
        />
        <text
          v-for="tick in ticks"
          :key="`label-${tick.value}`"
          :x="PAD_LEFT - 8"
          :y="tick.y + 3"
          text-anchor="end"
          font-size="10"
          class="fill-accent-600 dark:fill-dark-300"
        >
          {{ tick.label }}
        </text>
      </g>

      <!-- 零轴（存在负利润时提示基线） -->
      <line
        v-if="yMin < 0"
        :x1="PAD_LEFT"
        :x2="WIDTH - PAD_RIGHT"
        :y1="yScale(0)"
        :y2="yScale(0)"
        class="stroke-accent-300 dark:stroke-dark-600"
        stroke-width="1"
      />

      <!-- 利润面积（青） -->
      <path :d="profitAreaPath" class="fill-cyan-500/15 dark:fill-cyan-400/20" data-testid="trend-profit-area" />

      <!-- 收入（indigo） / 成本（灰） -->
      <path
        :d="revenuePath"
        fill="none"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        class="text-primary-600 dark:text-primary-400"
        stroke="currentColor"
        data-testid="trend-revenue-line"
      />
      <path
        :d="costPath"
        fill="none"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        class="text-accent-600 dark:text-dark-300"
        stroke="currentColor"
        data-testid="trend-cost-line"
      />

      <!-- 数据点 + 横轴标签 -->
      <g v-for="(point, index) in points" :key="`p-${index}`">
        <circle
          :cx="xScale(index)"
          :cy="yScale(point.revenue)"
          r="2.5"
          class="fill-primary-600 dark:fill-primary-400"
        />
        <circle :cx="xScale(index)" :cy="yScale(point.cost)" r="2.5" class="fill-accent-600 dark:fill-dark-300" />
        <text
          :x="xScale(index)"
          :y="HEIGHT - 10"
          text-anchor="middle"
          font-size="10"
          class="fill-accent-600 dark:fill-dark-300"
        >
          {{ point.label }}
        </text>
      </g>
    </svg>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatUsd } from './format'
import type { TrendPoint } from './types'

const props = withDefaults(
  defineProps<{
    points: TrendPoint[]
    title: string
    subtitle?: string
    loading?: boolean
    unavailable?: boolean
    unavailableText?: string
    testId?: string
  }>(),
  {
    subtitle: '',
    loading: false,
    unavailable: false,
    unavailableText: '',
    testId: 'trend-chart'
  }
)

const { t } = useI18n()

const WIDTH = 640
const HEIGHT = 240
const PAD_LEFT = 64
const PAD_RIGHT = 16
const PAD_TOP = 16
const PAD_BOTTOM = 32

const innerWidth = WIDTH - PAD_LEFT - PAD_RIGHT
const innerHeight = HEIGHT - PAD_TOP - PAD_BOTTOM

const yMin = computed(() => {
  const values = props.points.flatMap((point) => [point.revenue, point.cost, point.profit])
  const min = Math.min(0, ...values.filter((value) => Number.isFinite(value)))
  return Number.isFinite(min) ? min : 0
})

const yMax = computed(() => {
  const values = props.points.flatMap((point) => [point.revenue, point.cost, point.profit])
  const max = Math.max(0, ...values.filter((value) => Number.isFinite(value)))
  if (!Number.isFinite(max) || max <= 0) return 1
  // 顶部留 12% 余量，避免折线贴边。
  return max * 1.12
})

function yScale(value: number): number {
  const span = yMax.value - yMin.value
  if (span <= 0) return PAD_TOP + innerHeight
  const ratio = (value - yMin.value) / span
  return PAD_TOP + innerHeight * (1 - ratio)
}

function xScale(index: number): number {
  if (props.points.length <= 1) return PAD_LEFT + innerWidth / 2
  return PAD_LEFT + (index * innerWidth) / (props.points.length - 1)
}

function linePath(values: number[]): string {
  return values
    .map((value, index) => `${index === 0 ? 'M' : 'L'} ${xScale(index).toFixed(2)} ${yScale(value).toFixed(2)}`)
    .join(' ')
}

const revenuePath = computed(() => linePath(props.points.map((point) => point.revenue)))
const costPath = computed(() => linePath(props.points.map((point) => point.cost)))

const profitAreaPath = computed(() => {
  if (!props.points.length) return ''
  const baseline = yScale(Math.max(0, Math.min(0, ...props.points.map((point) => point.profit))))
  const top = props.points
    .map((point, index) => `${index === 0 ? 'M' : 'L'} ${xScale(index).toFixed(2)} ${yScale(point.profit).toFixed(2)}`)
    .join(' ')
  const lastX = xScale(props.points.length - 1).toFixed(2)
  const firstX = xScale(0).toFixed(2)
  return `${top} L ${lastX} ${baseline.toFixed(2)} L ${firstX} ${baseline.toFixed(2)} Z`
})

const ticks = computed(() => {
  const count = 4
  const result: { value: number; y: number; label: string }[] = []
  for (let i = 0; i <= count; i += 1) {
    const value = yMin.value + ((yMax.value - yMin.value) * i) / count
    result.push({ value, y: yScale(value), label: formatUsd(value, 0) })
  }
  return result
})

const ariaLabel = computed(() => {
  const first = props.points[0]
  const last = props.points[props.points.length - 1]
  if (!first || !last) return t('admin.opsmgmt.dashboard.trend.empty')
  return t('admin.opsmgmt.dashboard.trend.aria', {
    from: first.label,
    to: last.label,
    revenue: formatUsd(last.revenue),
    cost: formatUsd(last.cost),
    profit: formatUsd(last.profit)
  })
})
</script>
