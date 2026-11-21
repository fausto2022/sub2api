package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	// 注册图片解码器，供 image.DecodeConfig 只读图片头（WebP 在标准库之外）。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 工单附件约束（design.md §1.1）。
const (
	// TicketAttachmentMaxPerMessage 是单条消息（工单首条或一条回复）最多附件数。
	TicketAttachmentMaxPerMessage = 3
	// TicketAttachmentMaxPerTicket 是单工单累计附件上限，防磁盘占满（design.md §1.6）。
	TicketAttachmentMaxPerTicket = 15
	// TicketAttachmentMaxBytes 是单张图片字节上限（5 MB）。
	TicketAttachmentMaxBytes int64 = 5 << 20
	// TicketAttachmentMaxRequestBytes 是单次 multipart 请求体上限（3×5MB + 余量）。
	TicketAttachmentMaxRequestBytes int64 = 12 << 20
	// TicketAttachmentMaxDimension 是宽/高像素上限（image.DecodeConfig 只读头，防解压炸弹）。
	TicketAttachmentMaxDimension = 8000
	// TicketAttachmentDailyQuota 是单用户每日上传张数上限，超限返回 429。
	TicketAttachmentDailyQuota = 50
	// TicketAttachmentOriginalNameMaxRunes 对应表字段 varchar(255)。
	TicketAttachmentOriginalNameMaxRunes = 255
)

// 附件允许的 MIME 类型（魔数嗅探结果，白名单不含 SVG）。
const (
	TicketAttachmentContentTypePNG  = "image/png"
	TicketAttachmentContentTypeJPEG = "image/jpeg"
	TicketAttachmentContentTypeWebP = "image/webp"
	TicketAttachmentContentTypeGIF  = "image/gif"
)

// 孤儿附件清理参数（design.md §1.4 ③）。
const (
	// TicketAttachmentOrphanTTL 是未绑定附件的保留时长，超过即可清理。
	TicketAttachmentOrphanTTL = 2 * time.Hour
	// TicketAttachmentCleanupInterval 是后台清理任务间隔。
	TicketAttachmentCleanupInterval = time.Hour
	// ticketAttachmentCleanupBatch 是单批处理行数，避免长事务/长循环占住连接。
	ticketAttachmentCleanupBatch = 500
)

// 工单附件业务错误。HTTP 状态码与 design.md §1.5/§1.6 一致。
var (
	// ErrTicketAttachmentNotFound 同时用于「不存在」与「无权访问」，避免暴露存在性。
	ErrTicketAttachmentNotFound = infraerrors.NotFound("TICKET_ATTACHMENT_NOT_FOUND", "ticket attachment not found")
	ErrTicketAttachmentEmpty    = infraerrors.BadRequest("TICKET_ATTACHMENT_EMPTY", "ticket attachment file is empty")
	ErrTicketAttachmentTooLarge = infraerrors.New(
		http.StatusRequestEntityTooLarge,
		"TICKET_ATTACHMENT_TOO_LARGE",
		fmt.Sprintf("ticket attachment cannot exceed %d MB", TicketAttachmentMaxBytes>>20),
	)
	ErrTicketAttachmentUnsupportedType = infraerrors.BadRequest(
		"TICKET_ATTACHMENT_TYPE_NOT_ALLOWED",
		"ticket attachment must be a PNG, JPEG, WebP or GIF image (SVG is not allowed)",
	)
	ErrTicketAttachmentInvalidImage = infraerrors.BadRequest(
		"TICKET_ATTACHMENT_INVALID_IMAGE",
		"ticket attachment is not a decodable image",
	)
	ErrTicketAttachmentDimensionExceeded = infraerrors.BadRequest(
		"TICKET_ATTACHMENT_DIMENSION_EXCEEDED",
		fmt.Sprintf("image width and height cannot exceed %d px", TicketAttachmentMaxDimension),
	)
	ErrTicketAttachmentDailyQuotaExceeded = infraerrors.TooManyRequests(
		"TICKET_ATTACHMENT_DAILY_QUOTA_EXCEEDED",
		fmt.Sprintf("daily ticket attachment quota (%d images) exceeded", TicketAttachmentDailyQuota),
	)
	ErrTicketAttachmentLimitExceeded = infraerrors.BadRequest(
		"TICKET_ATTACHMENT_LIMIT_EXCEEDED",
		fmt.Sprintf("a ticket message can carry at most %d attachments", TicketAttachmentMaxPerMessage),
	)
	ErrTicketAttachmentTicketLimitReached = infraerrors.BadRequest(
		"TICKET_ATTACHMENT_TICKET_LIMIT_REACHED",
		fmt.Sprintf("a ticket can carry at most %d attachments", TicketAttachmentMaxPerTicket),
	)
	ErrTicketAttachmentAlreadyBound = infraerrors.New(
		http.StatusConflict,
		"TICKET_ATTACHMENT_ALREADY_BOUND",
		"ticket attachment is already bound to a reply",
	)
	ErrTicketAttachmentUnavailable = infraerrors.New(
		http.StatusServiceUnavailable,
		"TICKET_ATTACHMENT_UNAVAILABLE",
		"ticket attachments are not available",
	)
)

