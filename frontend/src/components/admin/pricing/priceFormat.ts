/**
 * 模型价格展示/输入格式工具。
 *
 * 约定（design.md §3.4）：USD 显示 4 位小数、CNY 显示 2 位小数；
 * 数值单元格需配合 `tabular-nums` 类等宽对齐。
 */

import type { CurrencyMode } from '@/api/admin/modelPricing'

const USD_FRACTION_DIGITS = 4
const CNY_FRACTION_DIGITS = 2

function isFiniteNumber(value: number | null | undefined): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

/** USD / 1M tokens → 4 位小数字符串；非有限值显示占位符。 */
export function formatUsdPerMTok(value: number | null | undefined): string {
  if (!isFiniteNumber(value)) return '—'
  return value.toFixed(USD_FRACTION_DIGITS)
}

/** CNY / 1M tokens → 2 位小数字符串；非有限值显示占位符。 */
export function formatCnyPerMTok(value: number | null | undefined): string {
  if (!isFiniteNumber(value)) return '—'
  return value.toFixed(CNY_FRACTION_DIGITS)
}

/** 人民币折算成 USD 的比率：direct = 1:1，convert = ÷ cny_per_usd。 */
export function cnyToUsdRate(mode: CurrencyMode, cnyPerUsd: number): number {
  if (mode === 'direct') return 1
  return isFiniteNumber(cnyPerUsd) && cnyPerUsd > 0 ? cnyPerUsd : 1
}

/**
 * 「一键还原人民币价」的乘数：把内置表里「¥官方价 ÷ 汇率」折算出来的美元价
 * 乘回汇率。direct 口径本身已是 1:1，乘数为 1（按钮此时置灰）。
 */
export function cnyRestoreRate(mode: CurrencyMode, cnyPerUsd: number): number {
  return cnyToUsdRate(mode, cnyPerUsd)
}

/** 乘数的人读展示：去掉无意义尾零（7.14、8、6.9…）。 */
export function formatRate(rate: number): string {
  if (!isFiniteNumber(rate) || rate <= 0) return '1'
  return String(Math.round(rate * 1e6) / 1e6)
}

/** USD 价 → 人民币等值（convert 乘汇率，direct 乘 1），2 位小数。 */
export function usdToCnyValue(usd: number, mode: CurrencyMode, cnyPerUsd: number): string {
  if (!isFiniteNumber(usd)) return '—'
  return formatCnyPerMTok(usd * cnyToUsdRate(mode, cnyPerUsd))
}

/** 编辑框初始值：保留最多 6 位小数并去掉无意义的尾零。 */
export function formatPriceInput(value: number | null | undefined): string {
  if (!isFiniteNumber(value)) return ''
  const rounded = Math.round(value * 1e6) / 1e6
  return String(rounded)
}

/**
 * 一键还原：把输入框里的字符串价 × 乘数，返回新的输入框字符串。
 *
 * 规则（design.md §0.3）：
 * - 空输入保持为空 —— 不把「未配置」变成「配了值」；
 * - `0` 保持 `0` —— 0 × 7.14 仍然是 0，不能变成空；
 * - 其它值按 6 位小数取整后回写，避免 `0.3 × 7.14 = 2.1420000000000003`。
 */
export function restoreCnyPriceInput(raw: string, rate: number): string {
  const trimmed = raw.trim()
  if (trimmed === '') return raw
  const value = Number(trimmed)
  if (!Number.isFinite(value)) return raw
  if (value === 0) return '0'
  const multiplier = isFiniteNumber(rate) && rate > 0 ? rate : 1
  return formatPriceInput(value * multiplier)
}
