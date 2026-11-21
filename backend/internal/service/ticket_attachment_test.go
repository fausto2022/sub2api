package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

// --- 工单附件服务测试（design.md 第四部分 A1–A4、A7 + 存储层契约） ---

func TestTicketAttachmentObjectKeyUsesYearMonthUUIDLayout(t *testing.T) {
	at := time.Date(2026, 11, 15, 23, 59, 0, 0, time.UTC)
	key := ticketAttachmentObjectKey(at, "3f2b8c1e-0000-4000-8000-000000000000", ".png")
	require.Equal(t, "2026/11/3f2b8c1e-0000-4000-8000-000000000000.png", key)

	// 跨年边界用 UTC 分区
	key = ticketAttachmentObjectKey(time.Date(2027, 1, 1, 0, 0, 1, 0, time.UTC), "u", ".webp")
	require.Equal(t, "2027/01/u.webp", key)
}

func TestTicketAttachmentObjectIDIsUUIDv4(t *testing.T) {
	first := newTicketAttachmentObjectID()
	second := newTicketAttachmentObjectID()
	require.NotEqual(t, first, second, "对象名必须不可猜测且不重复")
	require.Len(t, first, 36)
	require.Equal(t, "4", first[14:15], "UUID v4 版本位")
	require.Contains(t, []string{"8", "9", "a", "b"}, first[19:20], "RFC 4122 variant 位")
}

// TestTicketAttachmentLocalStoragePutGetDelete 覆盖 local 实现：原子写入（同目录临时文件 +
// rename）、路径 {yyyy}/{mm}/{uuid}.{ext}、读回字节、幂等删除与路径穿越拒绝。
func TestTicketAttachmentLocalStoragePutGetDelete(t *testing.T) {
	root := t.TempDir()
	storage := NewLocalTicketAttachmentStorage(root)
	ctx := context.Background()

	require.Equal(t, TicketAttachmentStorageKindLocal, storage.Kind())

	key := ticketAttachmentObjectKey(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC), "uuid-abc", ".png")
	payload := []byte("attachment-bytes")
	require.NoError(t, storage.Put(ctx, key, payload, TicketAttachmentContentTypePNG))

	fullPath := filepath.Join(root, "2026", "11", "uuid-abc.png")
	onDisk, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	require.Equal(t, payload, onDisk)

	// 写入后不得残留临时文件（只允许正式对象存在）
	entries, err := os.ReadDir(filepath.Dir(fullPath))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "uuid-abc.png", entries[0].Name())

	got, err := storage.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, payload, got)

	require.NoError(t, storage.Delete(ctx, key))
	_, err = os.Stat(fullPath)
	require.True(t, os.IsNotExist(err), "删除后文件不应存在")
	require.NoError(t, storage.Delete(ctx, key), "删除不存在的对象应幂等成功")

	// 路径穿越与非法键一律拒绝
	for _, bad := range []string{"", "   ", "../evil.png", "/etc/passwd", "2026/../../evil.png", "2026//x.png"} {
		require.Error(t, storage.Put(ctx, bad, payload, TicketAttachmentContentTypePNG), "key=%q 应被拒绝", bad)
	}
}

// TestTicketAttachmentSniffContentTypeWhitelist 覆盖 A4（SVG 被拒）与白名单判定：
// 类型完全以魔数为准，声明类型不参与。
func TestTicketAttachmentSniffContentTypeWhitelist(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
		ok   bool
	}{
		{"png", attachmentTestPNG(t, 2, 2), TicketAttachmentContentTypePNG, true},
		{"jpeg", attachmentTestJPEG(t, 2, 2), TicketAttachmentContentTypeJPEG, true},
		{"gif", attachmentTestGIF(t, 2, 2), TicketAttachmentContentTypeGIF, true},
		{"webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 "), TicketAttachmentContentTypeWebP, true},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "", false},
		{"plain-text", []byte("this is not an image at all"), "", false},
		{"empty", nil, "", false},
	}
	for _, tc := range cases {
		got, ok := ticketAttachmentSniffContentType(tc.data)
		require.Equal(t, tc.ok, ok, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}

	// 扩展名由魔数反推，不信任客户端文件名
	require.Equal(t, ".png", ticketAttachmentExtension(TicketAttachmentContentTypePNG))
	require.Equal(t, ".jpg", ticketAttachmentExtension(TicketAttachmentContentTypeJPEG))
	require.Equal(t, ".webp", ticketAttachmentExtension(TicketAttachmentContentTypeWebP))
	require.Equal(t, ".gif", ticketAttachmentExtension(TicketAttachmentContentTypeGIF))
}

