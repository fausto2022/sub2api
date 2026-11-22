<template>
  <div
    class="plaza-pricing-table max-w-full overflow-x-auto"
    role="region"
    :aria-label="t('modelPlaza.title')"
    tabindex="0"
  >
    <table role="table" class="w-full min-w-[832px] table-auto border-collapse text-sm tabular-nums">
      <colgroup>
        <col class="w-[22%]" />
        <col class="w-[12%]" />
        <col class="w-[12%]" />
        <col class="w-[16%]" />
        <col class="w-[12%]" />
        <col class="w-[12%]" />
        <col class="w-[14%]" />
      </colgroup>
      <thead role="rowgroup">
        <tr
          role="row"
          class="text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400"
        >
          <th role="columnheader"
            rowspan="2"
            class="border-r border-gray-100 py-2.5 pl-5 pr-4 text-left align-middle dark:border-dark-700/60"
          >
            {{ t('modelPlaza.table.model') }}
          </th>
          <th role="columnheader" colspan="3" class="pz-bg pt-2 text-center">
            <div class="pz-title border-b pb-2 font-semibold">
              {{ t('modelPlaza.table.discountedPrice') }}
              <span class="pz-unit ml-1 normal-case font-normal">{{ t('modelPlaza.table.unitPerMillion') }}</span>
            </div>
          </th>
          <th role="columnheader"
            colspan="3"
            class="border-l border-gray-100 pt-2 text-center dark:border-dark-700/60"
          >
            <div class="border-b border-gray-200 pb-2 text-accent-600 dark:border-dark-600 dark:text-dark-300">
              {{ t('modelPlaza.table.officialPrice') }}
              <span class="ml-1 normal-case font-normal">{{ t('modelPlaza.table.unitPerMillion') }}</span>
            </div>
          </th>
        </tr>
        <tr
          role="row"
          class="border-b border-gray-200 text-left text-[11px] font-medium uppercase leading-4 tracking-wide text-gray-400 dark:border-dark-700 dark:text-dark-500"
        >
          <th role="columnheader" class="pz-bg px-3 py-2 font-medium">{{ t('modelPlaza.table.input') }}</th>
          <th role="columnheader" class="pz-bg px-3 py-2 font-medium">{{ t('modelPlaza.table.output') }}</th>
          <th role="columnheader" class="pz-bg px-3 py-2 font-medium">{{ t('modelPlaza.table.cache') }}</th>
          <th role="columnheader" class="border-l border-gray-100 px-3 py-2 font-medium dark:border-dark-700/60">
            {{ t('modelPlaza.table.input') }}
          </th>
          <th role="columnheader" class="px-3 py-2 font-medium">{{ t('modelPlaza.table.output') }}</th>
          <th role="columnheader" class="px-3 py-2 font-medium">{{ t('modelPlaza.table.cache') }}</th>
        </tr>
      </thead>
      <tbody role="rowgroup">
        <tr
          v-for="{ model: m, period, key } in rows"
          :key="key"
          role="row"
          class="border-b border-gray-100 transition-colors last:border-b-0 hover:bg-gray-50/70 dark:border-dark-800 dark:hover:bg-dark-800/50"
        >
          <!-- 模型名 + 非 token 计费模式徽章;分时时段行额外标注时段 -->
          <td role="cell" class="pz-model border-r border-gray-100 py-2.5 pl-5 pr-4 align-top dark:border-dark-700/60">
            <div class="flex flex-wrap items-center gap-1.5">
              <span class="font-medium text-gray-900 dark:text-white">{{ m.name }}</span>
              <!-- 时段徽章紧跟模型名,其余徽章排在后面,空间不足时先换行的是它们 -->
              <span
                v-if="period"
                class="inline-flex items-center whitespace-nowrap rounded-md bg-gray-100 px-1 py-0.5 font-mono text-[10px] font-medium text-gray-500 dark:bg-dark-700/70 dark:text-dark-300"
                :title="timePricingRowHint(m)"
              >
                <span v-if="m.time_pricing?.weekdays_only" class="mr-1 font-sans">{{
                  t('modelPlaza.table.timePricingWeekdays')
                }}</span>
                {{ formatTimeWindow(period) }}
              </span>
              <span
                v-if="platform && m.platform !== platform"
                :class="[
                  'inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium',
                  platformBadgeLightClass(m.platform)
                ]"
              >
                {{ platformLabel(m.platform) }}
              </span>
              <span
                v-if="billingMode(m) !== BILLING_MODE_TOKEN"
                class="rounded-md bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-500 dark:bg-dark-700/70 dark:text-dark-300"
              >
                {{ billingModeLabel(m) }}
              </span>
              <span
                v-if="m.long_context_basis === 'marginal'"
                class="rounded-md bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-500 dark:bg-dark-700/70 dark:text-dark-300"
                :title="t('modelPlaza.table.tierHintMarginal')"
              >
                {{ t('modelPlaza.table.marginalBadge') }}
              </span>
              <span
                v-for="([effort, multiplier]) in reasoningEffortMultipliers(m)"
                :key="effort"
                class="rounded-md bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
                :title="t('modelPlaza.table.reasoningMultiplierHint', { effort, multiplier })"
                :data-reasoning-effort="effort"
              >
                {{ t('modelPlaza.table.reasoningMultiplierBadge', { effort, multiplier }) }}
              </span>
            </div>
          </td>

          <!-- token 计费:输入 / 输出 / 缓存(写/读),有阶梯时每档一行;档位标签只放输入列,其余列按行对齐 -->
          <template v-if="billingMode(m) === BILLING_MODE_TOKEN">
            <td role="cell" class="pz-cell pz-compact-core px-3 py-2.5 align-top font-mono font-semibold text-gray-900 dark:text-gray-50">
              <span class="pz-compact-label" aria-hidden="true" :data-label="compactTokenLabel('discountedPrice')" />
              <template v-if="tokenIntervals(m).length">
                <div
                  v-for="(iv, idx) in tokenIntervals(m)"
                  :key="idx"
                  class="whitespace-nowrap text-xs leading-5"
                >
                  <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500" :title="tierHint(m)">{{ tierLabel(iv) }}</span>
                  {{ paidPerMillionTier(m, iv, 'input_price', period) }}
                </div>
              </template>
              <template v-else>{{ paidPerMillion(m, 'input_price', period) }}</template>
            </td>
            <td role="cell" class="pz-cell pz-compact-core px-3 py-2.5 align-top font-mono font-semibold text-gray-900 dark:text-gray-50">
              <span class="pz-compact-label" aria-hidden="true" :data-label="t('modelPlaza.table.output')" />
              <template v-if="tokenIntervals(m).length">
                <div
                  v-for="(iv, idx) in tokenIntervals(m)"
                  :key="idx"
                  class="whitespace-nowrap text-xs leading-5"
                  :title="tierHint(m)"
                >
                  {{ paidPerMillionTier(m, iv, 'output_price', period) }}
                </div>
              </template>
              <template v-else>{{ paidPerMillion(m, 'output_price', period) }}</template>
            </td>
          </template>
          <td
            role="cell"
            :colspan="billingMode(m) === BILLING_MODE_TOKEN ? undefined : 3"
            class="pz-cell pz-discounted-detail px-3 py-2.5 align-top"
          >
            <template v-if="billingMode(m) === BILLING_MODE_TOKEN">
              <span class="pz-compact-label" aria-hidden="true" :data-label="t('modelPlaza.table.cache')" />
              <template v-if="hasTierCachePricing(tokenIntervals(m))">
                <div
                  v-for="(iv, idx) in tokenIntervals(m)"
                  :key="idx"
                  class="whitespace-nowrap font-mono text-xs leading-5 text-gray-800 dark:text-gray-200"
                  :title="tierHint(m)"
                >
                  <template v-if="iv.cache_write_price != null || iv.cache_write_1h_price != null || iv.cache_write_12h_price != null || iv.cache_read_price != null">
                    <span class="font-sans font-normal text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheWriteShort') }}</span>
                    {{ paidPerMillionTier(m, iv, 'cache_write_price', period) }}
                    <template v-if="iv.cache_write_1h_price != null"
                      ><span class="font-sans font-normal text-gray-400 dark:text-dark-500"> (1h </span>{{ paidPerMillionTier(m, iv, 'cache_write_1h_price', period)
                      }}<span class="font-sans font-normal text-gray-400 dark:text-dark-500">)</span></template
                    ><template v-if="iv.cache_write_12h_price != null"
                      ><span class="font-sans font-normal text-gray-400 dark:text-dark-500"> (12h </span>{{ paidPerMillionTier(m, iv, 'cache_write_12h_price', period)
                      }}<span class="font-sans font-normal text-gray-400 dark:text-dark-500">)</span></template
                    >
                    <span class="ml-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheReadShort') }}</span>
                    {{ paidPerMillionTier(m, iv, 'cache_read_price', period) }}
                  </template>
                  <span v-else class="text-gray-400 dark:text-dark-500">-</span>
                </div>
              </template>
              <div
                v-else-if="hasCachePricing(m)"
                class="space-y-0.5 font-mono text-xs text-gray-800 dark:text-gray-200"
              >
                <div>
                  <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheWrite') }}</span>
                  {{ paidPerMillion(m, 'cache_write_price', period)
                  }}<template v-if="m.pricing?.cache_write_1h_price != null"
                    ><span class="font-sans font-normal text-gray-400 dark:text-dark-500"> (1h </span>{{ paidPerMillion(m, 'cache_write_1h_price', period)
                    }}<span class="font-sans font-normal text-gray-400 dark:text-dark-500">)</span></template
                  ><template v-if="m.pricing?.cache_write_12h_price != null"
                    ><span class="font-sans font-normal text-gray-400 dark:text-dark-500"> (12h </span>{{ paidPerMillion(m, 'cache_write_12h_price', period)
                    }}<span class="font-sans font-normal text-gray-400 dark:text-dark-500">)</span></template
                  >
                </div>
                <div>
                  <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheRead') }}</span>
                  {{ paidPerMillion(m, 'cache_read_price', period) }}
                </div>
              </div>
              <span v-else class="text-gray-400 dark:text-dark-500">-</span>
            </template>
            <template v-else>
              <span class="pz-compact-label" aria-hidden="true" :data-label="t('modelPlaza.table.discountedPrice')" />
              <div
                v-if="requestIntervals(m).length"
                class="flex flex-wrap items-center gap-1.5"
              >
                <span
                  v-for="(iv, idx) in requestIntervals(m)"
                  :key="idx"
                  class="inline-flex items-center gap-1 rounded-md bg-gray-100 px-2 py-0.5 font-mono text-xs text-gray-800 dark:bg-dark-700/60 dark:text-gray-200"
                >
                  <span class="font-sans text-gray-400 dark:text-dark-500">{{ tierLabel(iv) }}</span>
                  {{ paidRequestPriceTier(m, iv, iv.per_request_price)
                  }}<span class="font-sans text-gray-400 dark:text-dark-500">{{ perUnitSuffix(m) }}</span>
                </span>
              </div>
              <template v-else-if="m.pricing?.per_request_price != null">
                <span class="font-mono font-semibold text-gray-900 dark:text-gray-50">
                  {{ paidRequestPrice(m, m.pricing.per_request_price) }}
                </span>
                <span class="ml-1 text-xs text-gray-400 dark:text-dark-500">{{ perUnitSuffix(m) }}</span>
              </template>
              <span v-else class="text-gray-400 dark:text-dark-500">-</span>
            </template>

            <div
              v-if="!period && m.peak_pricing && (m.peak_pricing.input_price != null || m.peak_pricing.output_price != null)"
              class="pz-peak mt-2 text-xs text-amber-700 dark:text-amber-300"
              :title="t('modelPlaza.table.peakPriceHint')"
            >
              {{ t('modelPlaza.table.peakPrice') }}
              <span class="block text-accent-600 dark:text-dark-300">{{ t('modelPlaza.table.pricePair') }}</span>
              <span class="font-mono">
                {{ peakPrice(m.peak_pricing.input_price) }} / {{ peakPrice(m.peak_pricing.output_price) }}
              </span>
            </div>
            <div class="pz-rate mt-2 text-xs text-accent-600 dark:text-dark-300">
              {{ t('modelPlaza.table.rate') }}
              <span class="font-mono">
                <span
                  v-if="period"
                  class="font-semibold text-primary-700 dark:text-primary-300"
                  :title="t('modelPlaza.table.timePricingRateHint', { rate: effectiveRate, multiplier: period.multiplier })"
                >{{ periodRate(period) }}x</span>
                <span v-else-if="usesIndependentMediaRate(m)" class="font-semibold">{{ requestRate(m) }}x</span>
                <template v-else-if="hasCustomRate">
                  <span class="mr-1 line-through">{{ rateMultiplier }}x</span>
                  <span class="font-semibold text-primary-700 dark:text-primary-300">{{ effectiveRate }}x</span>
                </template>
                <span v-else class="font-semibold">{{ effectiveRate }}x</span>
              </span>
            </div>
          </td>

          <!-- 官方价格(参考价,不乘倍率;官方有阶梯时每档一行) -->
          <td
            role="cell"
            class="pz-compact-core border-l border-gray-100 px-3 py-2.5 align-top font-mono text-xs text-accent-600 dark:border-dark-700/60 dark:text-dark-300"
          >
            <span class="pz-compact-label" aria-hidden="true" :data-label="compactTokenLabel('officialPrice')" />
            <template v-if="officialIntervals(m).length">
              <div
                v-for="(iv, idx) in officialIntervals(m)"
                :key="idx"
                class="whitespace-nowrap leading-5"
              >
                <span class="mr-1 font-sans text-gray-400 dark:text-dark-500" :title="t('modelPlaza.table.tierHint')">{{ tierLabel(iv) }}</span>
                {{ official(iv.input_price) }}
              </div>
            </template>
            <template v-else>{{ official(m.official_pricing?.input_price) }}</template>
          </td>
          <td role="cell" class="pz-compact-core px-3 py-2.5 align-top font-mono text-xs text-accent-600 dark:text-dark-300">
            <span class="pz-compact-label" aria-hidden="true" :data-label="t('modelPlaza.table.output')" />
            <template v-if="officialIntervals(m).length">
              <div
                v-for="(iv, idx) in officialIntervals(m)"
                :key="idx"
                class="whitespace-nowrap leading-5"
                :title="t('modelPlaza.table.tierHint')"
              >
                {{ official(iv.output_price) }}
              </div>
            </template>
            <template v-else>{{ official(m.official_pricing?.output_price) }}</template>
          </td>
          <td role="cell" class="pz-official-cache px-3 py-2.5 align-top">
            <span class="pz-compact-label" aria-hidden="true" :data-label="t('modelPlaza.table.cache')" />
            <template v-if="hasTierCachePricing(officialIntervals(m))">
              <div
                v-for="(iv, idx) in officialIntervals(m)"
                :key="idx"
                class="whitespace-nowrap font-mono text-xs leading-5 text-gray-500 dark:text-dark-400"
                :title="t('modelPlaza.table.tierHint')"
              >
                <template v-if="iv.cache_write_price != null || iv.cache_write_1h_price != null || iv.cache_write_12h_price != null || iv.cache_read_price != null">
                  <span class="font-sans text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheWriteShort') }}</span>
                  {{ official(iv.cache_write_price) }}
                  <template v-if="iv.cache_write_1h_price != null"
                    ><span class="font-sans text-gray-400 dark:text-dark-500"> (1h </span>{{ official(iv.cache_write_1h_price)
                    }}<span class="font-sans text-gray-400 dark:text-dark-500">)</span></template
                  ><template v-if="iv.cache_write_12h_price != null"
                    ><span class="font-sans text-gray-400 dark:text-dark-500"> (12h </span>{{ official(iv.cache_write_12h_price)
                    }}<span class="font-sans text-gray-400 dark:text-dark-500">)</span></template
                  >
                  <span class="ml-1 font-sans text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheReadShort') }}</span>
                  {{ official(iv.cache_read_price) }}
                </template>
                <span v-else class="text-gray-400 dark:text-dark-500">-</span>
              </div>
            </template>
            <div
              v-else-if="m.official_pricing && hasOfficialCache(m.official_pricing)"
              class="space-y-0.5 font-mono text-xs text-gray-500 dark:text-dark-400"
            >
              <div>
                <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheWrite') }}</span>
                {{ official(m.official_pricing.cache_write_price)
                }}<template v-if="m.official_pricing.cache_write_1h_price != null"
                  ><span class="font-sans text-gray-400 dark:text-dark-500"> (1h </span>{{ official(m.official_pricing.cache_write_1h_price)
                  }}<span class="font-sans text-gray-400 dark:text-dark-500">)</span></template
                ><template v-if="m.official_pricing.cache_write_12h_price != null"
                  ><span class="font-sans text-gray-400 dark:text-dark-500"> (12h </span>{{ official(m.official_pricing.cache_write_12h_price)
                  }}<span class="font-sans text-gray-400 dark:text-dark-500">)</span></template
                >
              </div>
              <div>
                <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ t('modelPlaza.table.cacheRead') }}</span>
                {{ official(m.official_pricing.cache_read_price) }}
              </div>
            </div>
            <span v-else class="text-gray-400 dark:text-dark-500">-</span>
          </td>

        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatScaled, resolveIntervalPrices, scalePricing } from '@/utils/pricing'
