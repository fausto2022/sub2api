<template>
  <AppLayout>
    <div class="space-y-6 pb-12" data-testid="opsmgmt-expand">
      <!-- 页头 -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h1 class="text-lg font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.expand.title') }}
          </h1>
          <p class="mt-0.5 text-xs text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.expand.description') }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="loading"
          :title="t('admin.opsmgmt.refresh')"
          :aria-label="t('admin.opsmgmt.refresh')"
          data-testid="opsmgmt-expand-refresh"
          @click="loadBase"
        >
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
      </div>

      <div
        v-if="errorMessage"
        class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
        data-testid="opsmgmt-expand-error"
      >
        {{ errorMessage }}
      </div>

      <div class="grid grid-cols-1 gap-6 xl:grid-cols-3">
        <!-- 向导 -->
        <div class="card space-y-5 p-5 xl:col-span-2" data-testid="opsmgmt-expand-wizard">
          <!-- ① 站点 -->
          <div>
            <label class="input-label" for="opsmgmt-expand-site">
              1 · {{ t('admin.opsmgmt.expand.step.site') }}
            </label>
            <select
              id="opsmgmt-expand-site"
              v-model="selectedSiteId"
              class="input"
              data-testid="opsmgmt-expand-site"
              @change="onSiteChange"
            >
              <option :value="null">{{ t('admin.opsmgmt.expand.sitePlaceholder') }}</option>
              <option v-for="site in sites" :key="site.id" :value="site.id">{{ site.name }}</option>
            </select>
          </div>

          <!-- ② 上游分组 -->
          <div>
            <p class="input-label">2 · {{ t('admin.opsmgmt.expand.step.groups') }}</p>
            <p v-if="!selectedSiteId" class="text-xs text-accent-600 dark:text-dark-300">
              {{ t('admin.opsmgmt.expand.sitePlaceholder') }}
            </p>
            <p
              v-else-if="upstreamGroups.length === 0"
              class="text-xs text-accent-600 dark:text-dark-300"
              data-testid="opsmgmt-expand-groups-empty"
            >
              {{ t('admin.opsmgmt.expand.groupsEmpty') }}
            </p>
            <div v-else class="max-h-64 space-y-1.5 overflow-y-auto rounded-xl border border-accent-200 p-2 dark:border-dark-700">
              <label
                v-for="group in upstreamGroups"
                :key="group.remote_group_id"
                class="flex cursor-pointer items-center justify-between gap-3 rounded-lg px-2 py-1.5 hover:bg-accent-50 dark:hover:bg-dark-800/50"
                :data-testid="`opsmgmt-expand-group-${group.remote_group_id}`"
              >
                <span class="flex min-w-0 items-center gap-2">
                  <input
                    v-model="selectedGroupIds"
                    type="checkbox"
                    :value="group.remote_group_id"
                    class="h-4 w-4 rounded border-accent-300 text-primary-600 focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
                  />
                  <span class="truncate text-sm text-accent-900 dark:text-dark-100">{{ group.name }}</span>
                  <span class="text-xs text-accent-600 dark:text-dark-300">{{ group.platform || '—' }}</span>
                </span>
                <RateChangeCell
                  :current="group.rate_multiplier"
                  :previous="group.previous_rate_multiplier ?? null"
                  :flag="group.rate_changed ? 'changed' : 'auto'"
                  :test-id="`opsmgmt-expand-rate-${group.remote_group_id}`"
                />
              </label>
            </div>
            <p class="input-hint" data-testid="opsmgmt-expand-groups-selected">
              {{ t('admin.opsmgmt.expand.groupsSelected', { count: selectedGroupIds.length }) }}
            </p>
          </div>

          <!-- ③ 上游 Key -->
          <div>
            <label class="input-label" for="opsmgmt-expand-key">
              3 · {{ t('admin.opsmgmt.expand.keyLabel') }}
            </label>
            <input
              id="opsmgmt-expand-key"
              v-model="upstreamKey"
              type="password"
              autocomplete="off"
              class="input"
              :placeholder="t('admin.opsmgmt.expand.keyPlaceholder')"
              data-testid="opsmgmt-expand-key"
            />
            <p class="input-hint">{{ t('admin.opsmgmt.expand.keyHint') }}</p>
          </div>

          <!-- ④ 目标分组 + 数量 -->
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label class="input-label" for="opsmgmt-expand-target">
                4 · {{ t('admin.opsmgmt.expand.targetLabel') }}
              </label>
              <select
                id="opsmgmt-expand-target"
                v-model="targetGroupId"
                class="input"
                data-testid="opsmgmt-expand-target"
              >
                <option :value="null">{{ t('admin.opsmgmt.expand.targetPlaceholder') }}</option>
                <option v-for="group in localGroups" :key="group.id" :value="group.id">
                  {{ group.name }}
                </option>
              </select>
              <p class="input-hint">{{ t('admin.opsmgmt.expand.targetHint') }}</p>
            </div>
            <div>
              <p class="input-label">{{ t('admin.opsmgmt.expand.countLabel') }}</p>
              <p class="text-2xl font-bold tabular-nums text-accent-900 dark:text-dark-50" data-testid="opsmgmt-expand-count">
                {{ selectedGroupIds.length }}
              </p>
              <p class="input-hint">
                {{ t('admin.opsmgmt.expand.countHint', { count: selectedGroupIds.length }) }}
              </p>
            </div>
          </div>

          <div>
            <label class="input-label" for="opsmgmt-expand-name-prefix">{{ t('admin.opsmgmt.expand.channelPrefix') }}</label>
            <input
              id="opsmgmt-expand-name-prefix"
              v-model="channelPrefix"
              type="text"
              class="input"
              data-testid="opsmgmt-expand-name-prefix"
            />
            <p class="input-hint">{{ t('admin.opsmgmt.expand.channelPrefixHint') }}</p>
          </div>

          <div class="flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              class="btn btn-primary"
              :disabled="submitting"
              data-testid="opsmgmt-expand-submit"
              @click="submit"
            >
              <Icon name="bolt" size="sm" class="mr-1" />
              {{ submitting ? t('admin.opsmgmt.expand.submitting') : t('admin.opsmgmt.expand.submit') }}
            </button>
          </div>
        </div>

        <!-- 进度 + 结果 -->
        <div class="space-y-6 xl:col-span-1">
          <section class="card p-5" data-testid="opsmgmt-expand-progress">
            <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
              {{ t('admin.opsmgmt.expand.progress.title') }}
            </h2>
            <p v-if="!task" class="mt-2 text-xs text-accent-600 dark:text-dark-300">
              {{ t('admin.opsmgmt.expand.result.empty') }}
            </p>
            <template v-else>
              <p class="mt-2 flex items-center justify-between text-xs tabular-nums text-accent-600 dark:text-dark-300">
                <span>{{ t('admin.opsmgmt.expand.progress.processed', { done: task.progress, total: task.total }) }}</span>
                <span class="badge" :class="taskStatusClass">{{ taskStatusLabel }}</span>
              </p>
              <div class="mt-2 h-2 w-full overflow-hidden rounded-full bg-accent-200 dark:bg-dark-700">
                <div class="h-2 rounded-full bg-primary-600 transition-all dark:bg-primary-400" :style="{ width: `${progressPercent}%` }" />
              </div>
            </template>
          </section>

          <section class="card p-5" data-testid="opsmgmt-expand-result">
            <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
              {{ t('admin.opsmgmt.expand.result.title') }}
            </h2>
            <dl class="mt-3 grid grid-cols-3 gap-2 text-center">
              <div class="rounded-xl bg-emerald-50 px-2 py-2 dark:bg-emerald-500/15">
                <dt class="text-xs text-emerald-700 dark:text-emerald-300">{{ t('admin.opsmgmt.expand.result.created') }}</dt>
                <dd class="text-lg font-bold tabular-nums text-emerald-700 dark:text-emerald-300" data-testid="opsmgmt-expand-created">
                  {{ resultCounts.created }}
                </dd>
              </div>
              <div class="rounded-xl bg-amber-50 px-2 py-2 dark:bg-amber-500/15">
                <dt class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.opsmgmt.expand.result.skipped') }}</dt>
                <dd class="text-lg font-bold tabular-nums text-amber-700 dark:text-amber-300" data-testid="opsmgmt-expand-skipped">
                  {{ resultCounts.skipped }}
                </dd>
              </div>
              <div class="rounded-xl bg-red-50 px-2 py-2 dark:bg-red-500/15">
                <dt class="text-xs text-red-700 dark:text-red-300">{{ t('admin.opsmgmt.expand.result.failed') }}</dt>
                <dd class="text-lg font-bold tabular-nums text-red-700 dark:text-red-300" data-testid="opsmgmt-expand-failed">
                  {{ resultCounts.failed }}
                </dd>
              </div>
            </dl>

            <ul v-if="taskItems.length" class="mt-3 space-y-1.5">
              <li
                v-for="item in taskItems"
                :key="`${item.ref}-${item.channel_id || 0}`"
                class="flex items-start justify-between gap-2 rounded-lg border border-accent-200 px-2 py-1.5 text-xs dark:border-dark-700"
              >
                <span class="min-w-0">
                  <span class="block truncate text-accent-900 dark:text-dark-100">
                    {{ t('admin.opsmgmt.expand.result.item', { ref: item.ref }) }}
                  </span>
                  <span v-if="item.message" class="block truncate text-accent-600 dark:text-dark-300">{{ item.message }}</span>
                </span>
                <span class="badge shrink-0" :class="itemStatusClass(item.status)">{{ itemStatusLabel(item.status) }}</span>
              </li>
            </ul>

            <RouterLink
              to="/admin/channels/pricing"
              class="btn btn-secondary btn-sm mt-3 inline-flex"
              data-testid="opsmgmt-expand-view-channels"
            >
              <Icon name="externalLink" size="xs" class="mr-1" />
              {{ t('admin.opsmgmt.expand.result.viewChannels') }}
            </RouterLink>
          </section>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import RateChangeCell from '@/components/opsmgmt/RateChangeCell.vue'
