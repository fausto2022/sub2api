<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center gap-2" data-testid="ticket-stats">
          <span
            v-for="stat in statChips"
            :key="stat.key"
            class="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs dark:border-dark-700 dark:bg-dark-800"
          >
            <span class="text-gray-500 dark:text-dark-400">{{ stat.label }}</span>
            <span class="font-semibold tabular-nums text-gray-900 dark:text-white">{{ stat.value }}</span>
          </span>
        </div>
      </template>

      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <div class="flex-1 sm:max-w-64">
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('admin.tickets.searchPlaceholder')"
              class="input"
              @input="handleSearch"
            />
          </div>
          <div class="w-40">
            <Select
              v-model="filters.status"
              :options="statusFilterOptions"
              :placeholder="t('admin.tickets.filterStatus')"
              @change="handleFilterChange"
            />
          </div>
          <div class="w-40">
            <Select
              v-model="filters.priority"
              :options="priorityFilterOptions"
              :placeholder="t('admin.tickets.filterPriority')"
              @change="handleFilterChange"
            />
          </div>
          <div class="flex flex-1 items-center justify-end gap-2">
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="loading"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadTickets"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="tickets" :loading="loading">
          <template #cell-subject="{ row }">
            <div class="min-w-0">
              <div class="truncate font-medium text-gray-900 dark:text-white">{{ row.subject }}</div>
              <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                <span class="tabular-nums">#{{ row.id }}</span>
                <span class="mx-1 text-gray-300 dark:text-dark-700">·</span>
                <span>{{ categoryLabel(row.category) }}</span>
              </div>
            </div>
          </template>

          <template #cell-user="{ row }">
            <div class="min-w-0">
              <div class="truncate text-sm text-gray-900 dark:text-gray-200">
                {{ row.user_name || t('admin.tickets.unknownUser') }}
              </div>
              <div class="truncate text-xs text-gray-500 dark:text-dark-400">{{ row.user_email }}</div>
            </div>
          </template>

          <template #cell-status="{ row }">
            <TicketStatusBadge :status="row.status" />
          </template>

          <template #cell-priority="{ row }">
            <span class="badge" :class="priorityClass(row.priority)">{{ priorityLabel(row.priority) }}</span>
          </template>

          <template #cell-replies="{ row }">
            <span class="tabular-nums text-sm text-gray-700 dark:text-gray-300">{{ row.reply_count }}</span>
          </template>

          <template #cell-updatedAt="{ row }">
            <span class="whitespace-nowrap text-sm text-gray-500 dark:text-dark-400">
              {{ formatDateTime(row.updated_at) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <button
              type="button"
              class="flex items-center gap-1 rounded-lg px-2 py-1 text-sm text-primary-600 transition-colors hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-primary-900/20"
              :title="t('admin.tickets.view')"
              @click="openDetail(row)"
            >
              <Icon name="eye" size="sm" />
              {{ t('admin.tickets.view') }}
            </button>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.tickets.empty.title')"
              :description="t('admin.tickets.empty.description')"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showDetail"
      :title="t('admin.tickets.detailTitle')"
      width="wide"
      @close="closeDetail"
    >
      <div v-if="detailLoading" class="space-y-3" data-testid="admin-ticket-detail-loading">
        <Skeleton height="96px" />
        <Skeleton height="200px" />
      </div>

      <div v-else-if="detailError" class="py-6 text-center" data-testid="admin-ticket-detail-error">
        <p class="text-sm text-red-600 dark:text-red-400">{{ detailError }}</p>
        <button type="button" class="btn btn-secondary mt-3" @click="loadDetail(detailTicketId)">
          {{ t('common.refresh') }}
        </button>
      </div>

      <div v-else-if="detail" class="space-y-5">
        <!-- Summary -->
        <div>
          <div class="flex flex-wrap items-center gap-2">
            <TicketStatusBadge :status="detail.ticket.status" />
            <span class="badge" :class="priorityClass(detail.ticket.priority)">
              {{ priorityLabel(detail.ticket.priority) }}
            </span>
            <span class="badge badge-gray">{{ categoryLabel(detail.ticket.category) }}</span>
          </div>
          <h3 class="mt-2 break-words text-lg font-semibold text-gray-900 dark:text-white">
            {{ detail.ticket.subject }}
          </h3>
          <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
            <span class="tabular-nums">#{{ detail.ticket.id }}</span>
            <span>{{ t('admin.tickets.createdAt') }}: {{ formatDateTime(detail.ticket.created_at) }}</span>
            <span>{{ t('tickets.replyCount', { count: detail.ticket.reply_count }) }}</span>
          </div>
          <p v-if="detail.user" class="mt-2 text-sm text-gray-600 dark:text-gray-300" data-testid="admin-ticket-submitter">
            {{ t('admin.tickets.submitter') }}:
            <span class="font-medium">{{ detail.user.username || detail.user.email }}</span>
            <span v-if="detail.user.username" class="text-gray-500 dark:text-dark-400">（{{ detail.user.email }}）</span>
          </p>
        </div>

        <!-- Status / priority editors -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label class="input-label mb-1.5 block">{{ t('admin.tickets.statusLabel') }}</label>
            <Select v-model="editStatus" :options="statusOptions" />
          </div>
          <div>
            <label class="input-label mb-1.5 block">{{ t('admin.tickets.priorityLabel') }}</label>
            <Select v-model="editPriority" :options="priorityOptions" />
          </div>
        </div>
        <div class="flex justify-end">
          <button type="button" class="btn btn-secondary" :disabled="savingDetail" @click="handleSaveTicket">
            {{ savingDetail ? t('admin.tickets.saving') : t('admin.tickets.saveChanges') }}
          </button>
        </div>

        <!-- Conversation -->
        <div>
          <h4 class="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-400">
            {{ t('tickets.detail.replies') }}
          </h4>
          <TicketThread :replies="detail.replies" />
        </div>

        <!-- Reply -->
        <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.tickets.replyTitle') }}</h4>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <Toggle v-model="notifyEmail" data-testid="admin-ticket-notify-toggle" />
              {{ t('admin.tickets.notifyEmail') }}
            </label>
          </div>
          <p class="mb-3 text-xs text-gray-500 dark:text-dark-400">{{ t('admin.tickets.notifyEmailHint') }}</p>
          <p v-if="detail.ticket.status === 'closed'" class="mb-3 text-sm text-amber-700 dark:text-amber-300" data-testid="admin-ticket-closed-hint">
            {{ t('admin.tickets.closedHint') }}
          </p>
          <TicketComposer
            ref="adminComposerRef"
            :loading="replyLoading"
            :disabled="detail.ticket.status === 'closed'"
            :placeholder="t('admin.tickets.replyPlaceholder')"
            :submit-label="t('admin.tickets.sendReply')"
            :loading-label="t('admin.tickets.sendingReply')"
            :required-message="t('admin.tickets.replyContentRequired')"
            :too-long-message="t('admin.tickets.replyContentTooLong')"
            @submit="handleReply"
          />
        </div>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import type { Column } from '@/components/common/types'
import type { TicketCategory, TicketPriority, TicketStatus } from '@/api/tickets'
import {
  adminTicketsAPI,
  type AdminTicketBrief,
  type AdminTicketDetailResponse,
  type TicketStats
} from '@/api/admin/tickets'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import TicketStatusBadge from '@/components/tickets/TicketStatusBadge.vue'
import TicketThread from '@/components/tickets/TicketThread.vue'
import TicketComposer from '@/components/tickets/TicketComposer.vue'

const { t } = useI18n()
const appStore = useAppStore()

const tickets = ref<AdminTicketBrief[]>([])
const loading = ref(false)
const searchQuery = ref('')

const filters = reactive({
  status: '' as TicketStatus | '',
  priority: '' as TicketPriority | ''
})

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0
})

