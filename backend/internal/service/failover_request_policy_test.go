package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type requestPolicyReaderFunc func(context.Context, int64) (*GroupFailoverPolicyRow, error)

func (f requestPolicyReaderFunc) LoadGroupFailoverPolicy(ctx context.Context, id int64) (*GroupFailoverPolicyRow, error) {
	return f(ctx, id)
}

func TestFailoverRequestPolicyCachesByGroupAndRequest(t *testing.T) {
	groupA, groupB := int64(11), int64(22)
	disabled, zero := false, 0
	cfg := config.GatewayFailoverPolicyConfig{Enabled: true, FailoverOn400: true, MaxSelectionRetries: 3}
	cfg.Budget.MaxTotalUpstreamAttempts = 7
	cfg.Cooldown.DefaultTTL = time.Minute
	base := FailoverPolicyFromConfig(cfg)
	overrideA, err := ParseGroupFailoverPolicyJSON([]byte(`{"failover_on_400":true,"max_selection_retries":0,"cooldown":{"enabled":true}}`))
	require.NoError(t, err)
	overrideB, err := ParseGroupFailoverPolicyJSON([]byte(`{"budget":{"max_total_upstream_attempts":2}}`))
	require.NoError(t, err)
	reads := make(map[int64]int)
	reader := requestPolicyReaderFunc(func(_ context.Context, id int64) (*GroupFailoverPolicyRow, error) {
		reads[id]++
		if id == groupA {
			return &GroupFailoverPolicyRow{GroupID: id, On400: &disabled, MaxAccountSwitches: &zero, Policy: overrideA}, nil
		}
		return &GroupFailoverPolicyRow{GroupID: id, Policy: overrideB}, nil
	})
	require.Nil(t, FailoverRequestPolicyFromContext(context.Background()))
	ctx := WithFailoverRequestPolicy(context.Background(), reader, base)
	cache := FailoverRequestPolicyFromContext(ctx)
	require.NotNil(t, cache)
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	require.Same(t, cache, FailoverRequestPolicyFromContext(child))

	a, err := cache.Resolve(ctx, &groupA)
	require.NoError(t, err)
	fallback, err := FailoverRequestPolicyFromContext(child).Resolve(child, &groupB)
	require.NoError(t, err)
	backToA, err := cache.Resolve(ctx, &groupA)
	require.NoError(t, err)
	require.Equal(t, map[int64]int{groupA: 1, groupB: 1}, reads)
	require.Equal(t, a, backToA)
	require.Equal(t, groupA, a.GroupID)
	require.Equal(t, groupB, fallback.GroupID)
	require.Equal(t, FailoverPolicySourceGroup, a.Source)
	require.Equal(t, FailoverPolicySourceGroup, fallback.Source)

	pa, pb := a.Policy(), fallback.Policy()
	require.False(t, *pa.FailoverOn400)
	require.Zero(t, *pa.MaxAccountSwitches)
	require.Zero(t, *pa.MaxSelectionRetries)
	require.True(t, *pa.CooldownEnabled)
	require.Equal(t, time.Minute, *pa.CooldownDefaultTTL)
	require.Equal(t, 7, *pa.BudgetMaxTotalUpstreamAttempts)
	require.True(t, *pb.FailoverOn400)
	require.Nil(t, pb.MaxAccountSwitches)
	require.Equal(t, 3, *pb.MaxSelectionRetries)
	require.False(t, *pb.CooldownEnabled)
	require.Equal(t, 2, *pb.BudgetMaxTotalUpstreamAttempts)

	ctx2 := WithFailoverRequestPolicy(context.Background(), reader, base)
	_, err = FailoverRequestPolicyFromContext(ctx2).Resolve(ctx2, &groupA)
	require.NoError(t, err)
	require.Equal(t, 2, reads[groupA], "a new request must read the group again")
}

func TestFailoverRequestPolicyGlobalFallbackEvidence(t *testing.T) {
	badID, existingID, missingID, brokenID, mismatchID := int64(-1), int64(10), int64(20), int64(30), int64(40)
	storageErr := errors.New("storage unavailable")
	calls := make(map[int64]int)
	reader := requestPolicyReaderFunc(func(_ context.Context, id int64) (*GroupFailoverPolicyRow, error) {
		calls[id]++
		switch id {
		case existingID:
			return &GroupFailoverPolicyRow{GroupID: id}, nil
		case missingID:
			return nil, nil
		case brokenID:
			return &GroupFailoverPolicyRow{GroupID: id}, storageErr
		default:
			return &GroupFailoverPolicyRow{GroupID: 99}, nil
		}
	})
	base := FailoverPolicyFromConfig(config.GatewayFailoverPolicyConfig{Enabled: true})
	ctx := WithFailoverRequestPolicy(context.Background(), reader, base)
	cache := FailoverRequestPolicyFromContext(ctx)
	for _, id := range []*int64{nil, &badID, &existingID, &missingID, &brokenID, &mismatchID} {
		first, err := cache.Resolve(ctx, id)
		second, cachedErr := cache.Resolve(ctx, id)
		require.Equal(t, first, second)
		require.Equal(t, FailoverPolicySourceGlobal, first.Source)
		require.True(t, *first.Policy().Enabled)
		switch id {
		case &brokenID:
			require.ErrorIs(t, err, storageErr)
			require.ErrorIs(t, cachedErr, storageErr)
		case &mismatchID:
			require.ErrorContains(t, err, "reader returned group 99")
			require.Equal(t, err, cachedErr)
		default:
			require.NoError(t, err)
			require.NoError(t, cachedErr)
		}
	}
	require.Equal(t, map[int64]int{existingID: 1, missingID: 1, brokenID: 1, mismatchID: 1}, calls)

	ctx = WithFailoverRequestPolicy(context.Background(), nil, base)
	fallback, err := FailoverRequestPolicyFromContext(ctx).Resolve(ctx, &existingID)
	require.ErrorIs(t, err, ErrFailoverPolicyReaderUnavailable)
	require.Equal(t, FailoverPolicySourceGlobal, fallback.Source)
}