import { platformBadgeLightClass, platformLabel } from '@/utils/platformColors'
import {
  BILLING_MODE_TOKEN,
  BILLING_MODE_IMAGE,
  BILLING_MODE_VIDEO,
  REASONING_EFFORT_LEVELS,
  type BillingMode
} from '@/constants/channel'
import type { PlazaModel, PlazaTimePricingPeriod } from '@/api/modelPlaza'
import type { UserPricingInterval, UserSupportedModelPricing } from '@/api/channels'

function reasoningEffortMultipliers(model: PlazaModel): [string, number][] {
  const multipliers = model.pricing?.reasoning_effort_multipliers
  return REASONING_EFFORT_LEVELS.flatMap(effort => {
    const multiplier = multipliers?.[effort]
    return typeof multiplier === 'number' && Number.isFinite(multiplier) && multiplier > 0
      ? [[effort, multiplier] as [string, number]]
      : []
  })
}

const props = defineProps<{
  models: PlazaModel[]
  /** 分组平台;实付分区底色随平台着色,未知平台回退品牌青。 */
  platform?: string
  /** 分组默认倍率。 */
  rateMultiplier: number
  /** 用户专属倍率;与默认不同,实付价按此计算并划线展示原倍率。 */
  userRateMultiplier?: number | null
  /** 生图独立倍率:true 时图片计费模型的实付倍率取 imageRateMultiplier,不取分组/专属倍率。 */
  imageRateIndependent?: boolean
  imageRateMultiplier?: number | null
  /** 视频独立倍率开启时，覆盖分组/用户专属倍率，与后端视频计费保持一致。 */
  videoRateIndependent?: boolean
  videoRateMultiplier?: number | null
  /**
   * 高峰窗口描述(含倍率与服务器时区标注),空串/缺省 = 分组未启用高峰。
   * 表格所有价格均为不含高峰因子的口径,该窗口仅用于分时时段行的 tooltip 披露:
   * 与高峰重叠的部分实付还会再乘高峰倍率。
   */
  peakWindow?: string
  peakRateMultiplier?: number | null
}>()