const stats = ref<TicketStats | null>(null)

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.tickets.allStatus') },
  { value: 'open', label: t('tickets.statusLabels.open') },
  { value: 'pending', label: t('tickets.statusLabels.pending') },
  { value: 'resolved', label: t('tickets.statusLabels.resolved') },
  { value: 'closed', label: t('tickets.statusLabels.closed') }
])

const priorityFilterOptions = computed(() => [
  { value: '', label: t('admin.tickets.allPriority') },
  { value: 'urgent', label: t('tickets.priorityLabels.urgent') },
  { value: 'high', label: t('tickets.priorityLabels.high') },
  { value: 'normal', label: t('tickets.priorityLabels.normal') },
  { value: 'low', label: t('tickets.priorityLabels.low') }
])

const statusOptions = computed(() => statusFilterOptions.value.filter((option) => option.value !== ''))
const priorityOptions = computed(() => priorityFilterOptions.value.filter((option) => option.value !== ''))

const columns = computed<Column[]>(() => [
  { key: 'subject', label: t('admin.tickets.columns.subject') },
  { key: 'user', label: t('admin.tickets.columns.user') },
  { key: 'status', label: t('admin.tickets.columns.status') },
  { key: 'priority', label: t('admin.tickets.columns.priority') },
  { key: 'replies', label: t('admin.tickets.columns.replies') },
  { key: 'updatedAt', label: t('admin.tickets.columns.updatedAt') },
  { key: 'actions', label: t('admin.tickets.columns.actions') }
])

