package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSwitchHopAccountSwitchBackfillsSelectedTarget(t *testing.T) {
	ctx := context.Background()
	state := NewFailoverState(3, false)
	err := &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}

	require.Equal(t, FailoverContinue, state.HandleFailoverError(ctx, nil, 41, service.PlatformAnthropic, 0, err))
	require.Equal(t, 1, state.SwitchCount)
	require.Contains(t, state.FailedAccountIDs, int64(41))
	chain := state.SwitchChainSnapshot()
	require.Len(t, chain, 1)
	require.False(t, chain[0].At.IsZero())
	want := SwitchHop{
		Seq: 1, At: chain[0].At, Kind: SwitchHopAccountSwitch, FromAccountID: 41,
		Reason: "upstream_status_502", UpstreamStatus: http.StatusBadGateway, SwitchCount: 1,
	}
	require.Equal(t, want, chain[0])

	state.RecordSelectedAccount(0)
	require.Equal(t, want, state.SwitchChainSnapshot()[0])
	state.RecordSelectedAccount(52)
	state.RecordSelectedAccount(53) // Later selections must not overwrite the selected target.
	want.ToAccountID = 52
	require.Equal(t, want, state.SwitchChainSnapshot()[0])
}

func TestSwitchHopSameAccountRetryDoesNotSpendSwitch(t *testing.T) {
	state := NewFailoverState(2, false)
	err := &service.UpstreamFailoverError{
		StatusCode: http.StatusTooManyRequests, RetryableOnSameAccount: true,
		SameAccountRetryDelay: time.Nanosecond,
	}
	require.Equal(t, FailoverContinue, state.HandleFailoverError(context.Background(), nil, 41, service.PlatformAnthropic, 1, err))
	state.RecordSelectedAccount(41)
	require.Zero(t, state.SwitchCount)
	require.Empty(t, state.FailedAccountIDs)
	require.Equal(t, 1, state.SameAccountRetryCount[41])
	chain := state.SwitchChainSnapshot()
	require.Len(t, chain, 1)
	require.Equal(t, SwitchHop{
		Seq: 1, At: chain[0].At, Kind: SwitchHopSameAccountRetry, FromAccountID: 41,
		Reason: "retryable_on_same_account", UpstreamStatus: http.StatusTooManyRequests,
		SameAccountRetryCount: 1,
	}, chain[0])
}

func TestSwitchHopSelectionExhaustedBeforeSwitchBudget(t *testing.T) {
	state := NewFailoverState(2, false)
	require.Equal(t, FailoverExhausted, state.HandleSelectionExhausted(context.Background()))
	require.Zero(t, state.SwitchCount)
	chain := state.SwitchChainSnapshot()
	require.Len(t, chain, 1)
	require.Equal(t, SwitchHop{Seq: 1, At: chain[0].At, Kind: SwitchHopExhausted, Reason: "selection_exhausted"}, chain[0])
}

func TestSwitchHopProfitVetoAndSelectionRetryBeforeSwitchBudget(t *testing.T) {
	ctx := context.Background()
	state := NewFailoverState(3, false)
	err := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	require.Equal(t, FailoverContinue, state.HandleFailoverError(ctx, nil, 41, service.PlatformAnthropic, 0, err))
	require.Equal(t, FailoverContinue, state.RecordProfitVeto(52))
	require.Equal(t, FailoverContinue, state.HandleSelectionExhausted(ctx))
	state.RecordSelectedAccount(63)

	require.Equal(t, 1, state.SwitchCount)
	require.Equal(t, 1, state.ProfitVetoCount())
	require.Equal(t, map[int64]struct{}{52: {}}, state.FailedAccountIDs, "backoff should preserve the profit veto and release the upstream failure")
	chain := state.SwitchChainSnapshot()
	require.Len(t, chain, 3)
	require.Equal(t, SwitchHop{
		Seq: 1, At: chain[0].At, Kind: SwitchHopAccountSwitch, FromAccountID: 41, ToAccountID: 63,
		Reason: "upstream_status_503", UpstreamStatus: http.StatusServiceUnavailable, SwitchCount: 1,
	}, chain[0])
	require.Equal(t, SwitchHop{
		Seq: 2, At: chain[1].At, Kind: SwitchHopProfitVeto, FromAccountID: 52,
		Reason: "profit_veto", SwitchCount: 1,
	}, chain[1])
	require.Equal(t, SwitchHop{
		Seq: 3, At: chain[2].At, Kind: SwitchHopSelectionExhausted,
		Reason: "all_candidates_excluded", SwitchCount: 1, SelectionRetry: 1,
	}, chain[2])
}

func TestSwitchHopSwitchBudgetExhausted(t *testing.T) {
	state := NewFailoverState(0, false)
	err := &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	require.Equal(t, FailoverExhausted, state.HandleFailoverError(context.Background(), nil, 41, service.PlatformAnthropic, 0, err))
	require.Zero(t, state.SwitchCount)
	chain := state.SwitchChainSnapshot()
	require.Len(t, chain, 1)
	require.Equal(t, SwitchHop{
		Seq: 1, At: chain[0].At, Kind: SwitchHopExhausted, FromAccountID: 41,
		Reason: "switch_budget_exhausted", UpstreamStatus: http.StatusBadGateway,
	}, chain[0])
}

func TestSwitchHopCapsRetainedHopsAtSixteen(t *testing.T) {
	state := NewFailoverState(20, false)
	for seq := 1; seq <= 16; seq++ {
		state.AppendHop(SwitchHop{Kind: SwitchHopAccountSwitch, FromAccountID: int64(seq)})
	}
	require.Len(t, state.SwitchChainSnapshot(), 16)
	require.Zero(t, state.DroppedSwitchHops())

	state.AppendHop(SwitchHop{Kind: SwitchHopExhausted, FromAccountID: 17})
	state.AppendHop(SwitchHop{Kind: SwitchHopExhausted, FromAccountID: 18})
	require.Equal(t, 2, state.DroppedSwitchHops())
	chain := state.SwitchChainSnapshot()
	require.Len(t, chain, 16)
	for i, hop := range chain {
		require.Equal(t, i+1, hop.Seq)
		require.Equal(t, int64(i+1), hop.FromAccountID)
		require.Equal(t, SwitchHopAccountSwitch, hop.Kind)
	}
}

func TestSwitchHopSnapshotIsIndependent(t *testing.T) {
	state := NewFailoverState(2, false)
	require.Empty(t, state.SwitchChainSnapshot())
	state.AppendHop(SwitchHop{Kind: SwitchHopAccountSwitch, FromAccountID: 41})

	snapshot := state.SwitchChainSnapshot()
	require.Len(t, snapshot, 1)
	state.RecordSelectedAccount(52)
	require.Zero(t, snapshot[0].ToAccountID, "later state changes must not mutate an existing snapshot")
	snapshot[0].FromAccountID = 99
	snapshot = append(snapshot, SwitchHop{Kind: SwitchHopProfitVeto})
	require.Len(t, snapshot, 2)
	current := state.SwitchChainSnapshot()
	require.Len(t, current, 1)
	require.Equal(t, int64(41), current[0].FromAccountID)
	require.Equal(t, int64(52), current[0].ToAccountID)
}
