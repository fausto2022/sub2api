/**
 * User Tickets API endpoints
 *
 * 契约对齐 backend/internal/handler/dto/tickets.go 与
 * backend/internal/handler/ticket_handler.go（用户侧 /api/v1/tickets）。
 */

import { apiClient } from './client'
import type { PaginatedResponse } from '@/types'

/** 合法工单状态，与 backend/internal/domain/ticket.go 保持一致。 */
export type TicketStatus = 'open' | 'pending' | 'resolved' | 'closed'

/** 合法工单优先级。 */
export type TicketPriority = 'low' | 'normal' | 'high' | 'urgent'

/** 合法工单分类。 */
export type TicketCategory = 'general' | 'billing' | 'api' | 'account' | 'other'

/** 回复作者角色。 */
export type TicketAuthorRole = 'user' | 'admin'

/** 标题最大字符数（runes），对齐 domain.TicketSubjectMaxLength。 */
export const TICKET_SUBJECT_MAX_LENGTH = 200

/** 单条回复最大字符数（runes），对齐 domain.TicketContentMaxLength。 */
export const TICKET_CONTENT_MAX_LENGTH = 8000

// ==================== 工单图片附件 ====================
// 契约对齐 backend/internal/handler/dto/tickets.go（TicketAttachment）与
// backend/internal/service/ticket_attachment_service.go 的限制常量。

/** 单条工单/回复最多附件数（对齐 service.TicketAttachmentMaxPerReply = 3）。 */
export const TICKET_ATTACHMENT_MAX_COUNT = 3

/** 单张附件最大字节数（5MB，对齐 service.TicketAttachmentMaxBytes）。 */
export const TICKET_ATTACHMENT_MAX_BYTES = 5 * 1024 * 1024

/**
 * 允许的附件 MIME 白名单。
 * 服务端以魔数嗅探为准（白名单刻意不含 SVG，避免 XSS），前端仅做快速反馈。
 */
export const TICKET_ATTACHMENT_ALLOWED_TYPES = [
  'image/png',
  'image/jpeg',
  'image/webp',
  'image/gif'
] as const

/** `<input type="file">` 的 accept 值。 */
export const TICKET_ATTACHMENT_ACCEPT = TICKET_ATTACHMENT_ALLOWED_TYPES.join(',')

/** 附件扩展名到 MIME 的兜底映射（部分浏览器/粘贴场景 file.type 可能为空）。 */
const TICKET_ATTACHMENT_EXTENSION_TYPES: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  webp: 'image/webp',
  gif: 'image/gif'
}

/** 判断文件是否属于附件白名单（MIME 优先，扩展名兜底）。 */
export function isAllowedTicketAttachmentType(file: File): boolean {
  const mime = (file.type || '').toLowerCase()
  if (mime) return (TICKET_ATTACHMENT_ALLOWED_TYPES as readonly string[]).includes(mime)
  const extension = file.name.includes('.') ? file.name.split('.').pop()!.toLowerCase() : ''
  const inferred = TICKET_ATTACHMENT_EXTENSION_TYPES[extension]
  return inferred ? (TICKET_ATTACHMENT_ALLOWED_TYPES as readonly string[]).includes(inferred) : false
}

/** dto.TicketAttachment：url 需带登录态请求（同源鉴权代理读取，可直接用于 <img src>）。 */
export interface TicketAttachment {
  id: number
  url: string
  original_name: string
  content_type: string
  byte_size: number
  width: number
  height: number
}

/** 上传接口响应（dto.TicketAttachmentResponse）。 */
export interface TicketAttachmentResponse {
  attachment: TicketAttachment
}

/** dto.TicketBrief */
export interface TicketBrief {
  id: number
  subject: string
  category: TicketCategory
  priority: TicketPriority
  status: TicketStatus
  reply_count: number
  /** 无回复时后端 omitempty，因此可能缺失。 */
  last_reply_at?: string | null
  last_reply_by?: TicketAuthorRole | ''
  created_at: string
  updated_at: string
}

/** dto.TicketReply */
export interface TicketReply {
  id: number
  author_id: number
  author_role: TicketAuthorRole
  author_name: string
  content: string
  email_notified: boolean
  created_at: string
  /** 该回复的附件；服务端恒下发数组（无附件时为 []），历史数据可能缺省。 */
  attachments?: TicketAttachment[]
}

