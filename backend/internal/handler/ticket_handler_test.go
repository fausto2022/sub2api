package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTicketHandlerRequiresAuthentication(t *testing.T) {
	router := newTicketTestRouter(t, newTicketTestService(), 0)
	rec := performTicketRequest(router, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTicketHandlerCreateListAndGet(t *testing.T) {
	router := newTicketTestRouter(t, newTicketTestService(), 7)

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":  "无法调用 API",
		"category": "api",
		"priority": "high",
		"content":  "请求返回 500",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, 0, decodeTicketEnvelope(t, rec).Code)

	rec = performTicketRequest(router, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var listData struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	decodeTicketData(t, rec, &listData)
	require.Equal(t, int64(1), listData.Total)
	require.Len(t, listData.Items, 1)
	require.Equal(t, "open", listData.Items[0]["status"])
	require.Equal(t, "high", listData.Items[0]["priority"])
	require.Equal(t, float64(1), listData.Items[0]["reply_count"])

	id := int64(listData.Items[0]["id"].(float64))
	rec = performTicketRequest(router, http.MethodGet, "/api/v1/tickets/"+jsonID(id), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var detail struct {
		Ticket  map[string]any   `json:"ticket"`
		Replies []map[string]any `json:"replies"`
	}
	decodeTicketData(t, rec, &detail)
	require.Equal(t, id, int64(detail.Ticket["id"].(float64)))
	require.Len(t, detail.Replies, 1)
	require.Equal(t, "user", detail.Replies[0]["author_role"])
	require.Equal(t, "请求返回 500", detail.Replies[0]["content"])
}

func TestTicketHandlerCreateValidation(t *testing.T) {
	router := newTicketTestRouter(t, newTicketTestService(), 7)

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "subject",
		"content": "   ",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	envelope := decodeTicketEnvelope(t, rec)
	require.Equal(t, "TICKET_CONTENT_REQUIRED", envelope.Reason)

	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "",
		"content": "body",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "subject",
		"content": strings.Repeat("x", domain.TicketContentMaxLength+1),
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "TICKET_CONTENT_TOO_LONG", decodeTicketEnvelope(t, rec).Reason)
}

func TestTicketHandlerIsolatesOtherUsersTickets(t *testing.T) {
	svc := newTicketTestService()
	router := newTicketTestRouter(t, svc, 7)
	otherRouter := newTicketTestRouter(t, svc, 8)

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "mine",
		"content": "body",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created struct {
		Ticket map[string]any `json:"ticket"`
	}
	decodeTicketData(t, rec, &created)
	id := jsonID(int64(created.Ticket["id"].(float64)))

	rec = performTicketRequest(otherRouter, http.MethodGet, "/api/v1/tickets/"+id, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "TICKET_NOT_FOUND", decodeTicketEnvelope(t, rec).Reason)

	rec = performTicketRequest(otherRouter, http.MethodPost, "/api/v1/tickets/"+id+"/replies", map[string]any{"content": "hijack"})
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = performTicketRequest(otherRouter, http.MethodPost, "/api/v1/tickets/"+id+"/close", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTicketHandlerClosedTicketReplyReturns409(t *testing.T) {
	repo := newTicketTestRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusClosed)
	router := newTicketTestRouter(t, newTicketTestServiceWithRepo(repo), 7)

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets/"+jsonID(ticket.ID)+"/replies", map[string]any{"content": "again"})
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "TICKET_CLOSED", decodeTicketEnvelope(t, rec).Reason)
}

func TestTicketHandlerListRejectsInvalidStatus(t *testing.T) {
	router := newTicketTestRouter(t, newTicketTestService(), 7)
	rec := performTicketRequest(router, http.MethodGet, "/api/v1/tickets?status=bad", nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_TICKET_STATUS", decodeTicketEnvelope(t, rec).Reason)
}

func TestTicketHandlerCloseAndInvalidID(t *testing.T) {
	repo := newTicketTestRepo()
	ticket := repo.seedTicket(7, domain.TicketStatusOpen)
	router := newTicketTestRouter(t, newTicketTestServiceWithRepo(repo), 7)

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets/"+jsonID(ticket.ID)+"/close", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var closed struct {
		Ticket map[string]any `json:"ticket"`
	}
	decodeTicketData(t, rec, &closed)
	require.Equal(t, "closed", closed.Ticket["status"])

	rec = performTicketRequest(router, http.MethodGet, "/api/v1/tickets/not-a-number", nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func newTicketTestService() *service.TicketService {
	return newTicketTestServiceWithRepo(newTicketTestRepo())
}

func newTicketTestServiceWithRepo(repo *ticketTestRepo) *service.TicketService {
	return service.NewTicketService(
		repo,
		&ticketTestUserReader{users: map[int64]*service.User{
			7: {ID: 7, Email: "user@example.com", Username: "alice"},
		}},
		&ticketTestSettingRepo{},
		&ticketTestNotifier{},
	)
}

func newTicketTestRouter(t *testing.T, svc *service.TicketService, userID int64) *gin.Engine {
	t.Helper()
	return newTicketTestRouterWithHandler(t, NewTicketHandler(svc), userID)
}

// newTicketTestRouterWithHandler 用指定 handler 挂载用户侧全部工单路由，
// 便于限流测试注入可控时钟或复用同一个限流器实例。
func newTicketTestRouterWithHandler(t *testing.T, h *TicketHandler, userID int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	authenticated := router.Group("/api/v1", func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		}
		c.Next()
	})
	authenticated.GET("/tickets", h.List)
	authenticated.POST("/tickets", h.Create)
	authenticated.GET("/tickets/:id", h.Get)
	authenticated.POST("/tickets/:id/replies", h.AddReply)
	authenticated.POST("/tickets/:id/close", h.Close)
	return router
}

func performTicketRequest(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
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

type ticketEnvelope struct {
	Code   int             `json:"code"`
	Reason string          `json:"reason"`
	Data   json.RawMessage `json:"data"`
}

func decodeTicketEnvelope(t *testing.T, rec *httptest.ResponseRecorder) ticketEnvelope {
	t.Helper()
	var envelope ticketEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	return envelope
}

func decodeTicketData(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	envelope := decodeTicketEnvelope(t, rec)
	require.NoError(t, json.Unmarshal(envelope.Data, target))
}

func jsonID(id int64) string {
	payload, _ := json.Marshal(id)
	return string(payload)
}

// --- test doubles kept local so handler tests do not require a database ---

type ticketTestRepo struct {
	tickets     map[int64]*service.Ticket
	replies     map[int64][]service.TicketReply
	nextID      int64
	nextReplyID int64
}

func newTicketTestRepo() *ticketTestRepo {
	return &ticketTestRepo{tickets: map[int64]*service.Ticket{}, replies: map[int64][]service.TicketReply{}}
}

func (r *ticketTestRepo) seedTicket(userID int64, status string) *service.Ticket {
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
	return item
}

func (r *ticketTestRepo) CreateWithFirstReply(ctx context.Context, item *service.Ticket, reply *service.TicketReply) error {
	r.nextID++
	item.ID = r.nextID
	r.tickets[item.ID] = item
	r.nextReplyID++
	reply.ID = r.nextReplyID
	reply.TicketID = item.ID
	r.replies[item.ID] = append(r.replies[item.ID], *reply)
	return nil
}

func (r *ticketTestRepo) GetByID(ctx context.Context, id int64) (*service.Ticket, error) {
	item, ok := r.tickets[id]
	if !ok {
		return nil, service.ErrTicketNotFound
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *ticketTestRepo) GetByUser(ctx context.Context, id, userID int64) (*service.Ticket, error) {
	item, ok := r.tickets[id]
	if !ok || item.UserID != userID {
		return nil, service.ErrTicketNotFound
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *ticketTestRepo) ListByUser(
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

func (r *ticketTestRepo) ListAdmin(
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

func (r *ticketTestRepo) ListReplies(ctx context.Context, ticketID int64) ([]service.TicketReply, error) {
	return append([]service.TicketReply(nil), r.replies[ticketID]...), nil
}

func (r *ticketTestRepo) AddReply(ctx context.Context, reply *service.TicketReply, status string) (*service.Ticket, error) {
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
	item.LastReplyAt = &reply.CreatedAt
	item.LastReplyBy = reply.AuthorRole
	copyItem := *item
	return &copyItem, nil
}

func (r *ticketTestRepo) UpdateTicket(ctx context.Context, ticketID int64, status, priority *string) (*service.Ticket, error) {
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

func (r *ticketTestRepo) MarkReplyEmailNotified(ctx context.Context, replyID int64) error {
	return nil
}

func (r *ticketTestRepo) Stats(ctx context.Context) (*service.TicketStats, error) {
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

type ticketTestUserReader struct {
	users map[int64]*service.User
}

func (r *ticketTestUserReader) GetByID(ctx context.Context, id int64) (*service.User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

type ticketTestSettingRepo struct{}

func (r *ticketTestSettingRepo) Get(ctx context.Context, key string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}

func (r *ticketTestSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	return "", service.ErrSettingNotFound
}

func (r *ticketTestSettingRepo) Set(ctx context.Context, key, value string) error { return nil }

func (r *ticketTestSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *ticketTestSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	return nil
}

func (r *ticketTestSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *ticketTestSettingRepo) Delete(ctx context.Context, key string) error { return nil }

type ticketTestNotifier struct {
	calls []service.NotificationEmailSendInput
}

func (n *ticketTestNotifier) Send(ctx context.Context, input service.NotificationEmailSendInput) error {
	n.calls = append(n.calls, input)
	return nil
}

// --- 用户侧工单写操作限流（design.md §1.3：1 分钟 5 次轻量计数） ---

// ticketTestClock 是限流测试用的可推进时钟，避免真正等待 60 秒窗口。
type ticketTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTicketTestClock() *ticketTestClock {
	return &ticketTestClock{now: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
}

func (c *ticketTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ticketTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestTicketRateLimitAllowsFiveThenBlocksSixth 覆盖窗口内 5 次放行、第 6 次拒绝，
// 且被拒绝的请求不写入计数：窗口滑过后恢复完整额度。
func TestTicketRateLimitAllowsFiveThenBlocksSixth(t *testing.T) {
	clock := newTicketTestClock()
	limiter := newTicketWriteRateLimiter(ticketWriteRateLimit, ticketWriteRateWindow)
	limiter.now = clock.Now

	for i := 1; i <= ticketWriteRateLimit; i++ {
		require.True(t, limiter.Allow(7), "第 %d 次写操作应放行", i)
	}
	require.False(t, limiter.Allow(7), "第 6 次写操作应被限流")
	require.Greater(t, limiter.RetryAfter(7), time.Duration(0), "被限流时应给出重试等待时间")

	clock.Advance(30 * time.Second)
	require.False(t, limiter.Allow(7), "窗口尚未结束时仍应限流，被拒请求不得顺延窗口")

	clock.Advance(31 * time.Second)
	require.True(t, limiter.Allow(7), "窗口滑过后应恢复")
	for i := 1; i < ticketWriteRateLimit; i++ {
		require.True(t, limiter.Allow(7), "新窗口内仍可继续写（第 %d 次）", i+1)
	}
	require.False(t, limiter.Allow(7), "新窗口第 6 次再次限流")
}

// TestTicketRateLimitIsolatesUsersAndPrunesExpired 覆盖按 user_id 独立计数，
// 以及过期用户键在后续 Allow 中的惰性清理（map 不会无限增长）。
func TestTicketRateLimitIsolatesUsersAndPrunesExpired(t *testing.T) {
	clock := newTicketTestClock()
	limiter := newTicketWriteRateLimiter(ticketWriteRateLimit, ticketWriteRateWindow)
	limiter.now = clock.Now

	for i := 0; i < ticketWriteRateLimit; i++ {
		require.True(t, limiter.Allow(7))
	}
	require.False(t, limiter.Allow(7))

	for i := 1; i <= ticketWriteRateLimit; i++ {
		require.True(t, limiter.Allow(8), "用户 8 不应受用户 7 影响（%d/%d）", i, ticketWriteRateLimit)
	}
	require.False(t, limiter.Allow(8))
	require.Len(t, limiter.hits, 2, "每个用户各自一条计数记录")

	clock.Advance(ticketWriteRateWindow + time.Second)
	require.True(t, limiter.Allow(9), "新窗口内新用户放行")
	require.Len(t, limiter.hits, 1, "过期的用户计数键应被惰性清理")
}

// TestTicketRateLimitHandlerReturns429 覆盖 HTTP 层：创建工单与追加回复共享
// 60 秒 5 次额度，第 6 次返回 429 RATE_LIMITED + Retry-After；读接口不受影响；
// 窗口滑过后恢复。
func TestTicketRateLimitHandlerReturns429(t *testing.T) {
	clock := newTicketTestClock()
	h := NewTicketHandler(newTicketTestService())
	h.writeLimiter = newTicketWriteRateLimiter(ticketWriteRateLimit, ticketWriteRateWindow)
	h.writeLimiter.now = clock.Now
	router := newTicketTestRouterWithHandler(t, h, 7)

	var ticketID int64
	for i := 1; i <= 4; i++ {
		rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "subject",
			"content": "body",
		})
		require.Equal(t, http.StatusCreated, rec.Code, "第 %d 次创建应放行", i)
		var created struct {
			Ticket map[string]any `json:"ticket"`
		}
		decodeTicketData(t, rec, &created)
		ticketID = int64(created.Ticket["id"].(float64))
	}

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets/"+jsonID(ticketID)+"/replies", map[string]any{
		"content": "fifth write",
	})
	require.Equal(t, http.StatusOK, rec.Code, "第 5 次写（追加回复）应放行")

	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "subject",
		"content": "body",
	})
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "第 6 次写应返回 429")
	envelope := decodeTicketEnvelope(t, rec)
	require.Equal(t, http.StatusTooManyRequests, envelope.Code)
	require.Equal(t, "RATE_LIMITED", envelope.Reason)
	require.NotEmpty(t, rec.Header().Get("Retry-After"), "429 响应应带 Retry-After")

	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets/"+jsonID(ticketID)+"/replies", map[string]any{
		"content": "sixth write",
	})
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "创建与追加回复共用同一份额度")
	require.Equal(t, "RATE_LIMITED", decodeTicketEnvelope(t, rec).Reason)

	rec = performTicketRequest(router, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, rec.Code, "列表读接口不受限流影响")
	rec = performTicketRequest(router, http.MethodGet, "/api/v1/tickets/"+jsonID(ticketID), nil)
	require.Equal(t, http.StatusOK, rec.Code, "详情读接口不受限流影响")

	clock.Advance(ticketWriteRateWindow + time.Second)
	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "after window",
		"content": "body",
	})
	require.Equal(t, http.StatusCreated, rec.Code, "窗口滑过后应恢复写入")
}

// TestTicketRateLimitConcurrentAccessIsSafe 并发写同一/不同用户：每个用户严格
// 只放行额度内的次数，供 `go test -race` 验证互斥正确。
func TestTicketRateLimitConcurrentAccessIsSafe(t *testing.T) {
	limiter := newTicketWriteRateLimiter(ticketWriteRateLimit, ticketWriteRateWindow)

	var allowed7, allowed8 atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		userID := int64(7)
		counter := &allowed7
		if i%2 == 1 {
			userID = 8
			counter = &allowed8
		}
		wg.Add(1)
		go func(userID int64, counter *atomic.Int64) {
			defer wg.Done()
			if limiter.Allow(userID) {
				counter.Add(1)
			}
		}(userID, counter)
	}
	wg.Wait()

	require.Equal(t, int64(ticketWriteRateLimit), allowed7.Load(), "并发下用户 7 只应放行 5 次")
	require.Equal(t, int64(ticketWriteRateLimit), allowed8.Load(), "并发下用户 8 只应放行 5 次")
}
