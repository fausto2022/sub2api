/**
 * 运营管理组件的共享类型（`<script setup>` 不允许 export，故独立成文件）。
 */

export interface TrendPoint {
  /** 横轴标签（日期，例如 11-09）。 */
  label: string
  revenue: number
  cost: number
  profit: number
}

export type KpiTone = 'good' | 'bad' | 'neutral'

export type SourceKind = 'opsmgmt' | 'manual'
