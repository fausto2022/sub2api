package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFailoverTelemetryLegacyFilters(t *testing.T) {
	const predicate = "error_phase <> 'failover_telemetry'"
	start := time.Now().UTC().Truncate(time.Hour)
	end := start.Add(time.Hour)
	filter := &service.OpsDashboardFilter{StartTime: start, EndTime: end}
	where, args, _ := buildErrorWhere(filter, start, end, 1)
	require.Contains(t, where, predicate)
	require.Equal(t, []any{start, end}, args)
	for _, f := range []*service.OpsErrorLogFilter{{}, {Phase: "upstream", IncludeRecoveredUpstream: true}} {
		where, _ := buildOpsErrorLogsWhere(f)
		require.Contains(t, where, "e."+predicate)
	}

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := &opsRepository{db: db}
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM usage_logs`).WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM ops_error_logs .*error_phase <> 'failover_telemetry'`).WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	exists, err := r.rawOpsDataExists(context.Background(), filter, start, end)
	require.NoError(t, err)
	require.False(t, exists)
	mock.ExpectExec(`error_base AS \(.*FROM ops_error_logs.*error_phase <> 'failover_telemetry' \), error_agg AS \(`).WithArgs(start, end).
		WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, r.UpsertHourlyMetrics(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}
