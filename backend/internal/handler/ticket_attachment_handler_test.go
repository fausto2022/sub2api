package handler

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// --- 工单附件 HTTP 契约测试（design.md 第四部分 A1–A6 + 详情 DTO 聚合） ---

// TestTicketAttachmentHandlerUploadAndReadPermission 覆盖 A5：提交人/上传者与管理员可读，
// 其他用户 404 且不暴露存在性。
func TestTicketAttachmentHandlerUploadAndReadPermission(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	data := attachmentHandlerTestPNG(t, 6, 4)

	rec := performAttachmentUpload(t, fixture.router(7, ""), "/api/v1/tickets/attachments", "file", "shot.png", data)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	attachment := decodeAttachmentEnvelope(t, rec)
	require.NotZero(t, attachment.ID)
	require.Equal(t, int64(len(data)), attachment.ByteSize)
	require.Equal(t, 6, attachment.Width)
	require.Equal(t, 4, attachment.Height)
	require.Equal(t, "image/png", attachment.ContentType)
	require.Equal(t, "shot.png", attachment.OriginalName)
	require.Equal(t, fmt.Sprintf("/api/v1/tickets/attachments/%d", attachment.ID), attachment.URL)

	// 上传者本人可读，返回原始字节 + 私密缓存头
	rec = performTicketRequest(fixture.router(7, ""), http.MethodGet, attachment.URL, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, data, rec.Body.Bytes())
	require.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	require.Equal(t, "private, max-age=3600", rec.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))

	// 其他用户 404，不暴露存在性
	rec = performTicketRequest(fixture.router(8, ""), http.MethodGet, attachment.URL, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_NOT_FOUND", decodeTicketEnvelope(t, rec).Reason)
	require.NotEqual(t, data, rec.Body.Bytes())

	// 管理员（用户侧路由带 admin 角色）可读
	rec = performTicketRequest(fixture.router(99, service.RoleAdmin), http.MethodGet, attachment.URL, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, data, rec.Body.Bytes())

	// 未登录 401
	rec = performTicketRequest(fixture.router(0, ""), http.MethodGet, attachment.URL, nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestTicketAttachmentHandlerUploadRejectsSVGAndFakeExtension 覆盖 A3 + A4：
// 文本改名 .png、SVG 均被魔数嗅探拒绝。
func TestTicketAttachmentHandlerUploadRejectsSVGAndFakeExtension(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	router := fixture.router(7, "")

	rec := performAttachmentUpload(t, router, "/api/v1/tickets/attachments", "file", "evil.png",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_TYPE_NOT_ALLOWED", decodeTicketEnvelope(t, rec).Reason)

	rec = performAttachmentUpload(t, router, "/api/v1/tickets/attachments", "file", "evil.png",
		[]byte("plain text pretending to be a png"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_TYPE_NOT_ALLOWED", decodeTicketEnvelope(t, rec).Reason)

	// 缺少文件字段
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tickets/attachments", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=none")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 未登录
	rec = performAttachmentUpload(t, fixture.router(0, ""), "/api/v1/tickets/attachments", "file", "a.png",
		attachmentHandlerTestPNG(t, 2, 2))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	require.Zero(t, fixture.repo.count(), "被拒绝的上传不得落库")
}

// TestTicketAttachmentHandlerUploadSizeBoundary 覆盖 A2：5MB 通过 / 6MB 被拒（413）。
func TestTicketAttachmentHandlerUploadSizeBoundary(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	router := fixture.router(7, "")
	base := attachmentHandlerTestPNG(t, 2, 2)

	exactly5MB := append(append([]byte{}, base...), make([]byte, service.TicketAttachmentMaxBytes-int64(len(base)))...)
	rec := performAttachmentUpload(t, router, "/api/v1/tickets/attachments", "file", "five.png", exactly5MB)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, service.TicketAttachmentMaxBytes, decodeAttachmentEnvelope(t, rec).ByteSize)

	sixMB := append(append([]byte{}, base...), make([]byte, 6<<20)...)
	rec = performAttachmentUpload(t, router, "/api/v1/tickets/attachments", "file", "six.png", sixMB)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_TOO_LARGE", decodeTicketEnvelope(t, rec).Reason)
}

// TestTicketAttachmentHandlerUploadDailyQuota 覆盖日配额 50 张 → 429。
func TestTicketAttachmentHandlerUploadDailyQuota(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	now := time.Now().UTC()
	for i := 0; i < service.TicketAttachmentDailyQuota; i++ {
		fixture.repo.seed(service.TicketAttachment{
			UploaderID: 7,
			StorageKey: fmt.Sprintf("2026/11/quota-%d.png", i),
			SHA256:     fmt.Sprintf("%064d", i),
			ByteSize:   int64(i + 1),
			CreatedAt:  now.Add(-time.Minute),
		})
	}

	rec := performAttachmentUpload(t, fixture.router(7, ""), "/api/v1/tickets/attachments", "file", "over.png",
		attachmentHandlerTestPNG(t, 2, 2))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_DAILY_QUOTA_EXCEEDED", decodeTicketEnvelope(t, rec).Reason)
}

// TestTicketAttachmentHandlerDeleteUnbound 覆盖删除：本人可删、他人 404、已绑定 409。
func TestTicketAttachmentHandlerDeleteUnbound(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)

	rec := performAttachmentUpload(t, fixture.router(7, ""), "/api/v1/tickets/attachments", "file", "del.png",
		attachmentHandlerTestPNG(t, 3, 3))
	require.Equal(t, http.StatusCreated, rec.Code)
	attachment := decodeAttachmentEnvelope(t, rec)

	// 他人删除按不存在处理
	rec = performTicketRequest(fixture.router(8, ""), http.MethodDelete, attachment.URL, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_NOT_FOUND", decodeTicketEnvelope(t, rec).Reason)

	// 本人删除成功
	rec = performTicketRequest(fixture.router(7, ""), http.MethodDelete, attachment.URL, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var deleted struct {
		Deleted bool  `json:"deleted"`
		ID      int64 `json:"id"`
	}
	decodeTicketData(t, rec, &deleted)
	require.True(t, deleted.Deleted)
	require.Equal(t, attachment.ID, deleted.ID)

	// 再删一次 → 404
	rec = performTicketRequest(fixture.router(7, ""), http.MethodDelete, attachment.URL, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 已绑定附件 → 409
	rec = performAttachmentUpload(t, fixture.router(7, ""), "/api/v1/tickets/attachments", "file", "bound.png",
		attachmentHandlerTestPNG(t, 3, 4))
	require.Equal(t, http.StatusCreated, rec.Code)
	bound := decodeAttachmentEnvelope(t, rec)
	rec = performTicketRequest(fixture.router(7, ""), http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":        "bind then delete",
		"content":        "body",
		"attachment_ids": []int64{bound.ID},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = performTicketRequest(fixture.router(7, ""), http.MethodDelete, bound.URL, nil)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_ALREADY_BOUND", decodeTicketEnvelope(t, rec).Reason)
}

// TestTicketAttachmentHandlerCreateTicketBindsAttachments 覆盖两阶段绑定 + 详情聚合：
// POST /tickets 带 attachment_ids → 回填 reply_id，详情 replies[].attachments[] 可读。
func TestTicketAttachmentHandlerCreateTicketBindsAttachments(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	router := fixture.router(7, "")

	first := decodeAttachmentEnvelope(t, performAttachmentUpload(t, router, "/api/v1/tickets/attachments",
		"file", "a.png", attachmentHandlerTestPNG(t, 2, 2)))
	second := decodeAttachmentEnvelope(t, performAttachmentUpload(t, router, "/api/v1/tickets/attachments",
		"file", "b.png", attachmentHandlerTestPNG(t, 3, 2)))

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":        "带图工单",
		"content":        "截图见附件",
		"attachment_ids": []int64{first.ID, second.ID},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var created struct {
		Ticket map[string]any `json:"ticket"`
		Reply  struct {
			ID          int64            `json:"id"`
			Attachments []map[string]any `json:"attachments"`
		} `json:"reply"`
	}
	decodeTicketData(t, rec, &created)
	require.Len(t, created.Reply.Attachments, 2, "创建响应应回带已绑定附件")
	require.Equal(t, float64(first.ID), created.Reply.Attachments[0]["id"])
	require.Equal(t, "image/png", created.Reply.Attachments[0]["content_type"])
	require.Equal(t, float64(2), created.Reply.Attachments[0]["width"])
	require.Equal(t, float64(2), created.Reply.Attachments[0]["height"])
	require.Equal(t, fmt.Sprintf("/api/v1/tickets/attachments/%d", first.ID), created.Reply.Attachments[0]["url"])
	require.NotEmpty(t, created.Reply.Attachments[0]["original_name"])

	ticketID := jsonID(int64(created.Ticket["id"].(float64)))
	rec = performTicketRequest(router, http.MethodGet, "/api/v1/tickets/"+ticketID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var detail struct {
		Replies []struct {
			ID          int64            `json:"id"`
			Attachments []map[string]any `json:"attachments"`
		} `json:"replies"`
	}
	decodeTicketData(t, rec, &detail)
	require.Len(t, detail.Replies, 1)
	require.Len(t, detail.Replies[0].Attachments, 2)

	// 已绑定：记录里 reply_id/ticket_id 均已回填
	stored := fixture.repo.get(first.ID)
	require.NotNil(t, stored)
	require.NotNil(t, stored.ReplyID)
	require.NotNil(t, stored.TicketID)

	// 无附件回复的 attachments 恒为 []（不是 null）
	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets/"+ticketID+"/replies", map[string]any{
		"content": "再补一句",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"attachments":[]`)
}

// TestTicketAttachmentHandlerReplyBindsAttachments 覆盖 POST /tickets/:id/replies 的 attachment_ids。
func TestTicketAttachmentHandlerReplyBindsAttachments(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	router := fixture.router(7, "")

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "先建工单",
		"content": "第一段",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created struct {
		Ticket map[string]any `json:"ticket"`
	}
	decodeTicketData(t, rec, &created)
	ticketID := jsonID(int64(created.Ticket["id"].(float64)))

	attachment := decodeAttachmentEnvelope(t, performAttachmentUpload(t, router, "/api/v1/tickets/attachments",
		"file", "reply.png", attachmentHandlerTestPNG(t, 4, 4)))

	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets/"+ticketID+"/replies", map[string]any{
		"content":        "补充截图",
		"attachment_ids": []int64{attachment.ID},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var replied struct {
		Reply struct {
			Attachments []map[string]any `json:"attachments"`
		} `json:"reply"`
	}
	decodeTicketData(t, rec, &replied)
	require.Len(t, replied.Reply.Attachments, 1)
	require.Equal(t, float64(attachment.ID), replied.Reply.Attachments[0]["id"])
}

// TestTicketAttachmentHandlerCreateTicketRejectsForeignAttachment 覆盖 A6（HTTP 层）：
// 绑定他人附件被拒，且不留下半成品工单。
func TestTicketAttachmentHandlerCreateTicketRejectsForeignAttachment(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)

	foreign := decodeAttachmentEnvelope(t, performAttachmentUpload(t, fixture.router(8, ""),
		"/api/v1/tickets/attachments", "file", "foreign.png", attachmentHandlerTestPNG(t, 3, 3)))

	rec := performTicketRequest(fixture.router(7, ""), http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":        "抢别人图",
		"content":        "body",
		"attachment_ids": []int64{foreign.ID},
	})
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_NOT_FOUND", decodeTicketEnvelope(t, rec).Reason)

	// 工单未落库
	rec = performTicketRequest(fixture.router(7, ""), http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Total int64 `json:"total"`
	}
	decodeTicketData(t, rec, &list)
	require.Zero(t, list.Total, "附件校验失败不得先建工单")

	// 附件仍未绑定
	stored := fixture.repo.get(foreign.ID)
	require.NotNil(t, stored)
	require.Nil(t, stored.ReplyID)
}

// TestTicketAttachmentHandlerCreateTicketRejectsFourAttachments 覆盖 A1（HTTP 层）：
// 4 张被拒 / 3 张通过。
func TestTicketAttachmentHandlerCreateTicketRejectsFourAttachments(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	router := fixture.router(7, "")

	ids := make([]int64, 0, 4)
	for i := 0; i < 4; i++ {
		ids = append(ids, decodeAttachmentEnvelope(t, performAttachmentUpload(t, router,
			"/api/v1/tickets/attachments", "file", fmt.Sprintf("shot-%d.png", i),
			attachmentHandlerTestPNG(t, 2+i, 2))).ID)
	}

	rec := performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":        "四张图",
		"content":        "body",
		"attachment_ids": ids,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "TICKET_ATTACHMENT_LIMIT_EXCEEDED", decodeTicketEnvelope(t, rec).Reason)

	rec = performTicketRequest(router, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":        "三张图",
		"content":        "body",
		"attachment_ids": ids[:3],
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// TestTicketAttachmentHandlerAdminCanUpload 覆盖 t6 的权限项：管理员也能调用用户侧上传接口
// （用户路由组的 BackendModeUserGuard 显式放行 role=admin，上传 handler 只要求登录态）。
func TestTicketAttachmentHandlerAdminCanUpload(t *testing.T) {
	fixture := newTicketAttachmentHandlerFixture(t)
	adminRouter := fixture.router(99, service.RoleAdmin)

	rec := performAttachmentUpload(t, adminRouter, "/api/v1/tickets/attachments", "file", "admin.png",
		attachmentHandlerTestPNG(t, 4, 4))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	attachment := decodeAttachmentEnvelope(t, rec)
	require.Equal(t, "admin.png", attachment.OriginalName)

	// 管理员本人可读
	rec = performTicketRequest(adminRouter, http.MethodGet, attachment.URL, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// 普通用户读不到管理员上传的附件（404，不暴露存在性）
	rec = performTicketRequest(fixture.router(7, ""), http.MethodGet, attachment.URL, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- 夹具 ---

type ticketAttachmentHandlerFixture struct {
	tickets         *ticketTestRepo
	ticketService   *service.TicketService
	attachmentSvc   *service.TicketAttachmentService
	repo            *fakeAttachmentRepo
	storage         service.TicketAttachmentStorage
	storageRootPath string
	routerCache     map[string]*gin.Engine
}

func newTicketAttachmentHandlerFixture(t *testing.T) *ticketAttachmentHandlerFixture {
	t.Helper()
	tickets := newTicketTestRepo()
	ticketService := newTicketTestServiceWithRepo(tickets)
	root := t.TempDir()
	storage := service.NewLocalTicketAttachmentStorage(root)
	repo := newFakeAttachmentRepo()
	attachmentService := service.NewTicketAttachmentService(repo, storage, tickets)
	ticketService.SetAttachmentBinder(attachmentService)
	return &ticketAttachmentHandlerFixture{
		tickets:         tickets,
		ticketService:   ticketService,
		attachmentSvc:   attachmentService,
		repo:            repo,
		storage:         storage,
		storageRootPath: root,
		routerCache:     make(map[string]*gin.Engine),
	}
}

// router 按 (userID, role) 返回挂载了用户侧工单 + 附件全部路由的引擎。
// 这里同时验证 /tickets/attachments/* 与 /tickets/:id 能共存（gin 静态段优先）。
func (f *ticketAttachmentHandlerFixture) router(userID int64, role string) *gin.Engine {
	key := fmt.Sprintf("%d/%s", userID, role)
	if cached, ok := f.routerCache[key]; ok {
		return cached
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	authenticated := engine.Group("/api/v1", func(c *gin.Context) {
		if userID > 0 {
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		}
		if role != "" {
			c.Set(string(middleware2.ContextKeyUserRole), role)
		}
		c.Next()
	})
	ticketHandler := NewTicketHandler(f.ticketService)
	attachmentHandler := NewTicketAttachmentHandler(f.attachmentSvc)
	authenticated.GET("/tickets", ticketHandler.List)
	authenticated.POST("/tickets", ticketHandler.Create)
	authenticated.POST("/tickets/attachments", attachmentHandler.Upload)
	authenticated.GET("/tickets/attachments/:id", attachmentHandler.Get)
	authenticated.DELETE("/tickets/attachments/:id", attachmentHandler.Delete)
	authenticated.GET("/tickets/:id", ticketHandler.Get)
	authenticated.POST("/tickets/:id/replies", ticketHandler.AddReply)
	authenticated.POST("/tickets/:id/close", ticketHandler.Close)
	f.routerCache[key] = engine
	return engine
}

func performAttachmentUpload(
	t *testing.T,
	router *gin.Engine,
	path, field, fileName string,
	data []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, fileName)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

type attachmentEnvelopeData struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	OriginalName string `json:"original_name"`
	ContentType  string `json:"content_type"`
	ByteSize     int64  `json:"byte_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

func decodeAttachmentEnvelope(t *testing.T, rec *httptest.ResponseRecorder) attachmentEnvelopeData {
	t.Helper()
	var payload struct {
		Attachment attachmentEnvelopeData `json:"attachment"`
	}
	decodeTicketData(t, rec, &payload)
	return payload.Attachment
}

func attachmentHandlerTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, width, height), []color.Color{color.Black})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// fakeAttachmentRepo 是 handler 层测试用的内存附件仓储。
type fakeAttachmentRepo struct {
	mu    sync.Mutex
	items map[int64]*service.TicketAttachment
	next  int64
}

func newFakeAttachmentRepo() *fakeAttachmentRepo {
	return &fakeAttachmentRepo{items: make(map[int64]*service.TicketAttachment)}
}

func (r *fakeAttachmentRepo) clone(item service.TicketAttachment) service.TicketAttachment {
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

func (r *fakeAttachmentRepo) seed(item service.TicketAttachment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	item.ID = r.next
	stored := r.clone(item)
	r.items[item.ID] = &stored
}

func (r *fakeAttachmentRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

func (r *fakeAttachmentRepo) get(id int64) *service.TicketAttachment {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return nil
	}
	copied := r.clone(*item)
	return &copied
}

func (r *fakeAttachmentRepo) Insert(_ context.Context, item *service.TicketAttachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	item.ID = r.next
	stored := r.clone(*item)
	r.items[item.ID] = &stored
	return nil
}

func (r *fakeAttachmentRepo) GetByID(_ context.Context, id int64) (*service.TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return nil, service.ErrTicketAttachmentNotFound
	}
	copied := r.clone(*item)
	return &copied, nil
}

func (r *fakeAttachmentRepo) FindReusable(
	_ context.Context,
	uploaderID int64,
	sha256Hex string,
	byteSize int64,
) (*service.TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.items {
		if item.UploaderID == uploaderID && item.SHA256 == sha256Hex &&
			item.ByteSize == byteSize && item.ReplyID == nil {
			copied := r.clone(*item)
			return &copied, nil
		}
	}
	return nil, nil
}

func (r *fakeAttachmentRepo) ListByTicket(_ context.Context, ticketID int64) ([]service.TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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

func (r *fakeAttachmentRepo) BindReply(
	_ context.Context,
	ticketID, replyID, uploaderID int64,
	ids []int64,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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

func (r *fakeAttachmentRepo) DeleteUnbound(_ context.Context, id int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok || item.ReplyID != nil {
		return false, nil
	}
	delete(r.items, id)
	return true, nil
}

func (r *fakeAttachmentRepo) CountByUploaderSince(
	_ context.Context,
	uploaderID int64,
	since time.Time,
) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for _, item := range r.items {
		if item.UploaderID == uploaderID && !item.CreatedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

func (r *fakeAttachmentRepo) CountBoundByTicket(_ context.Context, ticketID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for _, item := range r.items {
		if item.TicketID != nil && *item.TicketID == ticketID && item.ReplyID != nil {
			count++
		}
	}
	return count, nil
}

func (r *fakeAttachmentRepo) ListOrphansBefore(
	_ context.Context,
	cutoff time.Time,
	limit int,
) ([]service.TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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

func (r *fakeAttachmentRepo) DeleteOrphanBefore(
	_ context.Context,
	id int64,
	cutoff time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok || item.ReplyID != nil || !item.CreatedAt.Before(cutoff) {
		return false, nil
	}
	delete(r.items, id)
	return true, nil
}

// 断言 fakeAttachmentRepo 满足服务层契约。
var _ service.TicketAttachmentRepository = (*fakeAttachmentRepo)(nil)
