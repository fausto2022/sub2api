package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestFailoverCooldownRecoveryBatchAndExpiry(t *testing.T) {
	ctx := context.Background()
	hook := newFailoverCooldownCommandRecorder()
	legacy, server, _ := newFailoverCooldownCacheForTest(t, func(cfg *config.FailoverCooldownConfig) { cfg.BatchReadMax = 2 }, hook)
	store := legacy.(service.FailoverRecoveryStore)
	account := service.FailoverCooldownScope{AccountID: 7}
	model := service.FailoverCooldownScope{AccountID: 7, Model: "gemini-pro"}
	old := service.FailoverCooldownScope{AccountID: 8}
	never := service.FailoverCooldownScope{AccountID: 9}
	anomalous := service.FailoverCooldownScope{AccountID: 10}
	oldKey := recoveryScopeKeys(old)[0]
	require.NoError(t, server.Set(oldKey, string(service.ClassUpstream5xx)+"|1"))
	server.SetTTL(oldKey, time.Second)
	bad := recoveryScopeKeys(anomalous)
	require.NoError(t, server.Set(bad[0], string(service.ClassUpstream5xx)+"|1"))
	require.NoError(t, server.Set(bad[1], string(service.ClassUpstream5xx)+"|1000|0"))
	require.True(t, store.CoolWithRecovery(ctx, service.FailoverCooldownWrite{AccountID: 7, Model: model.Model, Class: service.ClassQuotaExhausted, TTL: time.Second}, true).Applied)

	scopes := []service.FailoverCooldownScope{account, model, old, never, anomalous}
	hook.reset()
	before := store.ReadRecoveryBatch(ctx, scopes)
	require.False(t, before.Degraded)
	require.Len(t, before.Active, 2)
	require.Equal(t, service.ClassQuotaExhausted, before.Active[model].Class)
	require.Contains(t, before.Active, old, "the key's TTL wins even when the legacy value has a stale deadline")
	require.Empty(t, before.Pending)
	require.Equal(t, 3, hook.count("eval"), "one EVAL per chunk")
	require.Zero(t, hook.count("get"))
	require.Equal(t, service.FailoverProbeCooling, store.AcquireRecoveryProbe(ctx, model).Status)

	server.FastForward(time.Second)
	after := store.ReadRecoveryBatch(ctx, scopes)
	require.Empty(t, after.Active)
	require.Contains(t, after.Pending, model)
	require.Len(t, after.Pending, 1)
	for _, scope := range []service.FailoverCooldownScope{account, old, never, anomalous} {
		require.Equal(t, service.FailoverProbeNotRequired, store.AcquireRecoveryProbe(ctx, scope).Status)
	}
	server.FastForward(failoverRecoveryGrace)
	require.Empty(t, store.ReadRecoveryBatch(ctx, scopes).Pending)
}

