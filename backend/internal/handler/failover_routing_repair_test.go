package handler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type repairRoutingRepo struct {
	service.AccountRepository
	writes int
}

func (r *repairRoutingRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.writes++
	return nil
}

type repairRoutingUpstream struct {
	service.HTTPUpstream
	t        *testing.T
	accounts []int64
	slots    *t28IsolationSlots
	repo     *repairRoutingRepo
}

func (u *repairRoutingUpstream) DoWithTLS(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.accounts = append(u.accounts, id)
	require.Equal(u.t, 1, u.slots.held[id])
	if id == 1 {
		require.Zero(u.t, u.repo.writes)
		return &http.Response{StatusCode: 502, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"type":"api_error","message":"unavailable"}}`))}, nil
	}
	require.Equal(u.t, 1, u.repo.writes)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(costAuditPartialSSE + costAuditFinalSSE))}, nil
}

func TestFailoverRoutingRepairOriginalAndAdmittedSwitch(t *testing.T) {
	for _, deny := range []bool{false, true} {
		name := "replacement"
		if deny {
			name = "budget_denied"
		}
		t.Run(name, func(t *testing.T) {
			h, group := newCostAuditGateway(t, &costAuditGatewayUpstream{}, &costAuditGatewayBilling{})
			h.cfg.Gateway.Scheduling.LoadBatchEnabled = true
			h.cfg.Gateway.FailoverPolicy.Enabled = true
			if deny {
				h.cfg.Gateway.FailoverPolicy.Budget.MaxTotalUpstreamAttempts = 2
			}
			accounts := make([]*service.Account, 0, 2)
			for id := int64(1); id <= 2; id++ {
				accounts = append(accounts, &service.Account{ID: id, Priority: int(id), Platform: group.Platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
					Credentials:   map[string]any{"api_key": "test", "pool_mode": true, "pool_mode_retry_count": float64(1), "pool_mode_retry_status_codes": []any{float64(502)}},
					AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: group.ID}}})
			}
			slots := &t28IsolationSlots{held: make(map[int64]int), released: make(map[int64]int)}
			repo := &repairRoutingRepo{}
			upstream := &repairRoutingUpstream{t: t, slots: slots, repo: repo}
			concurrency := service.NewConcurrencyService(slots)
			snapshot := service.NewSchedulerSnapshotService(costAuditGatewaySched{accounts: accounts}, nil, nil, nil, nil)
			gw := service.NewGatewayService(repo, costAuditGatewayGroup{group: group}, nil, &costAuditGatewayBilling{}, nil, nil, nil, nil, h.cfg, snapshot, concurrency, service.NewBillingService(h.cfg, nil), nil, nil, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			gw.EnableHTTPFailoverObservation()
			h.gatewayService = gw
			h.concurrencyHelper = NewConcurrencyHelper(concurrency, SSEPingFormatClaude, 0)
			res := serveCostAuditResponses(h, group)
			if deny {
				require.Equal(t, []int64{1, 1}, upstream.accounts, res.Body.String())
				require.Zero(t, repo.writes)
			} else {
				require.Equal(t, []int64{1, 1, 2}, upstream.accounts, res.Body.String())
				require.Equal(t, 1, repo.writes)
			}
			for _, count := range slots.held {
				require.Zero(t, count)
			}
		})
	}
}