/** dto.CreateTicketResponse / dto.TicketReplyResponse */
export interface TicketWithReplyResponse {
  ticket: TicketBrief
  reply: TicketReply
}

/** dto.TicketDetailResponse */
export interface TicketDetailResponse {
  ticket: TicketBrief
  replies: TicketReply[]
}

/** dto.TicketResponse（关闭/改状态） */
export interface TicketResponse {
  ticket: TicketBrief
}

export interface ListTicketsParams {
  page?: number
  page_size?: number
  status?: TicketStatus | ''
  signal?: AbortSignal
}

export interface CreateTicketRequest {
  subject: string
  category?: TicketCategory
  priority?: TicketPriority
  content: string
  /** 已上传待绑定的附件 ID（≤3，服务端二次校验归属与数量）。 */
  attachment_ids?: number[]
}

export async function listTickets(params: ListTicketsParams = {}): Promise<PaginatedResponse<TicketBrief>> {
  const { page = 1, page_size = 20, status, signal } = params
  const { data } = await apiClient.get<PaginatedResponse<TicketBrief>>('/tickets', {
    params: {
      page,
      page_size,
      ...(status ? { status } : {})
    },
    signal
  })
  return data
}

export async function createTicket(payload: CreateTicketRequest): Promise<TicketWithReplyResponse> {
  const { data } = await apiClient.post<TicketWithReplyResponse>('/tickets', payload)
  return data
}

export async function getTicket(
  id: number,
  options: { signal?: AbortSignal } = {}
): Promise<TicketDetailResponse> {
  const { data } = await apiClient.get<TicketDetailResponse>(`/tickets/${id}`, {
    signal: options.signal
  })
  return data
}

export async function addTicketReply(
  id: number,
  content: string,
  attachmentIds: number[] = []
): Promise<TicketWithReplyResponse> {
  const { data } = await apiClient.post<TicketWithReplyResponse>(`/tickets/${id}/replies`, {
    content,
    ...(attachmentIds.length > 0 ? { attachment_ids: attachmentIds } : {})
  })
  return data
}

export async function closeTicket(id: number): Promise<TicketResponse> {
  const { data } = await apiClient.post<TicketResponse>(`/tickets/${id}/close`)
  return data
}

export interface UploadTicketAttachmentOptions {
  /** 上传进度回调（0–100）。 */
  onProgress?: (percent: number) => void
  /** 取消信号（组件卸载或用户移除该项时中断请求）。 */
  signal?: AbortSignal
}

/**
 * 上传单张工单图片附件（multipart 字段名 `file`）。
 *
 * 服务端做魔数嗅探与尺寸/配额二次校验，前端的预校验只是快速反馈。
 */
export async function uploadTicketAttachment(
  file: File,
  options: UploadTicketAttachmentOptions = {}
): Promise<TicketAttachment> {
  const formData = new FormData()
  formData.append('file', file)

  const { data } = await apiClient.post<TicketAttachmentResponse>('/tickets/attachments', formData, {
    // 浏览器会忽略该头并自动补 boundary（与 api/admin/plugins.ts 的上传写法一致）。
    headers: { 'Content-Type': 'multipart/form-data' },
    signal: options.signal,
    onUploadProgress: (event) => {
      if (!options.onProgress) return
      const total = event.total && event.total > 0 ? event.total : file.size
      if (!total) return
      const loaded = event.loaded ?? 0
      options.onProgress(Math.min(100, Math.max(0, Math.round((loaded / total) * 100))))
    }
  })
  return data.attachment
}

/**
 * 删除未绑定的工单附件。
 *
 * 已绑定到工单/回复的附件返回 409 TICKET_ATTACHMENT_ALREADY_BOUND，调用方可忽略。
 */
export async function deleteTicketAttachment(id: number): Promise<void> {
  await apiClient.delete(`/tickets/attachments/${id}`)
}

export const ticketsAPI = {
  list: listTickets,
  create: createTicket,
  get: getTicket,
  addReply: addTicketReply,
  close: closeTicket,
  uploadAttachment: uploadTicketAttachment,
  deleteAttachment: deleteTicketAttachment
}

export default ticketsAPI