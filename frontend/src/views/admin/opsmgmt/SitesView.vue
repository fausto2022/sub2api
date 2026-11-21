<template>
  <AppLayout>
    <div class="space-y-6 pb-12" data-testid="opsmgmt-sites">
      <!-- 页头 -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h1 class="text-lg font-semibold text-accent-900 dark:text-dark-50">
            {{ t('admin.opsmgmt.sites.title') }}
          </h1>
          <p class="mt-0.5 text-xs text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.sites.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loadingSites"
            :title="t('admin.opsmgmt.refresh')"
            :aria-label="t('admin.opsmgmt.refresh')"
            data-testid="opsmgmt-sites-refresh"
            @click="reloadSites"
          >
            <Icon name="refresh" size="md" :class="loadingSites ? 'animate-spin' : ''" />
          </button>
          <button type="button" class="btn btn-primary" data-testid="opsmgmt-site-create" @click="openCreate">
            <Icon name="plus" size="sm" class="mr-1" />
            {{ t('admin.opsmgmt.sites.create') }}
          </button>
        </div>
      </div>

      <div
        v-if="pageError"
        class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300"
        data-testid="opsmgmt-sites-error"
      >
        {{ pageError }}
      </div>

      <div
        v-if="notice"
        class="rounded-xl border px-4 py-3 text-sm"
        :class="
          noticeKind === 'error'
            ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-300'
            : 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-900/20 dark:text-emerald-200'
        "
        data-testid="opsmgmt-sites-notice"
      >
        {{ notice }}
      </div>

      <div class="grid grid-cols-1 gap-6 xl:grid-cols-3">
        <!-- 站点列表 -->
        <section class="card overflow-hidden xl:col-span-1" data-testid="opsmgmt-site-list">
          <div class="border-b border-accent-200 px-4 py-3 dark:border-dark-700">
            <h2 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
              {{ t('admin.opsmgmt.sites.list.name') }}
            </h2>
          </div>

          <div v-if="loadingSites && sites.length === 0" class="px-4 py-6 text-sm text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.loading') }}
          </div>
          <div
            v-else-if="sites.length === 0"
            class="px-4 py-6 text-sm text-accent-600 dark:text-dark-300"
            data-testid="opsmgmt-site-empty"
          >
            {{ t('admin.opsmgmt.sites.empty') }}
          </div>

          <ul v-else class="divide-y divide-accent-200 dark:divide-dark-700">
            <li
              v-for="site in sites"
              :key="site.id"
              class="cursor-pointer px-4 py-3 transition-colors hover:bg-accent-50 dark:hover:bg-dark-800/40"
              :class="selectedId === site.id ? 'bg-primary-50 dark:bg-primary-900/20' : ''"
              :data-testid="`opsmgmt-site-item-${site.id}`"
              @click="selectSite(site.id)"
            >
              <div class="flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="truncate text-sm font-medium text-accent-900 dark:text-dark-50">{{ site.name }}</p>
                  <p class="truncate text-xs text-accent-600 dark:text-dark-300">{{ site.base_url }}</p>
                </div>
                <span class="badge" :class="site.status === 'active' ? 'badge-success' : 'badge-danger'">
                  {{ site.status === 'active' ? t('admin.opsmgmt.sites.status.active') : t('admin.opsmgmt.sites.status.error') }}
                </span>
              </div>
              <p class="mt-1 text-xs tabular-nums text-accent-600 dark:text-dark-300">
                {{ t('admin.opsmgmt.sites.list.lastSync') }}: {{ formatDateTime(site.last_synced_at) }}
              </p>
              <div class="mt-2 flex flex-wrap items-center gap-1.5">
                <button
                  type="button"
                  class="btn btn-ghost btn-sm"
                  :disabled="syncingSiteId === site.id"
                  :data-testid="`opsmgmt-site-sync-${site.id}`"
                  @click.stop="syncSite(site)"
                >
                  <Icon name="sync" size="xs" class="mr-1" :class="syncingSiteId === site.id ? 'animate-spin' : ''" />
                  {{ syncingSiteId === site.id ? t('admin.opsmgmt.sites.syncRunning') : t('admin.opsmgmt.sites.action.sync') }}
                </button>
                <button
                  type="button"
                  class="btn btn-ghost btn-sm"
                  :data-testid="`opsmgmt-site-edit-${site.id}`"
                  @click.stop="openEdit(site)"
                >
                  <Icon name="edit" size="xs" class="mr-1" />
                  {{ t('admin.opsmgmt.sites.action.edit') }}
                </button>
                <button
                  type="button"
                  class="btn btn-ghost btn-sm text-red-700 dark:text-red-300"
                  :data-testid="`opsmgmt-site-delete-${site.id}`"
                  @click.stop="askDelete(site)"
                >
                  <Icon name="trash" size="xs" class="mr-1" />
                  {{ t('admin.opsmgmt.sites.action.delete') }}
                </button>
              </div>
            </li>
          </ul>
        </section>

        <!-- 站点详情 -->
        <section class="card overflow-hidden xl:col-span-2" data-testid="opsmgmt-site-detail">
          <div v-if="!selectedSite" class="px-5 py-10 text-sm text-accent-600 dark:text-dark-300">
            {{ t('admin.opsmgmt.sites.selectHint') }}
          </div>

          <template v-else>
            <div class="flex flex-wrap items-start justify-between gap-3 border-b border-accent-200 px-5 py-3 dark:border-dark-700">
              <div class="min-w-0">
                <h2 class="truncate text-sm font-semibold text-accent-900 dark:text-dark-50">{{ selectedSite.name }}</h2>
                <p class="mt-0.5 text-xs tabular-nums text-accent-600 dark:text-dark-300" data-testid="opsmgmt-site-status-line">
                  {{
                    t('admin.opsmgmt.sites.detail.statusLine', {
                      status:
                        selectedSite.status === 'active'
                          ? t('admin.opsmgmt.sites.status.active')
                          : t('admin.opsmgmt.sites.status.error'),
                      synced: formatDateTime(selectedSite.last_synced_at),
                      interval: selectedSite.sync_interval_minutes
                    })
                  }}
                </p>
                <p v-if="selectedSite.last_error" class="mt-1 text-xs text-red-700 dark:text-red-300">
                  {{ selectedSite.last_error }}
                </p>
              </div>
              <div class="flex flex-wrap items-center gap-2">
                <button type="button" class="btn btn-secondary btn-sm" :disabled="testing" data-testid="opsmgmt-site-test" @click="testLogin">
                  <Icon name="login" size="xs" class="mr-1" />
                  {{ t('admin.opsmgmt.sites.action.test') }}
                </button>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="syncingSiteId === selectedSite.id"
                  data-testid="opsmgmt-site-sync-detail"
                  @click="syncSite(selectedSite)"
                >
                  <Icon name="sync" size="xs" class="mr-1" />
                  {{ t('admin.opsmgmt.sites.action.sync') }}
                </button>
                <button type="button" class="btn btn-secondary btn-sm" data-testid="opsmgmt-site-edit-detail" @click="openEdit(selectedSite)">
                  <Icon name="edit" size="xs" class="mr-1" />
                  {{ t('admin.opsmgmt.sites.action.edit') }}
                </button>
              </div>
            </div>

            <!-- 分组列表 -->
            <div class="border-b border-accent-200 px-5 py-3 dark:border-dark-700">
              <h3 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
                {{ t('admin.opsmgmt.sites.detail.groupsTitle') }}
              </h3>
            </div>
            <div v-if="groups.length === 0" class="px-5 py-6 text-sm text-accent-600 dark:text-dark-300" data-testid="opsmgmt-site-groups-empty">
              {{ t('admin.opsmgmt.sites.detail.groupsEmpty') }}
            </div>
            <div v-else class="overflow-x-auto">
              <table class="table w-full" data-testid="opsmgmt-site-groups">
                <thead>
                  <tr>
                    <th class="text-left">{{ t('admin.opsmgmt.sites.detail.groupName') }}</th>
                    <th class="text-left">{{ t('admin.opsmgmt.sites.detail.platform') }}</th>
                    <th class="text-left">{{ t('admin.opsmgmt.sites.detail.rate') }}</th>
                    <th class="text-right">{{ t('admin.opsmgmt.sites.detail.minMargin') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="group in groups"
                    :key="group.remote_group_id"
                    :data-testid="`opsmgmt-site-group-${group.remote_group_id}`"
                    :data-rate-changed="group.rate_changed ? 'true' : 'false'"
                  >
                    <td class="text-left">
                      <div class="flex flex-wrap items-center gap-2">
                        <span class="font-medium text-accent-900 dark:text-dark-100">{{ group.name }}</span>
                        <span v-if="group.rate_changed" class="badge badge-danger" data-testid="opsmgmt-site-rate-changed">
                          {{ t('admin.opsmgmt.sites.detail.rateChanged') }}
                        </span>
                      </div>
                    </td>
                    <td class="text-left text-accent-600 dark:text-dark-300">{{ group.platform || '—' }}</td>
                    <td class="text-left">
                      <RateChangeCell
                        :current="group.rate_multiplier"
                        :previous="group.previous_rate_multiplier ?? null"
                        :flag="group.rate_changed ? 'changed' : 'auto'"
                        :test-id="`opsmgmt-site-rate-${group.remote_group_id}`"
                      />
                    </td>
                    <td class="text-right">
                      <div class="flex items-center justify-end gap-1">
                        <input
                          v-model.number="marginDraft[group.remote_group_id]"
                          type="number"
                          min="0"
                          max="99"
                          step="0.01"
                          class="input w-20 text-right tabular-nums"
                          :data-testid="`opsmgmt-site-margin-${group.remote_group_id}`"
                        />
                        <span class="text-xs text-accent-600 dark:text-dark-300">%</span>
                        <button
                          type="button"
                          class="btn btn-ghost btn-sm"
                          :disabled="savingMarginId === group.remote_group_id"
                          :data-testid="`opsmgmt-site-margin-save-${group.remote_group_id}`"
                          @click="saveGroupMargin(group)"
                        >
                          {{ t('admin.opsmgmt.save') }}
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <!-- 账号列表 -->
            <div class="border-y border-accent-200 px-5 py-3 dark:border-dark-700">
              <h3 class="text-sm font-semibold text-accent-900 dark:text-dark-50">
                {{ t('admin.opsmgmt.sites.detail.accountsTitle') }}
              </h3>
            </div>
            <div v-if="accounts.length === 0" class="px-5 py-6 text-sm text-accent-600 dark:text-dark-300" data-testid="opsmgmt-site-accounts-empty">
              {{ t('admin.opsmgmt.sites.detail.accountsEmpty') }}
            </div>
            <div v-else class="overflow-x-auto">
              <table class="table w-full" data-testid="opsmgmt-site-accounts">
                <thead>
                  <tr>
                    <th class="text-left">{{ t('admin.opsmgmt.sites.detail.accountName') }}</th>
                    <th class="text-left">{{ t('admin.opsmgmt.sites.detail.platform') }}</th>
                    <th class="text-left">{{ t('admin.opsmgmt.sites.detail.accountStatus') }}</th>
                    <th class="text-right">{{ t('admin.opsmgmt.sites.detail.accountRate') }}</th>
                    <th class="text-left">{{ t('admin.opsmgmt.sites.detail.accountSource') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="account in accounts" :key="account.remote_account_id">
                    <td class="text-left font-medium text-accent-900 dark:text-dark-100">
                      {{ account.name || `#${account.remote_account_id}` }}
                    </td>
                    <td class="text-left text-accent-600 dark:text-dark-300">{{ account.platform || '—' }}</td>
                    <td class="text-left">
                      <span class="badge" :class="account.status === 'active' ? 'badge-success' : 'badge-gray'">
                        {{ account.status || '—' }}
                      </span>
                    </td>
                    <td class="text-right tabular-nums">{{ formatMultiplier(account.rate_multiplier ?? null) }}</td>
                    <td class="text-left">
                      <SourceBadge kind="opsmgmt" :site-name="selectedSite.name" :test-id="`opsmgmt-site-account-source-${account.remote_account_id}`" />
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </template>
        </section>
      </div>

      <!-- 新增 / 编辑站点 -->
      <BaseDialog :show="dialogOpen" :title="dialogTitle" width="wide" @close="closeDialog">
        <form class="space-y-3" data-testid="opsmgmt-site-form" @submit.prevent="submitForm">
          <div>
            <label class="input-label" for="opsmgmt-site-name">{{ t('admin.opsmgmt.sites.form.name') }}</label>
            <input
              id="opsmgmt-site-name"
              v-model="form.name"
              type="text"
              class="input"
              :placeholder="t('admin.opsmgmt.sites.form.namePlaceholder')"
              data-testid="opsmgmt-site-form-name"
            />
          </div>
          <div>
            <label class="input-label" for="opsmgmt-site-base-url">{{ t('admin.opsmgmt.sites.form.baseUrl') }}</label>
            <input
              id="opsmgmt-site-base-url"
              v-model="form.base_url"
              type="url"
              class="input"
              :placeholder="t('admin.opsmgmt.sites.form.baseUrlPlaceholder')"
              data-testid="opsmgmt-site-form-base-url"
            />
          </div>
          <div>
            <label class="input-label" for="opsmgmt-site-username">{{ t('admin.opsmgmt.sites.form.username') }}</label>
            <input
              id="opsmgmt-site-username"
              v-model="form.username"
              type="email"
              autocomplete="off"
              class="input"
              :placeholder="t('admin.opsmgmt.sites.form.usernamePlaceholder')"
              data-testid="opsmgmt-site-form-username"
            />
          </div>
          <div>
            <label class="input-label" for="opsmgmt-site-password">{{ t('admin.opsmgmt.sites.form.password') }}</label>
            <input
              id="opsmgmt-site-password"
              v-model="form.password"
              type="password"
              autocomplete="new-password"
              class="input"
              :placeholder="editingSite ? t('admin.opsmgmt.sites.form.passwordKeep') : ''"
              data-testid="opsmgmt-site-form-password"
            />
          </div>
          <div>
            <label class="input-label" for="opsmgmt-site-api-key">{{ t('admin.opsmgmt.sites.form.upstreamApiKey') }}</label>
            <input
              id="opsmgmt-site-api-key"
              v-model="form.upstream_api_key"
              type="password"
              autocomplete="off"
              class="input"
              :placeholder="editingSite?.upstream_api_key_set ? t('admin.opsmgmt.sites.form.passwordKeep') : ''"
              data-testid="opsmgmt-site-form-upstream-key"
            />
            <p class="input-hint">{{ t('admin.opsmgmt.sites.form.upstreamApiKeyHint') }}</p>
            <label
              v-if="editingSite"
              class="mt-1 flex cursor-pointer items-center gap-2 text-xs text-accent-600 dark:text-dark-300"
            >
              <input
                v-model="form.clear_upstream_api_key"
                type="checkbox"
                class="h-3.5 w-3.5 rounded border-accent-300 text-primary-600 focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
                data-testid="opsmgmt-site-form-clear-key"
              />
              {{ t('admin.opsmgmt.sites.form.clearUpstreamApiKey') }}
            </label>
          </div>
          <div>
            <label class="input-label" for="opsmgmt-site-interval">{{ t('admin.opsmgmt.sites.form.syncInterval') }}</label>
            <input
              id="opsmgmt-site-interval"
              v-model.number="form.sync_interval_minutes"
              type="number"
              min="1"
              max="10080"
              step="1"
              class="input tabular-nums"
              data-testid="opsmgmt-site-form-interval"
            />
          </div>
          <p v-if="formError" class="text-xs text-red-700 dark:text-red-300" data-testid="opsmgmt-site-form-error">
            {{ formError }}
          </p>
        </form>
        <template #footer>
          <div class="flex justify-end gap-2">
            <button type="button" class="btn btn-secondary" @click="closeDialog">
              {{ t('admin.opsmgmt.cancel') }}
            </button>
            <button
              type="button"
              class="btn btn-primary"
              :disabled="savingForm"
              data-testid="opsmgmt-site-form-submit"
              @click="submitForm"
            >
              {{ savingForm ? t('admin.opsmgmt.saving') : t('admin.opsmgmt.save') }}
            </button>
          </div>
        </template>
      </BaseDialog>

      <ConfirmDialog
        :show="deleteTarget !== null"
        :title="t('admin.opsmgmt.sites.action.delete')"
        :message="deleteMessage"
        :confirm-text="t('admin.opsmgmt.sites.action.delete')"
        danger
        @cancel="deleteTarget = null"
        @confirm="confirmDelete"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import RateChangeCell from '@/components/opsmgmt/RateChangeCell.vue'
import SourceBadge from '@/components/opsmgmt/SourceBadge.vue'
import { formatDateTime, formatMultiplier } from '@/components/opsmgmt/format'
import {
  opsMgmtAPI,
  type OpsSite,
  type OpsSiteInput,
  type OpsTask,
  type OpsUpstreamAccount,
  type OpsUpstreamGroup
} from '@/api/admin/opsmgmt'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

const sites = ref<OpsSite[]>([])
const groups = ref<OpsUpstreamGroup[]>([])
const accounts = ref<OpsUpstreamAccount[]>([])
const selectedId = ref<number | null>(null)

const loadingSites = ref(false)
const savingForm = ref(false)
const testing = ref(false)
const syncingSiteId = ref<number | null>(null)
const savingMarginId = ref<number | null>(null)

const pageError = ref('')
const notice = ref('')
const noticeKind = ref<'success' | 'error'>('success')
const formError = ref('')

const dialogOpen = ref(false)
const editingSite = ref<OpsSite | null>(null)
const deleteTarget = ref<OpsSite | null>(null)

const marginDraft = reactive<Record<number, number>>({})

const form = reactive({
  name: '',
  base_url: '',
  username: '',
  password: '',
  upstream_api_key: '',
  sync_interval_minutes: 30,
  clear_upstream_api_key: false
})

const selectedSite = computed(() => sites.value.find((site) => site.id === selectedId.value) ?? null)
const dialogTitle = computed(() =>
  editingSite.value
    ? t('admin.opsmgmt.sites.form.editTitle')
    : t('admin.opsmgmt.sites.form.createTitle')
)
const deleteMessage = computed(() =>
  deleteTarget.value ? t('admin.opsmgmt.sites.deleteConfirm', { name: deleteTarget.value.name }) : ''
)

function setNotice(message: string, kind: 'success' | 'error' = 'success'): void {
  notice.value = message
  noticeKind.value = kind
}

async function reloadSites(): Promise<void> {
  loadingSites.value = true
  pageError.value = ''
  try {
    const response = await opsMgmtAPI.listSites()
    sites.value = response.items || []
    if (selectedId.value && !sites.value.some((site) => site.id === selectedId.value)) {
      selectedId.value = null
      groups.value = []
      accounts.value = []
    }
    if (!selectedId.value && sites.value.length > 0) {
      selectSite(sites.value[0].id)
    }
  } catch (caught) {
    pageError.value = extractApiErrorMessage(caught)
  } finally {
    loadingSites.value = false
  }
}

async function selectSite(id: number): Promise<void> {
  selectedId.value = id
  await Promise.all([loadGroups(id), loadAccounts(id)])
}

async function loadGroups(id: number): Promise<void> {
  try {
    const response = await opsMgmtAPI.listSiteGroups(id)
    groups.value = response.items || []
    for (const group of groups.value) {
      marginDraft[group.remote_group_id] =
        group.min_margin_basis_points === null || group.min_margin_basis_points === undefined
          ? 0
          : Math.round((group.min_margin_basis_points / 100) * 100) / 100
    }
  } catch (caught) {
    pageError.value = extractApiErrorMessage(caught)
  }
}

async function loadAccounts(id: number): Promise<void> {
  try {
    const response = await opsMgmtAPI.listSiteAccounts(id)
    accounts.value = response.items || []
  } catch {
    // 账号快照在 password 模式下为空；读失败不阻断分组视图。
    accounts.value = []
  }
}

function resetForm(): void {
  form.name = ''
  form.base_url = ''
  form.username = ''
  form.password = ''
  form.upstream_api_key = ''
  form.sync_interval_minutes = 30
  form.clear_upstream_api_key = false
  formError.value = ''
}

function openCreate(): void {
  editingSite.value = null
  resetForm()
  dialogOpen.value = true
}

function openEdit(site: OpsSite): void {
  editingSite.value = site
  resetForm()
  form.name = site.name
  form.base_url = site.base_url
  form.username = site.username
  form.sync_interval_minutes = site.sync_interval_minutes
  dialogOpen.value = true
}

function closeDialog(): void {
  dialogOpen.value = false
  editingSite.value = null
}

function validateForm(): boolean {
  if (!form.name.trim()) {
    formError.value = t('admin.opsmgmt.sites.form.nameRequired')
    return false
  }
  if (!form.base_url.trim()) {
    formError.value = t('admin.opsmgmt.sites.form.baseUrlRequired')
    return false
  }
  if (!form.username.trim()) {
    formError.value = t('admin.opsmgmt.sites.form.usernameRequired')
    return false
  }
  if (!editingSite.value && !form.password.trim()) {
    formError.value = t('admin.opsmgmt.sites.form.passwordRequired')
    return false
  }
  return true
}

function buildPayload(): OpsSiteInput {
  const payload: OpsSiteInput = {
    name: form.name.trim(),
    base_url: form.base_url.trim(),
    username: form.username.trim(),
    sync_interval_minutes: Number(form.sync_interval_minutes) || 30
  }
  if (form.password.trim()) payload.password = form.password
  if (form.upstream_api_key.trim()) payload.upstream_api_key = form.upstream_api_key
  if (editingSite.value && form.clear_upstream_api_key) payload.clear_upstream_api_key = true
  return payload
}

async function submitForm(): Promise<void> {
  if (savingForm.value || !validateForm()) return
  savingForm.value = true
  formError.value = ''
  try {
    if (editingSite.value) {
      await opsMgmtAPI.updateSite(editingSite.value.id, buildPayload())
      setNotice(t('admin.opsmgmt.saved'))
    } else {
      const created = await opsMgmtAPI.createSite(buildPayload())
      selectedId.value = created.id
    }
    closeDialog()
    await reloadSites()
    if (selectedId.value) await selectSite(selectedId.value)
  } catch (caught) {
    formError.value = extractApiErrorMessage(caught)
  } finally {
    savingForm.value = false
  }
}

function askDelete(site: OpsSite): void {
  deleteTarget.value = site
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  deleteTarget.value = null
  if (!target) return
  try {
    await opsMgmtAPI.deleteSite(target.id)
    if (selectedId.value === target.id) {
      selectedId.value = null
      groups.value = []
      accounts.value = []
    }
    await reloadSites()
  } catch (caught) {
    setNotice(extractApiErrorMessage(caught), 'error')
  }
}

async function testLogin(): Promise<void> {
  const site = selectedSite.value
  if (!site || testing.value) return
  testing.value = true
  try {
    const result = await opsMgmtAPI.testSite(site.id)
    if (result.ok) {
      setNotice(
        t('admin.opsmgmt.sites.testSuccess', { groups: result.group_count, latency: result.latency_ms })
      )
    } else {
      setNotice(t('admin.opsmgmt.sites.testFailure', { message: result.error || '' }), 'error')
    }
  } catch (caught) {
    setNotice(t('admin.opsmgmt.sites.testFailure', { message: extractApiErrorMessage(caught) }), 'error')
  } finally {
    testing.value = false
  }
}

/** 同步：POST 返回 202 + task，轮询任务直到终态后刷新快照。 */
async function syncSite(site: OpsSite): Promise<void> {
  if (syncingSiteId.value !== null) return
  syncingSiteId.value = site.id
  try {
    const task = await opsMgmtAPI.syncSite(site.id)
    setNotice(t('admin.opsmgmt.sites.syncQueued', { id: task.id }))
    const finished = await pollTask(task.id)
    if (finished.status === 'failed') {
      setNotice(t('admin.opsmgmt.sites.syncFailed', { message: finished.error || finished.message || '' }), 'error')
    } else {
      await reloadSites()
      if (selectedId.value) await selectSite(selectedId.value)
      setNotice(
        t('admin.opsmgmt.sites.syncDone', {
          groups: groups.value.length,
          accounts: accounts.value.length
        })
      )
    }
    await reloadSites()
    if (selectedId.value) await selectSite(selectedId.value)
  } catch (caught) {
    setNotice(t('admin.opsmgmt.sites.syncFailed', { message: extractApiErrorMessage(caught) }), 'error')
  } finally {
    syncingSiteId.value = null
  }
}

async function pollTask(taskId: string): Promise<OpsTask> {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    const task = await opsMgmtAPI.getTask(taskId)
    if (task.status === 'succeeded' || task.status === 'failed') return task
    await new Promise((resolve) => setTimeout(resolve, 800))
  }
  return opsMgmtAPI.getTask(taskId)
}

async function saveGroupMargin(group: OpsUpstreamGroup): Promise<void> {
  if (!selectedSite.value || savingMarginId.value !== null) return
  const percent = Number(marginDraft[group.remote_group_id])
  if (!Number.isFinite(percent) || percent < 0 || percent >= 100) {
    setNotice(t('admin.opsmgmt.groupMargin.rangeError'), 'error')
    return
  }
  savingMarginId.value = group.remote_group_id
  try {
    const basisPoints = percent === 0 ? null : Math.round(percent * 100)
    await opsMgmtAPI.setSiteGroupMargin(selectedSite.value.id, group.remote_group_id, basisPoints)
    await loadGroups(selectedSite.value.id)
    setNotice(t('admin.opsmgmt.saved'))
  } catch (caught) {
    setNotice(extractApiErrorMessage(caught), 'error')
  } finally {
    savingMarginId.value = null
  }
}

onMounted(reloadSites)
</script>