const { t } = useI18n()

function compactTokenLabel(family: 'discountedPrice' | 'officialPrice'): string {
  return [
    t(`modelPlaza.table.${family}`),
    t('modelPlaza.table.unitPerMillion'),
    t('modelPlaza.table.input')
  ].join('\n')
}

const PER_MILLION = 1_000_000

/**
 * 展示顺序:
 * 1. token 计费的排在前,按图/按次计费的沉到末尾——它们的官方 token 价与实付的按张/按次价不同量纲,混排无意义;
 * 2. 组内按官方输出价从高到低,无官方价的排最后;
 * 3. 同价按名称降序(新版本号在前,如 gpt-5.6 先于 gpt-5.5)。
 */
const sortedModels = computed(() => {
  return [...props.models].sort((a, b) => {
    const ta = billingMode(a) === BILLING_MODE_TOKEN
    const tb = billingMode(b) === BILLING_MODE_TOKEN
    if (ta !== tb) return ta ? -1 : 1
    const pa = a.official_pricing?.output_price ?? null
    const pb = b.official_pricing?.output_price ?? null
    if (pa != null && pb != null && pa !== pb) return pb - pa
    if (pa != null && pb == null) return -1
    if (pa == null && pb != null) return 1
    return b.name.localeCompare(a.name)
  })
})

