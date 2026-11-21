/**
 * 运营管理组件库（components/opsmgmt）。
 *
 * 全部遵循 design-system/sub2api/MASTER.md 方向 B 令牌：
 * 只用 primary / accent / dark / cyan / emerald / red / amber 语义色，
 * 不出现裸 hex / rgb；数字列一律 tabular-nums。
 */
export { default as KpiCard } from './KpiCard.vue'
export { default as TrendChart } from './TrendChart.vue'
export { default as SourceBadge } from './SourceBadge.vue'
export { default as RateChangeCell } from './RateChangeCell.vue'
export { default as ProfitStatusBadge } from './ProfitStatusBadge.vue'
export { default as GroupMarginProtect } from './GroupMarginProtect.vue'

export * from './format'
export * from './source'
export type { TrendPoint, KpiTone, SourceKind } from './types'
