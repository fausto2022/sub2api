package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type isolationTestKey struct {
	accountID int64
	model     string
}

type isolationTestStore struct {
	FailoverCooldownStore
	calls        []string
	writes       map[isolationTestKey]FailoverCooldownWrite
	streak       int64
	streakWindow time.Duration
	probeHeld    bool
	degraded     bool
}

func (s *isolationTestStore) IncrFailureStreak(_ context.Context, _ int64, window time.Duration) int64 {
	s.calls = append(s.calls, "streak")
	s.streakWindow = window
	if s.degraded {
		return 0
	}
	s.streak++
	return s.streak
}

func (s *isolationTestStore) Cool(_ context.Context, write FailoverCooldownWrite) FailoverCooldownWriteResult {
	s.calls = append(s.calls, "cool")
	if s.degraded {
		return FailoverCooldownWriteResult{Degraded: true}
	}
	if s.writes == nil {
		s.writes = make(map[isolationTestKey]FailoverCooldownWrite)
	}
	s.writes[isolationTestKey{write.AccountID, write.Model}] = write
	return FailoverCooldownWriteResult{Applied: true, TTL: write.TTL}
}

func (s *isolationTestStore) Clear(_ context.Context, id int64) {
	s.calls = append(s.calls, "clear")
	delete(s.writes, isolationTestKey{id, ""})
	s.streak = 0
}

func (s *isolationTestStore) AcquireProbeToken(context.Context, int64) bool {
	s.calls = append(s.calls, "probe")
	if s.probeHeld || s.degraded {
		return false
	}
	s.probeHeld = true
	return true
}

func (s *isolationTestStore) ClearProbeToken(context.Context, int64) {
	s.calls = append(s.calls, "clear_probe")
	s.probeHeld = false
}

func isolationTestPolicy() *FailoverPolicy {
	cfg := testGlobalFailoverConfig()
	cfg.Cooldown.Enabled = true
	policy := FailoverPolicyFromConfig(cfg)
	return &policy
}

func isolationTestFailure(class FailureClass) FailoverIsolationFailure {
	return FailoverIsolationFailure{
		Policy:           isolationTestPolicy(),
		Account:          &Account{ID: 42},
		Decision:         FailoverDecision{Class: class, Scope: GatewayFailureScopeAccount, MarkAccount: true},
		FailedAccountIDs: []int64{42},
	}
}

func TestIsolationStateMachineStreakAndRecovery(t *testing.T) {
	ctx := context.Background()
	in := isolationTestFailure(ClassUpstream5xx)
	store := &isolationTestStore{}
	machine := NewFailoverIsolation(store)
	for i := int64(1); i <= 3; i++ {
		result := machine.NoteFailure(ctx, in)
		require.Equal(t, i, result.Streak)
		require.Equal(t, i == 3, result.Write.Applied)
	}
	require.Equal(t, 120*time.Second, store.streakWindow)
	require.Equal(t, []string{"streak", "streak", "streak", "cool"}, store.calls)
	require.Equal(t, time.Minute, store.writes[isolationTestKey{42, ""}].TTL)

	// The integration must provide evidence that the previous window expired.
	require.Equal(t, FailoverIsolationProbeNotRequired, machine.Probe(ctx, in.Policy, in.Account, FailoverIsolationProbeState{}))
	state := FailoverIsolationProbeState{Pending: true, BaseTTL: time.Minute}
	require.Equal(t, FailoverIsolationProbeAcquired, machine.Probe(ctx, in.Policy, in.Account, state))
	require.Equal(t, FailoverIsolationProbeUnavailable, machine.Probe(ctx, in.Policy, in.Account, state))
	machine.NoteSuccess(ctx, in.Policy, in.Account)
	require.Empty(t, store.writes)
	require.Zero(t, store.streak)
	require.False(t, machine.NoteFailure(ctx, in).Write.Applied)

	machine.Clear(ctx, in.Account.ID)
	require.False(t, store.probeHeld)
	require.Zero(t, store.streak)
	require.Equal(t, []string{"clear", "clear_probe"}, store.calls[len(store.calls)-2:])
}

func TestIsolationStateMachineProbeBackoffAndEscape(t *testing.T) {
	policy := isolationTestPolicy()
	policy.CooldownMaxTTL = fpDurPtr(3 * time.Minute)
	state := FailoverIsolationProbeState{Pending: true, BaseTTL: time.Minute}
	for i, tc := range []struct {
		delay time.Duration
		clear bool
	}{{2 * time.Minute, false}, {3 * time.Minute, false}, {0, true}} {
		state.FailedAttempts = i
		plan, ok := PlanProbeFailure(policy, state, FailoverIsolationProbeAcquired)
		require.True(t, ok)
		require.Equal(t, FailoverIsolationProbeFailure{FailedAttempts: i + 1, RetryAfter: tc.delay, Clear: tc.clear}, plan)
	}
	_, ok := PlanProbeFailure(policy, state, FailoverIsolationProbeUnavailable)
	require.False(t, ok, "no observed probe result must produce no mutation plan")
	state.FailedAttempts = 3
	_, ok = PlanProbeFailure(policy, state, FailoverIsolationProbeAcquired)
	require.False(t, ok, "the exhausted cycle cannot be extended again")
}

