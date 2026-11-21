package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type isolationSchedulerAccounts struct {
	AccountRepository
	byGroup map[int64][]Account
}

func (r *isolationSchedulerAccounts) ListSchedulableByGroupIDAndPlatforms(_ context.Context, group int64, _ []string) ([]Account, error) {
	return r.byGroup[group], nil
}

type isolationSchedulerGroups struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *isolationSchedulerGroups) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	return r.groups[id], nil
}

type isolationSchedulerCache struct {
	GatewayCache
	sticky       int64
	refreshCount int
	rebound      int64
}

func (c *isolationSchedulerCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return c.sticky, nil
}
func (c *isolationSchedulerCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	c.refreshCount++
	return nil
}
func (c *isolationSchedulerCache) SetSessionAccountID(_ context.Context, _ int64, _ string, id int64, _ time.Duration) error {
	c.rebound = id
	return nil
}

type isolationSchedulerSlots struct {
	ConcurrencyCache
	busy     bool
	acquired []int64
	held     int
}

func (c *isolationSchedulerSlots) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	if c.busy {
		return false, nil
	}
	c.acquired = append(c.acquired, id)
	c.held++
	return true, nil
}
func (c *isolationSchedulerSlots) ReleaseAccountSlot(context.Context, int64, string) error {
	c.held--
	return nil
}
func (c *isolationSchedulerSlots) GetAccountsLoadBatch(context.Context, []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return nil, nil
}
func (c *isolationSchedulerSlots) GetAccountWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}

type isolationSchedulerRecovery struct {
	FailoverRecoveryStore
	snapshot FailoverCooldownSnapshot
	batches  [][]FailoverCooldownScope
	acquired []FailoverCooldownScope
	status   map[FailoverCooldownScope]FailoverProbeStatus
	finished []FailoverProbeOutcome
	slots    *isolationSchedulerSlots
}

func (r *isolationSchedulerRecovery) ReadRecoveryBatch(_ context.Context, scopes []FailoverCooldownScope) FailoverCooldownSnapshot {
	r.batches = append(r.batches, append([]FailoverCooldownScope(nil), scopes...))
	return r.snapshot
}
func (r *isolationSchedulerRecovery) AcquireRecoveryProbe(_ context.Context, scope FailoverCooldownScope) FailoverProbeAcquisition {
	if r.slots.held == 0 {
		panic("probe was acquired without a real slot")
	}
	r.acquired = append(r.acquired, scope)
	status := r.status[scope]
	if status == "" {
		status = FailoverProbeAcquired
	}
	return FailoverProbeAcquisition{Status: status, Lease: FailoverProbeLease{Scope: scope, Token: "lease"}}
}
func (r *isolationSchedulerRecovery) CompleteRecoveryProbe(_ context.Context, _ FailoverProbeLease, outcome FailoverProbeOutcome, _ *FailoverPolicy) FailoverProbeCompletion {
	r.finished = append(r.finished, outcome)
	return FailoverProbeCompletion{Applied: true}
}

type isolationSchedulerPolicyReader struct {
	ids  []int64
	rows map[int64]*GroupFailoverPolicyRow
}

func (r *isolationSchedulerPolicyReader) LoadGroupFailoverPolicy(_ context.Context, id int64) (*GroupFailoverPolicyRow, error) {
	r.ids = append(r.ids, id)
	return r.rows[id], nil
}