import { opsMgmtAPI, type OpsSite, type OpsTask, type OpsTaskItemStatus, type OpsUpstreamGroup } from '@/api/admin/opsmgmt'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'

const { t } = useI18n()

const sites = ref<OpsSite[]>([])
const localGroups = ref<AdminGroup[]>([])
const upstreamGroups = ref<OpsUpstreamGroup[]>([])

const selectedSiteId = ref<number | null>(null)
const selectedGroupIds = ref<number[]>([])
const upstreamKey = ref('')
const targetGroupId = ref<number | null>(null)
const channelPrefix = ref('')

const loading = ref(false)
const submitting = ref(false)
const errorMessage = ref('')
const task = ref<OpsTask | null>(null)

const progressPercent = computed(() => {
  if (!task.value || !task.value.total) return 0
  return Math.min(100, Math.round((task.value.progress / task.value.total) * 100))
})

const taskStatusLabel = computed(() => {
  const status = task.value?.status ?? 'pending'
  switch (status) {
    case 'running':
      return t('admin.opsmgmt.expand.progress.status.running')
    case 'succeeded':
      return t('admin.opsmgmt.expand.progress.status.succeeded')
    case 'failed':
      return t('admin.opsmgmt.expand.progress.status.failed')
    default:
      return t('admin.opsmgmt.expand.progress.status.pending')
  }
})

