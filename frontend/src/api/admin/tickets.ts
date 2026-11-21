/**
 * Admin Tickets API endpoints
 *
 * 契约对齐 backend/internal/handler/dto/tickets.go 与
 * backend/internal/handler/admin/ticket_handler.go（管理侧 /api/v1/admin/tickets）。
 */

import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'
import type {
  TicketBrief,
  TicketCategory,
  TicketPriority,
  TicketReply,
  TicketStatus
} from '@/api/tickets'

/** dto.TicketUserSummary：管理端详情里的提交人。 */
export interface TicketUserSummary {
  id: number
  email: string
  username: string
}

/** dto.AdminTicketBrief = TicketBrief + 提交人摘要。 */
export interface AdminTicketBrief extends TicketBrief {
  user_id: number
  user_email: string
  user_name: string
}

/** dto.AdminTicketDetailResponse */
export interface AdminTicketDetailResponse {
  ticket: TicketBrief
  replies: TicketReply[]
  user?: TicketUserSummary
}

/** dto.AdminTicketReplyResponse；发信失败不影响回复落库。 */
export interface AdminTicketReplyResponse {
  ticket: AdminTicketBrief
  reply: TicketReply
  email_sent: boolean
  email_error?: string
}

/** dto.AdminTicketResponse（改状态/优先级） */
export interface AdminTicketResponse {
  ticket: AdminTicketBrief
}

/** dto.TicketStats */
export interface TicketStats {
  open: number
  pending: number
  resolved: number
  closed: number
  total: number
}

export interface AdminListTicketsParams {
  page?: number
  page_size?: number
  status?: TicketStatus | ''
  priority?: TicketPriority | ''
  search?: string
  user_id?: number
  signal?: AbortSignal
}

export interface AdminReplyTicketRequest {
  content: string
  notify_email?: boolean
  status?: TicketStatus
}

export interface UpdateTicketRequest {
  status?: TicketStatus
  priority?: TicketPriority
}

export async function listTickets(
  params: AdminListTicketsParams = {}
): Promise<PaginatedResponse<AdminTicketBrief>> {
  const { page = 1, page_size = 20, status, priority, search, user_id, signal } = params
  const { data } = await apiClient.get<PaginatedResponse<AdminTicketBrief>>('/admin/tickets', {
    params: {
      page,
      page_size,
      ...(status ? { status } : {}),
      ...(priority ? { priority } : {}),
      ...(search ? { search } : {}),
      ...(user_id ? { user_id } : {})
    },
    signal
  })
  return data
}

export async function getTicket(
  id: number,
  options: { signal?: AbortSignal } = {}
): Promise<AdminTicketDetailResponse> {
  const { data } = await apiClient.get<AdminTicketDetailResponse>(`/admin/tickets/${id}`, {
    signal: options.signal
  })
  return data
}

export async function replyTicket(
  id: number,
  payload: AdminReplyTicketRequest
): Promise<AdminTicketReplyResponse> {
  const { data } = await apiClient.post<AdminTicketReplyResponse>(`/admin/tickets/${id}/replies`, payload)
  return data
}

export async function updateTicket(id: number, payload: UpdateTicketRequest): Promise<AdminTicketResponse> {
  const { data } = await apiClient.put<AdminTicketResponse>(`/admin/tickets/${id}`, payload)
  return data
}

export async function getTicketStats(): Promise<TicketStats> {
  const { data } = await apiClient.get<TicketStats>('/admin/tickets/stats')
  return data
}

export const adminTicketsAPI = {
  list: listTickets,
  get: getTicket,
  reply: replyTicket,
  update: updateTicket,
  stats: getTicketStats
}

export default adminTicketsAPI

export type { TicketCategory, TicketPriority, TicketStatus }
export type { AdminTicketBrief as AdminTicket }