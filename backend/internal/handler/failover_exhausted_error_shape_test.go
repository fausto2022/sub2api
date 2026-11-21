package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFailoverExhaustedTerminalShape(t *testing.T) {
	for _, tc := range []struct {
		name          string
		statuses      []int
		mixed, detail bool
		status        int
		retryAfter    string
	}{
		{"429 preserves old header behavior", []int{429, 429}, false, false, 429, ""},
		{"502 omits retry header", []int{500, 502}, false, false, 502, ""},
		{"mixed off maps final status", []int{429, 502}, false, false, 502, ""},
		{"mixed on uses 503 without pending retry aggregation", []int{429, 502}, true, true, 503, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			cfg := &config.Config{}
			cfg.Gateway.FailoverPolicy.Terminal.MixedStatus503 = tc.mixed
			cfg.Gateway.FailoverPolicy.ExposeFailoverDetail = tc.detail
			h := &GatewayHandler{cfg: cfg}
			state := NewFailoverState(2, false)
			for i, status := range tc.statuses {
				account := &service.Account{ID: int64(100 + i), Platform: service.PlatformAnthropic}
				state.observeForward(c, account, nil, "claude-test")
				requestFailoverObservation(c).beginSend(account.ID)(&http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"20"}}}, nil)
			}
			h.handleFailoverExhausted(c, &service.UpstreamFailoverError{StatusCode: tc.statuses[len(tc.statuses)-1]}, service.PlatformAnthropic, false)
			require.Equal(t, tc.status, r.Code)
			require.Equal(t, tc.retryAfter, r.Header().Get("Retry-After"))
			var payload struct {
				Error map[string]any `json:"error"`
			}
			require.NoError(t, json.Unmarshal(r.Body.Bytes(), &payload))
			require.EqualValues(t, 2, payload.Error["attempts"])
			require.Equal(t, "accounts", payload.Error["exhausted_reason"])
			_, exposed := payload.Error["failover"]
			require.Equal(t, tc.detail, exposed)
			require.NotContains(t, r.Body.String(), "account_id")
		})
	}
}

func TestFailoverBudgetTerminalFieldsHideAttemptsWithDetailEnabled(t *testing.T) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set(failoverExposeDetailKey, true)
	h := &GatewayHandler{}
	outcome := &FailoverOutcomeError{Reason: failoverReasonBudget, LastStatus: http.StatusTooManyRequests, Attempts: 3}
	h.handleFailoverBudgetExhausted(c, outcome, false)

	require.Equal(t, http.StatusTooManyRequests, r.Code)
	require.Equal(t, "budget", r.Header().Get("X-Failover-Reason"))
	var payload struct {
		Error map[string]any `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &payload))
	require.NotContains(t, payload.Error, "attempts")
	require.Equal(t, "budget", payload.Error["exhausted_reason"])
	require.EqualValues(t, http.StatusTooManyRequests, payload.Error["last_status"])
	require.Equal(t, *outcome, c.MustGet(failoverOutcomeContextKey))
}

func TestFailoverExhaustedBudgetErrorShape(t *testing.T) {
	for _, tc := range []struct{ upstream, want int }{
		{0, 504}, {429, 429}, {503, 502}, {529, 503},
	} {
		t.Run(http.StatusText(tc.want), func(t *testing.T) {
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			h := &GatewayHandler{}
			outcome := &FailoverOutcomeError{Reason: failoverReasonBudget, LastStatus: tc.upstream, Attempts: 3}
			h.handleFailoverBudgetExhausted(c, outcome, false)
			if r.Code != tc.want || r.Header().Get("X-Failover-Reason") != "budget" {
				t.Fatalf("status = %d, headers = %v", r.Code, r.Header())
			}
			body := r.Body.String()
			if !strings.Contains(body, failoverBudgetExhaustedMessage) || strings.Contains(strings.ToLower(body), "no available accounts") || strings.Contains(body, `"attempts"`) {
				t.Fatalf("unexpected budget response %q", body)
			}
			if got, ok := c.Get(failoverOutcomeContextKey); !ok || got != *outcome {
				t.Fatalf("P5 outcome = %v, exists = %v", got, ok)
			}
		})
	}
}