func newIsolationScheduler() (*GatewayService, *Group, *isolationSchedulerRecovery) {
	const groupID = 10
	group := &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	accounts := []Account{
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Priority: 1, Status: StatusActive, Schedulable: true, Concurrency: 5},
		{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Priority: 2, Status: StatusActive, Schedulable: true, Concurrency: 5},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 2
	cfg.Gateway.FailoverPolicy.Cooldown = config.FailoverCooldownConfig{Enabled: true, PoolModeEnabled: true, ProbeEnabled: true, ProbeMaxAttempts: 3, MaxTTL: time.Hour}
	slots := &isolationSchedulerSlots{}
	store := &isolationSchedulerRecovery{slots: slots}
	svc := &GatewayService{
		cfg: cfg, concurrencyService: NewConcurrencyService(slots),
		accountRepo: &isolationSchedulerAccounts{byGroup: map[int64][]Account{groupID: accounts}},
		groupRepo:   &isolationSchedulerGroups{groups: map[int64]*Group{groupID: group}},
		cache:       &isolationSchedulerCache{},
	}
	svc.SetFailoverCooldownStore(store)
	return svc, group, store
}

func TestSchedulerIsolationCooledStickyAndRoutedAccounts(t *testing.T) {
	for _, routing := range []bool{false, true} {
		name := "sticky"
		if routing {
			name = "routed sticky"
		}
		t.Run(name, func(t *testing.T) {
			svc, group, store := newIsolationScheduler()
			const model = "claude-sonnet-4-6"
			cache := svc.cache.(*isolationSchedulerCache)
			cache.sticky = 1
			if routing {
				group.ModelRoutingEnabled = true
				group.ModelRouting = map[string][]int64{model: {1, 123}}
			}
			store.snapshot.Active = map[FailoverCooldownScope]FailoverCooldownEntry{{AccountID: 1}: {}}
			selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &group.ID, "sticky", model, nil, "", 0)
			require.NoError(t, err)
			require.EqualValues(t, 2, selection.Account.ID)
			require.Equal(t, []int64{2}, store.slots.acquired)
			require.Len(t, store.batches, 1)
			require.Len(t, store.batches[0], 4)
			require.Zero(t, cache.refreshCount)
			require.EqualValues(t, 2, cache.rebound)
			selection.ReleaseFunc()
		})
	}
}

func TestSchedulerIsolationFallbackGroupPolicyAndModelScope(t *testing.T) {
	svc, group, store := newIsolationScheduler()
	original := &Group{ID: 30, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true, ClaudeCodeOnly: true, FallbackGroupID: &group.ID}
	svc.groupRepo.(*isolationSchedulerGroups).groups[original.ID] = original
	accounts := svc.accountRepo.(*isolationSchedulerAccounts).byGroup[group.ID]
	accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{"public-model": "upstream-a"}}
	accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"public-model": "upstream-b"}, "pool_mode": true}
	reader := &isolationSchedulerPolicyReader{rows: map[int64]*GroupFailoverPolicyRow{
		original.ID: {GroupID: original.ID, Policy: &FailoverPolicy{CooldownEnabled: fpBoolPtr(false)}},
		group.ID:    {GroupID: group.ID, Policy: &FailoverPolicy{CooldownEnabled: fpBoolPtr(true), CooldownPoolModeEnabled: fpBoolPtr(false)}},
	}}
	svc.cfg.Gateway.FailoverPolicy.Cooldown.Enabled = false
	ctx := WithFailoverRequestPolicy(context.Background(), reader, FailoverPolicyFromConfig(svc.cfg.Gateway.FailoverPolicy))
	_, err := FailoverRequestPolicyFromContext(ctx).Resolve(ctx, &original.ID)
	require.NoError(t, err)
	store.snapshot.Active = map[FailoverCooldownScope]FailoverCooldownEntry{
		{AccountID: 1, Model: "upstream-a"}: {}, {AccountID: 2}: {},
	}
	for range 2 {
		selection, err := svc.SelectAccountWithLoadAwareness(ctx, &original.ID, "", "public-model", nil, "", 0)
		require.NoError(t, err)
		require.EqualValues(t, 2, selection.Account.ID)
		selection.ReleaseFunc()
	}
	require.Equal(t, []int64{original.ID, group.ID}, reader.ids)
	require.Len(t, store.batches, 2)
	require.Equal(t, []FailoverCooldownScope{{AccountID: 1}, {AccountID: 1, Model: "upstream-a"}}, store.batches[0])
}