// TicketAttachment 是 ticket_attachments 表的业务视图。
type TicketAttachment struct {
	ID           int64
	TicketID     *int64
	ReplyID      *int64
	UploaderID   int64
	StorageKey   string
	StorageKind  string
	OriginalName string
	ContentType  string
	ByteSize     int64
	Width        int
	Height       int
	SHA256       string
	CreatedAt    time.Time
}

// Bound 报告附件是否已绑定到某条回复（未绑定的才可删除/绑定）。
func (a *TicketAttachment) Bound() bool { return a != nil && a.ReplyID != nil }

// TicketAttachmentRepository 是附件持久化契约（ent 实现在 repository/ticket_attachment_repo.go）。
type TicketAttachmentRepository interface {
	Insert(ctx context.Context, item *TicketAttachment) error
	// GetByID 未命中返回 ErrTicketAttachmentNotFound。
	GetByID(ctx context.Context, id int64) (*TicketAttachment, error)
	// FindReusable 查找同一用户、同一 sha256/大小且仍未绑定的附件；未命中返回 (nil, nil)。
	FindReusable(ctx context.Context, uploaderID int64, sha256Hex string, byteSize int64) (*TicketAttachment, error)
	ListByTicket(ctx context.Context, ticketID int64) ([]TicketAttachment, error)
	// BindReply 在单一事务内把未绑定附件回填到 (ticket_id, reply_id)；
	// 只要有一行不满足 uploader_id/未绑定条件，整体回滚。
	BindReply(ctx context.Context, ticketID, replyID, uploaderID int64, ids []int64) error
	// DeleteUnbound 只删除仍未绑定的附件，返回是否真的删除了一行。
	DeleteUnbound(ctx context.Context, id int64) (bool, error)
	CountByUploaderSince(ctx context.Context, uploaderID int64, since time.Time) (int64, error)
	CountBoundByTicket(ctx context.Context, ticketID int64) (int64, error)
	ListOrphansBefore(ctx context.Context, cutoff time.Time, limit int) ([]TicketAttachment, error)
	// DeleteOrphanBefore 条件删除（仍为孤儿且早于 cutoff），返回是否删除成功。
	DeleteOrphanBefore(ctx context.Context, id int64, cutoff time.Time) (bool, error)
}

// TicketAttachmentTicketReader 是读取鉴权所需的最小工单读取能力（TicketRepository 满足）。
type TicketAttachmentTicketReader interface {
	GetByID(ctx context.Context, id int64) (*Ticket, error)
}

// TicketAttachmentSubject 是附件操作的请求者身份。
type TicketAttachmentSubject struct {
	UserID  int64
	IsAdmin bool
}

// TicketAttachmentService 实现工单附件业务规则：上传校验（魔数/尺寸/大小/配额）、
// 鉴权读取、删除未绑定附件、两阶段绑定与孤儿清理。
type TicketAttachmentService struct {
	repo    TicketAttachmentRepository
	storage TicketAttachmentStorage
	tickets TicketAttachmentTicketReader
	now     func() time.Time
	newID   func() string

	cleanupMu   sync.Mutex
	cleanupStop chan struct{}
}

// NewTicketAttachmentService 构造附件服务。tickets 用于读取鉴权（工单提交人可读）。
func NewTicketAttachmentService(
	repo TicketAttachmentRepository,
	storage TicketAttachmentStorage,
	tickets TicketAttachmentTicketReader,
) *TicketAttachmentService {
	return &TicketAttachmentService{
		repo:    repo,
		storage: storage,
		tickets: tickets,
		now:     func() time.Time { return time.Now().UTC() },
		newID:   newTicketAttachmentObjectID,
	}
}

