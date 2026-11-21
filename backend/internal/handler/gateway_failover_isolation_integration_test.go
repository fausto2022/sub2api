package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type t28IsolationStore struct {
	service.FailoverRecoveryStore
	reads     int
	busy      int64
	pending   int64
	grants    []int64
	writes    []int64
	outcomes  []service.FailoverProbeOutcome
	onAcquire func(int64)
}

func (s *t28IsolationStore) ReadRecoveryBatch(_ context.Context, scopes []service.FailoverCooldownScope) service.FailoverCooldownSnapshot {
	s.reads++
	result := service.FailoverCooldownSnapshot{Pending: make(map[service.FailoverCooldownScope]struct{})}
	for _, scope := range scopes {
		if scope.Model == "" && scope.AccountID == s.pending {
			result.Pending[scope] = struct{}{}
		}
	}
	return result
}
func (s *t28IsolationStore) AcquireRecoveryProbe(_ context.Context, scope service.FailoverCooldownScope) service.FailoverProbeAcquisition {
	if s.onAcquire != nil {
		s.onAcquire(scope.AccountID)
	}
	s.grants = append(s.grants, scope.AccountID)
	if scope.AccountID == s.busy {
		return service.FailoverProbeAcquisition{Status: service.FailoverProbeBusy}
	}
	return service.FailoverProbeAcquisition{Status: service.FailoverProbeAcquired, Lease: service.FailoverProbeLease{Scope: scope, Token: "probe"}}
}
func (s *t28IsolationStore) CompleteRecoveryProbe(_ context.Context, _ service.FailoverProbeLease, outcome service.FailoverProbeOutcome, _ *service.FailoverPolicy) service.FailoverProbeCompletion {
	s.outcomes = append(s.outcomes, outcome)
	return service.FailoverProbeCompletion{Applied: true}
}
func (*t28IsolationStore) Clear(context.Context, int64)                                  {}
func (*t28IsolationStore) IncrFailureStreak(context.Context, int64, time.Duration) int64 { return 3 }
func (s *t28IsolationStore) CoolWithRecovery(_ context.Context, w service.FailoverCooldownWrite, _ bool) service.FailoverCooldownWriteResult {
	s.writes = append(s.writes, w.AccountID)
	return service.FailoverCooldownWriteResult{Applied: true}
}

type t28IsolationSlots struct {
	costAuditGatewaySlots
	held     map[int64]int
	released map[int64]int
}

func (s *t28IsolationSlots) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	s.held[id]++
	return true, nil
}
func (s *t28IsolationSlots) ReleaseAccountSlot(_ context.Context, id int64, _ string) error {
	s.held[id]--
	s.released[id]++
	return nil
}

func newT28IsolationGateway(t *testing.T, upstream *costAuditGatewayUpstream, store *t28IsolationStore) (*GatewayHandler, *service.Group, *t28IsolationSlots) {
	t.Helper()
	h, group := newCostAuditGateway(t, upstream, &costAuditGatewayBilling{})
	accounts := make([]*service.Account, 0, 3)
	for id := int64(1); id <= 3; id++ {
		accounts = append(accounts, &service.Account{ID: id, Priority: int(id), Platform: group.Platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test"}, AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: group.ID}}})
	}
	slots := &t28IsolationSlots{held: make(map[int64]int), released: make(map[int64]int)}
	concurrency := service.NewConcurrencyService(slots)
	snapshot := service.NewSchedulerSnapshotService(costAuditGatewaySched{accounts: accounts}, nil, nil, nil, nil)
	h.cfg.Gateway.Scheduling.LoadBatchEnabled = true
	h.cfg.Gateway.FailoverPolicy.Cooldown.Enabled = true
	h.cfg.Gateway.FailoverPolicy.Cooldown.ProbeEnabled = true
	h.cfg.Gateway.FailoverPolicy.Cooldown.ProbeMaxAttempts = 3
	h.cfg.Gateway.FailoverPolicy.Cooldown.ConsecutiveFailuresThreshold = 3
	h.cfg.Gateway.FailoverPolicy.Cooldown.StreakWindow = time.Minute
	gw := service.NewGatewayService(nil, costAuditGatewayGroup{group: group}, nil, &costAuditGatewayBilling{}, nil, nil, nil, nil, h.cfg, snapshot, concurrency, service.NewBillingService(h.cfg, nil), nil, nil, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	gw.ConfigureFailoverRouting(&gatewayFailoverIntegrationReader{}, store)
	gw.EnableHTTPFailoverObservation()
	h.gatewayService = gw
	store.onAcquire = func(id int64) { require.Equal(t, 1, slots.held[id]) }
	return h, group, slots
}

func TestGatewayFailoverIsolationProbeBusyOrAcquired(t *testing.T) {
	for _, busy := range []bool{true, false} {
		name := "success"
		if busy {
			name = "busy"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &costAuditGatewayUpstream{}
			store := &t28IsolationStore{pending: 1}
			if busy {
				store.busy = 1
			}
			h, group, slots := newT28IsolationGateway(t, upstream, store)
			require.Equal(t, http.StatusOK, serveCostAuditResponses(h, group).Code)
			require.Equal(t, []int64{1}, store.grants)
			require.Equal(t, 1, slots.released[1])
			require.Zero(t, slots.held[1])
			require.Equal(t, 1, store.reads)
			if busy {
				require.Equal(t, []int64{2}, upstream.accounts)
				require.Empty(t, store.outcomes)
			} else {
				require.Equal(t, []int64{1}, upstream.accounts)
				require.Equal(t, []service.FailoverProbeOutcome{service.FailoverProbeSuccess}, store.outcomes)
			}
		})
	}
}

func TestGatewayFailoverIsolationCommitAfterRealSendSummary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failCount  int
		wantWrites int
	}{{"one", 1, 1}, {"two", 2, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &costAuditGatewayUpstream{failures: tc.failCount}
			store := &t28IsolationStore{}
			h, group, _ := newT28IsolationGateway(t, upstream, store)
			res := serveCostAuditResponses(h, group)
			require.Equal(t, http.StatusOK, res.Code)
			require.Len(t, upstream.accounts, tc.failCount+1)
			require.Len(t, store.writes, tc.wantWrites)
			require.Equal(t, 1, store.reads)
		})
	}
}