const effectiveRate = computed(() => props.userRateMultiplier ?? props.rateMultiplier)
const hasCustomRate = computed(
  () => props.userRateMultiplier != null && props.userRateMultiplier !== props.rateMultiplier
)

function billingMode(m: PlazaModel): BillingMode {
  return (m.pricing?.billing_mode || BILLING_MODE_TOKEN) as BillingMode
}

function billingModeLabel(m: PlazaModel): string {
  return billingMode(m) === BILLING_MODE_IMAGE
    ? t('modelPlaza.table.perImage')
    : t('modelPlaza.table.perRequest')
}

/** 价格统一保底 2 位小数,更长的有效小数原样保留。 */
const MIN_DECIMALS = 2

/** 表格行:每个模型一行标准价;配置了分时倍率的模型再按时段各加一行。 */
interface PlazaRow {
  model: PlazaModel
  period: PlazaTimePricingPeriod | null
  key: string
}

const rows = computed<PlazaRow[]>(() =>
  sortedModels.value.flatMap((m) => {
    const base: PlazaRow = { model: m, period: null, key: `${m.platform}:${m.name}` }
    const periodRows = timePeriods(m).map<PlazaRow>((p, idx) => ({
      model: m,
      period: p,
      key: `${m.platform}:${m.name}:${idx}`
    }))
    return [base, ...periodRows]
  })
)

