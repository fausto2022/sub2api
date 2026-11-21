<template>
  <AppLayout>
    <div class="space-y-6">
      <RouterLink
        to="/tickets"
        class="inline-flex items-center gap-1 text-sm font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
      >
        <Icon name="arrowLeft" size="sm" />
        {{ t('tickets.detail.backToList') }}
      </RouterLink>

      <!-- Loading state -->
      <div v-if="loading" class="space-y-4" data-testid="ticket-detail-loading">
        <Skeleton height="120px" />
        <Skeleton height="220px" />
        <span class="sr-only">{{ t('common.loading') }}</span>
      </div>

      <!-- Error state -->
      <div v-else-if="loadError" class="card p-8 text-center" data-testid="ticket-detail-error">
        <Icon name="exclamationCircle" size="xl" class="mx-auto mb-3 text-red-500" />
        <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary mt-4" @click="loadTicket">
          {{ t('common.refresh') }}
        </button>
      </div>

      <template v-else-if="ticket">
        <!-- Ticket summary -->
        <div class="card p-5">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <TicketStatusBadge :status="ticket.status" />
                <span class="badge badge-gray">{{ categoryLabel(ticket.category) }}</span>
                <span class="badge" :class="priorityClass(ticket.priority)">
                  {{ priorityLabel(ticket.priority) }}
                </span>
              </div>
              <h1 class="mt-2 break-words text-xl font-semibold text-gray-900 dark:text-white">
                {{ ticket.subject }}
              </h1>
              <div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
                <span class="tabular-nums">{{ t('tickets.detail.ticketId') }} #{{ ticket.id }}</span>
                <span>{{ t('tickets.createdAt', { time: formatDateTime(ticket.created_at) }) }}</span>
                <span>{{ t('tickets.replyCount', { count: ticket.reply_count }) }}</span>
              </div>
            </div>

            <button
              v-if="ticket.status !== 'closed'"
              type="button"
              class="btn btn-secondary"
              :disabled="closing"
              @click="showCloseConfirm = true"
            >
              {{ t('tickets.detail.close') }}
            </button>
          </div>
        </div>

        <!-- Conversation -->
        <div class="card p-5">
          <h2 class="mb-4 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-400">
            {{ t('tickets.detail.replies') }}
          </h2>
          <TicketThread :replies="replies" />
        </div>

        <!-- Reply composer -->
        <div class="card p-5">
          <template v-if="ticket.status === 'closed'">
            <p class="text-sm text-gray-600 dark:text-gray-300" data-testid="ticket-closed-notice">
              {{ t('tickets.detail.closedNotice') }}
            </p>
          </template>
          <template v-else>
            <TicketComposer
              ref="composerRef"
              :loading="submitting"
              allow-attachments
              @submit="handleReply"
            />
          </template>
        </div>
      </template>
    </div>

    <ConfirmDialog
      :show="showCloseConfirm"
      :title="t('tickets.detail.closeConfirmTitle')"
      :message="t('tickets.detail.closeConfirmMessage')"
      :confirm-text="t('tickets.detail.close')"
      danger
      @confirm="handleCloseTicket"
      @cancel="showCloseConfirm = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import {
  ticketsAPI,
  type TicketBrief,
  type TicketCategory,
  type TicketPriority,
  type TicketReply
} from '@/api/tickets'

import AppLayout from '@/components/layout/AppLayout.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TicketStatusBadge from '@/components/tickets/TicketStatusBadge.vue'
import TicketThread from '@/components/tickets/TicketThread.vue'
import TicketComposer from '@/components/tickets/TicketComposer.vue'

const route = useRoute()
const { t } = useI18n()
const appStore = useAppStore()

const ticket = ref<TicketBrief | null>(null)
const replies = ref<TicketReply[]>([])
const loading = ref(false)
const loadError = ref('')
const submitting = ref(false)
const closing = ref(false)
const showCloseConfirm = ref(false)
const composerRef = ref<InstanceType<typeof TicketComposer> | null>(null)

const ticketId = computed(() => {
  const raw = route.params.id
  const value = Array.isArray(raw) ? raw[0] : raw
  const parsed = Number.parseInt(String(value ?? ''), 10)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
})

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

async function loadTicket(): Promise<void> {
  if (!ticketId.value) {
    loadError.value = t('tickets.detail.notFound')
    return
  }

  currentController?.abort()
  const requestController = new AbortController()
  currentController = requestController
  const { signal } = requestController

  loading.value = true
  loadError.value = ''

  try {
    const result = await ticketsAPI.get(ticketId.value, { signal })
    if (signal.aborted || currentController !== requestController) return

    ticket.value = result.ticket
    replies.value = result.replies
  } catch (error) {
    if (
      signal.aborted ||
      currentController !== requestController ||
      (error as { code?: string })?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading ticket detail:', error)
    loadError.value = extractApiErrorMessage(error, t('tickets.detail.loadFailed'))
    appStore.showError(loadError.value)
  } finally {
    if (currentController === requestController) {
      loading.value = false
      currentController = null
    }
  }
}

async function handleReply(content: string, attachmentIds: number[] = []) {
  if (!ticket.value || submitting.value) return

  submitting.value = true
  try {
    const result = await ticketsAPI.addReply(ticket.value.id, content, attachmentIds)
    ticket.value = result.ticket
    if (!replies.value.some((item) => item.id === result.reply.id)) {
      replies.value = [...replies.value, result.reply]
    }
    composerRef.value?.reset()
    appStore.showSuccess(t('tickets.detail.replySuccess'))
  } catch (error) {
    console.error('Error sending ticket reply:', error)
    const message = extractApiErrorMessage(error, t('tickets.detail.replyFailed'))
    appStore.showError(message)
    composerRef.value?.setError(message)
  } finally {
    submitting.value = false
  }
}

async function handleCloseTicket() {
  if (!ticket.value || closing.value) return

  closing.value = true
  showCloseConfirm.value = false
  try {
    const result = await ticketsAPI.close(ticket.value.id)
    ticket.value = result.ticket
    appStore.showSuccess(t('tickets.detail.closeSuccess'))
  } catch (error) {
    console.error('Error closing ticket:', error)
    appStore.showError(extractApiErrorMessage(error, t('tickets.detail.closeFailed')))
  } finally {
    closing.value = false
  }
}

onMounted(loadTicket)
onUnmounted(() => currentController?.abort())
</script>