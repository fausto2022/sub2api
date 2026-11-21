<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Header -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('tickets.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('tickets.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <div class="min-w-36">
            <Select
              v-model="statusFilter"
              :options="statusOptions"
              :placeholder="t('tickets.filterStatus')"
              @change="handleStatusChange"
            />
          </div>
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
          <button type="button" class="btn btn-primary" @click="showCreateModal = true">
            <Icon name="plus" size="md" class="mr-1" />
            {{ t('tickets.newTicket') }}
          </button>
        </div>
      </div>

      <!-- Loading state -->
      <div v-if="loading" class="space-y-3" data-testid="tickets-loading">
        <Skeleton v-for="index in 3" :key="index" height="96px" />
        <span class="sr-only">{{ t('common.loading') }}</span>
      </div>

      <!-- Error state -->
      <div v-else-if="loadError" class="card p-8 text-center" data-testid="tickets-error">
        <Icon name="exclamationCircle" size="xl" class="mx-auto mb-3 text-red-500" />
        <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary mt-4" @click="loadTickets">
          {{ t('common.refresh') }}
        </button>
      </div>

      <!-- Empty state -->
      <div v-else-if="tickets.length === 0" class="card p-8">
        <EmptyState
          :title="t('tickets.empty.title')"
          :description="t('tickets.empty.description')"
          :action-text="t('tickets.newTicket')"
          @action="showCreateModal = true"
        />
      </div>

      <!-- Ticket list -->
      <div v-else class="space-y-3" data-testid="tickets-list">
        <RouterLink
          v-for="ticket in tickets"
          :key="ticket.id"
          :to="{ name: 'TicketDetail', params: { id: ticket.id } }"
          class="card block p-4 transition-colors hover:border-primary-300 dark:hover:border-primary-700"
          :data-testid="`ticket-row-${ticket.id}`"
        >
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <TicketStatusBadge :status="ticket.status" />
                <span class="badge badge-gray">{{ categoryLabel(ticket.category) }}</span>
                <span class="badge" :class="priorityClass(ticket.priority)">
                  {{ priorityLabel(ticket.priority) }}
                </span>
              </div>
              <h2 class="mt-2 truncate text-base font-medium text-gray-900 dark:text-white">
                {{ ticket.subject }}
              </h2>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
                <span class="tabular-nums">#{{ ticket.id }}</span>
                <span>{{ t('tickets.createdAt', { time: formatDateTime(ticket.created_at) }) }}</span>
                <span>{{ t('tickets.replyCount', { count: ticket.reply_count }) }}</span>
                <span v-if="ticket.last_reply_at">
                  {{ t('tickets.lastReplyAt') }}: {{ formatDateTime(ticket.last_reply_at) }}
                </span>
                <span v-else>{{ t('tickets.noReplyYet') }}</span>
              </div>
            </div>
            <span class="inline-flex items-center gap-1 text-sm text-primary-600 dark:text-primary-400">
              {{ t('tickets.viewDetail') }}
              <Icon name="chevronRight" size="sm" />
            </span>
          </div>
        </RouterLink>
      </div>

      <Pagination
        v-if="!loading && !loadError && pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <CreateTicketModal
      :show="showCreateModal"
      @close="showCreateModal = false"
      @created="handleCreated"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import {
  ticketsAPI,
  type TicketBrief,
  type TicketCategory,
  type TicketPriority,
  type TicketStatus
} from '@/api/tickets'

import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import TicketStatusBadge from '@/components/tickets/TicketStatusBadge.vue'
import CreateTicketModal from '@/components/tickets/CreateTicketModal.vue'

const { t } = useI18n()
const appStore = useAppStore()

const tickets = ref<TicketBrief[]>([])
const loading = ref(false)
const loadError = ref('')
const statusFilter = ref<TicketStatus | ''>('')
const showCreateModal = ref(false)

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0
})

const statusOptions = computed(() => [
  { value: '', label: t('tickets.allStatus') },
  { value: 'open', label: t('tickets.statusLabels.open') },
  { value: 'pending', label: t('tickets.statusLabels.pending') },
  { value: 'resolved', label: t('tickets.statusLabels.resolved') },
  { value: 'closed', label: t('tickets.statusLabels.closed') }
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

let currentController: AbortController | null = null

async function loadTickets(): Promise<void> {
  currentController?.abort()
  const requestController = new AbortController()
  currentController = requestController
  const { signal } = requestController

  loading.value = true
  loadError.value = ''

  try {
    const result = await ticketsAPI.list({
      page: pagination.page,
      page_size: pagination.page_size,
      status: statusFilter.value,
      signal
    })
    if (signal.aborted || currentController !== requestController) return

    tickets.value = result.items
    pagination.total = result.total
    pagination.page = result.page
    pagination.page_size = result.page_size
  } catch (error) {
    if (
      signal.aborted ||
      currentController !== requestController ||
      (error as { code?: string })?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading tickets:', error)
    loadError.value = extractApiErrorMessage(error, t('tickets.loadFailed'))
    appStore.showError(loadError.value)
  } finally {
    if (currentController === requestController) {
      loading.value = false
      currentController = null
    }
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

function handleStatusChange() {
  pagination.page = 1
  loadTickets()
}

function handleCreated(ticket: TicketBrief) {
  showCreateModal.value = false
  pagination.page = 1
  loadTickets()
  if (ticket?.id) {
    tickets.value = [ticket, ...tickets.value.filter((item) => item.id !== ticket.id)]
  }
}

onMounted(loadTickets)
onUnmounted(() => currentController?.abort())
</script>