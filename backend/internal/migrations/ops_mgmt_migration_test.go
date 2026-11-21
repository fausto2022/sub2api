// 运营管理迁移 253 的契约断言。
//
// 迁移 SQL 与 embed 源位于 backend/migrations（单一来源）；本包只做跨包只读断言，
// 确保 `go test ./internal/migrations/...` 能验证运营管理迁移已随二进制嵌入且语义完整：
// 六张表、唯一约束/索引、以及 groups / channels 上的运营扩展列。
//
// 命名说明：仓库既有 `ops_*` 前缀属于可观测性模块，本模块全栈统一 `ops_mgmt_`。
package migrations

import (
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func readOpsMgmtMigration(t *testing.T) string {
	t.Helper()
	content, err := dbmigrations.FS.ReadFile("253_ops_mgmt.sql")
	require.NoError(t, err, "迁移 253_ops_mgmt.sql 必须被 embed 到二进制中")
	return strings.Join(strings.Fields(string(content)), " ")
}

func TestOpsMgmtMigrationCreatesSixTables(t *testing.T) {
	sql := readOpsMgmtMigration(t)

	for _, table := range []string{
		"opsmgmt_upstream_sites",
		"opsmgmt_upstream_groups",
		"opsmgmt_upstream_accounts",
		"opsmgmt_profit_checks",
		"opsmgmt_guard_locks",
		"opsmgmt_settings",
	} {
		require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS "+table, "缺少表 %s", table)
	}

	// 上游站点：账号密码登录 + 登录态缓存 + 上游普通 Key（非管理员 Key）
	require.Contains(t, sql, "password_enc TEXT NOT NULL")
	require.Contains(t, sql, "access_token_enc TEXT NULL")
	require.Contains(t, sql, "refresh_token_enc TEXT NULL")
	require.Contains(t, sql, "token_expires_at TIMESTAMPTZ NULL")
	require.Contains(t, sql, "upstream_api_key_enc TEXT NULL")
	require.Contains(t, sql, "sync_interval_minutes INT NOT NULL DEFAULT 30")
	require.Contains(t, sql, "last_error TEXT NULL")

	// 上游分组：ratio / rate_multiplier / peak / image / video / 分组级最低利润率
	require.Contains(t, sql, "ratio NUMERIC(10,4) NOT NULL DEFAULT 1")
	require.Contains(t, sql, "rate_multiplier NUMERIC(10,4) NOT NULL DEFAULT 1")
	require.Contains(t, sql, "peak_enabled BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "peak_start VARCHAR(8) NULL")
	require.Contains(t, sql, "peak_end VARCHAR(8) NULL")
	require.Contains(t, sql, "peak_multiplier NUMERIC(10,4) NOT NULL DEFAULT 1")
	require.Contains(t, sql, "image_separate_rate BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "video_separate_rate BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "min_margin_basis_points BIGINT NULL")

	// 上游账号：credentials / group_ids / rate_multiplier / schedulable
	require.Contains(t, sql, "remote_account_id BIGINT NOT NULL")
	require.Contains(t, sql, "credentials JSONB NULL")
	require.Contains(t, sql, "group_ids BIGINT[] NULL")
	require.Contains(t, sql, "rate_multiplier NUMERIC(10,4) NULL")
	require.Contains(t, sql, "schedulable BOOLEAN NULL")

	// 利润检查：四种状态由应用层写入，表侧只约束字段完备
	require.Contains(t, sql, "status VARCHAR(16) NOT NULL")
	require.Contains(t, sql, "margin_basis_points BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "cost_reliable BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "cost_reason TEXT NULL")

	// 守护锁：active 幂等唯一
	require.Contains(t, sql, "lock_type VARCHAR(32) NOT NULL")
	require.Contains(t, sql, "active BOOLEAN NOT NULL DEFAULT TRUE")
	require.Contains(t, sql, "cleared_at TIMESTAMPTZ NULL")

	// 运营配置
	require.Contains(t, sql, "key VARCHAR(64) PRIMARY KEY")
	require.Contains(t, sql, "value JSONB NOT NULL")
}

func TestOpsMgmtMigrationUniqueConstraintsAndIndexes(t *testing.T) {
	sql := readOpsMgmtMigration(t)

	// 站点名唯一（避免重复添加同一上游）
	require.Contains(t, sql,
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_upstream_sites_name ON opsmgmt_upstream_sites (name)")
	// 上游分组/账号按 (site, remote id) 唯一
	require.Contains(t, sql,
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_upstream_groups_site_remote ON opsmgmt_upstream_groups (site_id, remote_group_id)")
	require.Contains(t, sql,
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_upstream_accounts_site_remote ON opsmgmt_upstream_accounts (site_id, remote_account_id)")
	// 守护锁幂等：active 行部分唯一索引
	require.Contains(t, sql,
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_guard_locks_active ON opsmgmt_guard_locks (site_id, target_ref, lock_type) WHERE active")
	// 利润检查的时间线查询索引
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_opsmgmt_profit_checks_group ON opsmgmt_profit_checks (site_id, remote_group_id, created_at DESC)")
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_opsmgmt_profit_checks_channel ON opsmgmt_profit_checks (channel_ref, created_at DESC)")

	// 外键：分组/账号随站点级联删除
	require.Contains(t, sql,
		"site_id BIGINT NOT NULL REFERENCES opsmgmt_upstream_sites(id) ON DELETE CASCADE")
}

func TestOpsMgmtMigrationLocalExtensions(t *testing.T) {
	sql := readOpsMgmtMigration(t)

	// 分组级最低利润率（就近覆盖：上游分组 > 本地分组 > 全局）
	require.Contains(t, sql,
		"ALTER TABLE groups ADD COLUMN IF NOT EXISTS opsmgmt_min_margin_basis_points BIGINT NULL")
	require.Contains(t, sql,
		"ALTER TABLE opsmgmt_upstream_groups ADD COLUMN IF NOT EXISTS min_margin_basis_points BIGINT NULL")

	// 扩池 = 对接渠道：channels 上的运营标签与自动停用标记
	require.Contains(t, sql, "ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_site_id BIGINT NULL")
	require.Contains(t, sql, "ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_remote_group_id BIGINT NULL")
	require.Contains(t, sql, "ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_auto_disabled_at TIMESTAMPTZ NULL")
	require.Contains(t, sql, "ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_auto_disabled_reason VARCHAR(64) NULL")
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_channels_opsmgmt_site ON channels (opsmgmt_site_id)")
	require.Contains(t, sql,
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_opsmgmt_unique ON channels (opsmgmt_site_id, opsmgmt_remote_group_id, opsmgmt_upstream_key_hash) WHERE opsmgmt_site_id IS NOT NULL AND opsmgmt_upstream_key_hash IS NOT NULL")

	// 不触碰计费链路与工单模块（利润数据源是 usage_logs 只读聚合）
	upper := strings.ToUpper(sql)
	require.NotContains(t, upper, "ALTER TABLE USAGE_LOGS")
	require.NotContains(t, upper, "ALTER TABLE TICKETS")
	require.NotContains(t, upper, "ALTER TABLE TICKET_ATTACHMENTS")
	require.NotContains(t, upper, "DELETE FROM USAGE_LOGS")
}
