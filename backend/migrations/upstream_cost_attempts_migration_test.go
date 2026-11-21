package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamCostAttemptsMigration(t *testing.T) {
	content, err := FS.ReadFile("255_upstream_cost_attempts.sql")
	require.NoError(t, err)
	ddl := strings.Join(strings.Fields(string(content)), " ")
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS upstream_cost_attempts (",
		"execution_id UUID NOT NULL",
		"attempt_index INTEGER NOT NULL CHECK (attempt_index > 0)",
		"account_id BIGINT NOT NULL CHECK (account_id > 0)",
		"status SMALLINT NULL CHECK (status BETWEEN 100 AND 999)",
		"elapsed_ms BIGINT NOT NULL CHECK (elapsed_ms >= 0)",
		"billed_usage JSONB NULL CHECK (jsonb_typeof(billed_usage) = 'object')",
		"decision VARCHAR(64) NOT NULL CHECK (btrim(decision) <> '')",
		"created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"PRIMARY KEY (execution_id, attempt_index)",
	} {
		require.Contains(t, ddl, fragment)
	}
	require.NotContains(t, ddl, "usage_billing_dedup")
	require.NotContains(t, ddl, "api_key_id")
}
