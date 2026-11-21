<template>
  <div class="space-y-3" data-testid="ticket-attachment-uploader">
    <!-- 添加入口：点击 / 拖拽 / 粘贴 -->
    <div
      class="rounded-xl border-2 border-dashed px-4 py-5 text-center transition-colors"
      :class="dropZoneClass"
      data-testid="ticket-attachment-dropzone"
      role="button"
      :tabindex="entryDisabled ? -1 : 0"
      :aria-disabled="entryDisabled ? 'true' : 'false'"
      :aria-label="t('tickets.attachments.select')"
      @click="openFilePicker"
      @keydown.enter.prevent="openFilePicker"
      @keydown.space.prevent="openFilePicker"
      @dragenter.prevent="handleDragEnter"
      @dragover.prevent="handleDragOver"
      @dragleave.prevent="handleDragLeave"
      @drop.prevent="handleDrop"
    >
      <input
        ref="fileInput"
        type="file"
        class="hidden"
        data-testid="ticket-attachment-input"
        :accept="accept"
        :disabled="entryDisabled"
        multiple
        @click.stop
        @change="handleFileInputChange"
      />
      <Icon name="upload" size="md" class="mx-auto mb-2 text-gray-400 dark:text-dark-400" />
      <p class="text-sm font-medium text-gray-700 dark:text-gray-300">
        {{ t('tickets.attachments.dropHint') }}
      </p>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
        {{ t('tickets.attachments.pasteHint') }}
      </p>
      <p class="mt-1 text-xs tabular-nums text-gray-500 dark:text-dark-400" data-testid="ticket-attachment-count">
        {{ t('tickets.attachments.countHint', { count: items.length, max: maxCount }) }}
      </p>
    </div>

    <!-- 缩略图网格 -->
    <div
      v-if="items.length > 0"
      class="grid grid-cols-3 gap-3 sm:grid-cols-4"
      data-testid="ticket-attachment-grid"
    >
      <div
        v-for="item in items"
        :key="item.key"
        class="relative aspect-square overflow-hidden rounded-xl border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800"
        data-testid="ticket-attachment-item"
        :data-status="item.status"
      >
        <img
          v-if="item.attachment"
          :src="item.attachment.url"
          :alt="item.name"
          class="h-full w-full object-cover"
          data-testid="ticket-attachment-thumb"
        />
        <div
          v-else
          class="flex h-full w-full flex-col items-center justify-center gap-1 px-1 text-center"
        >
          <Icon
            :name="item.status === 'error' ? 'exclamationTriangle' : 'upload'"
            size="sm"
            :class="item.status === 'error' ? 'text-red-500' : 'text-gray-400 dark:text-dark-400'"
          />
          <span class="line-clamp-2 break-all text-[11px] leading-tight text-gray-500 dark:text-dark-400">
            {{ item.name }}
          </span>
        </div>

        <!-- 上传进度 -->
        <div
          v-if="item.status === 'uploading'"
          class="absolute inset-x-0 bottom-0 bg-black/60 px-1.5 pb-1 pt-1.5"
          data-testid="ticket-attachment-progress"
        >
          <div class="h-1 w-full overflow-hidden rounded-full bg-white/30">
            <div
              class="h-full rounded-full bg-primary-400 transition-all duration-200"
              :style="{ width: `${item.progress}%` }"
            />
          </div>
          <span class="mt-0.5 block text-center text-[10px] tabular-nums text-white">
            {{ item.progress }}%
          </span>
        </div>

        <!-- 删除 -->
        <button
          type="button"
          class="absolute right-1 top-1 rounded-full bg-black/55 p-1 text-white transition-colors hover:bg-black/75 disabled:opacity-50"
          :aria-label="t('tickets.attachments.remove')"
          :title="t('tickets.attachments.remove')"
          :disabled="item.status === 'deleting'"
          data-testid="ticket-attachment-remove"
          @click.stop="removeItem(item)"
        >
          <Icon name="x" size="xs" />
        </button>

        <!-- 失败重试 -->
        <button
          v-if="item.status === 'error'"
          type="button"
          class="absolute inset-x-1 bottom-1 rounded-md bg-white/95 py-0.5 text-[11px] font-medium text-red-600 shadow-sm transition-colors hover:bg-white dark:bg-dark-700/95 dark:text-red-400"
          data-testid="ticket-attachment-retry"
          @click.stop="retryItem(item)"
        >
          {{ t('tickets.attachments.retry') }}
        </button>
      </div>
    </div>

    <!-- 校验 / 上传失败提示 -->
    <ul v-if="validationErrors.length > 0" class="space-y-1" data-testid="ticket-attachment-errors">
      <li
        v-for="(message, index) in validationErrors"
        :key="`${index}-${message}`"
        class="text-xs text-red-600 dark:text-red-400"
        data-testid="ticket-attachment-error"
      >
        {{ message }}
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
/**
 * 工单图片附件上传组件（design.md §1.8）。
 *
 * - 三种入口：点击选择、拖拽、粘贴（Ctrl+V 截图）；
 * - 客户端预校验（数量 / 5MB / 格式白名单）只做快速反馈，服务端为准；
 * - 缩略图网格 + 单张删除 + 上传进度 + 失败重试；
 * - 已达上限时禁用添加入口。
 *
 * v-model 绑定“已成功上传且可绑定的附件 ID 列表”，提交工单/回复时原样作为
 * `attachment_ids` 下发；v-model:pending 用于父组件在上传中禁用提交按钮。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import {
  TICKET_ATTACHMENT_ACCEPT,
  TICKET_ATTACHMENT_MAX_BYTES,
  TICKET_ATTACHMENT_MAX_COUNT,
  deleteTicketAttachment,
  isAllowedTicketAttachmentType,
  uploadTicketAttachment,
  type TicketAttachment
} from '@/api/tickets'

type ItemStatus = 'uploading' | 'success' | 'error' | 'deleting'

interface UploadItem {
  key: string
  file: File
  name: string
  status: ItemStatus
  progress: number
  attachment: TicketAttachment | null
  controller: AbortController | null
}

const props = withDefaults(
  defineProps<{
    /** 已上传成功的附件 ID 列表（v-model）。 */
    modelValue?: number[]
    /** 是否存在进行中的上传（v-model:pending）。 */
    pending?: boolean
    maxCount?: number
    maxSizeBytes?: number
    accept?: string
    disabled?: boolean
  }>(),
  {
    modelValue: () => [],
    pending: false,
    maxCount: TICKET_ATTACHMENT_MAX_COUNT,
    maxSizeBytes: TICKET_ATTACHMENT_MAX_BYTES,
    accept: TICKET_ATTACHMENT_ACCEPT,
    disabled: false
  }
)