/** 时段行的生效倍率 = 生效倍率 × 时段倍率(去掉浮点噪声)。 */
function periodRate(period: PlazaTimePricingPeriod): number {
  return Math.round(effectiveRate.value * period.multiplier * 1000) / 1000
}

/** 可展示的 token 单价字段名。 */
type TokenPriceKey = 'input_price' | 'output_price' | 'cache_write_price' | 'cache_write_1h_price' | 'cache_write_12h_price' | 'cache_read_price'

/**
 * 折后价的展示数据源——design.md §4.3 的核心契约。
 *
 * 优先用后端预计算的 `discounted_pricing`：由 `PlazaDiscountedPricing` 按**生效倍率**
 * 折算，已含登录态专属倍率与图片/视频独立倍率，与计费同源。前端若再乘一次
 * `rate_multiplier`，专属倍率用户会看到与实付不一致的价格。
 *
 * 仅当后端未下发（旧响应）时才回退本地 `pricing × 生效倍率`。
 */
const displayPricing = computed<Map<string, UserSupportedModelPricing>>(() => {
  const out = new Map<string, UserSupportedModelPricing>()
  for (const m of props.models) {
    if (m.pricing == null) continue
    out.set(modelKey(m), m.discounted_pricing ?? scalePricing(m.pricing, effectiveRate.value))
  }
  return out
})

