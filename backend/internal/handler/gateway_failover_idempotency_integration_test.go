package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayFailoverIdempotencyGroupHTTP(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		groupOverride         *bool
		global, key, mutating bool
		wantSends             int
	}{
		{"disabled", nil, false, false, true, 2},
		{"group_blocks", new(true), false, false, true, 1},
		{"group_allows", new(false), true, false, true, 2},
		{"with_key", nil, true, true, true, 2},
		{"read_only", nil, true, false, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &costAuditGatewayUpstream{failures: 1}
			store := &t28IsolationStore{}
			h, group, slots := newT28IsolationGateway(t, upstream, store)
			h.cfg.Gateway.FailoverPolicy.Idempotency.RequireKeyForSideEffecting = tc.global
			reader := &gatewayFailoverIntegrationReader{rows: map[int64]*service.GroupFailoverPolicyRow{
				group.ID: {GroupID: group.ID, Policy: &service.FailoverPolicy{IdempotencyRequireKeyForSideEffecting: tc.groupOverride}},
			}}
			h.gatewayService.ConfigureFailoverRouting(reader, store)
			tool := "read_file"
			if tc.mutating {
				tool = "write_file"
			}
			body := `{"model":"claude-sonnet-4-5","stream":true,"input":"hello","tools":[{"type":"function","name":"` + tool + `","parameters":{"type":"object","properties":{}}}]}`
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.key {
				c.Request.Header.Set("Idempotency-Key", "client-key")
			}
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 9502, UserID: 9503, GroupID: &group.ID, Group: group, User: &service.User{ID: 9503, Balance: 100}})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9503})
			h.Responses(c)
			require.Len(t, upstream.accounts, tc.wantSends, r.Body.String())
			require.Equal(t, []int64{group.ID}, reader.ids)
			if tc.wantSends == 1 {
				require.Empty(t, store.writes)
				require.Equal(t, string(service.FailoverSuppressedSideEffectNoKey), requestFailoverObservation(c).suppression)
			}
			for _, held := range slots.held {
				require.Zero(t, held)
			}
		})
	}
}

type idempotencyEvaluatorFunc func(http.Header, []byte) (service.FailoverIdempotencyDecision, error)

func (f idempotencyEvaluatorFunc) Evaluate(h http.Header, b []byte) (service.FailoverIdempotencyDecision, error) {
	return f(h, b)
}

func TestGatewayFailoverIdempotencyRetryAndBlocking(t *testing.T) {
	for _, body := range []string{`{"tools":[{"name":"write_file"}]}`, `{"tools":`} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		captureFailoverIdempotencyInput(c, []byte(body))
		input := c.Request.Context().Value(failoverIdempotencyInputKey{}).(failoverIdempotencyInput)
		sink := service.InstallFailoverSideEffectSinkForRequest(c)
		writes := 0
		sink.Add(1, service.FailoverDecision{}, func() { writes++ })
		fs := NewFailoverState(3, true)
		on := true
		gate := service.NewFailoverIdempotencyGateFromPolicy(service.FailoverPolicy{IdempotencyRequireKeyForSideEffecting: &on})
		calls := 0
		fs.idempotency = &failoverIdempotencyCheck{input: input, evaluator: idempotencyEvaluatorFunc(func(h http.Header, b []byte) (service.FailoverIdempotencyDecision, error) {
			calls++
			return gate.Evaluate(h, b)
		})}
		err := &service.UpstreamFailoverError{StatusCode: 502, RetryableOnSameAccount: true, SameAccountRetryDelay: time.Nanosecond}
		unschedule := &mockTempUnscheduler{}
		ctx := c.Request.Context()
		require.Equal(t, FailoverContinue, fs.HandleFailoverError(ctx, unschedule, 1, service.PlatformAnthropic, 1, err))
		require.Zero(t, calls, "same-account decision never invokes the detector")
		require.False(t, fs.rejectIdempotencyRetrySelection(ctx, &service.AccountSelectionResult{Account: &service.Account{ID: 1}}))
		require.Equal(t, FailoverExhausted, fs.HandleFailoverError(ctx, unschedule, 1, service.PlatformAnthropic, 1, err))
		require.Equal(t, 1, calls)
		require.Zero(t, fs.SwitchCount)
		require.False(t, fs.ForceCacheBilling)
		require.Empty(t, unschedule.calls)
		require.Zero(t, sink.Apply(1))
		require.Zero(t, writes)
		require.Equal(t, string(service.FailoverSuppressedSideEffectNoKey), fs.SwitchChain[len(fs.SwitchChain)-1].Reason)
	}
}

func TestGatewayFailoverIdempotencyReleasesMisselectedRetry(t *testing.T) {
	fs := NewFailoverState(2, false)
	on := true
	gate := service.NewFailoverIdempotencyGateFromPolicy(service.FailoverPolicy{IdempotencyRequireKeyForSideEffecting: &on})
	fs.idempotency = &failoverIdempotencyCheck{input: failoverIdempotencyInput{body: []byte(`{"tools":[{"name":"write_file"}]}`)}, evaluator: gate, retryID: 1}
	fs.LastFailoverErr = &service.UpstreamFailoverError{StatusCode: 502}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	released := 0
	require.True(t, fs.rejectIdempotencyRetrySelection(c.Request.Context(), &service.AccountSelectionResult{
		Account: &service.Account{ID: 2}, Acquired: true, ReleaseFunc: func() { released++ },
	}))
	require.Equal(t, 1, released)
	require.Zero(t, fs.SwitchCount)
}
