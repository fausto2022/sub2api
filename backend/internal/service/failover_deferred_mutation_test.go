package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failoverMutationAccountRepo struct {
	AccountRepository
	writes int
}

func (r *failoverMutationAccountRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.writes++
	return nil
}

func (r *failoverMutationAccountRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	r.writes++
	return nil
}

func TestFailoverDeferredMutationPolicyMatchPrecedesWrite(t *testing.T) {
	repo := &failoverMutationAccountRepo{}
	rates := &RateLimitService{accountRepo: repo}
	account := &Account{ID: 1, Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules":   []any{map[string]any{"error_code": float64(503), "keywords": []any{"unavailable"}, "duration_minutes": float64(1)}},
		},
	}
	body := []byte(`{"error":{"message":"unavailable"}}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	sink := InstallHTTPFailoverMutationSink(c)
	ctx := c.Request.Context()
	for range 2 {
		require.Equal(t, ErrorPolicyTempUnscheduled, rates.CheckErrorPolicy(ctx, account, 503, body, "model"))
	}
	sink.CommitCrossAccount(ctx, 2)
	require.Zero(t, repo.writes)
	sink.ApproveCrossAccount(1)
	sink.CommitCrossAccount(ctx, 1)
	require.Zero(t, repo.writes)
	sink.CommitCrossAccount(ctx, 2)
	require.Equal(t, 1, repo.writes)
	require.Equal(t, ErrorPolicyTempUnscheduled, rates.CheckErrorPolicy(context.Background(), account, 503, body, "model"))
	require.Equal(t, 2, repo.writes, "unmanaged entry keeps immediate persistence")
}

func TestFailoverDeferredMutationRequiresApprovedDifferentSend(t *testing.T) {
	for _, tc := range []string{"approved", "same", "canceled", "suppressed", "not_approved"} {
		t.Run(tc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
			sink := InstallHTTPFailoverMutationSink(c)
			ctx = c.Request.Context()
			var got []string
			for _, value := range []string{"first", "latest"} {
				require.True(t, DeferFailoverMutation(ctx, 1, "account", func(context.Context) { got = append(got, value) }))
			}
			require.True(t, DeferFailoverMutation(ctx, 1, "model:x", func(context.Context) { got = append(got, "model") }))
			if tc != "not_approved" {
				sink.ApproveCrossAccount(1)
			}
			if tc == "canceled" {
				cancel()
			}
			if tc == "suppressed" {
				sink.Suppress(1, FailoverSuppressedSemanticBytes)
			}
			target := int64(2)
			if tc == "same" {
				target = 1
			}
			sink.CommitCrossAccount(ctx, target)
			sink.CommitCrossAccount(ctx, target)
			if tc == "approved" {
				require.ElementsMatch(t, []string{"latest", "model"}, got)
			} else {
				require.Empty(t, got)
			}
		})
	}
}
