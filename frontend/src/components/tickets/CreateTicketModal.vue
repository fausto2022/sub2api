<template>
  <BaseDialog :show="show" :title="t('tickets.create.title')" width="wide" @close="handleClose">
    <form id="create-ticket-form" class="space-y-4" @submit.prevent="handleSubmit">
      <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('tickets.create.subtitle') }}</p>

      <div>
        <label for="ticket-subject" class="input-label mb-1.5 block">
          {{ t('tickets.create.subject') }}
        </label>
        <input
          id="ticket-subject"
          v-model="form.subject"
          type="text"
          class="input w-full"
          :placeholder="t('tickets.create.subjectPlaceholder')"
          :maxlength="TICKET_SUBJECT_MAX_LENGTH + 1"
          autocomplete="off"
        />
        <p v-if="errors.subject" class="input-error-text mt-1.5">{{ errors.subject }}</p>
      </div>

      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div>
          <label class="input-label mb-1.5 block">{{ t('tickets.create.category') }}</label>
          <Select v-model="form.category" :options="categoryOptions" />
        </div>
        <div>
          <label class="input-label mb-1.5 block">{{ t('tickets.create.priority') }}</label>
          <Select v-model="form.priority" :options="priorityOptions" />
        </div>
      </div>

      <div>
        <TextArea
          id="ticket-content"
          v-model="form.content"
          :label="t('tickets.create.content')"
          :placeholder="t('tickets.create.contentPlaceholder')"
          :rows="8"
          :error="errors.content"
        />
      </div>

      <div>
        <p class="input-label mb-1.5">{{ t('tickets.attachments.label') }}</p>
        <TicketAttachmentUploader
          ref="uploaderRef"
          v-model="attachmentIds"
          v-model:pending="attachmentPending"
          :disabled="submitting"
        />
      </div>
    </form>

    <template #footer>
      <div class="flex items-center justify-end gap-2">
        <button type="button" class="btn btn-secondary" :disabled="submitting" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="create-ticket-form"
          class="btn btn-primary"
          :disabled="submitting || attachmentPending"
        >
          {{ submitting ? t('tickets.create.submitting') : t('tickets.create.submit') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  TICKET_CONTENT_MAX_LENGTH,
  TICKET_SUBJECT_MAX_LENGTH,
  ticketsAPI,
  type TicketBrief,
  type TicketCategory,
  type TicketPriority
} from '@/api/tickets'

import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import TicketAttachmentUploader from '@/components/tickets/TicketAttachmentUploader.vue'

const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created', ticket: TicketBrief): void
}>()

const { t } = useI18n()
const appStore = useAppStore()

const submitting = ref(false)
const attachmentIds = ref<number[]>([])
const attachmentPending = ref(false)
const uploaderRef = ref<InstanceType<typeof TicketAttachmentUploader> | null>(null)

const form = reactive({
  subject: '',
  category: 'general' as TicketCategory,
  priority: 'normal' as TicketPriority,
  content: ''
})

const errors = reactive({
  subject: '',
  content: ''
})

const categoryOptions = computed(() => [
  { value: 'general', label: t('tickets.categoryLabels.general') },
  { value: 'billing', label: t('tickets.categoryLabels.billing') },
  { value: 'api', label: t('tickets.categoryLabels.api') },
  { value: 'account', label: t('tickets.categoryLabels.account') },
  { value: 'other', label: t('tickets.categoryLabels.other') }
])

const priorityOptions = computed(() => [
  { value: 'low', label: t('tickets.priorityLabels.low') },
  { value: 'normal', label: t('tickets.priorityLabels.normal') },
  { value: 'high', label: t('tickets.priorityLabels.high') },
  { value: 'urgent', label: t('tickets.priorityLabels.urgent') }
])

function resetForm() {
  form.subject = ''
  form.category = 'general'
  form.priority = 'normal'
  form.content = ''
  errors.subject = ''
  errors.content = ''
  attachmentIds.value = []
  attachmentPending.value = false
  uploaderRef.value?.reset()
}

function validate(): boolean {
  const subject = form.subject.trim()
  const content = form.content.trim()
  errors.subject = ''
  errors.content = ''

  if (!subject) {
    errors.subject = t('tickets.create.subjectRequired')
  } else if (subject.length > TICKET_SUBJECT_MAX_LENGTH) {
    errors.subject = t('tickets.create.subjectTooLong', { max: TICKET_SUBJECT_MAX_LENGTH })
  }

  if (!content) {
    errors.content = t('tickets.create.contentRequired')
  } else if (content.length > TICKET_CONTENT_MAX_LENGTH) {
    errors.content = t('tickets.create.contentTooLong', { max: TICKET_CONTENT_MAX_LENGTH })
  }

  return !errors.subject && !errors.content
}

async function handleSubmit() {
  if (submitting.value) return
  if (!validate()) return

  submitting.value = true
  try {
    const result = await ticketsAPI.create({
      subject: form.subject.trim(),
      category: form.category,
      priority: form.priority,
      content: form.content.trim(),
      ...(attachmentIds.value.length > 0 ? { attachment_ids: [...attachmentIds.value] } : {})
    })
    appStore.showSuccess(t('tickets.create.success'))
    resetForm()
    emit('created', result.ticket)
    emit('close')
  } catch (error) {
    console.error('Error creating ticket:', error)
    appStore.showError(extractApiErrorMessage(error, t('tickets.create.failed')))
  } finally {
    submitting.value = false
  }
}

function handleClose() {
  if (submitting.value) return
  emit('close')
}

watch(
  () => props.show,
  (visible) => {
    if (visible) resetForm()
  }
)
</script>