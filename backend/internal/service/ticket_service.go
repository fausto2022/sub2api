package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// Ticket 业务错误。HTTP 状态码与 design.md §1.2/§1.3 保持一致。
var (
	ErrTicketNotFound = infraerrors.NotFound("TICKET_NOT_FOUND", "ticket not found")
	// 已关闭工单拒绝新增回复；管理员需先通过 PUT 改回 open。
	ErrTicketClosed          = infraerrors.New(http.StatusConflict, "TICKET_CLOSED", "ticket is closed and cannot accept new replies")
	ErrTicketInvalidStatus   = infraerrors.BadRequest("INVALID_TICKET_STATUS", "ticket status is invalid")
	ErrTicketInvalidPriority = infraerrors.BadRequest("INVALID_TICKET_PRIORITY", "ticket priority is invalid")
	ErrTicketInvalidCategory = infraerrors.BadRequest("INVALID_TICKET_CATEGORY", "ticket category is invalid")
	ErrTicketSubjectRequired = infraerrors.BadRequest("TICKET_SUBJECT_REQUIRED", "ticket subject is required")
	ErrTicketSubjectTooLong  = infraerrors.BadRequest(
		"TICKET_SUBJECT_TOO_LONG",
		fmt.Sprintf("ticket subject cannot exceed %d characters", domain.TicketSubjectMaxLength),
	)
	ErrTicketContentRequired = infraerrors.BadRequest("TICKET_CONTENT_REQUIRED", "ticket content is required")
	ErrTicketContentTooLong  = infraerrors.BadRequest(
		"TICKET_CONTENT_TOO_LONG",
		fmt.Sprintf("ticket content cannot exceed %d characters", domain.TicketContentMaxLength),
	)
	ErrTicketReplyLimitReached = infraerrors.BadRequest(
		"TICKET_REPLY_LIMIT_REACHED",
		fmt.Sprintf("ticket cannot have more than %d replies", domain.TicketMaxRepliesPerTicket),
	)
	ErrTicketNothingToUpdate = infraerrors.BadRequest(
		"TICKET_UPDATE_EMPTY",
		"at least one of status or priority must be provided",
	)
)

// Ticket 是 tickets 表的业务视图。UserEmail/UserName 仅在管理侧列表/详情填充。
type Ticket struct {
	ID          int64
	UserID      int64
	Subject     string
	Category    string
	Priority    string
	Status      string
	LastReplyAt *time.Time
	LastReplyBy string
	ReplyCount  int
	CreatedAt   time.Time
	UpdatedAt   time.Time

	UserEmail string
	UserName  string
}

// TicketReply 是 ticket_replies 表的业务视图。AuthorName 为展示用派生字段。
type TicketReply struct {
	ID            int64
	TicketID      int64
	AuthorID      int64
	AuthorRole    string
	Content       string
	EmailNotified bool
	AuthorName    string
	CreatedAt     time.Time
	// Attachments 是该回复携带的图片附件（详情聚合时填充，按创建顺序）。
	Attachments []TicketAttachment
}

// TicketUser 是管理侧详情里的提交人摘要。
type TicketUser struct {
	ID       int64
	Email    string
	Username string
}

// TicketDetail 是单工单详情（工单 + 全量回复 + 可选提交人）。
type TicketDetail struct {
	Ticket  *Ticket
	Replies []TicketReply
	User    *TicketUser
}

// TicketStats 是管理端状态计数。
type TicketStats struct {
	Open     int64
	Pending  int64
	Resolved int64
	Closed   int64
	Total    int64
}

// TicketListFilters 是用户侧列表筛选。
type TicketListFilters struct {
	Status string
}

// AdminTicketFilters 是管理侧列表筛选。
type AdminTicketFilters struct {
	Status   string
	Priority string
	Search   string
	UserID   int64
}

// CreateTicketInput 是创建工单输入（含首条内容）。
type CreateTicketInput struct {
	Subject  string
	Category string
	Priority string
	Content  string
}

