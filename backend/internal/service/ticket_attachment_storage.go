package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 工单附件存储后端类型。表字段 ticket_attachments.storage_kind 取这些值。
const (
	TicketAttachmentStorageKindLocal = "local"
	// TicketAttachmentStorageKindS3 为预留值：S3 兼容实现需使用私有 ACL（design.md §1.2），
	// 当前只落地 local；接入时实现 TicketAttachmentStorage 并在 ProvideTicketAttachmentStorage
	// 按配置返回即可，其余业务代码无需改动。
	TicketAttachmentStorageKindS3 = "s3"
)

// DefaultTicketAttachmentStorageDir 是本地存储的默认根目录（相对进程工作目录）。
const DefaultTicketAttachmentStorageDir = "data/ticket-attachments"

// TicketAttachmentStorage 承载工单附件的二进制内容。
//
// 与既有 ImageStorage 的区别（design.md §1.2）：工单附件是私密内容，只能经鉴权代理
// 接口 GET /api/v1/tickets/attachments/:id 读取，绝不能像异步生图结果那样公开静态目录。
// 因此单独定义接口与实现，不复用 ImageStorage。
//
// key 是存储根目录下的相对对象键，格式 {yyyy}/{mm}/{uuid}.{ext}（正斜杠分隔）。
type TicketAttachmentStorage interface {
	// Kind 返回 storage_kind（local / s3）。
	Kind() string
	// Put 写入对象。实现必须保证原子性：任何失败都不留下半成品对象。
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get 读取对象全部字节。
	Get(ctx context.Context, key string) ([]byte, error)
	// Delete 删除对象；对象不存在视为成功（幂等）。
	Delete(ctx context.Context, key string) error
}

// localTicketAttachmentStorage 把附件写到本地磁盘，读写均需经过业务层鉴权。
type localTicketAttachmentStorage struct {
	root string
}

var _ TicketAttachmentStorage = (*localTicketAttachmentStorage)(nil)

// NewLocalTicketAttachmentStorage 构造本地磁盘实现；root 为空时回落到
// DefaultTicketAttachmentStorageDir。root 会被清理为绝对路径，越界键一律拒绝。
func NewLocalTicketAttachmentStorage(root string) TicketAttachmentStorage {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		trimmed = DefaultTicketAttachmentStorageDir
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		trimmed = abs
	}
	return &localTicketAttachmentStorage{root: trimmed}
}

func (s *localTicketAttachmentStorage) Kind() string { return TicketAttachmentStorageKindLocal }

// Put 采用「同目录临时文件 + rename」保证原子性：读者永远看不到写了一半的图片，
// 进程崩溃最多留下 .upload-* 临时文件，不会产生损坏的正式对象。
func (s *localTicketAttachmentStorage) Put(ctx context.Context, key string, data []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, err := s.resolve(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create ticket attachment dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return fmt.Errorf("create ticket attachment temp file: %w", err)
	}
	tmpName := tmp.Name()
	discard := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		discard()
		return fmt.Errorf("write ticket attachment temp file: %w", err)
	}
	// fsync 后再 rename，避免断电后出现长度不足的对象。
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		discard()
		return fmt.Errorf("sync ticket attachment temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		discard()
		return fmt.Errorf("close ticket attachment temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		discard()
		return fmt.Errorf("chmod ticket attachment temp file: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		discard()
		return fmt.Errorf("commit ticket attachment file: %w", err)
	}
	return nil
}

func (s *localTicketAttachmentStorage) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read ticket attachment file: %w", err)
	}
	return data, nil
}

func (s *localTicketAttachmentStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete ticket attachment file: %w", err)
	}
	return nil
}

// resolve 把相对对象键解析成根目录下的绝对路径，拒绝绝对路径、上跳与空键，
// 防止路径穿越读到根目录之外的文件。
func (s *localTicketAttachmentStorage) resolve(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "", errors.New("ticket attachment storage key is empty")
	}
	if strings.Contains(trimmed, "\\") {
		trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("ticket attachment storage key must be relative: %q", key)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("ticket attachment storage key is invalid: %q", key)
		}
	}
	full := filepath.Join(s.root, filepath.FromSlash(trimmed))
	prefix := s.root + string(os.PathSeparator)
	if !strings.HasPrefix(full, prefix) {
		return "", fmt.Errorf("ticket attachment storage key escapes root: %q", key)
	}
	return full, nil
}

// ticketAttachmentObjectKey 生成相对对象键 {yyyy}/{mm}/{uuid}.{ext}（UTC 时间分区）。
func ticketAttachmentObjectKey(uploadedAt time.Time, objectID, ext string) string {
	return uploadedAt.UTC().Format("2006/01") + "/" + objectID + ext
}

// newTicketAttachmentObjectID 生成 UUIDv4 作为对象名：不可猜测，避免路径枚举。
func newTicketAttachmentObjectID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand 不可用时退回纳秒时间戳，保证仍不重复。
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40 // version 4
	raw[8] = (raw[8] & 0x3f) | 0x80 // variant RFC 4122
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// ticketAttachmentExtension 由魔数嗅探出的 MIME 反推扩展名（不信任客户端文件名）。
func ticketAttachmentExtension(contentType string) string {
	switch contentType {
	case TicketAttachmentContentTypePNG:
		return ".png"
	case TicketAttachmentContentTypeJPEG:
		return ".jpg"
	case TicketAttachmentContentTypeWebP:
		return ".webp"
	case TicketAttachmentContentTypeGIF:
		return ".gif"
	default:
		return ".bin"
	}
}