const statChips = computed(() => [
  { key: 'open', label: t('admin.tickets.stats.open'), value: stats.value?.open ?? 0 },
  { key: 'pending', label: t('admin.tickets.stats.pending'), value: stats.value?.pending ?? 0 },
  { key: 'resolved', label: t('admin.tickets.stats.resolved'), value: stats.value?.resolved ?? 0 },
  { key: 'closed', label: t('admin.tickets.stats.closed'), value: stats.value?.closed ?? 0 },
  { key: 'total', label: t('admin.tickets.stats.total'), value: stats.value?.total ?? 0 }
])

function categoryLabel(category: TicketCategory): string {
  switch (category) {
    case 'billing':
      return t('tickets.categoryLabels.billing')
    case 'api':
      return t('tickets.categoryLabels.api')
    case 'account':
      return t('tickets.categoryLabels.account')
    case 'other':
      return t('tickets.categoryLabels.other')
    default:
      return t('tickets.categoryLabels.general')
  }
}

function priorityLabel(priority: TicketPriority): string {
  switch (priority) {
    case 'low':
      return t('tickets.priorityLabels.low')
    case 'high':
      return t('tickets.priorityLabels.high')
    case 'urgent':
      return t('tickets.priorityLabels.urgent')
    default:
      return t('tickets.priorityLabels.normal')
  }
}

function priorityClass(priority: TicketPriority): string {
  switch (priority) {
    case 'urgent':
      return 'badge-danger'
    case 'high':
      return 'badge-warning'
    case 'low':
      return 'badge-gray'
    default:
      return 'badge-primary'
  }
}

// ===== List =====
let listController: AbortController | null = null

async function loadTickets(): Promise<void> {
  listController?.abort()
  const requestController = new AbortController()
  listController = requestController
  const { signal } = requestController

  loading.value = true
  try {
    const result = await adminTicketsAPI.list({
      page: pagination.page,
      page_size: pagination.page_size,
      status: filters.status,
      priority: filters.priority,
      search: searchQuery.value.trim(),
      signal
    })
    if (signal.aborted || listController !== requestController) return

    tickets.value = result.items
    pagination.total = result.total
    pagination.page = result.page
    pagination.page_size = result.page_size
  } catch (error) {
    if (
      signal.aborted ||
      listController !== requestController ||
      (error as { code?: string })?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading admin tickets:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.loadFailed')))
  } finally {
    if (listController === requestController) {
      loading.value = false
      listController = null
    }
  }
}

async function loadStats(): Promise<void> {
  try {
    stats.value = await adminTicketsAPI.stats()
  } catch (error) {
    console.error('Error loading ticket stats:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.statsLoadFailed')))
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  loadTickets()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadTickets()
}