// AdminTicketReplyResult 是管理员回复结果；发信失败不影响回复落库。
type AdminTicketReplyResult struct {
	Ticket     *Ticket
	Reply      *TicketReply
	EmailSent  bool
	EmailError string
}

// TicketRepository 是工单持久化契约（ent 实现在 repository/ticket_repo.go）。
type TicketRepository interface {
	// CreateWithFirstReply 在同一事务内创建工单并写入首条 user 回复。
	CreateWithFirstReply(ctx context.Context, ticket *Ticket, reply *TicketReply) error
	GetByID(ctx context.Context, id int64) (*Ticket, error)
	// GetByUser 按 (id, user_id) 查询；他人工单一律返回 ErrTicketNotFound。
	GetByUser(ctx context.Context, id, userID int64) (*Ticket, error)
	ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams, status string) ([]Ticket, *pagination.PaginationResult, error)
	ListAdmin(ctx context.Context, params pagination.PaginationParams, filters AdminTicketFilters) ([]Ticket, *pagination.PaginationResult, error)
	ListReplies(ctx context.Context, ticketID int64) ([]TicketReply, error)
	// AddReply 在同一事务内写入回复、递增 reply_count 并更新工单状态/最后回复信息。
	AddReply(ctx context.Context, reply *TicketReply, status string) (*Ticket, error)
	UpdateTicket(ctx context.Context, ticketID int64, status, priority *string) (*Ticket, error)
	MarkReplyEmailNotified(ctx context.Context, replyID int64) error
	Stats(ctx context.Context) (*TicketStats, error)
}

// TicketUserReader 是工单服务所需的最小用户读取能力。
type TicketUserReader interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

// TicketNotifier 是工单邮件通知所需的最小发送能力（*NotificationEmailService 实现）。
type TicketNotifier interface {
	Send(ctx context.Context, input NotificationEmailSendInput) error
}

// TicketAttachmentBinder 是工单服务所需的附件能力（*TicketAttachmentService 实现）：
// 两阶段绑定的校验/回填，以及详情聚合时的按回复分组读取。
type TicketAttachmentBinder interface {
	// ValidateBinding 在工单/回复落库前做只读校验（归属、未绑定、数量）。
	ValidateBinding(ctx context.Context, userID int64, attachmentIDs []int64) error
	// BindReply 在事务内回填 reply_id，返回绑定后的附件列表。
	BindReply(ctx context.Context, userID, ticketID, replyID int64, attachmentIDs []int64) ([]TicketAttachment, error)
	// ListReplyAttachments 返回工单下按 reply_id 分组的附件。
	ListReplyAttachments(ctx context.Context, ticketID int64) (map[int64][]TicketAttachment, error)
}

// TicketService 实现工单业务规则：状态机、归属校验、长度校验、分页边界、
// 回复上限、关闭语义与管理员回复邮件通知。
type TicketService struct {
	repo     TicketRepository
	userRepo TicketUserReader
	settings SettingRepository
	notifier TicketNotifier
	// attachments 为可选依赖：未注入时带 attachment_ids 的请求返回 503，
	// 其余工单功能不受影响（保持既有单测可独立构造）。
	attachments TicketAttachmentBinder
}

func NewTicketService(
	repo TicketRepository,
	userRepo TicketUserReader,
	settings SettingRepository,
	notifier TicketNotifier,
) *TicketService {
	return &TicketService{repo: repo, userRepo: userRepo, settings: settings, notifier: notifier}
}

// SetAttachmentBinder 注入工单附件能力（两阶段绑定 + 详情聚合）。
func (s *TicketService) SetAttachmentBinder(binder TicketAttachmentBinder) {
	if s == nil {
		return
	}
	s.attachments = binder
}

