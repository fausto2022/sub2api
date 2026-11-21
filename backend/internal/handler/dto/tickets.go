package dto

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ticketAttachmentURLPrefix 是附件鉴权读取地址前缀（非公开静态目录，见 design.md §1.2）。
const ticketAttachmentURLPrefix = "/api/v1/tickets/attachments/"

// TicketAttachment 是工单附件的对外视图。url 需带登录态请求（鉴权代理读取）。
type TicketAttachment struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	OriginalName string `json:"original_name"`
	ContentType  string `json:"content_type"`
	ByteSize     int64  `json:"byte_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

// TicketBrief 是工单列表/详情的精简视图。
type TicketBrief struct {
	ID          int64      `json:"id"`
	Subject     string     `json:"subject"`
	Category    string     `json:"category"`
	Priority    string     `json:"priority"`
	Status      string     `json:"status"`
	ReplyCount  int        `json:"reply_count"`
	LastReplyAt *time.Time `json:"last_reply_at,omitempty"`
	LastReplyBy string     `json:"last_reply_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TicketReply 是单条回复视图；author_name 由服务端按查看者侧规则填充。
type TicketReply struct {
	ID            int64     `json:"id"`
	AuthorID      int64     `json:"author_id"`
	AuthorRole    string    `json:"author_role"`
	AuthorName    string    `json:"author_name"`
	Content       string    `json:"content"`
	EmailNotified bool      `json:"email_notified"`
	CreatedAt     time.Time `json:"created_at"`
	// Attachments 恒为数组（无附件时为 []），前端可直接展开渲染缩略图。
	Attachments []TicketAttachment `json:"attachments"`
}

// AdminTicketBrief = TicketBrief + 提交人摘要。
type AdminTicketBrief struct {
	TicketBrief
	UserID    int64  `json:"user_id"`
	UserEmail string `json:"user_email"`
	UserName  string `json:"user_name"`
}

// TicketUserSummary 是管理端详情里的提交人。
type TicketUserSummary struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

// TicketDetailResponse 是用户侧详情响应。
type TicketDetailResponse struct {
	Ticket  *TicketBrief  `json:"ticket"`
	Replies []TicketReply `json:"replies"`
}

// AdminTicketDetailResponse 是管理侧详情响应。
type AdminTicketDetailResponse struct {
	Ticket  *TicketBrief       `json:"ticket"`
	Replies []TicketReply      `json:"replies"`
	User    *TicketUserSummary `json:"user,omitempty"`
}

// CreateTicketResponse 是创建工单响应（工单 + 首条回复）。
type CreateTicketResponse struct {
	Ticket *TicketBrief `json:"ticket"`
	Reply  *TicketReply `json:"reply"`
}

// TicketReplyResponse 是用户追加回复响应。
type TicketReplyResponse struct {
	Ticket *TicketBrief `json:"ticket"`
	Reply  *TicketReply `json:"reply"`
}

// AdminTicketReplyResponse 是管理员回复响应；发信失败不影响回复落库。
type AdminTicketReplyResponse struct {
	Ticket     *AdminTicketBrief `json:"ticket"`
	Reply      *TicketReply      `json:"reply"`
	EmailSent  bool              `json:"email_sent"`
	EmailError string            `json:"email_error,omitempty"`
}

// TicketResponse 是仅返回工单的操作响应（关闭/改状态）。
type TicketResponse struct {
	Ticket *TicketBrief `json:"ticket"`
}

// TicketAttachmentResponse 是上传/读取附件元数据的响应。
type TicketAttachmentResponse struct {
	Attachment *TicketAttachment `json:"attachment"`
}

// TicketAttachmentDeleteResponse 是删除未绑定附件的响应。
type TicketAttachmentDeleteResponse struct {
	Deleted bool  `json:"deleted"`
	ID      int64 `json:"id"`
}

// AdminTicketResponse 是管理侧仅返回工单的操作响应。
type AdminTicketResponse struct {
	Ticket *AdminTicketBrief `json:"ticket"`
}

// TicketStats 是管理端状态计数。
type TicketStats struct {
	Open     int64 `json:"open"`
	Pending  int64 `json:"pending"`
	Resolved int64 `json:"resolved"`
	Closed   int64 `json:"closed"`
	Total    int64 `json:"total"`
}

func TicketBriefFromService(item *service.Ticket) *TicketBrief {
	if item == nil {
		return nil
	}
	return &TicketBrief{
		ID:          item.ID,
		Subject:     item.Subject,
		Category:    item.Category,
		Priority:    item.Priority,
		Status:      item.Status,
		ReplyCount:  item.ReplyCount,
		LastReplyAt: item.LastReplyAt,
		LastReplyBy: item.LastReplyBy,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}

func TicketReplyFromService(item *service.TicketReply) *TicketReply {
	if item == nil {
		return nil
	}
	return &TicketReply{
		ID:            item.ID,
		AuthorID:      item.AuthorID,
		AuthorRole:    item.AuthorRole,
		AuthorName:    item.AuthorName,
		Content:       item.Content,
		EmailNotified: item.EmailNotified,
		CreatedAt:     item.CreatedAt,
		Attachments:   TicketAttachmentsFromService(item.Attachments),
	}
}

// TicketAttachmentFromService 把服务层附件映射为对外视图。
func TicketAttachmentFromService(item *service.TicketAttachment) *TicketAttachment {
	if item == nil {
		return nil
	}
	return &TicketAttachment{
		ID:           item.ID,
		URL:          ticketAttachmentURLPrefix + strconv.FormatInt(item.ID, 10),
		OriginalName: item.OriginalName,
		ContentType:  item.ContentType,
		ByteSize:     item.ByteSize,
		Width:        item.Width,
		Height:       item.Height,
	}
}

// TicketAttachmentsFromService 恒返回非 nil 切片，保证 JSON 输出 [] 而不是 null。
func TicketAttachmentsFromService(items []service.TicketAttachment) []TicketAttachment {
	out := make([]TicketAttachment, 0, len(items))
	for i := range items {
		out = append(out, *TicketAttachmentFromService(&items[i]))
	}
	return out
}

func AdminTicketBriefFromService(item *service.Ticket) *AdminTicketBrief {
	if item == nil {
		return nil
	}
	return &AdminTicketBrief{
		TicketBrief: *TicketBriefFromService(item),
		UserID:      item.UserID,
		UserEmail:   item.UserEmail,
		UserName:    item.UserName,
	}
}

func TicketRepliesFromService(items []service.TicketReply) []TicketReply {
	out := make([]TicketReply, 0, len(items))
	for i := range items {
		out = append(out, *TicketReplyFromService(&items[i]))
	}
	return out
}

func TicketStatsFromService(stats *service.TicketStats) *TicketStats {
	if stats == nil {
		return nil
	}
	return &TicketStats{
		Open:     stats.Open,
		Pending:  stats.Pending,
		Resolved: stats.Resolved,
		Closed:   stats.Closed,
		Total:    stats.Total,
	}
}
