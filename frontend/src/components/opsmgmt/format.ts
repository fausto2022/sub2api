/**
 * 运营管理界面的纯展示格式化工具（无 Vue 依赖，便于单测）。
 *
 * 口径与后端一致：
 *   - 金额为 USD 浮点（后端已按分位四舍五入）；
 *   - 利润率 / 保底比例以「基点」下发（10000 = 100%）；
 *   - 倍率以「微倍率」下发（1_000_000 = 1.000000x）。
 */

/** 金额：$1,284.50（千分位 + 两位小数）。非法值返回占位符。 */
export function formatUsd(value: number | null | undefined, digits = 2): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '—'
  const fixed = Number(value).toFixed(digits)
  const [intPart, decimalPart] = fixed.split('.')
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return decimalPart ? `$${grouped}.${decimalPart}` : `$${grouped}`
}

/** 基点 → 百分比字符串：3055 → 30.55%。 */
export function formatBasisPoints(basisPoints: number | null | undefined, digits = 2): string {
  if (basisPoints === null || basisPoints === undefined || !Number.isFinite(basisPoints)) return '—'
  return `${(Number(basisPoints) / 100).toFixed(digits)}%`
}

/** 基点差 → 百分点字符串：+420 → +4.20pp。 */
export function formatBasisPointDelta(delta: number | null | undefined, digits = 2): string {
  if (delta === null || delta === undefined || !Number.isFinite(delta)) return '—'
  const value = Number(delta) / 100
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(digits)}pp`
}

/** 微倍率 → 倍率数值：1_200_000 → 1.2。 */
export function microsToMultiplier(micros: number | null | undefined): number | null {
  if (micros === null || micros === undefined || !Number.isFinite(micros)) return null
  return Number(micros) / 1_000_000
}

/** 倍率展示：1.2 → 1.20x。 */
export function formatMultiplier(value: number | null | undefined, digits = 2): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '—'
  return `${Number(value).toFixed(digits)}x`
}

/**
 * 环比百分比：baseline 为 0 / 缺失时返回 null（不编造 0%）。
 */
export function changePercent(
  current: number | null | undefined,
  baseline: number | null | undefined
): number | null {
  if (current === null || current === undefined || !Number.isFinite(current)) return null
  if (baseline === null || baseline === undefined || !Number.isFinite(baseline)) return null
  if (Math.abs(Number(baseline)) < 1e-9) return null
  return ((Number(current) - Number(baseline)) / Math.abs(Number(baseline))) * 100
}

/** 环比文本：12.3 → +12.3%，-4.2 → -4.2%，null → —。 */
export function formatChangePercent(percent: number | null | undefined, digits = 1): string {
  if (percent === null || percent === undefined || !Number.isFinite(percent)) return '—'
  const value = Number(percent)
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(digits)}%`
}

export type ChangeTrend = 'up' | 'down' | 'flat'

/** 由环比数值推导方向（用于箭头 + 文字标签，不单靠颜色）。 */
export function changeTrend(percent: number | null | undefined, epsilon = 0.05): ChangeTrend {
  if (percent === null || percent === undefined || !Number.isFinite(percent)) return 'flat'
  if (percent > epsilon) return 'up'
  if (percent < -epsilon) return 'down'
  return 'flat'
}

/** ISO 时间 → 本地短格式（yyyy-MM-dd HH:mm）；非法值返回 —。 */
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const pad = (input: number) => String(input).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(
    date.getHours()
  )}:${pad(date.getMinutes())}`
}

/** ISO 时间 → yyyy-MM-dd（趋势图横轴）。 */
export function formatDay(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (input: number) => String(input).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

/** 分组利润率是否亏损（负利润率或负利润）。 */
export function isLossRow(row: { profit?: number; margin_basis_points?: number }): boolean {
  if (typeof row.profit === 'number' && row.profit < 0) return true
  if (typeof row.margin_basis_points === 'number' && row.margin_basis_points < 0) return true
  return false
}