const taskStatusClass = computed(() => {
  switch (task.value?.status) {
    case 'succeeded':
      return 'badge-success'
    case 'failed':
      return 'badge-danger'
    case 'running':
      return 'badge-primary'
    default:
      return 'badge-gray'
  }
})

const taskItems = computed(() => task.value?.items ?? [])

const resultCounts = computed(() => {
  const items = taskItems.value
  if (!items.length) {
    const result = task.value?.result ?? {}
    return {
      created: Number(result.created ?? 0),
      skipped: Number(result.skipped ?? 0),
      failed: Number(result.failed ?? 0)
    }
  }
  return {
    created: items.filter((item) => item.status === 'succeeded').length,
    skipped: items.filter((item) => item.status === 'skipped').length,
    failed: items.filter((item) => item.status === 'failed').length
  }
})

function itemStatusLabel(status: OpsTaskItemStatus): string {
  switch (status) {
    case 'succeeded':
      return t('admin.opsmgmt.expand.progress.status.succeeded')
    case 'failed':
      return t('admin.opsmgmt.expand.progress.status.failed')
    case 'running':
      return t('admin.opsmgmt.expand.progress.status.running')
    default:
      return t('admin.opsmgmt.expand.progress.status.pending')
  }
}

function itemStatusClass(status: OpsTaskItemStatus): string {
  switch (status) {
    case 'succeeded':
      return 'badge-success'
    case 'failed':
      return 'badge-danger'
    case 'skipped':
      return 'badge-warning'
    default:
      return 'badge-gray'
  }
}

