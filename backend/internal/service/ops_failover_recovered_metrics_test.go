package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFailoverRecoveredMetricsPrefersExecutionID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	collector := &OpsMetricsCollector{db: db}
	end := time.Now().UTC()
	start := end.Add(-5 * time.Minute)
	group := int64(42)
	mock.ExpectQuery(`'execution:' \|\| \(ev->>'execution_id'\).*'legacy:' \|\| BTRIM\(o.request_id\).*jsonb_typeof\(o.upstream_errors\) = 'array'.*COUNT\(DISTINCT request_id\) FILTER \(WHERE switched\).*COUNT\(DISTINCT row_id\) FILTER \(WHERE switched AND request_id IS NULL\)`).
		WithArgs(start, end, PlatformGemini, group, OpsFailoverSwitchEvent, OpsFailoverExhaustedEvent).
		WillReturnRows(sqlmock.NewRows([]string{"switched", "exhausted"}).AddRow(2, 1))
	counts, err := collector.QueryFailoverRequestCounts(context.Background(), &OpsDashboardFilter{StartTime: start, EndTime: end, Platform: PlatformGemini, GroupID: &group})
	require.NoError(t, err)
	require.Equal(t, OpsFailoverRequestCounts{Switched: 2, Exhausted: 1}, counts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFailoverRecoveredMetricsExecutionContext(t *testing.T) {
	root := context.Background()
	require.Equal(t, uuid.Nil, GatewayExecutionIDFromContext(root))
	ctx, id := EnsureGatewayExecutionID(root)
	other, otherID := EnsureGatewayExecutionID(root)
	require.NotEqual(t, id, otherID)
	require.Equal(t, otherID, GatewayExecutionIDFromContext(other))
	same, again := EnsureGatewayExecutionID(context.WithoutCancel(ctx))
	require.Equal(t, id, again)
	require.Equal(t, id, GatewayExecutionIDFromContext(same))
}
