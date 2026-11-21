package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminTicketHandlerReplyDefaultsToNotifyEmail(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	notifier := &adminTicketFakeNotifier{}
	router := newAdminTicketTestRouter(t, newAdminTicketTestServiceWith(repo, notifier), 99)

	rec := performAdminTicketRequest(router, http.MethodPost, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
		"content": "已修复，请重试",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var data struct {
		Ticket     map[string]any `json:"ticket"`
		Reply      map[string]any `json:"reply"`
		EmailSent  bool           `json:"email_sent"`
		EmailError string         `json:"email_error"`
	}
	decodeAdminTicketData(t, rec, &data)
	require.True(t, data.EmailSent)
	require.Empty(t, data.EmailError)
	require.Equal(t, "pending", data.Ticket["status"])
	require.Equal(t, "已修复，请重试", data.Reply["content"])
	require.Len(t, notifier.calls, 1)
	require.Equal(t, service.NotificationEmailEventTicketReply, notifier.calls[0].Event)
	require.Equal(t, "user@example.com", notifier.calls[0].RecipientEmail)

	notifier.calls = nil
	rec = performAdminTicketRequest(router, http.MethodPost, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
		"content":      "internal",
		"notify_email": false,
	})
	require.Equal(t, http.StatusOK, rec.Code)
	decodeAdminTicketData(t, rec, &data)
	require.False(t, data.EmailSent)
	require.Empty(t, notifier.calls)
}

