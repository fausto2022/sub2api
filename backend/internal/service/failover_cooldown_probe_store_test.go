package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type isolationRecoveryStoreSpy struct {
	FailoverRecoveryStore
	writes []FailoverCooldownWrite
	probe  []bool
}

func (s *isolationRecoveryStoreSpy) CoolWithRecovery(_ context.Context, write FailoverCooldownWrite, probe bool) FailoverCooldownWriteResult {
	s.writes = append(s.writes, write)
	s.probe = append(s.probe, probe)
	return FailoverCooldownWriteResult{Applied: true, TTL: write.TTL}
}

func TestFailoverCooldownIsolationUsesRecoveryWrite(t *testing.T) {
	in := isolationTestFailure(ClassRateLimited)
	in.ModelScoped, in.Model = true, "model-a"
	in.UpstreamResetAfter = fpDurPtr(24 * time.Second)
	store := &isolationRecoveryStoreSpy{}
	machine := NewFailoverIsolation(store)
	ctx := context.Background()

	require.True(t, machine.NoteFailure(ctx, in).Write.Applied)
	in.Policy.CooldownProbeEnabled = fpBoolPtr(false)
	require.True(t, machine.NoteFailure(ctx, in).Write.Applied)
	in.Policy.CooldownEnabled = fpBoolPtr(false)
	require.Zero(t, machine.NoteFailure(ctx, in))
	in.Policy.CooldownEnabled = fpBoolPtr(true)
	in.FailedAccountIDs = append(in.FailedAccountIDs, 43)
	require.Zero(t, machine.NoteFailure(ctx, in))

	expected := FailoverCooldownWrite{AccountID: 42, Model: "model-a", Class: ClassRateLimited, TTL: 24 * time.Second}
	require.Equal(t, []FailoverCooldownWrite{expected, expected}, store.writes)
	require.Equal(t, []bool{true, false}, store.probe)
}