// CreateTicket 创建工单并同事务写入首条回复。初始状态 open，last_reply_by=user。
func (s *TicketService) CreateTicket(ctx context.Context, userID int64, input CreateTicketInput) (*Ticket, *TicketReply, error) {
	return s.CreateTicketWithAttachments(ctx, userID, input, nil)
}

// CreateTicketWithAttachments 在 CreateTicket 基础上支持两阶段附件绑定：
// attachment_ids（≤3）在工单与首条回复落库后于同一逻辑单元内回填 reply_id。
func (s *TicketService) CreateTicketWithAttachments(
	ctx context.Context,
	userID int64,
	input CreateTicketInput,
	attachmentIDs []int64,
) (*Ticket, *TicketReply, error) {
	subject, err := normalizeTicketSubject(input.Subject)
	if err != nil {
		return nil, nil, err
	}
	content, err := normalizeTicketContent(input.Content)
	if err != nil {
		return nil, nil, err
	}
	category, err := normalizeTicketCategory(input.Category)
	if err != nil {
		return nil, nil, err
	}
	priority, err := normalizeTicketPriority(input.Priority)
	if err != nil {
		return nil, nil, err
	}
	// 先做只读校验：附件非法时不留下只建了一半的工单。
	if err := s.validateAttachmentBinding(ctx, userID, attachmentIDs); err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	ticket := &Ticket{
		UserID:      userID,
		Subject:     subject,
		Category:    category,
		Priority:    priority,
		Status:      domain.TicketStatusOpen,
		LastReplyAt: &now,
		LastReplyBy: domain.TicketAuthorRoleUser,
		ReplyCount:  1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	reply := &TicketReply{
		AuthorID:   userID,
		AuthorRole: domain.TicketAuthorRoleUser,
		Content:    content,
		CreatedAt:  now,
	}
	if err := s.repo.CreateWithFirstReply(ctx, ticket, reply); err != nil {
		return nil, nil, err
	}
	if bound, err := s.bindAttachments(ctx, userID, ticket.ID, reply.ID, attachmentIDs); err != nil {
		return nil, nil, err
	} else {
		reply.Attachments = bound
	}
	s.fillTicketUser(ctx, ticket)
	s.decorateReplies(ctx, ticket, []*TicketReply{reply})
	return ticket, reply, nil
}

// ListUserTickets 分页列出当前用户工单，可按状态筛选。
func (s *TicketService) ListUserTickets(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
	status string,
) ([]Ticket, *pagination.PaginationResult, error) {
	status = strings.TrimSpace(status)
	if status != "" && !domain.IsValidTicketStatus(status) {
		return nil, nil, ErrTicketInvalidStatus
	}
	params = normalizeTicketPagination(params)
	return s.repo.ListByUser(ctx, userID, params, status)
}

// GetUserTicket 按归属读取工单详情；他人工单返回 404。
func (s *TicketService) GetUserTicket(ctx context.Context, userID, ticketID int64) (*TicketDetail, error) {
	ticket, err := s.repo.GetByUser(ctx, ticketID, userID)
	if err != nil {
		return nil, err
	}
	replies, err := s.repo.ListReplies(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}
	s.fillTicketUser(ctx, ticket)
	s.decorateReplies(ctx, ticket, ptrReplies(replies))
	s.loadReplyAttachments(ctx, ticket.ID, ptrReplies(replies))
	return &TicketDetail{Ticket: ticket, Replies: replies}, nil
}

// AddUserReply 追加用户回复：非关闭状态自动回到 open；已关闭工单返回 409。
func (s *TicketService) AddUserReply(ctx context.Context, userID, ticketID int64, content string) (*Ticket, *TicketReply, error) {
	return s.AddUserReplyWithAttachments(ctx, userID, ticketID, content, nil)
}

// AddUserReplyWithAttachments 在 AddUserReply 基础上支持两阶段附件绑定。
func (s *TicketService) AddUserReplyWithAttachments(
	ctx context.Context,
	userID, ticketID int64,
	content string,
	attachmentIDs []int64,
) (*Ticket, *TicketReply, error) {
	normalized, err := normalizeTicketContent(content)
	if err != nil {
		return nil, nil, err
	}
	ticket, err := s.repo.GetByUser(ctx, ticketID, userID)
	if err != nil {
		return nil, nil, err
	}
	if domain.IsTicketClosed(ticket.Status) {
		return nil, nil, ErrTicketClosed
	}
	if ticket.ReplyCount >= domain.TicketMaxRepliesPerTicket {
		return nil, nil, ErrTicketReplyLimitReached
	}
	if err := s.validateAttachmentBinding(ctx, userID, attachmentIDs); err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	reply := &TicketReply{
		TicketID:   ticket.ID,
		AuthorID:   userID,
		AuthorRole: domain.TicketAuthorRoleUser,
		Content:    normalized,
		CreatedAt:  now,
	}
	updated, err := s.repo.AddReply(ctx, reply, domain.TicketStatusOpen)
	if err != nil {
		return nil, nil, err
	}
	if bound, err := s.bindAttachments(ctx, userID, ticket.ID, reply.ID, attachmentIDs); err != nil {
		return nil, nil, err
	} else {
		reply.Attachments = bound
	}
	s.fillTicketUser(ctx, updated)
	s.decorateReplies(ctx, updated, []*TicketReply{reply})
	return updated, reply, nil
}

// CloseUserTicket 关闭本人工单（open/pending/resolved → closed）；已关闭时幂等返回。
func (s *TicketService) CloseUserTicket(ctx context.Context, userID, ticketID int64) (*Ticket, error) {
	ticket, err := s.repo.GetByUser(ctx, ticketID, userID)
	if err != nil {
		return nil, err
	}
	if domain.IsTicketClosed(ticket.Status) {
		return ticket, nil
	}
	closed := domain.TicketStatusClosed
	updated, err := s.repo.UpdateTicket(ctx, ticket.ID, &closed, nil)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// AdminListTickets 分页/筛选列出全部工单（管理侧）。
func (s *TicketService) AdminListTickets(
	ctx context.Context,
	params pagination.PaginationParams,
	filters AdminTicketFilters,
) ([]Ticket, *pagination.PaginationResult, error) {
	filters.Status = strings.TrimSpace(filters.Status)
	filters.Priority = strings.TrimSpace(filters.Priority)
	filters.Search = strings.TrimSpace(filters.Search)
	if filters.Status != "" && !domain.IsValidTicketStatus(filters.Status) {
		return nil, nil, ErrTicketInvalidStatus
	}
	if filters.Priority != "" && !domain.IsValidTicketPriority(filters.Priority) {
		return nil, nil, ErrTicketInvalidPriority
	}
	if utf8.RuneCountInString(filters.Search) > 200 {
		filters.Search = string([]rune(filters.Search)[:200])
	}
	if filters.UserID < 0 {
		filters.UserID = 0
	}
	params = normalizeTicketPagination(params)
	return s.repo.ListAdmin(ctx, params, filters)
}

// AdminGetTicket 读取任意工单详情，附带提交人摘要。
func (s *TicketService) AdminGetTicket(ctx context.Context, ticketID int64) (*TicketDetail, error) {
	ticket, err := s.repo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	replies, err := s.repo.ListReplies(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}
	s.fillTicketUser(ctx, ticket)
	s.decorateReplies(ctx, ticket, ptrReplies(replies))
	s.loadReplyAttachments(ctx, ticket.ID, ptrReplies(replies))

	detail := &TicketDetail{Ticket: ticket, Replies: replies}
	if user, lookupErr := s.lookupUser(ctx, ticket.UserID); lookupErr == nil && user != nil {
		detail.User = &TicketUser{ID: user.ID, Email: user.Email, Username: user.Username}
	}
	return detail, nil
}

// AdminReply 管理员回复：默认把工单置为 pending；notifyEmail 时发送 ticket.reply。
// AdminReply 管理员回复：默认把工单置为 pending；notifyEmail 时发送 ticket.reply。
//
// attachmentIDs 为可选参数（≤3 张）：管理员与用户一样可以先上传附件、再在回复时绑定。
// 这里用变长切片保留既有调用签名，历史调用方（含单测）无需改动。
func (s *TicketService) AdminReply(
	ctx context.Context,
	adminID, ticketID int64,
	content string,
	notifyEmail bool,
	status *string,
	attachmentIDs ...[]int64,
) (*AdminTicketReplyResult, error) {
	var ids []int64
	if len(attachmentIDs) > 0 {
		ids = attachmentIDs[0]
	}
	return s.AdminReplyWithAttachments(ctx, adminID, ticketID, content, notifyEmail, status, ids)
}

// AdminReplyWithAttachments 是 AdminReply 的显式展开形式：管理员回复可携带图片附件。
//
// 绑定规则与用户侧完全一致（复用 TicketAttachmentBinder）：
//   - 数量 ≤3，超限 400（ErrTicketAttachmentLimitExceeded）；
//   - 附件必须由当前管理员上传（uploader_id == adminID）且未绑定，否则按不存在处理（404）；
//   - 回复落库后在事务内回填 reply_id（repository.BindReply 条件更新，失败整体回滚）。
func (s *TicketService) AdminReplyWithAttachments(
	ctx context.Context,
	adminID, ticketID int64,
	content string,
	notifyEmail bool,
	status *string,
	attachmentIDs []int64,
) (*AdminTicketReplyResult, error) {
	normalized, err := normalizeTicketContent(content)
	if err != nil {
		return nil, err
	}
	targetStatus := domain.TicketStatusPending
	if status != nil && strings.TrimSpace(*status) != "" {
		value := strings.TrimSpace(*status)
		if !domain.IsValidTicketStatus(value) {
			return nil, ErrTicketInvalidStatus
		}
		targetStatus = value
	}

	ticket, err := s.repo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if domain.IsTicketClosed(ticket.Status) {
		return nil, ErrTicketClosed
	}
	if ticket.ReplyCount >= domain.TicketMaxRepliesPerTicket {
		return nil, ErrTicketReplyLimitReached
	}
	// 先做只读校验：附件非法时不留下"回复已落库但附件未绑定"的中间态。
	if err := s.validateAttachmentBinding(ctx, adminID, attachmentIDs); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	reply := &TicketReply{
		TicketID:   ticket.ID,
		AuthorID:   adminID,
		AuthorRole: domain.TicketAuthorRoleAdmin,
		Content:    normalized,
		CreatedAt:  now,
	}
	updated, err := s.repo.AddReply(ctx, reply, targetStatus)
	if err != nil {
		return nil, err
	}
	if bound, bindErr := s.bindAttachments(ctx, adminID, ticket.ID, reply.ID, attachmentIDs); bindErr != nil {
		return nil, bindErr
	} else {
		reply.Attachments = bound
	}
	s.fillTicketUser(ctx, updated)
	s.decorateReplies(ctx, updated, []*TicketReply{reply})

	result := &AdminTicketReplyResult{Ticket: updated, Reply: reply}
	if !notifyEmail {
		return result, nil
	}
	s.sendTicketReplyEmail(ctx, ticket, reply, result)
	return result, nil
}

// sendTicketReplyEmail 在用户邮箱可用时发送 ticket.reply；失败只记录并返回错误信息，
// 不回滚已落库的回复。
func (s *TicketService) sendTicketReplyEmail(
	ctx context.Context,
	ticket *Ticket,
	reply *TicketReply,
	result *AdminTicketReplyResult,
) {
	user, err := s.lookupUser(ctx, ticket.UserID)
	if err != nil || user == nil || strings.TrimSpace(user.Email) == "" {
		// 用户邮箱为空/不可用：跳过发信，email_sent=false 且无错误。
		return
	}
	recipientName := strings.TrimSpace(user.Username)
	if recipientName == "" {
		recipientName = strings.TrimSpace(user.Email)
	}
	input := NotificationEmailSendInput{
		Event:          NotificationEmailEventTicketReply,
		UserID:         user.ID,
		RecipientEmail: strings.TrimSpace(user.Email),
		RecipientName:  recipientName,
		SourceType:     "ticket_reply",
		SourceID:       strconv.FormatInt(reply.ID, 10),
		Variables: map[string]string{
			"recipient_name": recipientName,
			"ticket_subject": ticket.Subject,
			"ticket_id":      strconv.FormatInt(ticket.ID, 10),
			"reply_content":  reply.Content,
		},
	}
	if s.notifier == nil {
		result.EmailError = "notification email service is not configured"
		slog.Warn("ticket reply email skipped: notifier is not configured", "ticket_id", ticket.ID, "reply_id", reply.ID)
		return
	}
	if err := s.notifier.Send(ctx, input); err != nil {
		result.EmailError = err.Error()
		slog.Warn("ticket reply email failed", "ticket_id", ticket.ID, "reply_id", reply.ID, "error", err)
		return
	}
	result.EmailSent = true
	if err := s.repo.MarkReplyEmailNotified(ctx, reply.ID); err != nil {
		slog.Warn("failed to mark ticket reply as email notified", "ticket_id", ticket.ID, "reply_id", reply.ID, "error", err)
		return
	}
	reply.EmailNotified = true
}

// AdminUpdateTicket 修改状态/优先级（两者至少提供一个）。
func (s *TicketService) AdminUpdateTicket(
	ctx context.Context,
	ticketID int64,
	status, priority *string,
) (*Ticket, error) {
	var normalizedStatus, normalizedPriority *string
	if status != nil {
		value := strings.TrimSpace(*status)
		if value != "" {
			if !domain.IsValidTicketStatus(value) {
				return nil, ErrTicketInvalidStatus
			}
			normalizedStatus = &value
		}
	}
	if priority != nil {
		value := strings.TrimSpace(*priority)
		if value != "" {
			if !domain.IsValidTicketPriority(value) {
				return nil, ErrTicketInvalidPriority
			}
			normalizedPriority = &value
		}
	}
	if normalizedStatus == nil && normalizedPriority == nil {
		return nil, ErrTicketNothingToUpdate
	}
	updated, err := s.repo.UpdateTicket(ctx, ticketID, normalizedStatus, normalizedPriority)
	if err != nil {
		return nil, err
	}
	s.fillTicketUser(ctx, updated)
	return updated, nil
}

// AdminStats 返回各状态计数与总数。
func (s *TicketService) AdminStats(ctx context.Context) (*TicketStats, error) {
	return s.repo.Stats(ctx)
}

// validateAttachmentBinding 在工单/回复落库前做附件只读校验（归属/未绑定/数量 ≤3）。
func (s *TicketService) validateAttachmentBinding(ctx context.Context, userID int64, attachmentIDs []int64) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	if s.attachments == nil {
		return ErrTicketAttachmentUnavailable
	}
	return s.attachments.ValidateBinding(ctx, userID, attachmentIDs)
}

// bindAttachments 在工单/回复落库后回填 reply_id（事务内原子执行）。
func (s *TicketService) bindAttachments(
	ctx context.Context,
	userID, ticketID, replyID int64,
	attachmentIDs []int64,
) ([]TicketAttachment, error) {
	if len(attachmentIDs) == 0 {
		return nil, nil
	}
	if s.attachments == nil {
		return nil, ErrTicketAttachmentUnavailable
	}
	return s.attachments.BindReply(ctx, userID, ticketID, replyID, attachmentIDs)
}

// loadReplyAttachments 拉取工单附件并按 reply_id 回填到响应 DTO 的 replies[].attachments[]。
// 读失败只降级为「无附件」，不影响工单详情本身。
func (s *TicketService) loadReplyAttachments(ctx context.Context, ticketID int64, replies []*TicketReply) {
	if s.attachments == nil || len(replies) == 0 {
		return
	}
	grouped, err := s.attachments.ListReplyAttachments(ctx, ticketID)
	if err != nil {
		slog.Warn("failed to load ticket reply attachments", "ticket_id", ticketID, "error", err)
		return
	}
	if len(grouped) == 0 {
		return
	}
	for _, reply := range replies {
		if reply == nil {
			continue
		}
		if items, ok := grouped[reply.ID]; ok {
			reply.Attachments = items
		}
	}
}

// decorateReplies 填充回复展示名：管理员回复显示站点名（或“客服”），
// 用户回复显示用户名/邮箱。
func (s *TicketService) decorateReplies(ctx context.Context, ticket *Ticket, replies []*TicketReply) {
	adminName := s.adminDisplayName(ctx)
	userName := ""
	if ticket != nil {
		userName = ticketFirstNonEmpty(ticket.UserName, ticket.UserEmail)
	}
	for _, reply := range replies {
		if reply == nil {
			continue
		}
		switch reply.AuthorRole {
		case domain.TicketAuthorRoleAdmin:
			reply.AuthorName = adminName
		default:
			reply.AuthorName = userName
		}
	}
}

func (s *TicketService) adminDisplayName(ctx context.Context) string {
	if s.settings != nil {
		if name, err := s.settings.GetValue(ctx, SettingKeySiteName); err == nil {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				return trimmed
			}
		}
	}
	return "客服"
}

