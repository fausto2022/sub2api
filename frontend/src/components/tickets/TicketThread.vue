<template>
  <div class="space-y-3" data-testid="ticket-thread">
    <p v-if="replies.length === 0" class="rounded-xl border border-dashed border-gray-200 px-4 py-6 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
      {{ emptyText || t('tickets.detail.emptyReplies') }}
    </p>

    <article
      v-for="reply in replies"
      :key="reply.id"
      data-testid="ticket-reply"
      class="rounded-2xl border p-4 transition-colors"
      :class="
        reply.author_role === 'admin'
          ? 'border-primary-200 bg-primary-50/40 dark:border-primary-900/60 dark:bg-primary-900/10'
          : 'border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800'
      "
    >
      <header class="flex flex-wrap items-center justify-between gap-2">
        <div class="flex min-w-0 items-center gap-2">
          <span
            class="badge"
            :class="reply.author_role === 'admin' ? 'badge-primary' : 'badge-gray'"
          >
            {{ roleLabel(reply.author_role) }}
          </span>
          <span class="truncate text-sm font-medium text-gray-900 dark:text-white">
            {{ reply.author_name || roleLabel(reply.author_role) }}
          </span>
        </div>
        <span class="text-xs text-gray-500 dark:text-dark-400">
          {{ formatDateTime(reply.created_at) }}
        </span>
      </header>

      <!-- 纯文本渲染：保留换行与空白，禁止 v-html -->
      <p class="mt-3 whitespace-pre-wrap break-words text-sm leading-relaxed text-gray-700 dark:text-gray-300">{{ reply.content }}</p>

      <!-- 附件缩略图：服务端白名单不含 SVG，且经鉴权代理读取，因此可安全 <img> 渲染 -->
      <ul
        v-if="replyAttachments(reply).length > 0"
        class="mt-3 flex flex-wrap gap-2"
        data-testid="ticket-reply-attachments"
      >
        <li v-for="attachment in replyAttachments(reply)" :key="attachment.id">
          <button
            type="button"
            class="group relative block h-20 w-20 overflow-hidden rounded-xl border border-gray-200 bg-gray-50 transition-shadow hover:shadow-md focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-700 dark:bg-dark-800"
            data-testid="ticket-attachment-thumb"
            :aria-label="t('tickets.attachments.viewOriginal')"
            :title="attachment.original_name"
            @click="openPreview(attachment)"
          >
            <img
              :src="attachment.url"
              :alt="attachment.original_name"
              class="h-full w-full object-cover"
              loading="lazy"
              decoding="async"
            />
          </button>
        </li>
      </ul>

      <p
        v-if="reply.email_notified"
        class="mt-2 inline-flex items-center gap-1 text-xs text-emerald-700 dark:text-emerald-300"
      >
        <Icon name="mail" size="xs" />
        {{ t('tickets.detail.emailNotified') }}
      </p>
    </article>

    <!-- 原图预览（无 v-html；图片来自同源鉴权代理） -->
    <div
      v-if="previewAttachment"
      class="fixed inset-0 z-50 flex flex-col items-center justify-center gap-3 bg-black/80 p-4"
      role="dialog"
      aria-modal="true"
      data-testid="ticket-attachment-lightbox"
      :aria-label="t('tickets.attachments.viewOriginal')"
      @click.self="closePreview"
    >
      <button
        type="button"
        class="absolute right-4 top-4 rounded-full bg-white/15 p-2 text-white transition-colors hover:bg-white/30"
        data-testid="ticket-attachment-lightbox-close"
        :aria-label="t('tickets.attachments.closePreview')"
        :title="t('tickets.attachments.closePreview')"
        @click="closePreview"
      >
        <Icon name="x" size="md" />
      </button>
      <img
        :src="previewAttachment.url"
        :alt="previewAttachment.original_name"
        class="max-h-[85vh] max-w-full rounded-lg object-contain shadow-2xl"
        data-testid="ticket-attachment-lightbox-image"
      />
      <a
        :href="previewAttachment.url"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex items-center gap-1 rounded-full bg-white/15 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-white/30"
        data-testid="ticket-attachment-open-original"
      >
        <Icon name="externalLink" size="xs" />
        {{ t('tickets.attachments.openOriginal') }}
      </a>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import type { TicketAttachment, TicketAuthorRole, TicketReply } from '@/api/tickets'

withDefaults(
  defineProps<{
    replies: TicketReply[]
    emptyText?: string
  }>(),
  { emptyText: '' }
)

const { t } = useI18n()

const previewAttachment = ref<TicketAttachment | null>(null)

function roleLabel(role: TicketAuthorRole): string {
  return role === 'admin' ? t('tickets.detail.authorSupport') : t('tickets.detail.authorUser')
}

/** 服务端恒下发 attachments（历史数据可能缺省），这里统一归属到空数组。 */
function replyAttachments(reply: TicketReply): TicketAttachment[] {
  return reply.attachments ?? []
}

function openPreview(attachment: TicketAttachment) {
  previewAttachment.value = attachment
  window.addEventListener('keydown', handleKeydown)
}

function closePreview() {
  previewAttachment.value = null
  window.removeEventListener('keydown', handleKeydown)
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') closePreview()
}

onBeforeUnmount(() => window.removeEventListener('keydown', handleKeydown))
</script>