// TestTicketAttachmentUploadPNGStoresMetadata 覆盖正常上传：落盘路径、元数据、文件名清洗。
func TestTicketAttachmentUploadPNGStoresMetadata(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()

	data := attachmentTestPNG(t, 12, 8)
	item, err := fixture.service.Upload(ctx, 7, "../../截图 (1).png", "image/png", data)
	require.NoError(t, err)
	require.NotZero(t, item.ID)
	require.Equal(t, int64(7), item.UploaderID)
	require.Equal(t, TicketAttachmentContentTypePNG, item.ContentType)
	require.Equal(t, int64(len(data)), item.ByteSize)
	require.Equal(t, 12, item.Width)
	require.Equal(t, 8, item.Height)
	require.Len(t, item.SHA256, 64)
	require.Equal(t, "截图 (1).png", item.OriginalName, "原始文件名要去掉目录部分")
	require.Equal(t, TicketAttachmentStorageKindLocal, item.StorageKind)
	require.Nil(t, item.ReplyID, "上传阶段必须未绑定")

	// storage_key 形如 {yyyy}/{mm}/{uuid}.png，且磁盘上确实存在
	parts := strings.Split(item.StorageKey, "/")
	require.Len(t, parts, 3)
	require.Len(t, parts[0], 4)
	require.Len(t, parts[1], 2)
	require.True(t, strings.HasSuffix(parts[2], ".png"))
	_, statErr := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(item.StorageKey)))
	require.NoError(t, statErr)

	// 读回字节
	_, stored, err := fixture.service.GetForRead(ctx, TicketAttachmentSubject{UserID: 7}, item.ID)
	require.NoError(t, err)
	require.Equal(t, data, stored)
}

// TestTicketAttachmentUploadIgnoresDeclaredContentType 覆盖「魔数优先于 Content-Type」：
// 声明成 SVG 但内容是真 PNG 时按 PNG 接受。
func TestTicketAttachmentUploadIgnoresDeclaredContentType(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	data := attachmentTestPNG(t, 3, 3)

	item, err := fixture.service.Upload(context.Background(), 7, "shot.png", "image/svg+xml", data)
	require.NoError(t, err)
	require.Equal(t, TicketAttachmentContentTypePNG, item.ContentType)
}

