package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// TicketHandler handles admin-side ticket management.
type TicketHandler struct {
	ticketService *service.TicketService
}

func NewTicketHandler(ticketService *service.TicketService) *TicketHandler {
	return &TicketHandler{ticketService: ticketService}
}

type AdminReplyTicketRequest struct {
	Content     string  `json:"content"`
	NotifyEmail *bool   `json:"notify_email"`
	Status      *string `json:"status"`
	// AttachmentIDs 是管理员先前通过 POST /api/v1/tickets/attachments 上传的附件 ID（≤3）。
	// 上传者必须为当前管理员且附件未被绑定，否则绑定失败。
	AttachmentIDs []int64 `json:"attachment_ids"`
}

type UpdateTicketRequest struct {
	Status   *string `json:"status"`
	Priority *string `json:"priority"`
}

// List handles listing all tickets with filters.
// GET /api/v1/admin/tickets
func (h *TicketHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	userID := int64(0)
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			userID = parsed
		}
	}
	items, result, err := h.ticketService.AdminListTickets(
		c.Request.Context(),
		pagination.PaginationParams{Page: page, PageSize: pageSize},
		service.AdminTicketFilters{
			Status:   c.Query("status"),
			Priority: c.Query("priority"),
			Search:   c.Query("search"),
			UserID:   userID,
		},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.AdminTicketBrief, 0, len(items))
	for i := range items {
		out = append(out, *dto.AdminTicketBriefFromService(&items[i]))
	}
	response.Paginated(c, out, result.Total, result.Page, result.PageSize)
}

// Get handles reading one ticket with submitter info.
// GET /api/v1/admin/tickets/:id
func (h *TicketHandler) Get(c *gin.Context) {
	ticketID, ok := parseAdminTicketID(c)
	if !ok {
		return
	}
	detail, err := h.ticketService.AdminGetTicket(c.Request.Context(), ticketID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	resp := dto.AdminTicketDetailResponse{
		Ticket:  dto.TicketBriefFromService(detail.Ticket),
		Replies: dto.TicketRepliesFromService(detail.Replies),
	}
	if detail.User != nil {
		resp.User = &dto.TicketUserSummary{
			ID:       detail.User.ID,
			Email:    detail.User.Email,
			Username: detail.User.Username,
		}
	}
	response.Success(c, resp)
}

// Reply handles an admin reply; notify_email defaults to true.
// POST /api/v1/admin/tickets/:id/replies
func (h *TicketHandler) Reply(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	ticketID, ok := parseAdminTicketID(c)
	if !ok {
		return
	}
	var req AdminReplyTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	notifyEmail := true
	if req.NotifyEmail != nil {
		notifyEmail = *req.NotifyEmail
	}
	result, err := h.ticketService.AdminReply(
		c.Request.Context(),
		subject.UserID,
		ticketID,
		req.Content,
		notifyEmail,
		req.Status,
		req.AttachmentIDs,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AdminTicketReplyResponse{
		Ticket:     dto.AdminTicketBriefFromService(result.Ticket),
		Reply:      dto.TicketReplyFromService(result.Reply),
		EmailSent:  result.EmailSent,
		EmailError: result.EmailError,
	})
}

// Update handles changing ticket status/priority.
// PUT /api/v1/admin/tickets/:id
func (h *TicketHandler) Update(c *gin.Context) {
	ticketID, ok := parseAdminTicketID(c)
	if !ok {
		return
	}
	var req UpdateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	ticket, err := h.ticketService.AdminUpdateTicket(c.Request.Context(), ticketID, req.Status, req.Priority)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AdminTicketResponse{Ticket: dto.AdminTicketBriefFromService(ticket)})
}

// Stats handles ticket status counters.
// GET /api/v1/admin/tickets/stats
func (h *TicketHandler) Stats(c *gin.Context) {
	stats, err := h.ticketService.AdminStats(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.TicketStatsFromService(stats))
}

func parseAdminTicketID(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid ticket ID")
		return 0, false
	}
	return id, true
}
