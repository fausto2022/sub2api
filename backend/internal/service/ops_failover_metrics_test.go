package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestFailoverMetricsQueryKeepsLegacyAndDecisionCountsSeparate(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	collector := &OpsMetricsCollector{db: db}

	end := time.Date(2026, 11, 20, 0, 5, 0, 0, time.UTC)
	start := end.Add(-5 * time.Minute)
	groupID := int64(12)
	mock.ExpectQuery(`split_part\(ev->>'kind', ':', 1\) IN \('failover', 'retry_exhausted_failover', 'failover_on_400'\)`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"switch_count"}).AddRow(3))
	mock.ExpectQuery(`created_at >= \$1 AND created_at < \$2 AND \(\$3 = '' OR platform = \$3\) AND \(\$4::bigint IS NULL OR group_id = \$4\) AND is_count_tokens = FALSE.*COUNT\(DISTINCT request_id\) FILTER \(WHERE switched\).*COUNT\(DISTINCT request_id\) FILTER \(WHERE exhausted\)`).
		WithArgs(start, end, PlatformAnthropic, groupID, "failover_switch", "failover_exhausted").
		WillReturnRows(sqlmock.NewRows([]string{"switched", "exhausted"}).AddRow(2, 1))

	legacyCount, err := collector.queryAccountSwitchCount(context.Background(), start, end)
	require.NoError(t, err)
	require.EqualValues(t, 3, legacyCount)
	counts, err := collector.QueryFailoverRequestCounts(context.Background(), &OpsDashboardFilter{
		StartTime: start, EndTime: end, Platform: " ANTHROPIC ", GroupID: &groupID,
	})
	require.NoError(t, err)
	require.Equal(t, OpsFailoverRequestCounts{Switched: 2, Exhausted: 1}, counts)
	require.NoError(t, mock.ExpectationsWereMet())
}

type failoverMetricsWindowRepo struct {
	OpsRepository
	window *OpsWindowStats
	filter *OpsDashboardFilter
	err    error
}

func (r *failoverMetricsWindowRepo) GetWindowStats(_ context.Context, filter *OpsDashboardFilter) (*OpsWindowStats, error) {
	r.filter = filter
	return r.window, r.err
}

func TestFailoverMetricAlertEvaluator(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 11, 20, 0, 5, 0, 0, time.UTC)
	start := end.Add(-5 * time.Minute)
	groupID := int64(12)
	for _, tc := range []struct {
		metric    string
		value     float64
		threshold float64
	}{
		{OpsFailoverSwitchRatioMetric, 30, 30},
		{OpsFailoverExhaustedRatioMetric, 10, 5},
	} {
		t.Run(tc.metric, func(t *testing.T) {
			repo := &failoverMetricsWindowRepo{window: &OpsWindowStats{SuccessCount: 8, ErrorCountTotal: 2}}
			svc := &OpsAlertEvaluatorService{opsRepo: repo}
			svc.failoverRequestCounts = func(_ context.Context, filter *OpsDashboardFilter) (OpsFailoverRequestCounts, error) {
				require.Equal(t, &OpsDashboardFilter{StartTime: start, EndTime: end, GroupID: &groupID, Platform: PlatformAnthropic, QueryMode: OpsQueryModeRaw}, filter)
				require.Same(t, repo.filter, filter)
				return OpsFailoverRequestCounts{Switched: 3, Exhausted: 1}, nil
			}
			rule := &OpsAlertRule{MetricType: tc.metric, Operator: ">=", Threshold: tc.threshold}
			value, ok := svc.computeRuleMetric(context.Background(), rule, nil, start, end, PlatformAnthropic, &groupID)
			require.True(t, ok)
			require.Equal(t, tc.value, value)
			require.True(t, compareMetric(value, rule.Operator, rule.Threshold))
		})
	}
}

func TestFailoverMetricAlertEvaluatorSkipsUnavailableSamples(t *testing.T) {
	t.Parallel()
	end := time.Now()
	start := end.Add(-time.Minute)
	rule := &OpsAlertRule{MetricType: OpsFailoverSwitchRatioMetric}
	for _, tc := range []struct {
		name       string
		traffic    OpsWindowStats
		trafficErr error
		counts     OpsFailoverRequestCounts
		countErr   error
	}{
		{name: "no traffic"},
		{name: "traffic unavailable", trafficErr: errors.New("traffic unavailable")},
		{name: "decisions unavailable", traffic: OpsWindowStats{SuccessCount: 4}, countErr: errors.New("decision counts unavailable")},
		{name: "inconsistent ingestion", traffic: OpsWindowStats{SuccessCount: 2}, counts: OpsFailoverRequestCounts{Switched: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpsAlertEvaluatorService{
				opsRepo: &failoverMetricsWindowRepo{window: &tc.traffic, err: tc.trafficErr},
				failoverRequestCounts: func(context.Context, *OpsDashboardFilter) (OpsFailoverRequestCounts, error) {
					return tc.counts, tc.countErr
				},
			}
			_, ok := svc.computeRuleMetric(context.Background(), rule, nil, start, end, "", nil)
			require.False(t, ok)
		})
	}
}