async function loadBase(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const [siteResponse, groups] = await Promise.all([
      opsMgmtAPI.listSites(),
      adminAPI.groups.getAll().catch(() => [] as AdminGroup[])
    ])
    sites.value = siteResponse.items || []
    localGroups.value = groups || []
    if (!selectedSiteId.value && sites.value.length > 0) {
      selectedSiteId.value = sites.value[0].id
      await loadUpstreamGroups()
    }
  } catch (caught) {
    errorMessage.value = extractApiErrorMessage(caught)
  } finally {
    loading.value = false
  }
}

async function loadUpstreamGroups(): Promise<void> {
  if (!selectedSiteId.value) {
    upstreamGroups.value = []
    return
  }
  try {
    const response = await opsMgmtAPI.listSiteGroups(selectedSiteId.value)
    upstreamGroups.value = response.items || []
  } catch (caught) {
    upstreamGroups.value = []
    errorMessage.value = extractApiErrorMessage(caught)
  }
}

async function onSiteChange(): Promise<void> {
  selectedGroupIds.value = []
  await loadUpstreamGroups()
}

function validate(): boolean {
  if (!selectedSiteId.value) {
    errorMessage.value = t('admin.opsmgmt.expand.errors.noSite')
    return false
  }
  if (selectedGroupIds.value.length === 0) {
    errorMessage.value = t('admin.opsmgmt.expand.errors.noGroups')
    return false
  }
  if (!upstreamKey.value.trim()) {
    errorMessage.value = t('admin.opsmgmt.expand.errors.noKey')
    return false
  }
  if (!targetGroupId.value) {
    errorMessage.value = t('admin.opsmgmt.expand.errors.noTarget')
    return false
  }
  return true
}

/** 提交批量对接（items 非空 → 后端走异步任务，返回 202 + task）。 */
async function submit(): Promise<void> {
  if (submitting.value || !validate()) return
  submitting.value = true
  errorMessage.value = ''
  task.value = null
  try {
    const siteId = selectedSiteId.value as number
    const targetGroupIdValue = targetGroupId.value as number
    const key = upstreamKey.value.trim()
    const prefix = channelPrefix.value.trim()
    const items = selectedGroupIds.value.map((remoteGroupId) => ({
      site_id: siteId,
      remote_group_id: remoteGroupId,
      upstream_api_key: key,
      target_group_id: targetGroupIdValue,
      channel_name: prefix ? `${prefix}-${remoteGroupId}` : undefined
    }))
    const created = await opsMgmtAPI.expandBatch(items)
    task.value = created
    await pollTask(created.id)
  } catch (caught) {
    errorMessage.value = t('admin.opsmgmt.expand.errors.submitFailed', {
      message: extractApiErrorMessage(caught)
    })
  } finally {
    submitting.value = false
  }
}

async function pollTask(taskId: string): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    const current = await opsMgmtAPI.getTask(taskId)
    task.value = current
    if (current.status === 'succeeded' || current.status === 'failed') return
    await new Promise((resolve) => setTimeout(resolve, 1000))
  }
}

onMounted(loadBase)
</script>
