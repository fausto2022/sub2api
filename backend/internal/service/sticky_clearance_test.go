package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStickyClearanceFailureScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*StickyClearanceInput)
		clear  bool
		reason string
	}{
		{name: "account_failure", clear: true, reason: "account_failure"},
		{name: "gate_disabled", change: func(in *StickyClearanceInput) { in.GateOn = false }, reason: "gate_disabled"},
		{name: "config_disabled", change: func(in *StickyClearanceInput) { in.ConfigEnabled = false }, reason: "config_off"},
		{name: "no_binding", change: func(in *StickyClearanceInput) { in.HasBoundSession = false }, reason: "no_binding"},
		{name: "provider_failure", change: func(in *StickyClearanceInput) { in.Scope = GatewayFailureScopeProvider }, reason: "scope_provider"},
		{name: "request_failure", change: func(in *StickyClearanceInput) { in.Scope = GatewayFailureScopeRequest }, reason: "scope_request"},
		{name: "bound_passthrough_overrides_account_scope", change: func(in *StickyClearanceInput) { in.BoundUpstreamPassthrough = true }, reason: "bound_passthru"},
		{name: "request_transient_overrides_account_scope", change: func(in *StickyClearanceInput) { in.RequestScopedTransient = true }, reason: "request_scoped_transient"},
		{name: "planned_same_account_retry", change: func(in *StickyClearanceInput) { in.RetryableOnSameAccount = true }, reason: "transient_retry"},
		{name: "previous_same_account_retry", change: func(in *StickyClearanceInput) { in.SameAccountRetried = true }, reason: "transient_retry"},
		{name: "unset_scope_keeps_binding", change: func(in *StickyClearanceInput) { in.Scope = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := StickyClearanceInput{
				GateOn: true, ConfigEnabled: true, HasBoundSession: true,
				Scope: GatewayFailureScopeAccount,
			}
			if tc.change != nil {
				tc.change(&in)
			}
			clear, reason := ShouldClearStickyBinding(in)
			require.Equal(t, tc.clear, clear, "scope=%q reason=%q", in.Scope, reason)
			if tc.reason != "" {
				require.Equal(t, tc.reason, reason)
			}
		})
	}
}

type stickyClearanceCacheStub struct {
	GatewayCache
	delete func(context.Context, int64, string) error
}

func (s stickyClearanceCacheStub) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	return s.delete(ctx, groupID, sessionHash)
}

func TestStickyClearanceCacheDeletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	groupID := int64(42)
	for _, tc := range []struct {
		name      string
		deleteErr error
	}{
		{name: "removed"},
		{name: "cache_error_is_best_effort", deleteErr: errors.New("cache unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			svc := &GatewayService{cache: stickyClearanceCacheStub{delete: func(gotCtx context.Context, gotGroupID int64, gotHash string) error {
				calls++
				require.Same(t, ctx, gotCtx)
				require.Equal(t, groupID, gotGroupID)
				require.Equal(t, "session-hash", gotHash)
				return tc.deleteErr
			}}}
			svc.ClearStickySessionBinding(ctx, &groupID, "session-hash")
			require.Equal(t, 1, calls)
		})
	}
}