func TestFailoverRequestPolicyCanceledReadsDoNotPoisonCache(t *testing.T) {
	groupID := int64(11)
	reads := 0
	ctx := WithFailoverRequestPolicy(context.Background(), requestPolicyReaderFunc(func(_ context.Context, id int64) (*GroupFailoverPolicyRow, error) {
		reads++
		return &GroupFailoverPolicyRow{GroupID: id}, nil
	}), FailoverPolicy{})
	cache := FailoverRequestPolicyFromContext(ctx)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fallback, err := cache.Resolve(cancelled, &groupID)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, FailoverPolicySourceGlobal, fallback.Source)
	require.Zero(t, reads)
	_, err = cache.Resolve(ctx, &groupID)
	require.NoError(t, err)
	_, err = cache.Resolve(cancelled, &groupID)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, reads)

	ctx = WithFailoverRequestPolicy(context.Background(), requestPolicyReaderFunc(func(_ context.Context, id int64) (*GroupFailoverPolicyRow, error) {
		reads++
		if reads == 2 {
			cancel()
		}
		return &GroupFailoverPolicyRow{GroupID: id}, nil
	}), FailoverPolicy{})
	cancelled, cancel = context.WithCancel(ctx)
	defer cancel()
	cache = FailoverRequestPolicyFromContext(ctx)
	_, err = cache.Resolve(cancelled, &groupID)
	require.ErrorIs(t, err, context.Canceled, "reader completion cannot mask context cancellation")
	_, err = cache.Resolve(ctx, &groupID)
	require.NoError(t, err)
	require.Equal(t, 3, reads)
}

func TestFailoverRequestPolicyConcurrentConsumers(t *testing.T) {
	groupID := int64(11)
	var reads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	reader := requestPolicyReaderFunc(func(ctx context.Context, id int64) (*GroupFailoverPolicyRow, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return &GroupFailoverPolicyRow{GroupID: id}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = WithFailoverRequestPolicy(ctx, reader, FailoverPolicy{})
	cache := FailoverRequestPolicyFromContext(ctx)
	results := make(chan error, 8)
	for range cap(results) {
		go func() {
			_, err := cache.Resolve(ctx, &groupID)
			results <- err
		}()
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiter, cancelWaiter := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelWaiter()
	_, err := cache.Resolve(waiter, &groupID)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	for range cap(results) {
		require.NoError(t, <-results)
	}
	require.EqualValues(t, 1, reads.Load())
}

func TestFailoverRequestPolicySnapshotDoesNotShareMutableValues(t *testing.T) {
	group := int64(11)
	baseline := config.GatewayFailoverPolicyConfig{Enabled: true, Compat400Keywords: []string{"tool"}}
	allow := true
	baseline.StatusClasses = []config.FailoverStatusClassRuleConfig{{
		Class: config.FailoverClassUpstream5xx, Codes: []int{503}, Ranges: []string{"500-599"},
		Advice: &config.FailoverRetryAdviceConfig{Failover: &allow},
	}}
	global := FailoverPolicyFromConfig(baseline)
	stopClock := false
	row := &GroupFailoverPolicyRow{GroupID: group, Policy: &FailoverPolicy{BudgetStopClockOnStream: &stopClock}}
	ctx := WithFailoverRequestPolicy(context.Background(), requestPolicyReaderFunc(func(context.Context, int64) (*GroupFailoverPolicyRow, error) {
		return row, nil
	}), global)
	cache := FailoverRequestPolicyFromContext(ctx)
	*global.Enabled = false
	baseline.StatusClasses[0].Codes[0] = 418
	allow = false
	first, err := cache.Resolve(ctx, &group)
	require.NoError(t, err)
	stopClock = true
	p := first.Policy()
	*p.Enabled = false
	(*p.StatusClasses)[0].Codes[0] = 400
	(*p.StatusClasses)[0].Ranges[0] = "400-499"
	*(*p.StatusClasses)[0].Advice.Failover = false
	(*p.Compat400Keywords)[0] = "changed"

	second, err := cache.Resolve(ctx, &group)
	require.NoError(t, err)
	q := second.Policy()
	require.True(t, *q.Enabled)
	require.False(t, *q.BudgetStopClockOnStream)
	require.Equal(t, []int{503}, (*q.StatusClasses)[0].Codes)
	require.Equal(t, []string{"500-599"}, (*q.StatusClasses)[0].Ranges)
	require.True(t, *(*q.StatusClasses)[0].Advice.Failover)
	require.Equal(t, []string{"tool"}, *q.Compat400Keywords)
}
