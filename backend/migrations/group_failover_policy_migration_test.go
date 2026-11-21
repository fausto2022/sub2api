package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupFailoverPolicyMigration(t *testing.T) {
	content, err := FS.ReadFile("254_group_failover_policy.sql")
	require.NoError(t, err)

	// Match complete statements, so extra clauses (DEFAULT, NOT NULL, extra
	// columns) or any unrelated DDL cannot pass as harmless substrings.
	addColumn := regexp.MustCompile(`(?i)^ALTER TABLE groups ADD COLUMN IF NOT EXISTS (\w+) (BOOLEAN|INT|JSONB) NULL$`)
	comment := regexp.MustCompile(`(?i)^COMMENT ON COLUMN groups\.(\w+) IS '(?:[^']|'')*'$`)
	columns := map[string]string{}
	comments := map[string]int{}
	var lines []string
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	sql := strings.TrimSpace(strings.Join(lines, "\n"))
	require.True(t, strings.HasSuffix(sql, ";"), "last SQL statement lacks a terminator")
	// The comments contain string literals. Keep any quoted semicolons in
	// their statements while recognizing ordinary SQL statement terminators.
	statement := regexp.MustCompile(`(?:[^';]|'(?:[^']|'')*')*;`)
	var parsed strings.Builder
	for _, raw := range statement.FindAllString(sql, -1) {
		parsed.WriteString(raw)
		text := strings.Join(strings.Fields(strings.TrimSuffix(raw, ";")), " ")
		if m := addColumn.FindStringSubmatch(text); m != nil {
			name := strings.ToLower(m[1])
			require.NotContains(t, columns, name, "duplicate column addition")
			columns[name] = strings.ToUpper(m[2])
			continue
		}
		if m := comment.FindStringSubmatch(text); m != nil {
			comments[strings.ToLower(m[1])]++
			continue
		}
		t.Fatalf("unexpected migration statement: %s", text)
	}
	require.Equal(t, sql, parsed.String(), "all SQL must be consumed")
	require.Equal(t, map[string]string{
		"failover_on_400":               "BOOLEAN",
		"failover_max_account_switches": "INT",
		"failover_policy":               "JSONB",
	}, columns)
	require.Equal(t, map[string]int{
		"failover_on_400":               1,
		"failover_max_account_switches": 1,
		"failover_policy":               1,
	}, comments)
}