/** 模型在投影里的主键：composite 分组下同名模型来自不同平台。 */
function modelKey(m: PlazaModel): string {
  return `${m.platform}:${m.name}`
}

/** 取模型折后价投影（未乘分时倍率）。 */
function discounted(m: PlazaModel): UserSupportedModelPricing | null {
  return displayPricing.value.get(modelKey(m)) ?? null
}

/**
 * 峰时价按生效倍率折算后展示：峰价是模型级绝对价（未乘倍率），
 * 折算后作为折后价的详情，不再独立成列。
 */
function peakPrice(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value * effectiveRate.value, PER_MILLION, MIN_DECIMALS)
}

/** 实付/折后价展示（$/1M token），时段行再乘时段倍率。 */
function paidPerMillion(
  m: PlazaModel,
  key: TokenPriceKey,
  period: PlazaTimePricingPeriod | null = null
): string {
  const value = discounted(m)?.[key]
  if (value == null) return '-'
  const scaled = period ? value * period.multiplier : value
  return formatScaled(scaled, PER_MILLION, MIN_DECIMALS)
}

/** 阶梯档的折后价展示：优先按档位区间匹配后端折后价阶梯，未下发时按解析后的档位价 × 生效倍率。 */
function paidPerMillionTier(
  m: PlazaModel,
  iv: UserPricingInterval,
  key: TokenPriceKey,
  period: PlazaTimePricingPeriod | null = null
): string {
  const value = discountedTierValue(m, iv, key)
  if (value == null) return '-'
  const scaled = period ? value * period.multiplier : value
  return formatScaled(scaled, PER_MILLION, MIN_DECIMALS)
}

/**
 * 档位折后单价：后端 `discounted_pricing.intervals` 与 `pricing.intervals` 同源同序，
 * 但展示前会按上下文排序/过滤，因此按区间（min/max tokens）配对而非下标。
 * iv 传入的已是 `resolveIntervalPrices` 解析过相对倍率的档位价。
 */
function discountedTierValue(
  m: PlazaModel,
  iv: UserPricingInterval,
  key: TokenPriceKey
): number | null {
  const preset = m.discounted_pricing?.intervals?.find(
    (d) => d.min_tokens === iv.min_tokens && d.max_tokens === iv.max_tokens
  )?.[key]
  if (preset != null) return preset
  const base = iv[key]
  return base == null ? null : base * effectiveRate.value
}

