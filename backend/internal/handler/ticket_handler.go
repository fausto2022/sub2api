package handler

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// TicketHandler handles user-side ticket operations.
type TicketHandler struct {
	ticketService *service.TicketService
	// writeLimiter 只作用于创建工单与追加回复，读接口与管理端不受影响。
	writeLimiter *ticketWriteRateLimiter
}

func NewTicketHandler(ticketService *service.TicketService) *TicketHandler {
	return &TicketHandler{
		ticketService: ticketService,
		writeLimiter:  newTicketWriteRateLimiter(ticketWriteRateLimit, ticketWriteRateWindow),
	}
}

// allowTicketWrite 在用户侧写操作（创建工单 / 追加回复）前做按 user_id 的轻量限流：
// 60 秒内最多 5 次，超限写 429 RATE_LIMITED（沿用 response 统一错误外壳）并返回 false。
func (h *TicketHandler) allowTicketWrite(c *gin.Context, userID int64) bool {
	if h.writeLimiter.Allow(userID) {
		return true
	}
	if wait := h.writeLimiter.RetryAfter(userID); wait > 0 {
		seconds := int64((wait + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	}
	response.ErrorFrom(c, infraerrors.TooManyRequests(
		"RATE_LIMITED",
		"Too many ticket submissions, please slow down and try again later",
	))
	return false
}

type CreateTicketRequest struct {
	Subject  string `json:"subject"`
	Category string `json:"category"`
	Priority string `json:"priority"`
	Content  string `json:"content"`
	// AttachmentIDs 是两阶段上传后待绑定的附件 ID（≤3）。
	AttachmentIDs []int64 `json:"attachment_ids"`
}

type AddTicketReplyRequest struct {
	Content string `json:"content"`
	// AttachmentIDs 是两阶段上传后待绑定的附件 ID（≤3）。
	AttachmentIDs []int64 `json:"attachment_ids"`
}

// List handles listing current user's tickets.
// GET /api/v1/tickets
func (h *TicketHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, result, err := h.ticketService.ListUserTickets(
		c.Request.Context(),
		subject.UserID,
		pagination.PaginationParams{Page: page, PageSize: pageSize},
		c.Query("status"),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.TicketBrief, 0, len(items))
	for i := range items {
		out = append(out, *dto.TicketBriefFromService(&items[i]))
	}
	response.Paginated(c, out, result.Total, result.Page, result.PageSize)
}

// Create handles creating a ticket with its first reply.
// POST /api/v1/tickets
func (h *TicketHandler) Create(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	if !h.allowTicketWrite(c, subject.UserID) {
		return
	}
	var req CreateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	ticket, reply, err := h.ticketService.CreateTicketWithAttachments(
		c.Request.Context(),
		subject.UserID,
		service.CreateTicketInput{
			Subject:  req.Subject,
			Category: req.Category,
			Priority: req.Priority,
			Content:  req.Content,
		},
		req.AttachmentIDs,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, dto.CreateTicketResponse{
		Ticket: dto.TicketBriefFromService(ticket),
		Reply:  dto.TicketReplyFromService(reply),
	})
}

// Get handles reading one ticket owned by the current user; other users' tickets return 404.
// GET /api/v1/tickets/:id
func (h *TicketHandler) Get(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	ticketID, ok := parseTicketID(c)
	if !ok {
		return
	}
	detail, err := h.ticketService.GetUserTicket(c.Request.Context(), subject.UserID, ticketID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.TicketDetailResponse{
		Ticket:  dto.TicketBriefFromService(detail.Ticket),
		Replies: dto.TicketRepliesFromService(detail.Replies),
	})
}

// AddReply handles appending a user reply (auto reopens non-closed tickets).
// POST /api/v1/tickets/:id/replies
func (h *TicketHandler) AddReply(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	if !h.allowTicketWrite(c, subject.UserID) {
		return
	}
	ticketID, ok := parseTicketID(c)
	if !ok {
		return
	}
	var req AddTicketReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	ticket, reply, err := h.ticketService.AddUserReplyWithAttachments(
		c.Request.Context(),
		subject.UserID,
		ticketID,
		req.Content,
		req.AttachmentIDs,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.TicketReplyResponse{
		Ticket: dto.TicketBriefFromService(ticket),
		Reply:  dto.TicketReplyFromService(reply),
	})
}

// Close handles closing a ticket owned by the current user.
// POST /api/v1/tickets/:id/close
func (h *TicketHandler) Close(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	ticketID, ok := parseTicketID(c)
	if !ok {
		return
	}
	ticket, err := h.ticketService.CloseUserTicket(c.Request.Context(), subject.UserID, ticketID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.TicketResponse{Ticket: dto.TicketBriefFromService(ticket)})
}

func parseTicketID(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid ticket ID")
		return 0, false
	}
	return id, true
}