func TestFailoverCooldownRecoveryContentionAndGeneration(t *testing.T) {
	ctx := context.Background()
	legacy, server, _ := newFailoverCooldownCacheForTest(t, nil)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	stores := [2]service.FailoverRecoveryStore{legacy.(service.FailoverRecoveryStore), NewFailoverCooldownCache(client, failoverCooldownTestConfig(), nil).(service.FailoverRecoveryStore)}
	scope := service.FailoverCooldownScope{AccountID: 7, Model: "model-a"}
	write := service.FailoverCooldownWrite{AccountID: scope.AccountID, Model: scope.Model, Class: service.ClassQuotaExhausted, TTL: time.Second}
	require.True(t, stores[0].CoolWithRecovery(ctx, write, true).Applied)
	server.FastForward(time.Second)

	const attempts = 20
	results := make(chan service.FailoverProbeAcquisition, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- stores[i%len(stores)].AcquireRecoveryProbe(ctx, scope)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var first service.FailoverProbeLease
	var acquiredCount int
	for result := range results {
		if result.Status == service.FailoverProbeAcquired {
			first = result.Lease
			acquiredCount++
		} else {
			require.Equal(t, service.FailoverProbeBusy, result.Status)
		}
	}
	require.Equal(t, 1, acquiredCount)
	require.Equal(t, failoverCooldownProbeTokenTTL, server.TTL(recoveryScopeKeys(scope)[2]))
	require.Equal(t, service.FailoverProbeNotRequired, stores[0].AcquireRecoveryProbe(ctx, service.FailoverCooldownScope{AccountID: 7, Model: "model-b"}).Status)

	server.FastForward(failoverCooldownProbeTokenTTL)
	second := stores[1].AcquireRecoveryProbe(ctx, scope)
	require.Equal(t, service.FailoverProbeAcquired, second.Status)
	require.NotEqual(t, first.Token, second.Lease.Token)
	for _, outcome := range []service.FailoverProbeOutcome{service.FailoverProbeRelease, service.FailoverProbeSuccess, service.FailoverProbeFailure} {
		require.False(t, stores[0].CompleteRecoveryProbe(ctx, first, outcome, nil).Applied)
	}
	require.Equal(t, service.FailoverProbeBusy, stores[0].AcquireRecoveryProbe(ctx, scope).Status)
	write.TTL = 5 * time.Second
	require.True(t, stores[0].CoolWithRecovery(ctx, write, true).Applied)
	require.False(t, stores[1].CompleteRecoveryProbe(ctx, second.Lease, service.FailoverProbeSuccess, nil).Applied)
	require.Equal(t, 5*time.Second, server.TTL(recoveryScopeKeys(scope)[0]))
}

func TestFailoverCooldownRecoveryProbeOutcomes(t *testing.T) {
	ctx := context.Background()
	legacy, server, _ := newFailoverCooldownCacheForTest(t, nil)
	store := legacy.(service.FailoverRecoveryStore)
	cfg := failoverCooldownTestConfig()
	cfg.MaxTTL = 3 * time.Second
	policy := service.FailoverPolicyFromConfig(config.GatewayFailoverPolicyConfig{Cooldown: cfg})
	scope := service.FailoverCooldownScope{AccountID: 7}
	keys := recoveryScopeKeys(scope)
	write := service.FailoverCooldownWrite{AccountID: scope.AccountID, Class: service.ClassUpstream5xx, TTL: time.Second}
	require.True(t, store.CoolWithRecovery(ctx, write, true).Applied)
	require.EqualValues(t, 1, store.IncrFailureStreak(ctx, scope.AccountID, time.Minute))
	server.FastForward(time.Second)
	lease := store.AcquireRecoveryProbe(ctx, scope)
	require.Equal(t, service.FailoverProbeAcquired, lease.Status)
	require.True(t, store.CompleteRecoveryProbe(ctx, lease.Lease, service.FailoverProbeRelease, &policy).Applied)
	for i, delay := range []time.Duration{2 * time.Second, 3 * time.Second, 0} {
		lease = store.AcquireRecoveryProbe(ctx, scope)
		require.Equal(t, service.FailoverProbeAcquired, lease.Status)
		require.Equal(t, i, lease.Lease.State.FailedAttempts)
		require.Equal(t, time.Second, lease.Lease.State.BaseTTL)
		require.True(t, store.CompleteRecoveryProbe(ctx, lease.Lease, service.FailoverProbeFailure, &policy).Applied)
		if delay > 0 {
			require.Equal(t, delay, server.TTL(keys[0]))
			server.FastForward(delay)
		}
	}
	require.Equal(t, service.FailoverProbeNotRequired, store.AcquireRecoveryProbe(ctx, scope).Status)
	require.False(t, server.Exists(failoverFailureStreakKey(scope.AccountID)))

	require.True(t, store.CoolWithRecovery(ctx, write, true).Applied)
	server.FastForward(time.Second)
	lease = store.AcquireRecoveryProbe(ctx, scope)
	require.Equal(t, service.FailoverProbeAcquired, lease.Status)
	require.True(t, store.CompleteRecoveryProbe(ctx, lease.Lease, service.FailoverProbeSuccess, &policy).Applied)
	require.Empty(t, store.ReadRecoveryBatch(ctx, []service.FailoverCooldownScope{scope}).Pending)
}

func TestFailoverCooldownRecoveryDebounceAndFailOpen(t *testing.T) {
	ctx := context.Background()
	legacy, server, counter := newFailoverCooldownCacheForTest(t, nil)
	store := legacy.(service.FailoverRecoveryStore)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	other := NewFailoverCooldownCache(client, failoverCooldownTestConfig(), nil).(service.FailoverRecoveryStore)
	scope := service.FailoverCooldownScope{AccountID: 7}
	keys := recoveryScopeKeys(scope)
	write := service.FailoverCooldownWrite{AccountID: 7, Class: service.ClassAuthInvalid, TTL: 10 * time.Second}
	require.NoError(t, server.Set(keys[3], "orphan"))
	require.True(t, store.CoolWithRecovery(ctx, write, true).Applied)
	write.TTL = 20 * time.Second
	require.Equal(t, service.FailoverCooldownWriteResult{TTL: 10 * time.Second}, other.CoolWithRecovery(ctx, write, true))
	server.FastForward(time.Second)
	require.True(t, other.CoolWithRecovery(ctx, write, true).Applied)
	write.TTL = time.Second
	require.Equal(t, service.FailoverCooldownWriteResult{TTL: 20 * time.Second}, store.CoolWithRecovery(ctx, write, true))
	write.Model = "other"
	require.True(t, store.CoolWithRecovery(ctx, write, false).Applied, "the debounce is per account/model scope")
	server.FastForward(time.Second)
	require.Empty(t, store.ReadRecoveryBatch(ctx, []service.FailoverCooldownScope{{AccountID: 7, Model: "other"}}).Pending)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	snapshot := store.ReadRecoveryBatch(canceled, []service.FailoverCooldownScope{scope})
	require.True(t, snapshot.Degraded)
	require.Empty(t, snapshot.Active)
	require.Empty(t, snapshot.Pending)
	require.Equal(t, service.FailoverProbeDegraded, store.AcquireRecoveryProbe(canceled, scope).Status)
	require.True(t, store.CompleteRecoveryProbe(canceled, service.FailoverProbeLease{Scope: scope, Token: "test"}, service.FailoverProbeRelease, nil).Degraded)
	require.True(t, store.CoolWithRecovery(canceled, write, true).Degraded)
	require.EqualValues(t, 4, counter.Total())
}
