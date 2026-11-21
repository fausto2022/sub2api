// Package migrations 提供契约验证路径（./internal/migrations/...）下的工单迁移断言。
//
// 迁移 SQL 与 embed 源位于 backend/migrations（单一来源）；本包只做跨包只读断言，
// 确保 `go test ./internal/migrations/...` 能验证工单迁移已随二进制嵌入且语义完整。
// 原先 backend/migrations/tickets_migration_test.go 与本文件断言同一份 SQL，已合并至此一份。
package migrations

import (
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestTicketsMigrationCreatesTablesColumnsAndCascades(t *testing.T) {
	content, err := dbmigrations.FS.ReadFile("251_tickets.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 幂等建表
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS tickets")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS ticket_replies")

	// tickets 字段
	require.Contains(t, sql, "id BIGSERIAL PRIMARY KEY")
	require.Contains(t, sql, "user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE")
	require.Contains(t, sql, "subject VARCHAR(200) NOT NULL")
	require.Contains(t, sql, "category VARCHAR(40) NOT NULL DEFAULT 'general'")
	require.Contains(t, sql, "priority VARCHAR(16) NOT NULL DEFAULT 'normal'")
	require.Contains(t, sql, "status VARCHAR(16) NOT NULL DEFAULT 'open'")
	require.Contains(t, sql, "last_reply_at TIMESTAMPTZ NULL")
	require.Contains(t, sql, "last_reply_by VARCHAR(16) NULL")
	require.Contains(t, sql, "reply_count INT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	require.Contains(t, sql, "updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")

	// tickets 索引（幂等）
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_tickets_user_created_at ON tickets (user_id, created_at DESC)")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_tickets_status_last_reply_at ON tickets (status, last_reply_at DESC)")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_tickets_created_at ON tickets (created_at DESC)")

	// ticket_replies 字段与外键
	require.Contains(t, sql, "ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE")
	require.Contains(t, sql, "author_id BIGINT NOT NULL")
	require.Contains(t, sql, "author_role VARCHAR(16) NOT NULL")
	require.Contains(t, sql, "content TEXT NOT NULL")
	require.Contains(t, sql, "email_notified BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_ticket_replies_ticket_created_at ON ticket_replies (ticket_id, created_at ASC)")

	// 不触碰既有表结构
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE USERS")
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE ACCOUNTS")
}