const emit = defineEmits<{
  (e: 'update:modelValue', ids: number[]): void
  (e: 'update:pending', pending: boolean): void
}>()

const { t, te } = useI18n()

const items = ref<UploadItem[]>([])
const validationErrors = ref<string[]>([])
const isDragging = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

let keySeed = 0
let disposed = false

const entryDisabled = computed(() => props.disabled || items.value.length >= props.maxCount)

const uploadingCount = computed(() => items.value.filter((item) => item.status === 'uploading').length)

const uploadedIds = computed(() =>
  items.value
    .filter((item) => item.status === 'success' && item.attachment)
    .map((item) => item.attachment!.id)
)

const dropZoneClass = computed(() => {
  if (entryDisabled.value) {
    return 'cursor-not-allowed border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800/60'
  }
  if (isDragging.value) {
    return 'cursor-copy border-primary-400 bg-primary-50 dark:border-primary-500 dark:bg-primary-900/20'
  }
  return 'cursor-pointer border-gray-300 bg-gray-50 hover:border-primary-400 hover:bg-primary-50/60 dark:border-dark-600 dark:bg-dark-800 dark:hover:border-primary-500 dark:hover:bg-primary-900/10'
})

const maxSizeLabel = computed(() => formatBytes(props.maxSizeBytes))

watch(
  uploadingCount,
  (count) => {
    emit('update:pending', count > 0)
  },
  { immediate: true }
)

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  const rounded = Number.isInteger(value) || value >= 10 ? Math.round(value) : Number(value.toFixed(1))
  return `${rounded} ${units[unitIndex]}`
}

function pushError(message: string) {
  validationErrors.value = [...validationErrors.value, message]
}

function emitIds() {
  emit('update:modelValue', uploadedIds.value)
}

function createItem(file: File): UploadItem {
  keySeed += 1
  return {
    key: `attachment-${keySeed}-${file.name}`,
    file,
    name: file.name || t('tickets.attachments.unnamedFile'),
    status: 'uploading',
    progress: 0,
    attachment: null,
    controller: null
  }
}

function openFilePicker() {
  if (entryDisabled.value || props.disabled) return
  fileInput.value?.click()
}

function handleFileInputChange(event: Event) {
  const input = event.target as HTMLInputElement
  handleFiles(input.files)
  // 允许重复选择同一文件（选择后清空，触发下次 change）。
  input.value = ''
}

function handleDragEnter() {
  if (entryDisabled.value || props.disabled) return
  isDragging.value = true
}

function handleDragOver() {
  if (entryDisabled.value || props.disabled) return
  isDragging.value = true
}

function handleDragLeave() {
  isDragging.value = false
}

function handleDrop(event: DragEvent) {
  isDragging.value = false
  if (props.disabled) return
  if (items.value.length >= props.maxCount) {
    // 已达上限：给出明确提示而不是静默丢弃。
    pushError(t('tickets.attachments.limitReached', { max: props.maxCount }))
    return
  }
  handleFiles(event.dataTransfer?.files ?? null)
}

/**
 * 粘贴上传：监听 document 级 paste，这样用户在文本域里 Ctrl+V 截图也能命中本组件。
 * 只在剪贴板里确有文件时拦截，不影响普通文本粘贴。
 */
