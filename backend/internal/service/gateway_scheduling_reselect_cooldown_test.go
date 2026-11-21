package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCooldownReselectKeepsObservedState(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "active"
		if pending {
			name = "pending"
		}
		t.Run(name, func(t *testing.T) {
			svc, group, store := newIsolationScheduler()
			a, b := FailoverCooldownScope{AccountID: 1}, FailoverCooldownScope{AccountID: 2}
			store.snapshot.Pending = map[FailoverCooldownScope]struct{}{a: {}}
			if pending {
				store.snapshot.Pending[b] = struct{}{}
			} else {
				store.snapshot.Active = map[FailoverCooldownScope]FailoverCooldownEntry{b: {}}
			}
			store.status = map[FailoverCooldownScope]FailoverProbeStatus{a: FailoverProbeBusy}
			ctx := WithFailoverSelectionSnapshots(context.Background())
			first, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "", nil, "", 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, first.Account.ID)
			_, err = first.AcquireFailoverProbeAfterSlot(ctx, first.ReleaseFunc)
			require.ErrorIs(t, err, ErrFailoverProbeNotAdmitted)
			require.Equal(t, 1, store.slots.held)
			first.ReleaseFunc()
			second, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "", map[int64]struct{}{1: {}}, "", 0)
			if pending {
				require.NoError(t, err)
				require.True(t, second.FailoverProbePending())
				leases, err := second.AcquireFailoverProbeAfterSlot(ctx, second.ReleaseFunc)
				require.NoError(t, err)
				require.Len(t, leases, 1)
				require.Equal(t, b, leases[0].Scope)
				second.ReleaseFunc()
			} else {
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
			}
			require.Len(t, store.batches, 1)
			require.Zero(t, store.slots.held)
		})
	}
}

func TestCooldownReselectReadsOnlyUnknownScope(t *testing.T) {
	svc, group, store := newIsolationScheduler()
	ctx := WithFailoverSelectionSnapshots(context.Background())
	accounts := svc.accountRepo.(*isolationSchedulerAccounts).byGroup[group.ID]
	svc.selectionCooldownSnapshot(ctx, &group.ID, accounts, "model-a", map[int64]struct{}{2: {}})
	svc.selectionCooldownSnapshot(ctx, &group.ID, accounts, "model-a", map[int64]struct{}{1: {}})
	other := group.ID + 1
	svc.selectionCooldownSnapshot(ctx, &other, accounts, "model-a", map[int64]struct{}{1: {}})
	svc.selectionCooldownSnapshot(ctx, &group.ID, accounts, "model-b", map[int64]struct{}{1: {}})
	svc.selectionCooldownSnapshot(ctx, &group.ID, accounts[:1], "model-a", nil)
	require.Equal(t, [][]FailoverCooldownScope{
		{{AccountID: 1}, {AccountID: 1, Model: "model-a"}},
		{{AccountID: 2}, {AccountID: 2, Model: "model-a"}},
		{{AccountID: 2}, {AccountID: 2, Model: "model-a"}},
		{{AccountID: 2}, {AccountID: 2, Model: "model-b"}},
	}, store.batches)
}