func (s *TicketService) fillTicketUser(ctx context.Context, ticket *Ticket) {
	if ticket == nil || ticket.UserID <= 0 || (ticket.UserEmail != "" || ticket.UserName != "") {
		return
	}
	user, err := s.lookupUser(ctx, ticket.UserID)
	if err != nil || user == nil {
		return
	}
	ticket.UserEmail = user.Email
	ticket.UserName = user.Username
}

func (s *TicketService) lookupUser(ctx context.Context, userID int64) (*User, error) {
	if s.userRepo == nil || userID <= 0 {
		return nil, errors.New("user repository is not configured")
	}
	return s.userRepo.GetByID(ctx, userID)
}

func normalizeTicketSubject(raw string) (string, error) {
	subject := strings.TrimSpace(raw)
	if subject == "" {
		return "", ErrTicketSubjectRequired
	}
	if utf8.RuneCountInString(subject) > domain.TicketSubjectMaxLength {
		return "", ErrTicketSubjectTooLong
	}
	return subject, nil
}

func normalizeTicketContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", ErrTicketContentRequired
	}
	if utf8.RuneCountInString(content) > domain.TicketContentMaxLength {
		return "", ErrTicketContentTooLong
	}
	return content, nil
}

func normalizeTicketCategory(raw string) (string, error) {
	category := strings.TrimSpace(raw)
	if category == "" {
		return domain.TicketCategoryGeneral, nil
	}
	if !domain.IsValidTicketCategory(category) {
		return "", ErrTicketInvalidCategory
	}
	return category, nil
}

func normalizeTicketPriority(raw string) (string, error) {
	priority := strings.TrimSpace(raw)
	if priority == "" {
		return domain.TicketPriorityNormal, nil
	}
	if !domain.IsValidTicketPriority(priority) {
		return "", ErrTicketInvalidPriority
	}
	return priority, nil
}

func normalizeTicketPagination(params pagination.PaginationParams) pagination.PaginationParams {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 {
		params.PageSize = 20
	}
	if params.PageSize > domain.TicketMaxPageSize {
		params.PageSize = domain.TicketMaxPageSize
	}
	return params
}

func ticketFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ptrReplies(replies []TicketReply) []*TicketReply {
	out := make([]*TicketReply, 0, len(replies))
	for i := range replies {
		out = append(out, &replies[i])
	}
	return out
}
