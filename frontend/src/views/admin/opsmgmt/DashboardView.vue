<template>
  <AppLayout>
    <div class="space-y-6 pb-12" data-testid="opsmgmt-dashboard">
      <!-- 页头 -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h1 class="text-lg font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.dashboard.title') }}
          </h1>
          <p class="mt-0.5 text-xs text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.dashboard.description') }}
          </p>
          <p v-if="lastUpdated" class="mt-0.5 text-xs tabular-nums text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.updatedAt', { time: formatDateTime(lastUpdated) }) }}
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <div class="tabs" role="tablist">
            <button
              type="button"
              class="tab"
              :class="{ 'tab-active': range === 'today' }"
              :aria-selected="range === 'today'"
              role="tab"
              data-testid="opsmgmt-range-today"
              @click="setRange('today')"
            >
              {{ t('admin.opsmgmt.dashboard.range.today') }}
            </button>
            <button
              type="button"
              class="tab"
              :class="{ 'tab-active': range === '7d' }"
              :aria-selected="range === '7d'"
              role="tab"
              data-testid="opsmgmt-range-7d"
              @click="setRange('7d')"
            >
              {{ t('admin.opsmgmt.dashboard.range.sevenDay') }}
            </button>
          </div>
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loading"
            :title="t('admin.opsmgmt.refresh')"
            :aria-label="t('admin.opsmgmt.refresh')"
            data-testid="opsmgmt-dashboard-refresh"
            @click="load"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </div>

      <!-- 数据不可用提示（绝不把「查不到」渲染成 0） -->
      <div
        v-if="unavailable"
        class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-200"
        data-testid="opsmgmt-dashboard-unavailable"
      >
        <p class="flex items-center gap-2 font-medium">
          <Icon name="exclamationTriangle" size="md" aria-hidden="true" />
          {{ t('admin.opsmgmt.dashboard.unavailable.title') }}
        </p>
        <p v-if="summary?.unavailable_reason" class="mt-1 text-xs">
          {{ t('admin.opsmgmt.dashboard.unavailable.reason', { reason: summary.unavailable_reason }) }}
        </p>
        <p class="mt-1 text-xs">{{ t('admin.opsmgmt.dashboard.unavailable.hint') }}</p>
      </div>

      <div
        v-if="errorMessage"
        class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
        data-testid="opsmgmt-dashboard-error"
      >
        {{ errorMessage }}
      </div>

      <!-- KPI -->
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <KpiCard
          :label="t('admin.opsmgmt.dashboard.kpi.revenue')"
          :value="kpiValue(summary?.today_revenue)"
          :change-label="t('admin.opsmgmt.kpi.compareSevenDay')"
          :change-value="revenueChange.text"
          :trend="revenueChange.trend"
          :tone="revenueChange.trend === 'down' ? 'bad' : revenueChange.trend === 'up' ? 'good' : 'neutral'"
          :hint="guaranteedHint"
          test-id="opsmgmt-kpi-revenue"
        />
        <KpiCard
          :label="t('admin.opsmgmt.dashboard.kpi.cost')"
          :value="kpiValue(summary?.today_cost)"
          :change-label="t('admin.opsmgmt.kpi.compareSevenDay')"
          :change-value="costChange.text"
          :trend="costChange.trend"
          :tone="costChange.trend === 'up' ? 'bad' : costChange.trend === 'down' ? 'good' : 'neutral'"
          test-id="opsmgmt-kpi-cost"
        />
        <KpiCard
          :label="t('admin.opsmgmt.dashboard.kpi.profit')"
          :value="kpiValue(summary?.today_profit)"
          :change-label="t('admin.opsmgmt.kpi.compareSevenDay')"
          :change-value="profitChange.text"
          :trend="profitChange.trend"
          :tone="profitChange.trend === 'up' ? 'good' : profitChange.trend === 'down' ? 'bad' : 'neutral'"
          test-id="opsmgmt-kpi-profit"
        />
        <KpiCard
          :label="t('admin.opsmgmt.dashboard.kpi.margin')"
          :value="unavailable ? '—' : formatBasisPoints(summary?.today_margin_basis_points)"
          :change-label="t('admin.opsmgmt.kpi.compareSevenDay')"
          :change-value="marginChange.text"
          :trend="marginChange.trend"
          :tone="marginChange.trend === 'up' ? 'good' : marginChange.trend === 'down' ? 'bad' : 'neutral'"
          test-id="opsmgmt-kpi-margin"
        />
      </div>

      <!-- 近 7 日趋势 -->
      <TrendChart
        :points="trendPoints"
        :title="t('admin.opsmgmt.dashboard.trend.title')"
        :subtitle="t('admin.opsmgmt.dashboard.trend.subtitle')"
        :loading="loading && !summary"
        :unavailable="trendUnavailable"
        :unavailable-text="t('admin.opsmgmt.dashboard.trend.empty')"
        test-id="opsmgmt-trend"
      />
      <p
        v-if="trendDerived"
        class="-mt-3 text-xs text-accent-600 dark:text-dark-300"
        data-testid="opsmgmt-trend-derived"
      >
        {{ t('admin.opsmgmt.dashboard.trend.derived') }}
      </p>

      <!-- 分组盈利榜 -->
      <section class="card overflow-hidden" data-testid="opsmgmt-group-profit">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-accent-200 px-5 py-3 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.dashboard.groups.title') }}
          </h2>
          <span v-if="summary" class="text-xs tabular-nums text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.dashboard.groups.sampled', { days: summary.sampled_days }) }}
          </span>
        </div>

        <div v-if="groupRows.length === 0" class="px-5 py-8 text-sm text-accent-600 dark:text-dark-300" data-testid="opsmgmt-group-profit-empty">
          {{ t('admin.opsmgmt.dashboard.groups.empty') }}
        </div>

        <div v-else class="overflow-x-auto">
          <table class="table w-full">
            <thead>
              <tr>
                <th class="text-left">{{ t('admin.opsmgmt.dashboard.groups.group') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.dashboard.groups.revenue') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.dashboard.groups.cost') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.dashboard.groups.profit') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.dashboard.groups.margin') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.dashboard.groups.requests') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="row in groupRows"
                :key="row.group_id"
                :class="isLossRow(row) ? 'bg-red-50 dark:bg-red-500/10' : ''"
                :data-testid="`opsmgmt-group-row-${row.group_id}`"
                :data-loss="isLossRow(row) ? 'true' : 'false'"
              >
                <td class="text-left">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="font-medium text-accent-900 dark:text-dark-100">{{ row.group_name }}</span>
                    <span v-if="isLossRow(row)" class="badge badge-danger" data-testid="opsmgmt-group-loss-badge">
                      {{ t('admin.opsmgmt.dashboard.groups.lossBadge') }}
                    </span>
                  </div>
                </td>
                <td class="text-right tabular-nums">{{ formatUsd(row.revenue) }}</td>
                <td class="text-right tabular-nums">{{ formatUsd(row.cost) }}</td>
                <td
                  class="text-right tabular-nums font-medium"
                  :class="row.profit < 0 ? 'text-red-700 dark:text-red-300' : 'text-accent-900 dark:text-dark-100'"
                >
                  {{ formatUsd(row.profit) }}
                </td>
                <td
                  class="text-right tabular-nums"
                  :class="row.margin_basis_points < 0 ? 'text-red-700 dark:text-red-300' : 'text-accent-900 dark:text-dark-100'"
                >
                  {{ formatBasisPoints(row.margin_basis_points) }}
                </td>
                <td class="text-right tabular-nums">{{ row.requests }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import KpiCard from '@/components/opsmgmt/KpiCard.vue'
import TrendChart from '@/components/opsmgmt/TrendChart.vue'
import {
  changePercent,
  changeTrend,
  formatBasisPoints,
  formatBasisPointDelta,
  formatChangePercent,
  formatDateTime,
  formatDay,
  formatUsd,
  isLossRow
} from '@/components/opsmgmt/format'
import type { TrendPoint } from '@/components/opsmgmt/types'
import { adminAPI } from '@/api/admin'
import { opsMgmtAPI, type OpsProfitSummary } from '@/api/admin/opsmgmt'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

const summary = ref<OpsProfitSummary | null>(null)
const dailyTrend = ref<TrendPoint[]>([])
const range = ref<'today' | '7d'>('today')
const loading = ref(false)
const errorMessage = ref('')
const lastUpdated = ref('')

const unavailable = computed(() => !summary.value || summary.value.available === false)

/** 未取到数据或不可用时显示占位符，绝不用 0 冒充。 */
function kpiValue(value: number | null | undefined): string {
  if (unavailable.value) return '—'
  return formatUsd(value)
}

/** 近 7 日日均（用于环比基线，避免把 7 日合计和今日直接比）。 */
function sevenDayAverage(total: number | null | undefined): number | null {
  if (typeof total !== 'number' || !Number.isFinite(total)) return null
  const days = Math.min(Math.max(summary.value?.sampled_days || 7, 1), 7)
  return total / days
}

const revenueChange = computed(() => {
  const percent = unavailable.value
    ? null
    : changePercent(summary.value?.today_revenue, sevenDayAverage(summary.value?.seven_day_revenue))
  return { text: formatChangePercent(percent), trend: changeTrend(percent) }
})

const costChange = computed(() => {
  const percent = unavailable.value
    ? null
    : changePercent(summary.value?.today_cost, sevenDayAverage(summary.value?.seven_day_cost))
  return { text: formatChangePercent(percent), trend: changeTrend(percent) }
})

const profitChange = computed(() => {
  const percent = unavailable.value
    ? null
    : changePercent(summary.value?.today_profit, sevenDayAverage(summary.value?.seven_day_profit))
  return { text: formatChangePercent(percent), trend: changeTrend(percent) }
})

const marginChange = computed(() => {
  if (unavailable.value || !summary.value) {
    return { text: formatBasisPointDelta(null), trend: 'flat' as const }
  }
  const delta = summary.value.today_margin_basis_points - summary.value.seven_day_margin_basis_points
  return { text: formatBasisPointDelta(delta), trend: changeTrend(delta / 100) }
})

/** 今日收入卡片下方小字：保底 $xxx（90%）。 */
const guaranteedHint = computed(() => {
  if (unavailable.value || !summary.value) return ''
  const ratioPercent = summary.value.guaranteed_revenue_config_bp / 100
  const percent = Number.isInteger(ratioPercent) ? String(ratioPercent) : ratioPercent.toFixed(2)
  return t('admin.opsmgmt.dashboard.kpi.guaranteed', {
    amount: formatUsd(summary.value.today_guaranteed_revenue),
    percent
  })
})

const groupRows = computed(() => {
  const rows = summary.value?.top_groups ?? []
  return [...rows].sort((left, right) => right.profit - left.profit)
})

/**
 * 趋势：优先逐日明细；逐日全 0 但看板有聚合收入/成本时（逐日接口不可用或口径不同）
 * 退回「今日 / 近 7 日」两个聚合点，并在图下方说明，避免画一条凭空为 0 的曲线。
 */
const trendDerived = computed(() => {
  if (unavailable.value || !summary.value) return false
  const revenue = dailyTrend.value.reduce((total, point) => total + point.revenue, 0)
  const cost = dailyTrend.value.reduce((total, point) => total + point.cost, 0)
  if (revenue > 0 || cost > 0) return false
  return summary.value.today_revenue > 0 || summary.value.seven_day_revenue > 0 || summary.value.today_cost > 0
})

const trendPoints = computed<TrendPoint[]>(() => {
  if (unavailable.value || !summary.value) return []
  if (!trendDerived.value && dailyTrend.value.length > 0) return dailyTrend.value
  const sum = summary.value
  return [
    {
      label: t('admin.opsmgmt.dashboard.range.today'),
      revenue: sum.today_revenue,
      cost: sum.today_cost,
      profit: sum.today_profit
    },
    {
      label: t('admin.opsmgmt.dashboard.range.sevenDay'),
      revenue: sum.seven_day_revenue,
      cost: sum.seven_day_cost,
      profit: sum.seven_day_profit
    }
  ]
})

const trendUnavailable = computed(() => unavailable.value || trendPoints.value.length === 0)

function pad(input: number): string {
  return String(input).padStart(2, '0')
}

function toDateString(date: Date): string {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

/**
 * 逐日趋势：/admin/opsmgmt/dashboard 只下发今日与 7 日聚合值，
 * 逐日明细取自既有的 /admin/dashboard/users-trend（cost = total_cost，
 * actual_cost = 收入，与看板口径一致），拿不到就退回聚合点。
 */
async function loadDailyTrend(): Promise<TrendPoint[]> {
  const end = new Date()
  const start = new Date()
  start.setDate(end.getDate() - 6)
  const response = await adminAPI.dashboard.getUserUsageTrend({
    start_date: toDateString(start),
    end_date: toDateString(end),
    granularity: 'day'
  })
  const buckets = new Map<string, { revenue: number; cost: number }>()
  for (const point of response.trend ?? []) {
    const day = (point.date || '').slice(0, 10)
    if (!day) continue
    const bucket = buckets.get(day) ?? { revenue: 0, cost: 0 }
    bucket.revenue += Number(point.actual_cost) || 0
    bucket.cost += Number(point.cost) || 0
    buckets.set(day, bucket)
  }
  const points: TrendPoint[] = []
  for (let offset = 0; offset < 7; offset += 1) {
    const day = new Date(start)
    day.setDate(start.getDate() + offset)
    const key = toDateString(day)
    const bucket = buckets.get(key) ?? { revenue: 0, cost: 0 }
    points.push({
      label: formatDay(key),
      revenue: bucket.revenue,
      cost: bucket.cost,
      profit: bucket.revenue - bucket.cost
    })
  }
  return points
}

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const [dashboard, trend] = await Promise.all([
      opsMgmtAPI.getDashboard(range.value),
      loadDailyTrend().catch(() => [] as TrendPoint[])
    ])
    summary.value = dashboard
    dailyTrend.value = trend
    lastUpdated.value = dashboard.generated_at
  } catch (caught) {
    errorMessage.value = extractApiErrorMessage(caught)
  } finally {
    loading.value = false
  }
}

function setRange(next: 'today' | '7d'): void {
  if (range.value === next) return
  range.value = next
  void load()
}

onMounted(load)
</script>