// SetClock 注入可替换时钟（测试用）。
func (s *TicketAttachmentService) SetClock(now func() time.Time) {
	if s == nil || now == nil {
		return
	}
	s.now = now
}

// SetObjectIDGenerator 注入可替换对象名生成器（测试用）。
func (s *TicketAttachmentService) SetObjectIDGenerator(generator func() string) {
	if s == nil || generator == nil {
		return
	}
	s.newID = generator
}

// Upload 校验并落盘一张图片，写入一条未绑定（reply_id IS NULL）的附件记录。
//
// 校验顺序：非空 → 字节上限 → 魔数白名单（优先于客户端 Content-Type，SVG 一律拒绝）
// → 宽高上限（只读图片头）→ 同用户同图复用 → 日配额 → 落盘。
func (s *TicketAttachmentService) Upload(
	ctx context.Context,
	userID int64,
	fileName string,
	declaredContentType string,
	data []byte,
) (*TicketAttachment, error) {
	if len(data) == 0 {
		return nil, ErrTicketAttachmentEmpty
	}
	if int64(len(data)) > TicketAttachmentMaxBytes {
		return nil, ErrTicketAttachmentTooLarge
	}
	// 魔数嗅探优先：声明类型只作为参考，客户端 MIME/扩展名可伪造（design.md §1.6）。
	contentType, ok := ticketAttachmentSniffContentType(data)
	if !ok {
		if declaredContentType != "" {
			slog.Debug("rejected ticket attachment",
				"declared_content_type", declaredContentType,
				"sniffed_content_type", http.DetectContentType(firstBytes(data, 512)))
		}
		return nil, ErrTicketAttachmentUnsupportedType
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrTicketAttachmentInvalidImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrTicketAttachmentInvalidImage
	}
	if cfg.Width > TicketAttachmentMaxDimension || cfg.Height > TicketAttachmentMaxDimension {
		return nil, ErrTicketAttachmentDimensionExceeded
	}

	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	byteSize := int64(len(data))

	// 同用户重复上传同一张图：复用仍未绑定的那条记录，节省磁盘与行数。
	// 复用不新增落盘，因此先于日配额判定（配额只统计真正新增的存储）。
	if reusable, err := s.repo.FindReusable(ctx, userID, sha, byteSize); err != nil {
		return nil, err
	} else if reusable != nil {
		return reusable, nil
	}

	now := s.now().UTC()
	if err := s.ensureDailyQuota(ctx, userID, now); err != nil {
		return nil, err
	}

	ext := ticketAttachmentExtension(contentType)
	key := ticketAttachmentObjectKey(now, s.newID(), ext)
	if err := s.storage.Put(ctx, key, data, contentType); err != nil {
		return nil, err
	}

	item := &TicketAttachment{
		UploaderID:   userID,
		StorageKey:   key,
		StorageKind:  s.storage.Kind(),
		OriginalName: sanitizeTicketAttachmentName(fileName, ext),
		ContentType:  contentType,
		ByteSize:     byteSize,
		Width:        cfg.Width,
		Height:       cfg.Height,
		SHA256:       sha,
		CreatedAt:    now,
	}
	if err := s.repo.Insert(ctx, item); err != nil {
		// 行写失败时清掉刚落的文件，避免产生无主的磁盘垃圾。
		if deleteErr := s.storage.Delete(ctx, key); deleteErr != nil {
			slog.Warn("failed to rollback ticket attachment file", "storage_key", key, "error", deleteErr)
		}
		return nil, err
	}
	return item, nil
}

// GetForRead 鉴权读取附件内容。权限矩阵（design.md §1.5）：管理员、工单提交人、
// 未绑定附件的上传者可读；其他人一律 ErrTicketAttachmentNotFound（404，不暴露存在性）。
func (s *TicketAttachmentService) GetForRead(
	ctx context.Context,
	subject TicketAttachmentSubject,
	id int64,
) (*TicketAttachment, []byte, error) {
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !s.canRead(ctx, subject, item) {
		return nil, nil, ErrTicketAttachmentNotFound
	}
	data, err := s.storage.Get(ctx, item.StorageKey)
	if err != nil {
		slog.Warn("ticket attachment object is missing", "attachment_id", item.ID, "storage_key", item.StorageKey, "error", err)
		return nil, nil, ErrTicketAttachmentNotFound
	}
	return item, data, nil
}