/** 图片和视频分别使用对应的独立倍率开关。 */
function usesIndependentMediaRate(m: PlazaModel): boolean {
  return (billingMode(m) === BILLING_MODE_IMAGE && props.imageRateIndependent === true) ||
    (billingMode(m) === BILLING_MODE_VIDEO && props.videoRateIndependent === true)
}

/** 非 token 行的生效倍率，图片和视频可分别覆盖通用倍率。 */
function requestRate(m: PlazaModel): number {
  if (billingMode(m) === BILLING_MODE_VIDEO && props.videoRateIndependent === true) {
    return Math.max(0, props.videoRateMultiplier ?? 1)
  }
  return billingMode(m) === BILLING_MODE_IMAGE && props.imageRateIndependent === true
    ? (props.imageRateMultiplier ?? 1)
    : effectiveRate.value
}

/**
 * 非 token 单价（已含独立倍率）展示。
 *
 * 同样优先用 discounted_pricing.per_request_price：后端按独立的图片/视频倍率折算，
 * 前端重复相乘会在独立倍率分组上算错。
 */
function paidRequestPrice(m: PlazaModel, value: number | null | undefined): string {
  if (value == null) return '-'
  const preset = m.discounted_pricing?.per_request_price
  const resolved = preset ?? value * requestRate(m)
  return formatScaled(resolved, 1, MIN_DECIMALS)
}

/** 按次/按图阶梯芯片的折后价：同样按档位区间配对后端折后价阶梯，未下发时本地折算。 */
function paidRequestPriceTier(m: PlazaModel, iv: UserPricingInterval, value: number | null | undefined): string {
  if (value == null) return '-'
  const preset = m.discounted_pricing?.intervals?.find(
    (d) => d.min_tokens === iv.min_tokens && d.max_tokens === iv.max_tokens
  )?.per_request_price
  const resolved = preset ?? value * requestRate(m)
  return formatScaled(resolved, 1, MIN_DECIMALS)
}

/** 官方参考价不乘倍率。 */
function official(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value, PER_MILLION, MIN_DECIMALS)
}

/** 非 token 计费的单位后缀:按图片 → “/ 张”,按次 → “/ 次”。 */
function perUnitSuffix(m: PlazaModel): string {
  return billingMode(m) === BILLING_MODE_IMAGE
    ? t('modelPlaza.table.perUnitImage')
    : t('modelPlaza.table.perUnitRequest')
}

function hasCachePricing(m: PlazaModel): boolean {
  return m.pricing?.cache_write_price != null || m.pricing?.cache_write_1h_price != null || m.pricing?.cache_write_12h_price != null || m.pricing?.cache_read_price != null
}

function hasOfficialCache(o: NonNullable<PlazaModel['official_pricing']>): boolean {
  return o.cache_write_price != null || o.cache_read_price != null || o.cache_write_1h_price != null || o.cache_write_12h_price != null
}

/** 分时倍率时段(后端只给出倍率 ≠ 1 的时段,已升序)。 */
function timePeriods(m: PlazaModel): PlazaTimePricingPeriod[] {
  return m.time_pricing?.periods ?? []
}

/**
 * 时段行 tooltip:仅工作日生效的配置换用带周末回落说明的文案;
 * 分组启用高峰倍率时追加披露——本行价格不含高峰因子,与高峰窗口重叠的部分实付再乘高峰倍率。
 */
function timePricingRowHint(m: PlazaModel): string {
  const key = m.time_pricing?.weekdays_only
    ? 'modelPlaza.table.timePricingRowHintWeekdays'
    : 'modelPlaza.table.timePricingRowHint'
  let hint = t(key, { timezone: m.time_pricing?.timezone })
  if (props.peakWindow) {
    hint += t('modelPlaza.table.timePricingRowHintPeak', {
      window: props.peakWindow,
      multiplier: props.peakRateMultiplier ?? 1
    })
  }
  return hint
}

/** “00:30–08:30”;整分钟的 HH:mm:ss 省略秒。 */
function formatTimeWindow(p: PlazaTimePricingPeriod): string {
  const clock = (v: string) => v.replace(/^(\d{2}:\d{2}):00$/, '$1')
  return `${clock(p.start_time)}–${clock(p.end_time)}`
}