function handlePaste(event: Event) {
  if (props.disabled) return
  const clipboard = (event as ClipboardEvent).clipboardData
  const files = clipboard?.files
  if (!files || files.length === 0) return
  event.preventDefault()
  handleFiles(files)
}

/**
 * 统一入口：预校验（格式 → 大小 → 数量）后入队并串行上传。
 */
function handleFiles(files: FileList | File[] | null | undefined) {
  if (!files || props.disabled) return
  const list = Array.from(files as ArrayLike<File>).filter((file): file is File => !!file)
  if (list.length === 0) return

  validationErrors.value = []

  const remaining = props.maxCount - items.value.length
  if (remaining <= 0) {
    pushError(t('tickets.attachments.limitReached', { max: props.maxCount }))
    return
  }

  const accepted: UploadItem[] = []
  let rejectedByCount = 0

  for (const file of list) {
    if (accepted.length >= remaining) {
      rejectedByCount += 1
      continue
    }
    if (!isAllowedTicketAttachmentType(file)) {
      pushError(t('tickets.attachments.typeNotAllowed', { name: file.name || t('tickets.attachments.unnamedFile') }))
      continue
    }
    if (file.size > props.maxSizeBytes) {
      pushError(
        t('tickets.attachments.tooLarge', {
          name: file.name || t('tickets.attachments.unnamedFile'),
          max: maxSizeLabel.value
        })
      )
      continue
    }
    if (file.size === 0) {
      pushError(t('tickets.attachments.emptyFile', { name: file.name || t('tickets.attachments.unnamedFile') }))
      continue
    }
    accepted.push(createItem(file))
  }

  if (rejectedByCount > 0) {
    pushError(t('tickets.attachments.limitReached', { max: props.maxCount }))
  }

  if (accepted.length === 0) return

  const startIndex = items.value.length
  items.value = [...items.value, ...accepted]
  // 取回响应式代理（直接改原始对象不会触发视图更新）。
  const queue = items.value.slice(startIndex)
  void uploadQueue(queue)
}

async function uploadQueue(queue: UploadItem[]) {
  for (const item of queue) {
    if (disposed) return
    // 上一项可能已被用户移除。
    if (!items.value.includes(item)) continue
    await uploadItem(item)
  }
}

async function uploadItem(item: UploadItem) {
  const controller = new AbortController()
  item.controller = controller
  item.status = 'uploading'
  item.progress = 0

  try {
    const attachment = await uploadTicketAttachment(item.file, {
      signal: controller.signal,
      onProgress: (percent) => {
        item.progress = percent
      }
    })
    if (disposed || controller.signal.aborted) return
    item.attachment = attachment
    item.progress = 100
    item.status = 'success'
    emitIds()
  } catch (error) {
    if (disposed || controller.signal.aborted) return
    item.status = 'error'
    item.attachment = null
    pushError(attachmentErrorMessage(error))
  } finally {
    item.controller = null
  }
}

/** 优先用错误码映射文案，其次用服务端 message，最后回落到兜底文案。 */
function attachmentErrorMessage(error: unknown): string {
  const code = extractApiErrorCode(error)
  if (code) {
    const key = `tickets.attachments.errors.${code}`
    if (typeof te === 'function' && te(key)) return t(key)
  }
  return extractApiErrorMessage(error, t('tickets.attachments.uploadFailed'))
}

function retryItem(item: UploadItem) {
  if (props.disabled || item.status === 'uploading' || item.status === 'deleting') return
  validationErrors.value = []
  void uploadItem(item)
}

async function removeItem(item: UploadItem) {
  if (item.status === 'uploading') {
    item.controller?.abort()
    dropItem(item)
    emitIds()
    return
  }

  if (item.status === 'success' && item.attachment) {
    item.status = 'deleting'
    try {
      await deleteTicketAttachment(item.attachment.id)
    } catch (error) {
      // 已绑定（409）或已被清理（404）都不再需要保留本地缩略图，不打扰用户。
      const code = extractApiErrorCode(error)
      if (code !== 'TICKET_ATTACHMENT_ALREADY_BOUND' && code !== 'TICKET_ATTACHMENT_NOT_FOUND') {
        pushError(extractApiErrorMessage(error, t('tickets.attachments.deleteFailed')))
      }
    } finally {
      dropItem(item)
      emitIds()
    }
    return
  }

  dropItem(item)
  emitIds()
}

function dropItem(item: UploadItem) {
  item.controller?.abort()
  items.value = items.value.filter((candidate) => candidate.key !== item.key)
}

function cancelAll() {
  for (const item of items.value) {
    item.controller?.abort()
    item.controller = null
  }
}

/** 清空全部附件（父组件提交成功后调用）。 */
function reset() {
  cancelAll()
  items.value = []
  validationErrors.value = []
  emit('update:modelValue', [])
}

onMounted(() => {
  document.addEventListener('paste', handlePaste)
})

onBeforeUnmount(() => {
  disposed = true
  document.removeEventListener('paste', handlePaste)
  cancelAll()
})

defineExpose({ reset, uploadedIds })
</script>