// DeleteUnbound 删除未绑定附件（归属校验：上传者本人或管理员）。已绑定返回 409。
func (s *TicketAttachmentService) DeleteUnbound(ctx context.Context, subject TicketAttachmentSubject, id int64) error {
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if !subject.IsAdmin && item.UploaderID != subject.UserID {
		return ErrTicketAttachmentNotFound
	}
	if item.Bound() {
		return ErrTicketAttachmentAlreadyBound
	}
	deleted, err := s.repo.DeleteUnbound(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		// 并发下已被绑定：视为冲突，不删文件。
		return ErrTicketAttachmentAlreadyBound
	}
	if err := s.storage.Delete(ctx, item.StorageKey); err != nil {
		slog.Warn("failed to delete ticket attachment file", "attachment_id", item.ID, "storage_key", item.StorageKey, "error", err)
	}
	return nil
}

// ValidateBinding 是绑定前的只读校验，供工单创建/回复在落库前先拒绝非法附件，
// 避免「工单已创建但附件绑定失败」。
func (s *TicketAttachmentService) ValidateBinding(ctx context.Context, userID int64, attachmentIDs []int64) error {
	normalized, err := normalizeTicketAttachmentIDs(attachmentIDs)
	if err != nil {
		return err
	}
	for _, id := range normalized {
		item, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if item.UploaderID != userID {
			// 绑定他人附件：按不存在处理（design.md A6，不暴露存在性）。
			return ErrTicketAttachmentNotFound
		}
		if item.Bound() {
			return ErrTicketAttachmentAlreadyBound
		}
	}
	return nil
}

// BindReply 在事务内把附件回填到 (ticketID, replyID)，返回绑定后的附件列表。
// 校验：数量 ≤3、归属（uploader_id == userID）、未绑定、单工单累计 ≤15。
func (s *TicketAttachmentService) BindReply(
	ctx context.Context,
	userID, ticketID, replyID int64,
	attachmentIDs []int64,
) ([]TicketAttachment, error) {
	normalized, err := normalizeTicketAttachmentIDs(attachmentIDs)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	items := make([]TicketAttachment, 0, len(normalized))
	for _, id := range normalized {
		item, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if item.UploaderID != userID {
			return nil, ErrTicketAttachmentNotFound
		}
		if item.Bound() {
			return nil, ErrTicketAttachmentAlreadyBound
		}
		items = append(items, *item)
	}
	bound, err := s.repo.CountBoundByTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if int(bound)+len(items) > TicketAttachmentMaxPerTicket {
		return nil, ErrTicketAttachmentTicketLimitReached
	}
	if err := s.repo.BindReply(ctx, ticketID, replyID, userID, normalized); err != nil {
		return nil, err
	}
	ticketIDCopy, replyIDCopy := ticketID, replyID
	for i := range items {
		items[i].TicketID = &ticketIDCopy
		items[i].ReplyID = &replyIDCopy
	}
	return items, nil
}

// ListReplyAttachments 返回工单下按 reply_id 分组的附件，供详情 DTO 聚合。
func (s *TicketAttachmentService) ListReplyAttachments(ctx context.Context, ticketID int64) (map[int64][]TicketAttachment, error) {
	items, err := s.repo.ListByTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	grouped := make(map[int64][]TicketAttachment, len(items))
	for _, item := range items {
		if item.ReplyID == nil {
			continue
		}
		grouped[*item.ReplyID] = append(grouped[*item.ReplyID], item)
	}
	return grouped, nil
}

// CleanupOrphans 删除 created_at < now-2h 且仍未绑定（reply_id IS NULL）的附件记录与磁盘文件，
// 返回清理条数。分批处理，单批 ticketAttachmentCleanupBatch 行（design.md §1.4 ③ / A7）。
func (s *TicketAttachmentService) CleanupOrphans(ctx context.Context) (int, error) {
	cutoff := s.now().UTC().Add(-TicketAttachmentOrphanTTL)
	removed := 0
	for {
		orphans, err := s.repo.ListOrphansBefore(ctx, cutoff, ticketAttachmentCleanupBatch)
		if err != nil {
			return removed, err
		}
		if len(orphans) == 0 {
			return removed, nil
		}
		for _, item := range orphans {
			deleted, err := s.repo.DeleteOrphanBefore(ctx, item.ID, cutoff)
			if err != nil {
				return removed, err
			}
			if !deleted {
				continue
			}
			removed++
			if err := s.storage.Delete(ctx, item.StorageKey); err != nil {
				slog.Warn("failed to delete orphan ticket attachment file",
					"attachment_id", item.ID, "storage_key", item.StorageKey, "error", err)
			}
		}
		if len(orphans) < ticketAttachmentCleanupBatch {
			return removed, nil
		}
	}
}