// TestTicketAttachmentUploadRejectsInvalidPayloads 覆盖 A2/A3/A4 的拒绝路径。
func TestTicketAttachmentUploadRejectsInvalidPayloads(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()
	pngData := attachmentTestPNG(t, 4, 4)

	exactly5MB := append(append([]byte{}, pngData...), make([]byte, TicketAttachmentMaxBytes-int64(len(pngData)))...)
	require.Equal(t, TicketAttachmentMaxBytes, int64(len(exactly5MB)))

	cases := []struct {
		name        string
		fileName    string
		declared    string
		data        []byte
		wantErr     error
		description string
	}{
		{
			name:     "empty",
			fileName: "empty.png",
			data:     nil,
			wantErr:  ErrTicketAttachmentEmpty,
		},
		{
			name:     "six-megabytes",
			fileName: "big.png",
			declared: "image/png",
			data:     append(append([]byte{}, pngData...), make([]byte, 6<<20)...),
			wantErr:  ErrTicketAttachmentTooLarge,
		},
		{
			name:     "text-renamed-to-png",
			fileName: "evil.png",
			declared: "image/png",
			data:     []byte("#!/bin/sh\necho not an image\n"),
			wantErr:  ErrTicketAttachmentUnsupportedType,
		},
		{
			name:     "svg-with-png-name",
			fileName: "evil.png",
			declared: "image/png",
			data:     []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`),
			wantErr:  ErrTicketAttachmentUnsupportedType,
		},
	}
	for _, tc := range cases {
		_, err := fixture.service.Upload(ctx, 7, tc.fileName, tc.declared, tc.data)
		require.Error(t, err, tc.name)
		require.True(t, errors.Is(err, tc.wantErr), "%s: want %v, got %v", tc.name, tc.wantErr, err)
	}
	require.Zero(t, fixture.repo.count(), "被拒绝的上传不得落库")

	// 恰好 5MB 必须通过（A2 的正向一半）
	item, err := fixture.service.Upload(ctx, 7, "exact.png", "image/png", exactly5MB)
	require.NoError(t, err)
	require.Equal(t, TicketAttachmentMaxBytes, item.ByteSize)
}

// TestTicketAttachmentUploadRejectsOversizedDimensions 覆盖宽高 > 8000 的拒绝
// 与 8000 边界放行（DecodeConfig 只读图片头）。
func TestTicketAttachmentUploadRejectsOversizedDimensions(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()

	_, err := fixture.service.Upload(ctx, 7, "wide.png", "image/png", attachmentTestPNG(t, TicketAttachmentMaxDimension+1, 1))
	require.ErrorIs(t, err, ErrTicketAttachmentDimensionExceeded)

	_, err = fixture.service.Upload(ctx, 7, "tall.png", "image/png", attachmentTestPNG(t, 1, TicketAttachmentMaxDimension+1))
	require.ErrorIs(t, err, ErrTicketAttachmentDimensionExceeded)

	item, err := fixture.service.Upload(ctx, 7, "edge.png", "image/png",
		attachmentTestPNG(t, TicketAttachmentMaxDimension, TicketAttachmentMaxDimension))
	require.NoError(t, err, "8000x8000 是允许的边界")
	require.Equal(t, TicketAttachmentMaxDimension, item.Width)
	require.Equal(t, TicketAttachmentMaxDimension, item.Height)
}

// TestTicketAttachmentUploadDedupesSameImage 覆盖 sha256 去重：同一用户重复上传同一张图
// 复用未绑定记录，不重复落盘。
func TestTicketAttachmentUploadDedupesSameImage(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()
	data := attachmentTestPNG(t, 5, 5)

	first, err := fixture.service.Upload(ctx, 7, "a.png", "image/png", data)
	require.NoError(t, err)
	second, err := fixture.service.Upload(ctx, 7, "b.png", "image/png", data)
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 1, fixture.repo.count(), "同图复用，不新增记录")

	// 不同用户相同图片各自独立（sha256 去重仅在同一 uploader 内）
	third, err := fixture.service.Upload(ctx, 8, "c.png", "image/png", data)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, third.ID)
}

// TestTicketAttachmentUploadDailyQuota 覆盖用户日配额 50 张：超限 429，次日恢复。
func TestTicketAttachmentUploadDailyQuota(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	fixture.clock = &now

	for i := 0; i < TicketAttachmentDailyQuota; i++ {
		fixture.repo.seed(TicketAttachment{
			UploaderID: 7,
			StorageKey: fmt.Sprintf("2026/11/seed-%d.png", i),
			SHA256:     fmt.Sprintf("%064d", i),
			ByteSize:   int64(i + 1),
			CreatedAt:  now.Add(-time.Minute),
		})
	}

	_, err := fixture.service.Upload(ctx, 7, "over.png", "image/png", attachmentTestPNG(t, 2, 2))
	require.ErrorIs(t, err, ErrTicketAttachmentDailyQuotaExceeded)

	// 其他用户不受影响
	_, err = fixture.service.Upload(ctx, 9, "other.png", "image/png", attachmentTestPNG(t, 2, 2))
	require.NoError(t, err)

	// 次日配额重置
	nextDay := now.Add(24 * time.Hour)
	fixture.clock = &nextDay
	_, err = fixture.service.Upload(ctx, 7, "tomorrow.png", "image/png", attachmentTestPNG(t, 2, 2))
	require.NoError(t, err)
}

// TestTicketAttachmentReadPermissionMatrix 覆盖 A5：提交人/上传者/管理员可读，其他人 404。
func TestTicketAttachmentReadPermissionMatrix(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()
	item, err := fixture.service.Upload(ctx, 7, "shot.png", "image/png", attachmentTestPNG(t, 4, 4))
	require.NoError(t, err)

	// 未绑定：上传者可读
	_, _, err = fixture.service.GetForRead(ctx, TicketAttachmentSubject{UserID: 7}, item.ID)
	require.NoError(t, err)

	// 未绑定：其他用户读不到（404，不暴露存在性）
	_, _, err = fixture.service.GetForRead(ctx, TicketAttachmentSubject{UserID: 8}, item.ID)
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)

	// 管理员可读
	_, _, err = fixture.service.GetForRead(ctx, TicketAttachmentSubject{UserID: 99, IsAdmin: true}, item.ID)
	require.NoError(t, err)

	// 绑定到用户 7 的工单后：提交人可读，其他用户仍 404
	ticket := fixture.tickets.seedTicket(7, "open", 1)
	_, err = fixture.service.BindReply(ctx, 7, ticket.ID, 1001, []int64{item.ID})
	require.NoError(t, err)

	_, _, err = fixture.service.GetForRead(ctx, TicketAttachmentSubject{UserID: 7}, item.ID)
	require.NoError(t, err)
	_, _, err = fixture.service.GetForRead(ctx, TicketAttachmentSubject{UserID: 8}, item.ID)
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)

	// 不存在的附件同样 404
	_, _, err = fixture.service.GetForRead(ctx, TicketAttachmentSubject{UserID: 7}, 99999)
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)
}

// TestTicketAttachmentDeleteUnboundOnly 覆盖删除：只有未绑定且属于本人（或管理员）才能删。
func TestTicketAttachmentDeleteUnboundOnly(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()
	item, err := fixture.service.Upload(ctx, 7, "shot.png", "image/png", attachmentTestPNG(t, 3, 3))
	require.NoError(t, err)
	fullPath := filepath.Join(fixture.root, filepath.FromSlash(item.StorageKey))

	// 他人删不得（按不存在处理）
	err = fixture.service.DeleteUnbound(ctx, TicketAttachmentSubject{UserID: 8}, item.ID)
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)
	_, statErr := os.Stat(fullPath)
	require.NoError(t, statErr, "越权删除不得影响文件")

	// 本人可删，磁盘文件同步删除
	require.NoError(t, fixture.service.DeleteUnbound(ctx, TicketAttachmentSubject{UserID: 7}, item.ID))
	_, statErr = os.Stat(fullPath)
	require.True(t, os.IsNotExist(statErr))
	require.Zero(t, fixture.repo.count())

	// 已绑定附件不可删（409）
	bound, err := fixture.service.Upload(ctx, 7, "bound.png", "image/png", attachmentTestPNG(t, 3, 4))
	require.NoError(t, err)
	ticket := fixture.tickets.seedTicket(7, "open", 1)
	_, err = fixture.service.BindReply(ctx, 7, ticket.ID, 2001, []int64{bound.ID})
	require.NoError(t, err)
	err = fixture.service.DeleteUnbound(ctx, TicketAttachmentSubject{UserID: 7}, bound.ID)
	require.ErrorIs(t, err, ErrTicketAttachmentAlreadyBound)

	// 管理员可删他人的未绑定附件
	adminTarget, err := fixture.service.Upload(ctx, 8, "admin-target.png", "image/png", attachmentTestPNG(t, 3, 5))
	require.NoError(t, err)
	require.NoError(t, fixture.service.DeleteUnbound(ctx, TicketAttachmentSubject{UserID: 99, IsAdmin: true}, adminTarget.ID))
}

// TestTicketAttachmentBindingLimits 覆盖 A1（4 张被拒 / 3 张通过）与 A6（绑定他人附件被拒）。
func TestTicketAttachmentBindingLimits(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()

	ids := make([]int64, 0, 4)
	for i := 0; i < 4; i++ {
		item, err := fixture.service.Upload(ctx, 7, fmt.Sprintf("shot-%d.png", i), "image/png",
			attachmentTestPNG(t, 2+i, 2))
		require.NoError(t, err)
		ids = append(ids, item.ID)
	}

	_, err := fixture.service.BindReply(ctx, 7, 10, 100, ids)
	require.ErrorIs(t, err, ErrTicketAttachmentLimitExceeded, "4 张必须被拒")

	// 3 张通过
	bound, err := fixture.service.BindReply(ctx, 7, 10, 100, ids[:3])
	require.NoError(t, err)
	require.Len(t, bound, 3)
	for _, item := range bound {
		require.NotNil(t, item.ReplyID)
		require.Equal(t, int64(100), *item.ReplyID)
	}

	// 重复绑定同一批 → 409
	_, err = fixture.service.BindReply(ctx, 7, 10, 100, ids[:1])
	require.ErrorIs(t, err, ErrTicketAttachmentAlreadyBound)

	// A6：用户 8 无法把用户 7 的附件绑到自己工单
	foreign, err := fixture.service.Upload(ctx, 7, "mine.png", "image/png", attachmentTestPNG(t, 7, 7))
	require.NoError(t, err)
	_, err = fixture.service.BindReply(ctx, 8, 11, 101, []int64{foreign.ID})
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)

	// 校验接口同样拒绝（工单落库前的只读校验）
	require.ErrorIs(t, fixture.service.ValidateBinding(ctx, 8, []int64{foreign.ID}), ErrTicketAttachmentNotFound)
	require.NoError(t, fixture.service.ValidateBinding(ctx, 7, []int64{foreign.ID}))

	// 不存在的附件 → 404
	_, err = fixture.service.BindReply(ctx, 7, 10, 100, []int64{123456})
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)
}

// TestTicketAttachmentBindEnforcesTicketCap 覆盖单工单累计附件上限 15（design.md §1.6）。
func TestTicketAttachmentBindEnforcesTicketCap(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()

	for i := 0; i < TicketAttachmentMaxPerTicket; i++ {
		fixture.repo.seed(TicketAttachment{
			UploaderID: 7,
			TicketID:   attachmentTestInt64Ptr(10),
			ReplyID:    attachmentTestInt64Ptr(int64(1000 + i)),
			StorageKey: fmt.Sprintf("2026/11/bound-%d.png", i),
			SHA256:     fmt.Sprintf("%064d", i),
			ByteSize:   1,
			CreatedAt:  time.Now().UTC(),
		})
	}

	extra, err := fixture.service.Upload(ctx, 7, "extra.png", "image/png", attachmentTestPNG(t, 2, 2))
	require.NoError(t, err)
	_, err = fixture.service.BindReply(ctx, 7, 10, 2000, []int64{extra.ID})
	require.ErrorIs(t, err, ErrTicketAttachmentTicketLimitReached)
}

// TestTicketAttachmentCleanupOrphans 覆盖 A7：只清理 reply_id IS NULL 且超过 2 小时的记录
// 与磁盘文件。
func TestTicketAttachmentCleanupOrphans(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	fixture.clock = &now

	put := func(key string) TicketAttachment {
		require.NoError(t, fixture.storage.Put(ctx, key, []byte("x"), TicketAttachmentContentTypePNG))
		return TicketAttachment{
			UploaderID: 7,
			StorageKey: key,
			SHA256:     strings.Repeat("a", 64),
			ByteSize:   1,
		}
	}

	oldOrphan := put("2026/11/orphan-old.png")
	oldOrphan.CreatedAt = now.Add(-3 * time.Hour)
	seededOld := fixture.repo.seed(oldOrphan)

	freshOrphan := put("2026/11/orphan-fresh.png")
	freshOrphan.CreatedAt = now.Add(-1 * time.Hour)
	seededFresh := fixture.repo.seed(freshOrphan)

	bound := put("2026/11/bound-old.png")
	bound.CreatedAt = now.Add(-5 * time.Hour)
	bound.TicketID = attachmentTestInt64Ptr(10)
	bound.ReplyID = attachmentTestInt64Ptr(100)
	seededBound := fixture.repo.seed(bound)

	removed, err := fixture.service.CleanupOrphans(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, removed, "只有超过 2 小时且未绑定的孤儿被清理")

	require.Nil(t, fixture.repo.get(seededOld.ID), "超期孤儿记录应删除")
	_, statErr := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(seededOld.StorageKey)))
	require.True(t, os.IsNotExist(statErr), "超期孤儿的磁盘文件应删除")

	require.NotNil(t, fixture.repo.get(seededFresh.ID), "未超期孤儿保留")
	require.NotNil(t, fixture.repo.get(seededBound.ID), "已绑定附件永不被清理")
	_, statErr = os.Stat(filepath.Join(fixture.root, filepath.FromSlash(seededBound.StorageKey)))
	require.NoError(t, statErr)

	// 幂等：再跑一次没有新删除
	removed, err = fixture.service.CleanupOrphans(ctx)
	require.NoError(t, err)
	require.Zero(t, removed)
}

// TestTicketAttachmentListReplyAttachmentsGroupsByReply 覆盖详情聚合：按 reply_id 分组。
func TestTicketAttachmentListReplyAttachmentsGroupsByReply(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ctx := context.Background()
	ticket := fixture.tickets.seedTicket(7, "open", 2)

	first, err := fixture.service.Upload(ctx, 7, "a.png", "image/png", attachmentTestPNG(t, 2, 2))
	require.NoError(t, err)
	second, err := fixture.service.Upload(ctx, 7, "b.png", "image/png", attachmentTestPNG(t, 3, 3))
	require.NoError(t, err)

	_, err = fixture.service.BindReply(ctx, 7, ticket.ID, 500, []int64{first.ID})
	require.NoError(t, err)
	_, err = fixture.service.BindReply(ctx, 7, ticket.ID, 501, []int64{second.ID})
	require.NoError(t, err)

	grouped, err := fixture.service.ListReplyAttachments(ctx, ticket.ID)
	require.NoError(t, err)
	require.Len(t, grouped, 2)
	require.Len(t, grouped[500], 1)
	require.Len(t, grouped[501], 1)
	require.Equal(t, first.ID, grouped[500][0].ID)
}

// TestTicketAttachmentSanitizeOriginalName 覆盖展示名清洗（不参与落盘路径）。
func TestTicketAttachmentSanitizeOriginalName(t *testing.T) {
	require.Equal(t, "shot.png", sanitizeTicketAttachmentName("shot.png", ".png"))
	require.Equal(t, "shot.png", sanitizeTicketAttachmentName(`C:\Users\a\shot.png`, ".png"))
	require.Equal(t, "shot.png", sanitizeTicketAttachmentName("/tmp/dir/shot.png", ".png"))
	require.Equal(t, "image.png", sanitizeTicketAttachmentName("   ", ".png"))
	require.Equal(t, "image.png", sanitizeTicketAttachmentName("..", ".png"))
	require.Equal(t, "abc", sanitizeTicketAttachmentName("a\x00b\x1fc", ".png"))
	require.LessOrEqual(t, len([]rune(sanitizeTicketAttachmentName(strings.Repeat("名", 400), ".png"))), 255)
}

// TestTicketAttachmentAdminReplyBindsAttachments 覆盖管理员回帖附图（t6）：
// 管理员上传 → AdminReply 带 attachment_ids → 事务内回填 reply_id → 管理端详情可回读。
func TestTicketAttachmentAdminReplyBindsAttachments(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ticketService := newFakeTicketServiceWith(fixture.tickets, &fakeTicketNotifier{})
	ticketService.SetAttachmentBinder(fixture.service)

	ctx := context.Background()
	ticket := fixture.tickets.seedTicket(7, domain.TicketStatusOpen, 1)

	const adminID int64 = 99
	first, err := fixture.service.Upload(ctx, adminID, "a.png", "image/png", attachmentTestPNG(t, 4, 3))
	require.NoError(t, err)
	second, err := fixture.service.Upload(ctx, adminID, "b.png", "image/png", attachmentTestPNG(t, 5, 3))
	require.NoError(t, err)

	result, err := ticketService.AdminReply(
		ctx, adminID, ticket.ID, "已修复，请看截图", false, nil, []int64{first.ID, second.ID},
	)
	require.NoError(t, err)
	require.Equal(t, domain.TicketAuthorRoleAdmin, result.Reply.AuthorRole)
	require.Len(t, result.Reply.Attachments, 2, "管理员回复响应必须回带已绑定附件")
	require.Equal(t, first.ID, result.Reply.Attachments[0].ID)

	// reply_id / ticket_id 已回填
	stored := fixture.repo.get(first.ID)
	require.NotNil(t, stored)
	require.NotNil(t, stored.ReplyID)
	require.Equal(t, result.Reply.ID, *stored.ReplyID)
	require.NotNil(t, stored.TicketID)
	require.Equal(t, ticket.ID, *stored.TicketID)

	// 管理端详情 replies[].attachments[] 与用户端一致
	detail, err := ticketService.AdminGetTicket(ctx, ticket.ID)
	require.NoError(t, err)
	last := detail.Replies[len(detail.Replies)-1]
	require.Equal(t, result.Reply.ID, last.ID)
	require.Len(t, last.Attachments, 2)
	require.Equal(t, second.ID, last.Attachments[1].ID)
}

// TestTicketAttachmentAdminReplyRejectsInvalidAttachments 覆盖管理员回帖附图的拒绝路径：
// 超 3 张 400、他人附件 404、已绑定 409，且失败时不得留下回复。
func TestTicketAttachmentAdminReplyRejectsInvalidAttachments(t *testing.T) {
	fixture := newTicketAttachmentFixture(t)
	ticketService := newFakeTicketServiceWith(fixture.tickets, &fakeTicketNotifier{})
	ticketService.SetAttachmentBinder(fixture.service)

	ctx := context.Background()
	ticket := fixture.tickets.seedTicket(7, domain.TicketStatusOpen, 1)
	const adminID int64 = 99
	baseline := len(fixture.tickets.replies[ticket.ID])

	ids := make([]int64, 0, 4)
	for i := 0; i < 4; i++ {
		item, err := fixture.service.Upload(ctx, adminID, fmt.Sprintf("a-%d.png", i), "image/png",
			attachmentTestPNG(t, 2+i, 2))
		require.NoError(t, err)
		ids = append(ids, item.ID)
	}

	// 4 张 → 400，且不落库回复
	_, err := ticketService.AdminReply(ctx, adminID, ticket.ID, "四张", false, nil, ids)
	require.ErrorIs(t, err, ErrTicketAttachmentLimitExceeded)
	require.Len(t, fixture.tickets.replies[ticket.ID], baseline, "超限时不得落库回复")

	// 借用用户上传的附件 → 按不存在处理
	foreign, err := fixture.service.Upload(ctx, 7, "user.png", "image/png", attachmentTestPNG(t, 3, 3))
	require.NoError(t, err)
	_, err = ticketService.AdminReply(ctx, adminID, ticket.ID, "借用用户附件", false, nil, []int64{foreign.ID})
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)
	require.Len(t, fixture.tickets.replies[ticket.ID], baseline)

	// 3 张通过
	bound, err := ticketService.AdminReply(ctx, adminID, ticket.ID, "三张", false, nil, ids[:3])
	require.NoError(t, err)
	require.Len(t, bound.Reply.Attachments, 3)

	// 重复绑定已占用附件 → 409
	_, err = ticketService.AdminReply(ctx, adminID, ticket.ID, "again", false, nil, ids[:1])
	require.ErrorIs(t, err, ErrTicketAttachmentAlreadyBound)
}

// --- 测试夹具 ---

type ticketAttachmentFixture struct {
	service *TicketAttachmentService
	repo    *fakeTicketAttachmentRepository
	storage TicketAttachmentStorage
	tickets *fakeTicketRepository
	root    string
	clock   *time.Time
}

func newTicketAttachmentFixture(t *testing.T) *ticketAttachmentFixture {
	t.Helper()
	root := t.TempDir()
	repo := newFakeTicketAttachmentRepository()
	storage := NewLocalTicketAttachmentStorage(root)
	tickets := newFakeTicketRepository()
	svc := NewTicketAttachmentService(repo, storage, tickets)
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	fixture := &ticketAttachmentFixture{
		service: svc,
		repo:    repo,
		storage: storage,
		tickets: tickets,
		root:    root,
		clock:   &now,
	}
	svc.SetClock(func() time.Time { return *fixture.clock })
	return fixture
}

func attachmentTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func attachmentTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func attachmentTestGIF(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, width, height), []color.Color{color.Black, color.White})
	var buf bytes.Buffer
	require.NoError(t, gif.Encode(&buf, img, nil))
	return buf.Bytes()
}

func attachmentTestInt64Ptr(v int64) *int64 { return &v }

// fakeTicketAttachmentRepository 是内存实现，用于不依赖数据库地验证服务规则。
type fakeTicketAttachmentRepository struct {
	mu    sync.Mutex
	items map[int64]*TicketAttachment
	next  int64
}

func newFakeTicketAttachmentRepository() *fakeTicketAttachmentRepository {
	return &fakeTicketAttachmentRepository{items: make(map[int64]*TicketAttachment)}
}

func (r *fakeTicketAttachmentRepository) clone(item TicketAttachment) TicketAttachment {
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

func (r *fakeTicketAttachmentRepository) seed(item TicketAttachment) *TicketAttachment {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	item.ID = r.next
	stored := r.clone(item)
	r.items[item.ID] = &stored
	copied := stored
	return &copied
}

func (r *fakeTicketAttachmentRepository) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

func (r *fakeTicketAttachmentRepository) get(id int64) *TicketAttachment {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return nil
	}
	copied := *item
	return &copied
}

func (r *fakeTicketAttachmentRepository) Insert(_ context.Context, item *TicketAttachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	item.ID = r.next
	stored := r.clone(*item)
	r.items[item.ID] = &stored
	return nil
}

func (r *fakeTicketAttachmentRepository) GetByID(_ context.Context, id int64) (*TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return nil, ErrTicketAttachmentNotFound
	}
	copied := r.clone(*item)
	return &copied, nil
}

func (r *fakeTicketAttachmentRepository) FindReusable(
	_ context.Context,
	uploaderID int64,
	sha256Hex string,
	byteSize int64,
) (*TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.items {
		if item.UploaderID == uploaderID && item.SHA256 == sha256Hex && item.ByteSize == byteSize && item.ReplyID == nil {
			copied := r.clone(*item)
			return &copied, nil
		}
	}
	return nil, nil
}

func (r *fakeTicketAttachmentRepository) ListByTicket(_ context.Context, ticketID int64) ([]TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TicketAttachment, 0)
	for _, item := range r.items {
		if item.TicketID != nil && *item.TicketID == ticketID {
			out = append(out, r.clone(*item))
		}
	}
	sortTicketAttachmentsByID(out)
	return out, nil
}

func (r *fakeTicketAttachmentRepository) BindReply(
	_ context.Context,
	ticketID, replyID, uploaderID int64,
	ids []int64,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 先全量校验再落库：模拟事务回滚语义。
	for _, id := range ids {
		item, ok := r.items[id]
		if !ok || item.UploaderID != uploaderID || item.ReplyID != nil {
			return ErrTicketAttachmentAlreadyBound
		}
	}
	for _, id := range ids {
		item := r.items[id]
		item.TicketID = attachmentTestInt64Ptr(ticketID)
		item.ReplyID = attachmentTestInt64Ptr(replyID)
	}
	return nil
}

func (r *fakeTicketAttachmentRepository) DeleteUnbound(_ context.Context, id int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok || item.ReplyID != nil {
		return false, nil
	}
	delete(r.items, id)
	return true, nil
}

func (r *fakeTicketAttachmentRepository) CountByUploaderSince(
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

func (r *fakeTicketAttachmentRepository) CountBoundByTicket(_ context.Context, ticketID int64) (int64, error) {
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

func (r *fakeTicketAttachmentRepository) ListOrphansBefore(
	_ context.Context,
	cutoff time.Time,
	limit int,
) ([]TicketAttachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TicketAttachment, 0)
	for _, item := range r.items {
		if item.ReplyID == nil && item.CreatedAt.Before(cutoff) {
			out = append(out, r.clone(*item))
		}
	}
	sortTicketAttachmentsByID(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeTicketAttachmentRepository) DeleteOrphanBefore(
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

func sortTicketAttachmentsByID(items []TicketAttachment) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1].ID > items[j].ID; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}