func TestSchedulerIsolationProbeRequiresImmediateOrWaitedSlot(t *testing.T) {
	for _, wait := range []bool{false, true} {
		svc, group, store := newIsolationScheduler()
		scope := FailoverCooldownScope{AccountID: 1}
		store.snapshot.Pending = map[FailoverCooldownScope]struct{}{scope: {}}
		store.slots.busy = wait
		selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &group.ID, "", "", nil, "", 0)
		require.NoError(t, err)
		require.Equal(t, !wait, selection.Acquired)
		require.EqualValues(t, 1, selection.Account.ID)
		require.True(t, selection.FailoverProbePending())
		require.Empty(t, store.acquired)
		_, err = selection.AcquireFailoverProbeAfterSlot(context.Background(), nil)
		require.ErrorIs(t, err, ErrFailoverProbeSlotRequired)
		require.Empty(t, store.acquired)
		release := selection.ReleaseFunc
		if wait {
			require.NotNil(t, selection.WaitPlan)
			store.slots.busy = false
			acquired, err := svc.concurrencyService.AcquireAccountSlot(context.Background(), selection.WaitPlan.AccountID, selection.WaitPlan.MaxConcurrency)
			require.NoError(t, err)
			require.True(t, acquired.Acquired)
			release = acquired.ReleaseFunc
		}
		leases, err := selection.AcquireFailoverProbeAfterSlot(context.Background(), release)
		require.NoError(t, err)
		require.Len(t, leases, 1)
		require.Equal(t, scope, leases[0].Scope)
		release()
		require.Zero(t, store.slots.held)
		require.True(t, selection.CompleteFailoverProbe(context.Background(), leases[0], FailoverProbeSuccess).Applied)
	}
}

func TestSchedulerIsolationBusyReleasesPartialProbe(t *testing.T) {
	svc, group, store := newIsolationScheduler()
	account := FailoverCooldownScope{AccountID: 1}
	model := FailoverCooldownScope{AccountID: 1, Model: "claude-sonnet-4-6"}
	store.snapshot.Pending = map[FailoverCooldownScope]struct{}{account: {}, model: {}}
	store.status = map[FailoverCooldownScope]FailoverProbeStatus{model: FailoverProbeBusy}
	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &group.ID, "", model.Model, nil, "", 0)
	require.NoError(t, err)
	leases, err := selection.AcquireFailoverProbeAfterSlot(context.Background(), selection.ReleaseFunc)
	require.ErrorIs(t, err, ErrFailoverProbeNotAdmitted)
	require.Empty(t, leases)
	require.Equal(t, []FailoverCooldownScope{account, model}, store.acquired)
	require.Equal(t, []FailoverProbeOutcome{FailoverProbeRelease}, store.finished)
	selection.ReleaseFunc()
	next, err := svc.SelectAccountWithLoadAwareness(context.Background(), &group.ID, "", model.Model, map[int64]struct{}{selection.Account.ID: {}}, "", 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.Account.ID)
	require.Len(t, store.batches, 1, "one remaining candidate performs no cooling I/O")
	next.ReleaseFunc()
}

func TestSchedulerIsolationDisabledAndDegraded(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		svc, group, store := newIsolationScheduler()
		svc.cfg.Gateway.FailoverPolicy.Cooldown.Enabled = enabled
		store.snapshot = FailoverCooldownSnapshot{Degraded: true, Active: map[FailoverCooldownScope]FailoverCooldownEntry{{AccountID: 1}: {}}}
		selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &group.ID, "", "", nil, "", 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, selection.Account.ID)
		require.False(t, selection.FailoverProbePending())
		if enabled {
			require.Len(t, store.batches, 1)
		} else {
			require.Empty(t, store.batches)
		}
		selection.ReleaseFunc()
	}
}
