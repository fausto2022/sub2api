// 网关账号级容灾迁移 254 的契约断言。
//
// 迁移 SQL 与 embed 源位于 backend/migrations（单一来源）；本包只做跨包只读断言，
// 确保 `go test ./internal/migrations/...` 能验证该迁移已随二进制嵌入且语义完整：
// groups 上恰好三个**可空且无默认值**的列 + 三条列注释 + 幂等 DDL。
package migrations

import (
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const groupFailoverPolicyMigrationFile = "254_group_failover_policy.sql"

func readGroupFailoverPolicyMigration(t *testing.T) string {
	t.Helper()
	content, err := dbmigrations.FS.ReadFile(groupFailoverPolicyMigrationFile)
	require.NoError(t, err, "迁移 254 必须被 embed 到二进制中")

	// 先剥掉 `--` 行注释：文件头注释会解释「禁止 DEFAULT / NOT NULL」，
	// 断言必须只看**可执行 SQL**，否则注释里的否定词会把断言反向打红。
	sqlLines := make([]string, 0, 8)
	for _, line := range strings.Split(string(content), "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		sqlLines = append(sqlLines, line)
	}
	// 折叠空白，便于用一行子串断言跨行的 ALTER/COMMENT。
	return strings.Join(strings.Fields(strings.Join(sqlLines, " ")), " ")
}

func TestGroupFailoverPolicyMigrationAddsThreeNullableColumns(t *testing.T) {
	sql := readGroupFailoverPolicyMigration(t)

	// 三列：单开关 / 切换次数 / 完整策略 JSONB。
	require.Contains(t, sql,
		"ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_on_400 BOOLEAN NULL")
	require.Contains(t, sql,
		"ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_max_account_switches INT NULL")
	require.Contains(t, sql,
		"ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_policy JSONB NULL")
}

func TestGroupFailoverPolicyMigrationForbidsNonNullableOrDefaultedColumns(t *testing.T) {
	sql := readGroupFailoverPolicyMigration(t)
	upper := strings.ToUpper(sql)

	// DEFAULT false 会把「未配置（回落全局）」静默变成「配置了 false」，
	// 而 failover_on_400 的 false 是有效语义 —— design §3.3.5 的指针语义依赖「NULL
	// 就是 NULL」，所以这两个词在本迁移里一次都不许出现。
	require.NotContains(t, upper, "DEFAULT", "三列必须没有默认值，NULL 才表示未覆盖")
	require.NotContains(t, upper, "NOT NULL", "三列必须可空")

	// 幂等：同一 SQL 连续应用两次不报错。
	require.Equal(t, 3, strings.Count(sql, "ADD COLUMN IF NOT EXISTS"),
		"三列都必须是 IF NOT EXISTS 的幂等 DDL")
}

func TestGroupFailoverPolicyMigrationCommentsColumns(t *testing.T) {
	sql := readGroupFailoverPolicyMigration(t)

	require.Contains(t, sql, "COMMENT ON COLUMN groups.failover_on_400")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.failover_max_account_switches")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.failover_policy")
	require.Equal(t, 3, strings.Count(sql, "COMMENT ON COLUMN"))
}

func TestGroupFailoverPolicyMigrationTouchesOnlyGroups(t *testing.T) {
	sql := readGroupFailoverPolicyMigration(t)
	upper := strings.ToUpper(sql)

	// P0 只加 groups 三列：不建表、不动 accounts/ent 相关表与既有计费链路。
	require.NotContains(t, upper, "CREATE TABLE")
	require.NotContains(t, upper, "ALTER TABLE ACCOUNTS")
	require.NotContains(t, upper, "ALTER TABLE USAGE_LOGS")
	require.NotContains(t, upper, "ALTER TABLE CHANNELS")
	require.NotContains(t, upper, "DROP ")
	require.Equal(t, 3, strings.Count(upper, "ALTER TABLE GROUPS"))
}

func TestGroupFailoverPolicyMigrationNumberIsUnique(t *testing.T) {
	entries, err := dbmigrations.FS.ReadDir(".")
	require.NoError(t, err)

	matched := make([]string, 0, 1)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "254_") && strings.HasSuffix(entry.Name(), ".sql") {
			matched = append(matched, entry.Name())
		}
	}
	require.Equal(t, []string{groupFailoverPolicyMigrationFile}, matched,
		"迁移编号 254 必须唯一（当前最大编号 253）")
}
