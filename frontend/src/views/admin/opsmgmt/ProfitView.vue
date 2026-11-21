<template>
  <AppLayout>
    <div class="space-y-6 pb-12" data-testid="opsmgmt-profit">
      <!-- 页头 -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h1 class="text-lg font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.profit.title') }}
          </h1>
          <p class="mt-0.5 text-xs text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.profit.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="evaluating"
            data-testid="opsmgmt-profit-evaluate"
            @click="evaluate"
          >
            <Icon name="bolt" size="sm" class="mr-1" />
            {{ evaluating ? t('admin.opsmgmt.profit.pending.evaluating') : t('admin.opsmgmt.profit.pending.evaluate') }}
          </button>
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loading"
            :title="t('admin.opsmgmt.refresh')"
            :aria-label="t('admin.opsmgmt.refresh')"
            data-testid="opsmgmt-profit-refresh"
            @click="loadAll"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </div>

      <div
        v-if="errorMessage"
        class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
        data-testid="opsmgmt-profit-error"
      >
        {{ errorMessage }}
      </div>
      <div
        v-if="notice"
        class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-900/20 dark:text-emerald-200"
        data-testid="opsmgmt-profit-notice"
      >
        {{ notice }}
      </div>

      <!-- 全局开关 -->
      <section class="card p-5" data-testid="opsmgmt-profit-settings">
        <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
          {{ t('admin.opsmgmt.profit.settings.title') }}
        </h2>

        <p
          v-if="settings && !settings.auto_margin_protection"
          class="mt-2 flex items-center gap-2 rounded-xl bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-500/15 dark:text-amber-200"
          data-testid="opsmgmt-profit-gray-hint"
        >
          <Icon name="exclamationTriangle" size="sm" aria-hidden="true" />
          {{ t('admin.opsmgmt.profit.settings.grayHint') }}
        </p>

        <div class="mt-4 grid grid-cols-1 gap-5 lg:grid-cols-2">
          <div class="space-y-4">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="text-sm font-medium text-accent-900 dark:text-dark-100">
                  {{ t('admin.opsmgmt.profit.settings.autoProtection') }}
                </p>
                <p class="text-xs text-accent-600 dark:text-dark-300">
                  {{ t('admin.opsmgmt.profit.settings.autoProtectionHint') }}
                </p>
              </div>
              <Toggle v-model="form.auto_margin_protection" />
            </div>
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="text-sm font-medium text-accent-900 dark:text-dark-100">
                  {{ t('admin.opsmgmt.profit.settings.autoRecovery') }}
                </p>
                <p class="text-xs text-accent-600 dark:text-dark-300">
                  {{ t('admin.opsmgmt.profit.settings.autoRecoveryHint') }}
                </p>
              </div>
              <Toggle v-model="form.auto_recovery" />
            </div>

            <div>
              <label class="input-label" for="opsmgmt-profit-min-margin">{{ t('admin.opsmgmt.profit.settings.minMargin') }}</label>
              <input
                id="opsmgmt-profit-min-margin"
                v-model.number="form.minimum_margin_percent"
                type="number"
                min="0"
                max="99"
                step="0.01"
                class="input tabular-nums"
                data-testid="opsmgmt-profit-min-margin"
              />
              <p class="input-hint">{{ t('admin.opsmgmt.profit.settings.minMarginHint') }}</p>
            </div>
          </div>

          <div class="space-y-4">
            <div>
              <label class="input-label" for="opsmgmt-profit-risk-confirmations">
                {{ t('admin.opsmgmt.profit.settings.riskConfirmations') }}
              </label>
              <input
                id="opsmgmt-profit-risk-confirmations"
                v-model.number="form.risk_confirmations"
                type="number"
                min="1"
                max="50"
                step="1"
                class="input tabular-nums"
                data-testid="opsmgmt-profit-risk-confirmations"
              />
              <p class="input-hint">{{ t('admin.opsmgmt.profit.settings.riskConfirmationsHint') }}</p>
            </div>
            <div>
              <label class="input-label" for="opsmgmt-profit-guaranteed">
                {{ t('admin.opsmgmt.profit.settings.guaranteedRatio') }}
              </label>
              <input
                id="opsmgmt-profit-guaranteed"
                v-model.number="form.guaranteed_ratio_percent"
                type="number"
                min="1"
                max="1000"
                step="0.01"
                class="input tabular-nums"
                data-testid="opsmgmt-profit-guaranteed"
              />
              <p class="input-hint">{{ t('admin.opsmgmt.profit.settings.guaranteedRatioHint') }}</p>
            </div>
            <div class="grid grid-cols-2 gap-3">
              <div>
                <label class="input-label" for="opsmgmt-profit-cost-age">{{ t('admin.opsmgmt.profit.settings.costMaxAge') }}</label>
                <input
                  id="opsmgmt-profit-cost-age"
                  v-model.number="form.cost_max_age_minutes"
                  type="number"
                  min="1"
                  max="10080"
                  step="1"
                  class="input tabular-nums"
                  data-testid="opsmgmt-profit-cost-age"
                />
              </div>
              <div>
                <label class="input-label" for="opsmgmt-profit-sync-interval">{{ t('admin.opsmgmt.profit.settings.syncInterval') }}</label>
                <input
                  id="opsmgmt-profit-sync-interval"
                  v-model.number="form.sync_interval_minutes"
                  type="number"
                  min="1"
                  max="10080"
                  step="1"
                  class="input tabular-nums"
                  data-testid="opsmgmt-profit-sync-interval"
                />
              </div>
            </div>
          </div>
        </div>

        <div class="mt-4 flex justify-end">
          <button
            type="button"
            class="btn btn-primary"
            :disabled="savingSettings"
            data-testid="opsmgmt-profit-settings-save"
            @click="saveSettings"
          >
            {{ savingSettings ? t('admin.opsmgmt.saving') : t('admin.opsmgmt.profit.settings.save') }}
          </button>
        </div>
      </section>

      <!-- 待处理 -->
      <section class="card overflow-hidden" data-testid="opsmgmt-profit-pending">
        <div class="border-b border-accent-200 px-5 py-3 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.profit.pending.title') }}
          </h2>
        </div>
        <div v-if="pendingChecks.length === 0" class="px-5 py-6 text-sm text-accent-600 dark:text-dark-300" data-testid="opsmgmt-profit-pending-empty">
          {{ t('admin.opsmgmt.profit.pending.empty') }}
        </div>
        <div v-else class="overflow-x-auto">
          <table class="table w-full">
            <thead>
              <tr>
                <th class="text-left">{{ t('admin.opsmgmt.profit.pending.group') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.pending.rateChange') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.profit.pending.margin') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.pending.status') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.pending.impact') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.history.time') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="check in pendingChecks" :key="check.id" :data-testid="`opsmgmt-profit-pending-row-${check.id}`">
                <td class="text-left font-medium text-accent-900 dark:text-dark-100">
                  {{ check.group_name || `#${check.remote_group_id}` }}
                </td>
                <td class="text-left">
                  <RateChangeCell
                    :current="microsToMultiplier(check.cost_multiplier_micros)"
                    :previous="microsToMultiplier(check.sale_multiplier_micros)"
                    :test-id="`opsmgmt-profit-rate-${check.id}`"
                  />
                </td>
                <td class="text-right tabular-nums text-red-700 dark:text-red-300">
                  {{ formatBasisPoints(check.margin_basis_points) }}
                </td>
                <td class="text-left">
                  <ProfitStatusBadge :status="check.status" :test-id="`opsmgmt-profit-status-${check.id}`" />
                </td>
                <!-- 灰度语义：自动保护关闭时只告警，开启时才会真停用 -->
                <td class="text-left">
                  <span
                    class="badge"
                    :class="settings?.auto_margin_protection ? 'badge-danger' : 'badge-warning'"
                    :data-testid="`opsmgmt-profit-action-${check.id}`"
                  >
                    {{
                      settings?.auto_margin_protection
                        ? t('admin.opsmgmt.profit.pending.autoDisabled')
                        : t('admin.opsmgmt.profit.pending.onlyWarn')
                    }}
                  </span>
                </td>
                <td class="text-left text-xs tabular-nums text-accent-600 dark:text-dark-300">
                  {{ formatDateTime(check.created_at) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- 已自动停用（本地 Channel） -->
      <section class="card overflow-hidden" data-testid="opsmgmt-profit-disabled">
        <div class="border-b border-accent-200 px-5 py-3 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.profit.disabled.title') }}
          </h2>
        </div>
        <div v-if="disabledChannels.length === 0" class="px-5 py-6 text-sm text-accent-600 dark:text-dark-300" data-testid="opsmgmt-profit-disabled-empty">
          {{ t('admin.opsmgmt.profit.disabled.empty') }}
        </div>
        <div v-else class="overflow-x-auto">
          <table class="table w-full">
            <thead>
              <tr>
                <th class="text-left">{{ t('admin.opsmgmt.profit.disabled.channel') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.disabled.site') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.disabled.reason') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.disabled.at') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.profit.disabled.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="channel in disabledChannels" :key="channel.channel_id" :data-testid="`opsmgmt-profit-disabled-row-${channel.channel_id}`">
                <td class="text-left font-medium text-accent-900 dark:text-dark-100">{{ channel.channel_name }}</td>
                <td class="text-left text-accent-600 dark:text-dark-300">{{ siteName(channel.site_id) }}</td>
                <td class="text-left">
                  <SourceBadge
                    kind="opsmgmt"
                    :site-name="siteName(channel.site_id)"
                    :remote-group-id="channel.remote_group_id ?? null"
                    :auto-disabled-reason="channel.auto_disabled_reason"
                    :auto-disabled-at="formatDateTime(channel.auto_disabled_at)"
                    :test-id="`opsmgmt-profit-disabled-source-${channel.channel_id}`"
                  />
                </td>
                <td class="text-left text-xs tabular-nums text-accent-600 dark:text-dark-300">
                  {{ formatDateTime(channel.auto_disabled_at) }}
                </td>
                <td class="text-right">
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm"
                    :disabled="restoringId === channel.channel_id"
                    :data-testid="`opsmgmt-profit-restore-${channel.channel_id}`"
                    @click="restore(channel.channel_id, channel.channel_name)"
                  >
                    {{ restoringId === channel.channel_id ? t('admin.opsmgmt.profit.disabled.restoring') : t('admin.opsmgmt.profit.disabled.restore') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- 变更历史（时间线） -->
      <section class="card p-5" data-testid="opsmgmt-profit-history">
        <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
          {{ t('admin.opsmgmt.profit.history.title') }}
        </h2>
        <p v-if="history.length === 0" class="mt-3 text-sm text-accent-600 dark:text-dark-300" data-testid="opsmgmt-profit-history-empty">
          {{ t('admin.opsmgmt.profit.history.empty') }}
        </p>
        <ol v-else class="mt-3 space-y-3 border-l border-accent-200 pl-4 dark:border-dark-700">
          <li
            v-for="check in history"
            :key="`history-${check.id}`"
            class="relative"
            :data-testid="`opsmgmt-profit-history-row-${check.id}`"
          >
            <span
              class="absolute -left-[21px] top-2 h-2 w-2 rounded-full"
              :class="check.status === 'risk' ? 'bg-red-500' : check.status === 'healthy' ? 'bg-emerald-500' : 'bg-accent-400 dark:bg-dark-400'"
              aria-hidden="true"
            />
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-accent-900 dark:text-dark-100">
                {{ check.group_name || `#${check.remote_group_id}` }}
              </span>
              <ProfitStatusBadge :status="check.status" :test-id="`opsmgmt-profit-history-status-${check.id}`" />
              <span class="text-xs tabular-nums text-accent-600 dark:text-dark-300">{{ formatDateTime(check.created_at) }}</span>
            </div>
            <p class="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs tabular-nums text-accent-600 dark:text-dark-300">
              <span>
                {{ t('admin.opsmgmt.profit.history.multiplier') }}:
                {{ formatMultiplier(microsToMultiplier(check.sale_multiplier_micros)) }} →
                {{ formatMultiplier(microsToMultiplier(check.cost_multiplier_micros)) }}
              </span>
              <span>
                {{ t('admin.opsmgmt.profit.history.margin') }}: {{ formatBasisPoints(check.margin_basis_points) }}
              </span>
              <span>
                {{ check.cost_reliable ? t('admin.opsmgmt.profit.history.reliable') : t('admin.opsmgmt.profit.history.unreliable') }}
              </span>
            </p>
          </li>
        </ol>
      </section>

      <!-- 分组最低利润率 -->
      <section class="card overflow-hidden" data-testid="opsmgmt-profit-margins">
        <div class="border-b border-accent-200 px-5 py-3 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.profit.margins.title') }}
          </h2>
        </div>
        <div v-if="groupMargins.length === 0" class="px-5 py-6 text-sm text-accent-600 dark:text-dark-300">
          {{ t('admin.opsmgmt.profit.margins.empty') }}
        </div>
        <div v-else class="overflow-x-auto">
          <table class="table w-full">
            <thead>
              <tr>
                <th class="text-left">{{ t('admin.opsmgmt.profit.margins.group') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.profit.margins.own') }}</th>
                <th class="text-right">{{ t('admin.opsmgmt.profit.margins.effective') }}</th>
                <th class="text-left">{{ t('admin.opsmgmt.profit.margins.source') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="group in groupMargins" :key="group.id" :data-testid="`opsmgmt-profit-margin-row-${group.id}`">
                <td class="text-left font-medium text-accent-900 dark:text-dark-100">{{ group.name }}</td>
                <td class="text-right tabular-nums">
                  {{ group.opsmgmt_min_margin_basis_points === null ? t('admin.opsmgmt.profit.margins.inherited') : formatBasisPoints(group.opsmgmt_min_margin_basis_points) }}
                </td>
                <td class="text-right tabular-nums">{{ formatBasisPoints(group.effective_min_margin_basis_points) }}</td>
                <td class="text-left text-xs text-accent-600 dark:text-dark-300">{{ marginSourceLabel(group.min_margin_source) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import RateChangeCell from '@/components/opsmgmt/RateChangeCell.vue'
import SourceBadge from '@/components/opsmgmt/SourceBadge.vue'
import ProfitStatusBadge from '@/components/opsmgmt/ProfitStatusBadge.vue'
import { formatBasisPoints, formatDateTime, formatMultiplier, microsToMultiplier } from '@/components/opsmgmt/format'
import {
  opsMgmtAPI,
  type OpsChannelLink,
  type OpsLocalGroup,
  type OpsMgmtSettings,
  type OpsProfitCheck,
  type OpsSite
} from '@/api/admin/opsmgmt'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

const settings = ref<OpsMgmtSettings | null>(null)
const sites = ref<OpsSite[]>([])
const pendingChecks = ref<OpsProfitCheck[]>([])
const history = ref<OpsProfitCheck[]>([])
const disabledChannels = ref<OpsChannelLink[]>([])
const groupMargins = ref<OpsLocalGroup[]>([])

const loading = ref(false)
const savingSettings = ref(false)
const evaluating = ref(false)
const restoringId = ref<number | null>(null)
const errorMessage = ref('')
const notice = ref('')

/** 表单镜像：百分比与后端基点/整数之间在提交时换算。 */
const form = reactive({
  auto_margin_protection: false,
  auto_recovery: true,
  minimum_margin_percent: 10,
  risk_confirmations: 2,
  guaranteed_ratio_percent: 100,
  cost_max_age_minutes: 60,
  sync_interval_minutes: 30
})

const siteNames = computed(() => {
  const map = new Map<number, string>()
  for (const site of sites.value) map.set(site.id, site.name)
  return map
})

function siteName(siteId: number | null | undefined): string {
  if (siteId === null || siteId === undefined) return '—'
  return siteNames.value.get(siteId) ?? `#${siteId}`
}

function marginSourceLabel(source: string): string {
  switch (source) {
    case 'group':
      return t('admin.opsmgmt.profit.margins.sourceGroup')
    case 'upstream':
      return t('admin.opsmgmt.profit.margins.sourceUpstream')
    default:
      return t('admin.opsmgmt.profit.margins.sourceGlobal')
  }
}

function applySettings(next: OpsMgmtSettings): void {
  settings.value = next
  form.auto_margin_protection = next.auto_margin_protection
  form.auto_recovery = next.auto_recovery
  form.minimum_margin_percent = Math.round((next.minimum_margin_basis_points / 100) * 100) / 100
  form.risk_confirmations = next.risk_confirmations
  form.guaranteed_ratio_percent = Math.round((next.guaranteed_revenue_ratio_bp / 100) * 100) / 100
  form.cost_max_age_minutes = next.cost_max_age_minutes
  form.sync_interval_minutes = next.sync_interval_minutes
}

async function loadAll(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const [settingsResponse, sitesResponse, pendingResponse, historyResponse, disabledResponse, marginsResponse] =
      await Promise.all([
        opsMgmtAPI.getSettings(),
        opsMgmtAPI.listSites().catch(() => ({ items: [], total: 0 })),
        opsMgmtAPI.listProfitChecks({ status: 'risk', page: 1, page_size: 50 }),
        opsMgmtAPI.listProfitChecks({ page: 1, page_size: 50 }),
        opsMgmtAPI.listAutoDisabledChannels().catch(() => ({ items: [], total: 0 })),
        opsMgmtAPI.listGroupMargins().catch(() => ({ items: [], total: 0 }))
      ])
    applySettings(settingsResponse)
    sites.value = sitesResponse.items || []
    pendingChecks.value = pendingResponse.items || []
    history.value = historyResponse.items || []
    disabledChannels.value = disabledResponse.items || []
    groupMargins.value = marginsResponse.items || []
  } catch (caught) {
    errorMessage.value = extractApiErrorMessage(caught)
  } finally {
    loading.value = false
  }
}

async function saveSettings(): Promise<void> {
  if (savingSettings.value) return
  savingSettings.value = true
  errorMessage.value = ''
  notice.value = ''
  try {
    const updated = await opsMgmtAPI.updateSettings({
      auto_margin_protection: form.auto_margin_protection,
      auto_recovery: form.auto_recovery,
      minimum_margin_basis_points: Math.round(Number(form.minimum_margin_percent) * 100),
      risk_confirmations: Number(form.risk_confirmations),
      guaranteed_revenue_ratio_bp: Math.round(Number(form.guaranteed_ratio_percent) * 100),
      cost_max_age_minutes: Number(form.cost_max_age_minutes),
      sync_interval_minutes: Number(form.sync_interval_minutes)
    })
    applySettings(updated)
    notice.value = t('admin.opsmgmt.profit.settings.saved')
  } catch (caught) {
    errorMessage.value = extractApiErrorMessage(caught)
  } finally {
    savingSettings.value = false
  }
}

async function evaluate(): Promise<void> {
  if (evaluating.value) return
  evaluating.value = true
  errorMessage.value = ''
  notice.value = ''
  try {
    const task = await opsMgmtAPI.evaluateProfit()
    notice.value = t('admin.opsmgmt.profit.pending.evaluateQueued', { id: task.id })
    // 评估是异步任务：等终态再刷新列表，避免显示上一轮结果。
    await pollTask(task.id)
  } catch (caught) {
    errorMessage.value = extractApiErrorMessage(caught)
  } finally {
    evaluating.value = false
    await loadAll()
  }
}

/** 轮询任务到终态（最多 ~60s），仅用于「立即评估」的进度反馈。 */
async function pollTask(taskId: string): Promise<void> {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    const task = await opsMgmtAPI.getTask(taskId)
    if (task.status === 'succeeded' || task.status === 'failed') return
    await new Promise((resolve) => setTimeout(resolve, 1000))
  }
}

async function restore(channelId: number, channelName: string): Promise<void> {
  if (restoringId.value !== null) return
  restoringId.value = channelId
  errorMessage.value = ''
  notice.value = ''
  try {
    await opsMgmtAPI.restoreChannel(channelId)
    notice.value = t('admin.opsmgmt.profit.disabled.restoreSuccess', { name: channelName })
    await loadAll()
  } catch (caught) {
    errorMessage.value = extractApiErrorMessage(caught)
  } finally {
    restoringId.value = null
  }
}

onMounted(loadAll)
</script>