func TestAdminTicketHandlerReplyEmailFailureDoesNotDropReply(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	notifier := &adminTicketFakeNotifier{err: errors.New("smtp down")}
	router := newAdminTicketTestRouter(t, newAdminTicketTestServiceWith(repo, notifier), 99)

	rec := performAdminTicketRequest(router, http.MethodPost, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
		"content": "reply still persisted",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var data struct {
		EmailSent  bool   `json:"email_sent"`
		EmailError string `json:"email_error"`
	}
	decodeAdminTicketData(t, rec, &data)
	require.False(t, data.EmailSent)
	require.Contains(t, data.EmailError, "smtp down")
	require.Len(t, repo.replies[ticket.ID], 2, "reply must persist when email fails")
}

func TestAdminTicketHandlerReplyValidation(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	openTicket := repo.seedTicket(7, domain.TicketStatusOpen)
	closedTicket := repo.seedTicket(7, domain.TicketStatusClosed)
	router := newAdminTicketTestRouter(t, newAdminTicketTestServiceWith(repo, &adminTicketFakeNotifier{}), 99)

	rec := performAdminTicketRequest(router, http.MethodPost, "/api/v1/admin/tickets/"+adminTicketJSONID(openTicket.ID)+"/replies", map[string]any{
		"content": "x",
		"status":  "bad",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_TICKET_STATUS", decodeAdminTicketEnvelope(t, rec).Reason)

	rec = performAdminTicketRequest(router, http.MethodPost, "/api/v1/admin/tickets/"+adminTicketJSONID(closedTicket.ID)+"/replies", map[string]any{
		"content": "x",
	})
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "TICKET_CLOSED", decodeAdminTicketEnvelope(t, rec).Reason)
}

func TestAdminTicketHandlerGetIncludesUser(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	router := newAdminTicketTestRouter(t, newAdminTicketTestServiceWith(repo, &adminTicketFakeNotifier{}), 99)

	rec := performAdminTicketRequest(router, http.MethodGet, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var data struct {
		Ticket  map[string]any   `json:"ticket"`
		Replies []map[string]any `json:"replies"`
		User    map[string]any   `json:"user"`
	}
	decodeAdminTicketData(t, rec, &data)
	require.Equal(t, ticket.ID, int64(data.Ticket["id"].(float64)))
	require.Equal(t, "user@example.com", data.User["email"])
	require.Len(t, data.Replies, 1)

	rec = performAdminTicketRequest(router, http.MethodGet, "/api/v1/admin/tickets/404", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "TICKET_NOT_FOUND", decodeAdminTicketEnvelope(t, rec).Reason)
}

func TestAdminTicketHandlerUpdateValidation(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	router := newAdminTicketTestRouter(t, newAdminTicketTestServiceWith(repo, &adminTicketFakeNotifier{}), 99)

	rec := performAdminTicketRequest(router, http.MethodPut, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID), map[string]any{
		"status": "bad",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_TICKET_STATUS", decodeAdminTicketEnvelope(t, rec).Reason)

	rec = performAdminTicketRequest(router, http.MethodPut, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID), map[string]any{
		"priority": "critical",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_TICKET_PRIORITY", decodeAdminTicketEnvelope(t, rec).Reason)

	rec = performAdminTicketRequest(router, http.MethodPut, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID), map[string]any{
		"status":   "resolved",
		"priority": "high",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var data struct {
		Ticket map[string]any `json:"ticket"`
	}
	decodeAdminTicketData(t, rec, &data)
	require.Equal(t, "resolved", data.Ticket["status"])
	require.Equal(t, "high", data.Ticket["priority"])
}

func TestAdminTicketHandlerListAndStats(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	repo.seedTicket(7, domain.TicketStatusOpen)
	repo.seedTicket(8, domain.TicketStatusResolved)
	router := newAdminTicketTestRouter(t, newAdminTicketTestServiceWith(repo, &adminTicketFakeNotifier{}), 99)

	rec := performAdminTicketRequest(router, http.MethodGet, "/api/v1/admin/tickets?status=open&page=1&page_size=20", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	decodeAdminTicketData(t, rec, &list)
	require.Equal(t, int64(1), list.Total)
	require.Len(t, list.Items, 1)
	require.Equal(t, "user@example.com", list.Items[0]["user_email"])

	rec = performAdminTicketRequest(router, http.MethodGet, "/api/v1/admin/tickets?status=bad", nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = performAdminTicketRequest(router, http.MethodGet, "/api/v1/admin/tickets/stats", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var stats struct {
		Open     int64 `json:"open"`
		Resolved int64 `json:"resolved"`
		Total    int64 `json:"total"`
	}
	decodeAdminTicketData(t, rec, &stats)
	require.Equal(t, int64(1), stats.Open)
	require.Equal(t, int64(1), stats.Resolved)
	require.Equal(t, int64(2), stats.Total)
}

func newAdminTicketTestService() *service.TicketService {
	return newAdminTicketTestServiceWith(newAdminTicketFakeRepo(), &adminTicketFakeNotifier{})
}

func newAdminTicketTestServiceWith(repo *adminTicketFakeRepo, notifier *adminTicketFakeNotifier) *service.TicketService {
	return service.NewTicketService(
		repo,
		&adminTicketFakeUserReader{users: map[int64]*service.User{
			7: {ID: 7, Email: "user@example.com", Username: "alice"},
		}},
		&adminTicketFakeSettingRepo{},
		notifier,
	)
}

func newAdminTicketTestRouter(t *testing.T, svc *service.TicketService, userID int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewTicketHandler(svc)
	group := router.Group("/api/v1/admin", func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		}
		c.Next()
	})
	group.GET("/tickets", h.List)
	group.GET("/tickets/stats", h.Stats)
	group.GET("/tickets/:id", h.Get)
	group.POST("/tickets/:id/replies", h.Reply)
	group.PUT("/tickets/:id", h.Update)
	return router
}

func performAdminTicketRequest(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, _ := json.Marshal(body)
		reader = bytes.NewReader(payload)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type adminTicketEnvelope struct {
	Code   int             `json:"code"`
	Reason string          `json:"reason"`
	Data   json.RawMessage `json:"data"`
}

func decodeAdminTicketEnvelope(t *testing.T, rec *httptest.ResponseRecorder) adminTicketEnvelope {
	t.Helper()
	var envelope adminTicketEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	return envelope
}

func decodeAdminTicketData(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	envelope := decodeAdminTicketEnvelope(t, rec)
	require.NoError(t, json.Unmarshal(envelope.Data, target))
}

func adminTicketJSONID(id int64) string {
	payload, _ := json.Marshal(id)
	return string(payload)
}

// --- admin-local test doubles ---

type adminTicketFakeRepo struct {
	tickets     map[int64]*service.Ticket
	replies     map[int64][]service.TicketReply
	nextID      int64
	nextReplyID int64
}

func newAdminTicketFakeRepo() *adminTicketFakeRepo {
	return &adminTicketFakeRepo{tickets: map[int64]*service.Ticket{}, replies: map[int64][]service.TicketReply{}}
}

func (r *adminTicketFakeRepo) seedTicket(userID int64, status string) *service.Ticket {
	r.nextID++
	now := time.Now().UTC()
	item := &service.Ticket{
		ID:          r.nextID,
		UserID:      userID,
		Subject:     "subject",
		Category:    domain.TicketCategoryGeneral,
		Priority:    domain.TicketPriorityNormal,
		Status:      status,
		ReplyCount:  1,
		LastReplyAt: &now,
		LastReplyBy: domain.TicketAuthorRoleUser,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	r.tickets[item.ID] = item
	r.nextReplyID++
	r.replies[item.ID] = append(r.replies[item.ID], service.TicketReply{
		ID:         r.nextReplyID,
		TicketID:   item.ID,
		AuthorID:   userID,
		AuthorRole: domain.TicketAuthorRoleUser,
		Content:    "first",
		CreatedAt:  now,
	})
	return item
}

func (r *adminTicketFakeRepo) CreateWithFirstReply(ctx context.Context, item *service.Ticket, reply *service.TicketReply) error {
	r.nextID++
	item.ID = r.nextID
	r.tickets[item.ID] = item
	r.nextReplyID++
	reply.ID = r.nextReplyID
	reply.TicketID = item.ID
	r.replies[item.ID] = append(r.replies[item.ID], *reply)
	return nil
}

func (r *adminTicketFakeRepo) GetByID(ctx context.Context, id int64) (*service.Ticket, error) {
	item, ok := r.tickets[id]
	if !ok {
		return nil, service.ErrTicketNotFound
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *adminTicketFakeRepo) GetByUser(ctx context.Context, id, userID int64) (*service.Ticket, error) {
	item, ok := r.tickets[id]
	if !ok || item.UserID != userID {
		return nil, service.ErrTicketNotFound
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *adminTicketFakeRepo) ListByUser(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
	status string,
) ([]service.Ticket, *pagination.PaginationResult, error) {
	out := make([]service.Ticket, 0)
	for _, item := range r.tickets {
		if item.UserID != userID || (status != "" && item.Status != status) {
			continue
		}
		out = append(out, *item)
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: params.Page, PageSize: params.Limit()}, nil
}

func (r *adminTicketFakeRepo) ListAdmin(
	ctx context.Context,
	params pagination.PaginationParams,
	filters service.AdminTicketFilters,
) ([]service.Ticket, *pagination.PaginationResult, error) {
	out := make([]service.Ticket, 0)
	for _, item := range r.tickets {
		if filters.Status != "" && item.Status != filters.Status {
			continue
		}
		if filters.Priority != "" && item.Priority != filters.Priority {
			continue
		}
		copyItem := *item
		copyItem.UserEmail = "user@example.com"
		out = append(out, copyItem)
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: params.Page, PageSize: params.Limit()}, nil
}

func (r *adminTicketFakeRepo) ListReplies(ctx context.Context, ticketID int64) ([]service.TicketReply, error) {
	return append([]service.TicketReply(nil), r.replies[ticketID]...), nil
}

func (r *adminTicketFakeRepo) AddReply(ctx context.Context, reply *service.TicketReply, status string) (*service.Ticket, error) {
	item, ok := r.tickets[reply.TicketID]
	if !ok {
		return nil, service.ErrTicketNotFound
	}
	if item.Status == domain.TicketStatusClosed {
		return nil, service.ErrTicketClosed
	}
	r.nextReplyID++
	reply.ID = r.nextReplyID
	r.replies[item.ID] = append(r.replies[item.ID], *reply)
	item.Status = status
	item.ReplyCount++
	copyItem := *item
	return &copyItem, nil
}

func (r *adminTicketFakeRepo) UpdateTicket(ctx context.Context, ticketID int64, status, priority *string) (*service.Ticket, error) {
	item, ok := r.tickets[ticketID]
	if !ok {
		return nil, service.ErrTicketNotFound
	}
	if status != nil {
		item.Status = *status
	}
	if priority != nil {
		item.Priority = *priority
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *adminTicketFakeRepo) MarkReplyEmailNotified(ctx context.Context, replyID int64) error {
	return nil
}

func (r *adminTicketFakeRepo) Stats(ctx context.Context) (*service.TicketStats, error) {
	stats := &service.TicketStats{}
	for _, item := range r.tickets {
		switch item.Status {
		case domain.TicketStatusOpen:
			stats.Open++
		case domain.TicketStatusPending:
			stats.Pending++
		case domain.TicketStatusResolved:
			stats.Resolved++
		case domain.TicketStatusClosed:
			stats.Closed++
		}
		stats.Total++
	}
	return stats, nil
}

type adminTicketFakeUserReader struct {
	users map[int64]*service.User
}

func (r *adminTicketFakeUserReader) GetByID(ctx context.Context, id int64) (*service.User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

type adminTicketFakeSettingRepo struct{}

func (r *adminTicketFakeSettingRepo) Get(ctx context.Context, key string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}

func (r *adminTicketFakeSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	return "", service.ErrSettingNotFound
}

func (r *adminTicketFakeSettingRepo) Set(ctx context.Context, key, value string) error { return nil }

func (r *adminTicketFakeSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *adminTicketFakeSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	return nil
}

func (r *adminTicketFakeSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *adminTicketFakeSettingRepo) Delete(ctx context.Context, key string) error { return nil }

type adminTicketFakeNotifier struct {
	calls []service.NotificationEmailSendInput
	err   error
}

func (n *adminTicketFakeNotifier) Send(ctx context.Context, input service.NotificationEmailSendInput) error {
	n.calls = append(n.calls, input)
	return n.err
}

// --- t6：管理员回帖附图（复用 t1 的两阶段绑定与安全校验） ---

// newAdminTicketTestServiceWithAttachments 装配带附件能力的管理端工单服务：
// 真实的 TicketAttachmentService + 本地存储（临时目录）+ 内存附件仓储。
func newAdminTicketTestServiceWithAttachments(
	t *testing.T,
	repo *adminTicketFakeRepo,
	notifier *adminTicketFakeNotifier,
	attachmentRepo *adminTicketFakeAttachmentRepo,
) *service.TicketService {
	t.Helper()
	storage := service.NewLocalTicketAttachmentStorage(t.TempDir())
	attachmentService := service.NewTicketAttachmentService(attachmentRepo, storage, repo)
	ticketService := newAdminTicketTestServiceWith(repo, notifier)
	ticketService.SetAttachmentBinder(attachmentService)
	return ticketService
}

func adminTicketSeedAttachment(
	repo *adminTicketFakeAttachmentRepo,
	uploaderID int64,
	name string,
	byteSize int64,
	width, height int,
) *service.TicketAttachment {
	return repo.seed(service.TicketAttachment{
		UploaderID:   uploaderID,
		StorageKey:   fmt.Sprintf("2026/11/%s", name),
		StorageKind:  service.TicketAttachmentStorageKindLocal,
		OriginalName: name,
		ContentType:  service.TicketAttachmentContentTypePNG,
		ByteSize:     byteSize,
		Width:        width,
		Height:       height,
		SHA256:       "sha-" + name,
		CreatedAt:    time.Now().UTC(),
	})
}

// TestAdminTicketHandlerReplyWithAttachments 覆盖管理员带 2 张附件回帖：
// 响应回带 attachments、记录回填 reply_id、管理端详情 replies[].attachments[] 一致。
func TestAdminTicketHandlerReplyWithAttachments(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	attachmentRepo := newAdminTicketFakeAttachmentRepo()
	router := newAdminTicketTestRouter(t,
		newAdminTicketTestServiceWithAttachments(t, repo, &adminTicketFakeNotifier{}, attachmentRepo), 99)

	first := adminTicketSeedAttachment(attachmentRepo, 99, "a.png", 128, 4, 3)
	second := adminTicketSeedAttachment(attachmentRepo, 99, "b.png", 256, 5, 3)

	rec := performAdminTicketRequest(router, http.MethodPost,
		"/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
			"content":        "已修复，见附件截图",
			"notify_email":   false,
			"attachment_ids": []int64{first.ID, second.ID},
		})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var data struct {
		Reply struct {
			ID          int64            `json:"id"`
			Attachments []map[string]any `json:"attachments"`
		} `json:"reply"`
	}
	decodeAdminTicketData(t, rec, &data)
	require.Len(t, data.Reply.Attachments, 2, "管理端回复响应必须回带已绑定附件")
	require.Equal(t, float64(first.ID), data.Reply.Attachments[0]["id"])
	require.Equal(t, fmt.Sprintf("/api/v1/tickets/attachments/%d", first.ID), data.Reply.Attachments[0]["url"])
	require.Equal(t, "a.png", data.Reply.Attachments[0]["original_name"])
	require.Equal(t, service.TicketAttachmentContentTypePNG, data.Reply.Attachments[0]["content_type"])
	require.Equal(t, float64(128), data.Reply.Attachments[0]["byte_size"])
	require.Equal(t, float64(4), data.Reply.Attachments[0]["width"])
	require.Equal(t, float64(3), data.Reply.Attachments[0]["height"])

	// reply_id / ticket_id 已回填
	stored := attachmentRepo.get(first.ID)
	require.NotNil(t, stored)
	require.NotNil(t, stored.ReplyID)
	require.Equal(t, data.Reply.ID, *stored.ReplyID)
	require.NotNil(t, stored.TicketID)
	require.Equal(t, ticket.ID, *stored.TicketID)

	// 管理端详情 replies[].attachments[] 与用户端 DTO 一致
	rec = performAdminTicketRequest(router, http.MethodGet, "/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var detail struct {
		Replies []struct {
			ID          int64            `json:"id"`
			Attachments []map[string]any `json:"attachments"`
		} `json:"replies"`
	}
	decodeAdminTicketData(t, rec, &detail)
	require.Len(t, detail.Replies, 2)
	require.Equal(t, data.Reply.ID, detail.Replies[1].ID)
	require.Len(t, detail.Replies[1].Attachments, 2)
	require.Equal(t, float64(second.ID), detail.Replies[1].Attachments[1]["id"])

	// 无附件的既有回复仍输出空数组（DTO 与用户端一致）
	require.Contains(t, rec.Body.String(), `"attachments":[]`)
}

// TestAdminTicketHandlerReplyRejectsTooManyAttachments 覆盖 A1 的管理端一半：
// 4 张附件 → 400，且不落库回复、不消耗附件。
func TestAdminTicketHandlerReplyRejectsTooManyAttachments(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	attachmentRepo := newAdminTicketFakeAttachmentRepo()
	router := newAdminTicketTestRouter(t,
		newAdminTicketTestServiceWithAttachments(t, repo, &adminTicketFakeNotifier{}, attachmentRepo), 99)

	ids := make([]int64, 0, 4)
	for i := 0; i < 4; i++ {
		ids = append(ids, adminTicketSeedAttachment(attachmentRepo, 99, fmt.Sprintf("shot-%d.png", i), 64, 2+i, 2).ID)
	}

	rec := performAdminTicketRequest(router, http.MethodPost,
		"/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
			"content":        "四张图",
			"attachment_ids": ids,
		})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_LIMIT_EXCEEDED", decodeAdminTicketEnvelope(t, rec).Reason)
	require.Len(t, repo.replies[ticket.ID], 1, "附件超限时不得落库回复")
	require.Nil(t, attachmentRepo.get(ids[0]).ReplyID, "附件不得被部分绑定")

	// 3 张通过（边界）
	rec = performAdminTicketRequest(router, http.MethodPost,
		"/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
			"content":        "三张图",
			"notify_email":   false,
			"attachment_ids": ids[:3],
		})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var ok struct {
		Reply struct {
			Attachments []map[string]any `json:"attachments"`
		} `json:"reply"`
	}
	decodeAdminTicketData(t, rec, &ok)
	require.Len(t, ok.Reply.Attachments, 3)
}

// TestAdminTicketHandlerReplyRejectsForeignAttachment 覆盖越权绑定（A6 管理端一半）：
// 管理员不能用「用户或其他管理员」上传的附件，按不存在处理（404）。
func TestAdminTicketHandlerReplyRejectsForeignAttachment(t *testing.T) {
	repo := newAdminTicketFakeRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	attachmentRepo := newAdminTicketFakeAttachmentRepo()
	router := newAdminTicketTestRouter(t,
		newAdminTicketTestServiceWithAttachments(t, repo, &adminTicketFakeNotifier{}, attachmentRepo), 99)

	foreign := adminTicketSeedAttachment(attachmentRepo, 7, "user-shot.png", 64, 3, 3)

	rec := performAdminTicketRequest(router, http.MethodPost,
		"/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
			"content":        "借用用户附件",
			"attachment_ids": []int64{foreign.ID},
		})
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_NOT_FOUND", decodeAdminTicketEnvelope(t, rec).Reason)
	require.Len(t, repo.replies[ticket.ID], 1, "越权绑定失败时不得落库回复")
	require.Nil(t, attachmentRepo.get(foreign.ID).ReplyID)

	// 已绑定附件重复绑定 → 409
	mine := adminTicketSeedAttachment(attachmentRepo, 99, "mine.png", 64, 3, 3)
	rec = performAdminTicketRequest(router, http.MethodPost,
		"/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
			"content":        "第一次",
			"notify_email":   false,
			"attachment_ids": []int64{mine.ID},
		})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = performAdminTicketRequest(router, http.MethodPost,
		"/api/v1/admin/tickets/"+adminTicketJSONID(ticket.ID)+"/replies", map[string]any{
			"content":        "重复绑定",
			"attachment_ids": []int64{mine.ID},
		})
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_ALREADY_BOUND", decodeAdminTicketEnvelope(t, rec).Reason)
}

// adminTicketFakeAttachmentRepo 是管理端 handler 测试用的内存附件仓储。
type adminTicketFakeAttachmentRepo struct {
	items map[int64]*service.TicketAttachment
	next  int64
}

func newAdminTicketFakeAttachmentRepo() *adminTicketFakeAttachmentRepo {
	return &adminTicketFakeAttachmentRepo{items: make(map[int64]*service.TicketAttachment)}
}

func (r *adminTicketFakeAttachmentRepo) clone(item service.TicketAttachment) service.TicketAttachment {
	copied := item
	if item.TicketID != nil {
		value := *item.TicketID
		copied.TicketID = &value
	}
	if item.ReplyID != nil {
		value := *item.ReplyID
		copied.ReplyID = &value
	}
	return copied
}

func (r *adminTicketFakeAttachmentRepo) seed(item service.TicketAttachment) *service.TicketAttachment {
	r.next++
	item.ID = r.next
	stored := r.clone(item)
	r.items[item.ID] = &stored
	copied := stored
	return &copied
}

func (r *adminTicketFakeAttachmentRepo) get(id int64) *service.TicketAttachment {
	item, ok := r.items[id]
	if !ok {
		return nil
	}
	copied := r.clone(*item)
	return &copied
}

func (r *adminTicketFakeAttachmentRepo) Insert(_ context.Context, item *service.TicketAttachment) error {
	r.next++
	item.ID = r.next
	stored := r.clone(*item)
	r.items[item.ID] = &stored
	return nil
}

func (r *adminTicketFakeAttachmentRepo) GetByID(_ context.Context, id int64) (*service.TicketAttachment, error) {
	item, ok := r.items[id]
	if !ok {
		return nil, service.ErrTicketAttachmentNotFound
	}
	copied := r.clone(*item)
	return &copied, nil
}

func (r *adminTicketFakeAttachmentRepo) FindReusable(
	_ context.Context,
	uploaderID int64,
	sha256Hex string,
	byteSize int64,
) (*service.TicketAttachment, error) {
	for _, item := range r.items {
		if item.UploaderID == uploaderID && item.SHA256 == sha256Hex &&
			item.ByteSize == byteSize && item.ReplyID == nil {
			copied := r.clone(*item)
			return &copied, nil
		}
	}
	return nil, nil
}

func (r *adminTicketFakeAttachmentRepo) ListByTicket(_ context.Context, ticketID int64) ([]service.TicketAttachment, error) {
	out := make([]service.TicketAttachment, 0)
	for id := int64(1); id <= r.next; id++ {
		item, ok := r.items[id]
		if !ok || item.TicketID == nil || *item.TicketID != ticketID {
			continue
		}
		out = append(out, r.clone(*item))
	}
	return out, nil
}

func (r *adminTicketFakeAttachmentRepo) BindReply(
	_ context.Context,
	ticketID, replyID, uploaderID int64,
	ids []int64,
) error {
	for _, id := range ids {
		item, ok := r.items[id]
		if !ok || item.UploaderID != uploaderID || item.ReplyID != nil {
			return service.ErrTicketAttachmentAlreadyBound
		}
	}
	for _, id := range ids {
		item := r.items[id]
		ticket, reply := ticketID, replyID
		item.TicketID = &ticket
		item.ReplyID = &reply
	}
	return nil
}

func (r *adminTicketFakeAttachmentRepo) DeleteUnbound(_ context.Context, id int64) (bool, error) {
	item, ok := r.items[id]
	if !ok || item.ReplyID != nil {
		return false, nil
	}
	delete(r.items, id)
	return true, nil
}

func (r *adminTicketFakeAttachmentRepo) CountByUploaderSince(
	_ context.Context,
	uploaderID int64,
	since time.Time,
) (int64, error) {
	var count int64
	for _, item := range r.items {
		if item.UploaderID == uploaderID && !item.CreatedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

func (r *adminTicketFakeAttachmentRepo) CountBoundByTicket(_ context.Context, ticketID int64) (int64, error) {
	var count int64
	for _, item := range r.items {
		if item.TicketID != nil && *item.TicketID == ticketID && item.ReplyID != nil {
			count++
		}
	}
	return count, nil
}

func (r *adminTicketFakeAttachmentRepo) ListOrphansBefore(
	_ context.Context,
	cutoff time.Time,
	limit int,
) ([]service.TicketAttachment, error) {
	out := make([]service.TicketAttachment, 0)
	for id := int64(1); id <= r.next; id++ {
		item, ok := r.items[id]
		if !ok || item.ReplyID != nil || !item.CreatedAt.Before(cutoff) {
			continue
		}
		out = append(out, r.clone(*item))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *adminTicketFakeAttachmentRepo) DeleteOrphanBefore(
	_ context.Context,
	id int64,
	cutoff time.Time,
) (bool, error) {
	item, ok := r.items[id]
	if !ok || item.ReplyID != nil || !item.CreatedAt.Before(cutoff) {
		return false, nil
	}
	delete(r.items, id)
	return true, nil
}

var _ service.TicketAttachmentRepository = (*adminTicketFakeAttachmentRepo)(nil)