func TestIsolationStateMachineClassTTLAndScope(t *testing.T) {
	for _, tc := range []struct {
		name        string
		class       FailureClass
		reset       *time.Duration
		modelScoped bool
		model       string
		wantTTL     time.Duration
		wantModel   string
		thresholded bool
	}{
		{"credential", ClassAuthInvalid, nil, false, "", time.Minute, "", false},
		{"rate limit fallback", ClassRateLimited, nil, false, "", time.Minute, "", false},
		{"known reset", ClassRateLimited, fpDurPtr(17 * time.Second), true, " gemini-pro ", 17 * time.Second, "gemini-pro", false},
		{"elapsed reset", ClassRateLimited, fpDurPtr(0), false, "", 0, "", false},
		{"model name without attribution", ClassRateLimited, nil, false, "model-x", time.Minute, "", false},
		{"model evidence without model", ClassRateLimited, nil, true, " ", 0, "", false},
		{"quota fallback", ClassQuotaExhausted, nil, false, "", 30 * time.Minute, "", false},
		{"quota capped", ClassQuotaExhausted, fpDurPtr(48 * time.Hour), true, "model-x", 24 * time.Hour, "model-x", false},
		{"network", ClassNetwork, nil, false, "", time.Minute, "", true},
		{"timeout", ClassUpstreamTimeout, nil, false, "", time.Minute, "", true},
		{"overload", ClassCapacityShed, nil, false, "", 0, "", false},
		{"invalid request", ClassRequestInvalid, nil, false, "", 0, "", false},
		{"cancel", ClassCanceled, nil, false, "", 0, "", false},
		{"bound", ClassBindingPassthru, nil, false, "", 0, "", false},
		{"policy", ClassPolicyBlocked, nil, false, "", 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := isolationTestFailure(tc.class)
			in.ModelScoped, in.Model, in.UpstreamResetAfter = tc.modelScoped, tc.model, tc.reset
			store := &isolationTestStore{}
			if tc.thresholded {
				store.streak = 2
			}
			result := NewFailoverIsolation(store).NoteFailure(context.Background(), in)
			if tc.wantTTL == 0 {
				require.Empty(t, store.calls)
				return
			}
			require.True(t, result.Write.Applied)
			require.Len(t, store.writes, 1)
			require.Equal(t, tc.wantTTL, store.writes[isolationTestKey{42, tc.wantModel}].TTL)
		})
	}
}

func TestIsolationStateMachineEffectiveOverrides(t *testing.T) {
	in := isolationTestFailure(ClassUpstream5xx)
	in.Policy.CooldownConsecutiveFailuresThreshold = fpIntPtr(2)
	in.Policy.CooldownStreakWindow = fpDurPtr(77 * time.Second)
	in.Policy.CooldownMaxTTL = fpDurPtr(25 * time.Second)
	in.Advice = &RetryAdvice{Cooldown: 90 * time.Second, CooldownSource: cooldownSourceFixed}
	store := &isolationTestStore{}
	machine := NewFailoverIsolation(store)
	require.False(t, machine.NoteFailure(context.Background(), in).Write.Applied)
	require.Equal(t, 25*time.Second, machine.NoteFailure(context.Background(), in).Write.TTL)
	require.Equal(t, 77*time.Second, store.streakWindow)
}

func TestIsolationStateMachineF01PreservesOtherRequestState(t *testing.T) {
	in := isolationTestFailure(ClassUpstream5xx)
	key := isolationTestKey{42, ""}
	existing := FailoverCooldownWrite{AccountID: 42, Class: ClassAuthInvalid, TTL: time.Minute}
	store := &isolationTestStore{streak: 2, writes: map[isolationTestKey]FailoverCooldownWrite{key: existing}}
	machine := NewFailoverIsolation(store)
	for _, ids := range [][]int64{{42, 43, 42}, {43}, nil} {
		in.FailedAccountIDs = ids
		require.Zero(t, machine.NoteFailure(context.Background(), in))
	}
	require.Empty(t, store.calls)
	require.Equal(t, existing, store.writes[key])
	require.EqualValues(t, 2, store.streak)
	in.FailedAccountIDs = []int64{42, 42}
	require.True(t, machine.NoteFailure(context.Background(), in).Write.Applied, "retries do not imply a second account")
}

func TestIsolationStateMachineAttributionAndFailOpen(t *testing.T) {
	in := isolationTestFailure(ClassUpstream5xx)
	store := &isolationTestStore{degraded: true}
	machine := NewFailoverIsolation(store)
	for _, scope := range []GatewayFailureScope{"", GatewayFailureScopeProvider, GatewayFailureScopeRequest} {
		in.Decision.Scope = scope
		require.Zero(t, machine.NoteFailure(context.Background(), in))
	}
	in.Decision.Scope = GatewayFailureScopeAccount
	in.Decision.MarkAccount = false
	require.Zero(t, machine.NoteFailure(context.Background(), in))
	require.Empty(t, store.calls)
	in.Decision.MarkAccount = true
	require.Zero(t, machine.NoteFailure(context.Background(), in))
	require.Equal(t, []string{"streak"}, store.calls)
	in.Decision.Class = ClassAuthInvalid
	require.True(t, machine.NoteFailure(context.Background(), in).Write.Degraded)
	state := FailoverIsolationProbeState{Pending: true, BaseTTL: time.Minute}
	require.Equal(t, FailoverIsolationProbeUnavailable, machine.Probe(context.Background(), in.Policy, in.Account, state))
}
