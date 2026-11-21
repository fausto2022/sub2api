package admin

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsAlertFailoverRatioPayload(t *testing.T) {
	t.Parallel()
	for _, metric := range []string{service.OpsFailoverSwitchRatioMetric, service.OpsFailoverExhaustedRatioMetric} {
		t.Run(metric, func(t *testing.T) {
			for _, tc := range []struct {
				threshold float64
				valid     bool
			}{{0, true}, {30, true}, {100, true}, {-1, false}, {101, false}} {
				t.Run(fmt.Sprint(tc.threshold), func(t *testing.T) {
					var raw map[string]json.RawMessage
					err := json.Unmarshal([]byte(fmt.Sprintf(`{"name":"group failover","metric_type":%q,"operator":">=","threshold":%g,"window_minutes":5,"filters":{"group_id":12}}`, metric, tc.threshold)), &raw)
					require.NoError(t, err)
					input, err := validateOpsAlertRulePayload(raw)
					if !tc.valid {
						require.ErrorContains(t, err, "threshold must be between 0 and 100")
						return
					}
					require.NoError(t, err)
					require.Equal(t, metric, input.MetricType)
					require.Equal(t, tc.threshold, input.Threshold)
					require.Equal(t, 5, input.WindowMinutes)
				})
			}
		})
	}
}