function handleFilterChange() {
  pagination.page = 1
  loadTickets()
}

let searchTimer: number | null = null
function handleSearch() {
  if (searchTimer) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    pagination.page = 1
    loadTickets()
  }, 300)
}

// ===== Detail dialog =====
const showDetail = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
const detail = ref<AdminTicketDetailResponse | null>(null)
const detailTicketId = ref(0)
const editStatus = ref<TicketStatus>('open')
const editPriority = ref<TicketPriority>('normal')
const savingDetail = ref(false)
const replyLoading = ref(false)
const notifyEmail = ref(true)
const adminComposerRef = ref<InstanceType<typeof TicketComposer> | null>(null)

let detailController: AbortController | null = null

function openDetail(row: AdminTicketBrief) {
  showDetail.value = true
  notifyEmail.value = true
  loadDetail(row.id)
}

function closeDetail() {
  showDetail.value = false
  detailController?.abort()
  detailController = null
  detail.value = null
  detailError.value = ''
  adminComposerRef.value?.reset()
}

async function loadDetail(id: number) {
  if (!id) return

  detailController?.abort()
  const requestController = new AbortController()
  detailController = requestController
  const { signal } = requestController

  detailTicketId.value = id
  detailLoading.value = true
  detailError.value = ''
  detail.value = null

  try {
    const result = await adminTicketsAPI.get(id, { signal })
    if (signal.aborted || detailController !== requestController) return

    detail.value = result
    editStatus.value = result.ticket.status
    editPriority.value = result.ticket.priority
  } catch (error) {
    if (
      signal.aborted ||
      detailController !== requestController ||
      (error as { code?: string })?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading admin ticket detail:', error)
    detailError.value = extractApiErrorMessage(error, t('admin.tickets.detailLoadFailed'))
    appStore.showError(detailError.value)
  } finally {
    if (detailController === requestController) {
      detailLoading.value = false
      detailController = null
    }
  }
}

async function handleSaveTicket() {
  if (!detail.value || savingDetail.value) return

  savingDetail.value = true
  try {
    const result = await adminTicketsAPI.update(detail.value.ticket.id, {
      status: editStatus.value,
      priority: editPriority.value
    })
    detail.value = { ...detail.value, ticket: result.ticket }
    tickets.value = tickets.value.map((item) =>
      item.id === result.ticket.id ? { ...item, ...result.ticket } : item
    )
    appStore.showSuccess(t('admin.tickets.updateSuccess'))
    loadStats()
  } catch (error) {
    console.error('Error updating ticket:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.updateFailed')))
  } finally {
    savingDetail.value = false
  }
}

async function handleReply(content: string) {
  if (!detail.value || replyLoading.value) return

  replyLoading.value = true
  try {
    const result = await adminTicketsAPI.reply(detail.value.ticket.id, {
      content,
      notify_email: notifyEmail.value
    })
    detail.value = {
      ...detail.value,
      ticket: result.ticket,
      replies: [...detail.value.replies, result.reply]
    }
    editStatus.value = result.ticket.status
    editPriority.value = result.ticket.priority
    adminComposerRef.value?.reset()

    if (result.email_sent) {
      appStore.showSuccess(t('admin.tickets.replyEmailSuccess'))
    } else if (notifyEmail.value && result.email_error) {
      appStore.showWarning(
        t('admin.tickets.replyEmailFailed', {
          error: result.email_error
        })
      )
    } else {
      appStore.showSuccess(t('admin.tickets.replySuccess'))
    }

    loadTickets()
    loadStats()
  } catch (error) {
    console.error('Error sending admin ticket reply:', error)
    const message = extractApiErrorMessage(error, t('admin.tickets.replyFailed'))
    appStore.showError(message)
    adminComposerRef.value?.setError(message)
  } finally {
    replyLoading.value = false
  }
}

onMounted(() => {
  loadTickets()
  loadStats()
})

onUnmounted(() => {
  listController?.abort()
  detailController?.abort()
  if (searchTimer) window.clearTimeout(searchTimer)
})
</script>