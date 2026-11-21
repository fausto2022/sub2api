package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPoolModeIsolationT16(t *testing.T) {
	for _, tc := range []struct {
		name        string
		enabled     bool
		poolEnabled bool
		wantWrite   bool
	}{
		{"enabled", true, true, true},
		{"pool opt out", true, false, false},
		{"global opt out", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			in := isolationTestFailure(ClassQuotaExhausted)
			in.Account = &Account{
				ID: 42, Type: AccountTypeAPIKey, Platform: PlatformAnthropic, Status: StatusActive,
				Schedulable: true, Credentials: map[string]any{"pool_mode": true},
			}
			in.Policy.CooldownEnabled = fpBoolPtr(tc.enabled)
			in.Policy.CooldownPoolModeEnabled = fpBoolPtr(tc.poolEnabled)
			store := &isolationTestStore{}
			machine := NewFailoverIsolation(store)
			require.True(t, in.Account.IsPoolMode())
			require.Equal(t, tc.wantWrite, machine.NoteFailure(ctx, in).Write.Applied)

			// An unexpected DB operation would panic through the nil repository.
			// Exercise the actual legacy pool branch alongside the T2 decision.
			rateLimit := NewRateLimitService(nil, nil, nil, nil, nil)
			require.False(t, rateLimit.HandleUpstreamError(ctx, in.Account, 529, nil, nil))
			require.True(t, in.Account.IsSchedulable())
			if tc.wantWrite {
				require.Equal(t, 30*time.Minute, store.writes[isolationTestKey{42, ""}].TTL)
				return
			}
			state := FailoverIsolationProbeState{Pending: true, BaseTTL: time.Minute}
			require.Equal(t, FailoverIsolationProbeNotRequired, machine.Probe(ctx, in.Policy, in.Account, state))
			machine.NoteSuccess(ctx, in.Policy, in.Account)
			require.Empty(t, store.calls, "opt-out performs zero T2 storage operations")
		})
	}
}
