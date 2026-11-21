// 工单附件迁移 252 的契约断言。
//
// 迁移 SQL 与 embed 源位于 backend/migrations（单一来源）；本包只做跨包只读断言，
// 确保 `go test ./internal/migrations/...` 能验证附件迁移已随二进制嵌入且语义完整
// （表结构、两个级联外键、三个索引，其中孤儿索引为部分索引）。
package migrations

import (
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestTicketAttachmentsMigrationCreatesTableColumnsIndexesAndCascades(t *testing.T) {
	content, err := dbmigrations.FS.ReadFile("252_ticket_attachments.sql")
	require.NoError(t, err, "迁移 252_ticket_attachments.sql 必须被 embed 到二进制中")

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 幂等建表
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS ticket_attachments")

	// 字段与两个外键（工单/回复硬删时级联清理附件行）
	require.Contains(t, sql, "id BIGSERIAL PRIMARY KEY")
	require.Contains(t, sql, "ticket_id BIGINT NULL REFERENCES tickets(id) ON DELETE CASCADE")
	require.Contains(t, sql, "reply_id BIGINT NULL REFERENCES ticket_replies(id) ON DELETE CASCADE")
	require.Contains(t, sql, "uploader_id BIGINT NOT NULL")
	require.Contains(t, sql, "storage_key TEXT NOT NULL")
	require.Contains(t, sql, "storage_kind VARCHAR(16) NOT NULL DEFAULT 'local'")
	require.Contains(t, sql, "original_name VARCHAR(255) NOT NULL")
	require.Contains(t, sql, "content_type VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "byte_size BIGINT NOT NULL")
	require.Contains(t, sql, "width INT NULL")
	require.Contains(t, sql, "height INT NULL")
	require.Contains(t, sql, "sha256 CHAR(64) NOT NULL")
	require.Contains(t, sql, "created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")

	// 三个索引：工单、回复、孤儿清理（部分索引，仅覆盖 reply_id IS NULL）
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_ticket_attachments_ticket ON ticket_attachments (ticket_id)")
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_ticket_attachments_reply ON ticket_attachments (reply_id)")
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_ticket_attachments_orphan ON ticket_attachments (created_at) WHERE reply_id IS NULL")

	// 两阶段上传：回复绑定前 ticket_id/reply_id 必须允许为空
	require.NotContains(t, sql, "ticket_id BIGINT NOT NULL")
	require.NotContains(t, sql, "reply_id BIGINT NOT NULL")

	// 不触碰既有表结构
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE USERS")
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE TICKETS")
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE TICKET_REPLIES")
}
