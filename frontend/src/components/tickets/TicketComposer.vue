<template>
  <form class="space-y-3" data-testid="ticket-composer" @submit.prevent="handleSubmit">
    <TextArea
      :id="textareaId"
      v-model="draft"
      :placeholder="placeholderText"
      :disabled="disabled || loading"
      :rows="rows"
      :error="errorText"
    />

    <!-- 工单图片附件（仅在调用方显式开启时渲染：管理端回复接口暂不接受 attachment_ids） -->
    <TicketAttachmentUploader
      v-if="allowAttachments"
      ref="uploaderRef"
      v-model="attachmentIds"
      v-model:pending="attachmentPending"
      :disabled="disabled || loading"
    />

    <div class="flex flex-wrap items-center justify-between gap-3">
      <span class="text-xs tabular-nums text-gray-500 dark:text-dark-400">
        {{ draft.length }} / {{ maxLength }}
      </span>
      <button type="submit" class="btn btn-primary" :disabled="disabled || loading || attachmentPending">
        {{ loading ? loadingText : submitText }}
      </button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import TextArea from '@/components/common/TextArea.vue'
import TicketAttachmentUploader from '@/components/tickets/TicketAttachmentUploader.vue'
import { TICKET_CONTENT_MAX_LENGTH } from '@/api/tickets'

const props = withDefaults(
  defineProps<{
    loading?: boolean
    disabled?: boolean
    placeholder?: string
    submitLabel?: string
    loadingLabel?: string
    maxLength?: number
    rows?: number
    requiredMessage?: string
    tooLongMessage?: string
    componentId?: string
    /** 是否启用图片附件上传（设计 §1.8）。 */
    allowAttachments?: boolean
  }>(),
  {
    loading: false,
    disabled: false,
    placeholder: '',
    submitLabel: '',
    loadingLabel: '',
    maxLength: TICKET_CONTENT_MAX_LENGTH,
    rows: 4,
    requiredMessage: '',
    tooLongMessage: '',
    componentId: '',
    allowAttachments: false
  }
)

const emit = defineEmits<{
  (e: 'submit', content: string, attachmentIds: number[]): void
}>()

const { t } = useI18n()

const fallbackId = useId()
const textareaId = computed(() => props.componentId || `ticket-composer-${fallbackId}`)

const draft = ref('')
const localError = ref('')
const attachmentIds = ref<number[]>([])
const attachmentPending = ref(false)
const uploaderRef = ref<InstanceType<typeof TicketAttachmentUploader> | null>(null)

const placeholderText = computed(() => props.placeholder || t('tickets.detail.replyPlaceholder'))
const submitText = computed(() => props.submitLabel || t('tickets.detail.replySubmit'))
const loadingText = computed(() => props.loadingLabel || t('tickets.detail.replySubmitting'))
const requiredText = computed(() => props.requiredMessage || t('tickets.detail.replyRequired'))

const tooLongText = computed(() => {
  if (props.tooLongMessage) {
    return props.tooLongMessage.replace('{max}', String(props.maxLength))
  }
  return t('tickets.detail.replyTooLong', { max: props.maxLength })
})

const errorText = computed(() => {
  if (draft.value.length > props.maxLength) return tooLongText.value
  return localError.value
})

function handleSubmit() {
  const content = draft.value.trim()
  if (!content) {
    localError.value = requiredText.value
    return
  }
  if (content.length > props.maxLength) {
    localError.value = tooLongText.value
    return
  }
  if (props.allowAttachments && attachmentPending.value) {
    localError.value = t('tickets.attachments.uploadInProgress')
    return
  }
  localError.value = ''
  emit('submit', content, props.allowAttachments ? [...attachmentIds.value] : [])
}

function reset() {
  draft.value = ''
  localError.value = ''
  attachmentIds.value = []
  uploaderRef.value?.reset()
}

function setError(message: string) {
  localError.value = message
}

defineExpose({ reset, setError })
</script>