// StartOrphanCleanupLoop 启动每小时一次的孤儿清理，重复调用无副作用。
// ctx 取消或调用 StopOrphanCleanup 时退出。
func (s *TicketAttachmentService) StartOrphanCleanupLoop(ctx context.Context) {
	if s == nil {
		return
	}
	s.cleanupMu.Lock()
	if s.cleanupStop != nil {
		s.cleanupMu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.cleanupStop = stop
	s.cleanupMu.Unlock()

	go func() {
		ticker := time.NewTicker(TicketAttachmentCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				removed, err := s.CleanupOrphans(runCtx)
				cancel()
				if err != nil {
					slog.Warn("ticket attachment orphan cleanup failed", "error", err, "removed", removed)
					continue
				}
				if removed > 0 {
					slog.Info("ticket attachment orphan cleanup finished", "removed", removed)
				}
			}
		}
	}()
}

// StopOrphanCleanup 停止后台清理循环（幂等）。
func (s *TicketAttachmentService) StopOrphanCleanup() {
	if s == nil {
		return
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.cleanupStop == nil {
		return
	}
	close(s.cleanupStop)
	s.cleanupStop = nil
}

func (s *TicketAttachmentService) canRead(
	ctx context.Context,
	subject TicketAttachmentSubject,
	item *TicketAttachment,
) bool {
	if item == nil {
		return false
	}
	if subject.IsAdmin {
		return true
	}
	if subject.UserID <= 0 {
		return false
	}
	if item.UploaderID == subject.UserID {
		return true
	}
	if item.TicketID == nil || s.tickets == nil {
		return false
	}
	ticket, err := s.tickets.GetByID(ctx, *item.TicketID)
	if err != nil || ticket == nil {
		return false
	}
	return ticket.UserID == subject.UserID
}

func (s *TicketAttachmentService) ensureDailyQuota(ctx context.Context, userID int64, now time.Time) error {
	// 日界按 UTC 划天：与 created_at 的存储时区一致，避免多实例/时区漂移导致额度抖动。
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	count, err := s.repo.CountByUploaderSince(ctx, userID, dayStart)
	if err != nil {
		return err
	}
	if count >= TicketAttachmentDailyQuota {
		return ErrTicketAttachmentDailyQuotaExceeded
	}
	return nil
}

// validateAttachmentIDs 供 TicketService 复用：去重、去空、限数量。
func normalizeTicketAttachmentIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > TicketAttachmentMaxPerMessage {
		return nil, ErrTicketAttachmentLimitExceeded
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrTicketAttachmentNotFound
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

// ticketAttachmentSniffContentType 用魔数嗅探判定白名单类型（SVG 会被嗅探成 text/* 而落榜）。
func ticketAttachmentSniffContentType(data []byte) (string, bool) {
	ct := strings.TrimSpace(strings.Split(http.DetectContentType(firstBytes(data, 512)), ";")[0])
	switch ct {
	case TicketAttachmentContentTypePNG,
		TicketAttachmentContentTypeJPEG,
		TicketAttachmentContentTypeWebP,
		TicketAttachmentContentTypeGIF:
		return ct, true
	default:
		return "", false
	}
}

func firstBytes(data []byte, limit int) []byte {
	if len(data) <= limit {
		return data
	}
	return data[:limit]
}

// sanitizeTicketAttachmentName 只保留展示用文件名：去目录、去控制字符、限制长度。
// 真实类型与扩展名由魔数决定，此处的名字不参与落盘路径。
func sanitizeTicketAttachmentName(raw, ext string) string {
	name := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.TrimSpace(path.Base(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		name = "image" + ext
	}
	if utf8.RuneCountInString(name) > TicketAttachmentOriginalNameMaxRunes {
		name = string([]rune(name)[:TicketAttachmentOriginalNameMaxRunes])
	}
	return name
}