/** 上下文档位按下限升序展示(后端已升序,此处兜底)。 */
function sortByContext(intervals: UserPricingInterval[]): UserPricingInterval[] {
  return [...intervals].sort((a, b) => a.min_tokens - b.min_tokens)
}

/** token 模式的阶梯定价(内联进输入/输出/缓存列)。 */
function tokenIntervals(m: PlazaModel): UserPricingInterval[] {
  return sortByContext(m.pricing?.intervals ?? []).map(iv => resolveIntervalPrices(iv, m.pricing!))
}

/** 官方阶梯(后端按目录规则合成,不受分组开关影响)。 */
function officialIntervals(m: PlazaModel): UserPricingInterval[] {
  return sortByContext(m.official_pricing?.intervals ?? [])
}

/** 任一档带缓存价才按档渲染缓存列;否则沿用平价的写入/读取两行。 */
function hasTierCachePricing(intervals: UserPricingInterval[]): boolean {
  return intervals.some((iv) =>
    iv.cache_write_price != null || iv.cache_write_1h_price != null || iv.cache_write_12h_price != null || iv.cache_read_price != null ||
    iv.cache_write_multiplier != null || iv.cache_read_multiplier != null
  )
}

/** 档位说明:整单按档计价,或(平台旧规则)仅超出部分按档计价。 */
function tierHint(m: PlazaModel): string {
  return m.long_context_basis === 'marginal'
    ? t('modelPlaza.table.tierHintMarginal')
    : t('modelPlaza.table.tierHint')
}


/** 按次/按图模式的阶梯定价(仅保留配了按次价的档位)。 */
function requestIntervals(m: PlazaModel): UserPricingInterval[] {
  return (m.pricing?.intervals ?? []).filter((iv) => iv.per_request_price != null)
}

/**
 * 档位标签:优先后端/管理员给出的 tier_label,否则按区间生成统一形态——
 * 有上限为「≤上限」,末档为「>下限」;档位升序排列,相邻的 ≤100K / ≤200K 即表示 (100K,200K]。
 */
function tierLabel(iv: UserPricingInterval): string {
  if (iv.tier_label) return iv.tier_label
  const { min_tokens: min, max_tokens: max } = iv
  return max == null ? `>${formatTokenCount(min)}` : `≤${formatTokenCount(max)}`
}

function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${trimZero(n / 1_000_000)}M`
  if (n >= 1_000) return `${trimZero(n / 1_000)}K`
  return String(n)
}

function trimZero(n: number): string {
  return String(Math.round(n * 100) / 100)
}
</script>

<style scoped>
/* Keep the effective price prominent across every provider. */
.pz-bg {
  @apply bg-primary-50 dark:bg-primary-900/25;
}

.pz-cell {
  @apply bg-primary-50/50 transition-colors motion-reduce:transition-none dark:bg-primary-900/10;
}

tbody tr:hover .pz-cell {
  @apply bg-primary-50 dark:bg-primary-900/25;
}

.pz-title {
  @apply border-primary-200 text-primary-700 dark:border-primary-800 dark:text-primary-200;
}

.plaza-pricing-table {
  container-type: inline-size;
}

.pz-compact-label {
  display: none;
}

@container (width < 52rem) {
  table,
  tbody {
    display: block;
    min-width: 0;
  }

  thead {
    @apply sr-only;
  }

  colgroup {
    display: none;
  }

  tbody tr {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    @apply border-b border-accent-200 dark:border-dark-700;
  }

  tbody td {
    min-width: 0;
    overflow-wrap: anywhere;
    @apply border-0 px-4 py-2;
  }

  tbody td .whitespace-nowrap {
    white-space: normal;
  }

  .pz-model,
  .pz-discounted-detail,
  .pz-official-cache {
    grid-column: 1 / -1;
  }

  .pz-model {
    @apply py-3;
  }

  .pz-compact-label {
    display: flex;
    align-items: flex-end;
    @apply mb-1 font-sans text-xs font-medium text-accent-600 dark:text-dark-300;
  }

  .pz-compact-label::before {
    content: attr(data-label);
    white-space: pre-line;
  }

  .pz-compact-core .pz-compact-label {
    min-height: 3lh;
  }

  .pz-discounted-detail,
  .pz-official-cache {
    @apply pb-3;
  }
}

.pz-unit {
  @apply text-primary-700 dark:text-primary-200;
}
</style>
