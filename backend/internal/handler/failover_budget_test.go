package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func testFailoverBudget(cfg config.FailoverBudgetConfig) *FailoverBudget {
	return newFailoverBudget(config.GatewayFailoverPolicyConfig{Enabled: true, Budget: cfg})
}

func TestFailoverBudgetHTTPStreamContinuesPastDeadline(t *testing.T) {
	finish := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		select {
		case <-finish:
			_, _ = io.WriteString(w, "last\n")
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	const total = 600 * time.Millisecond
	fs := NewFailoverState(3, false)
	fs.Budget = testFailoverBudget(config.FailoverBudgetConfig{TotalTimeout: total, StopClockOnStream: true})
	now := time.Now()
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, finishBudget := fs.beginForward(c.Request.Context(), c, true, total)
	writer := c.Writer.(*failoverBudgetWriter)
	a, err := writer.forward.beginSend(c.Request.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	req, err := http.NewRequestWithContext(a.Context, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	a.Headers(resp.StatusCode)
	first := make([]byte, len("first\n"))
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Writer.WriteString(": ping\n\n"); err != nil {
		t.Fatal(err)
	}
	if _, active := fs.Budget.Remaining(time.Now()); !active {
		t.Fatal("heartbeat stopped the budget")
	}
	if _, err := c.Writer.Write(first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(now.Add(total + 30*time.Millisecond)))
	close(finish)
	last, err := io.ReadAll(resp.Body)
	if budgetErr := finishBudget(); budgetErr != nil {
		t.Fatal(budgetErr)
	}
	if err != nil || strings.Join([]string{string(first), string(last)}, "") != "first\nlast\n" {
		t.Fatalf("stream ended with %q + %q, %v", first, last, err)
	}
}

func TestFailoverBudgetStartsAfterFirstSlot(t *testing.T) {
	b := testFailoverBudget(config.FailoverBudgetConfig{TotalTimeout: 30 * time.Second, MinSwitchCost: 2 * time.Second})
	arrived := time.Now()
	slot := arrived.Add(time.Minute)
	if _, active := b.Remaining(arrived); active {
		t.Fatal("waiting for a slot consumed budget")
	}
	b.StartClock(slot)
	b.StartClock(slot.Add(time.Second))
	if left, active := b.Remaining(slot.Add(time.Second)); !active || left != 29*time.Second {
		t.Fatalf("remaining budget = %v, active = %v", left, active)
	}
	if b.CanSwitch(slot.Add(29*time.Second)) || !b.CanAttempt(slot.Add(29*time.Second)) || b.CanAttempt(slot.Add(30*time.Second)) {
		t.Fatal("switch reserve and deadline must be checked independently")
	}
}

func TestFailoverBudgetActualAttemptsAndSameAccountPool(t *testing.T) {
	b := testFailoverBudget(config.FailoverBudgetConfig{MaxTotalUpstreamAttempts: 4, MaxSameAccountAttemptsTotal: 1})
	now := time.Now()
	b.StartClock(now)
	for _, tc := range []struct {
		account int64
		want    error
	}{
		{1, nil}, {1, nil}, {1, errFailoverSameAccount},
		{2, nil}, {2, errFailoverSameAccount}, {3, nil}, {4, errFailoverBudget},
	} {
		a, err := b.beginAttempt(context.Background(), tc.account, now, 0, false)
		if !errors.Is(err, tc.want) {
			t.Fatalf("account %d: got %v, want %v", tc.account, err, tc.want)
		}
		if a != nil {
			a.headersReceived(http.StatusTooManyRequests)
			a.close()
		}
	}
	if got := b.outcome(); got.Attempts != 4 || got.LastStatus != 429 || got.Reason != "budget" {
		t.Fatalf("terminal snapshot = %+v", got)
	}
}

func TestFailoverBudgetDeadlinesAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		total, account, header time.Duration
		outputSeconds          int
		want                   error
	}{
		{"request", 25 * time.Millisecond, time.Second, time.Second, 0, errFailoverBudget},
		{"account", time.Second, 25 * time.Millisecond, time.Second, 0, errFailoverAccountTimeout},
		{"header", time.Second, time.Second, 25 * time.Millisecond, 0, errFailoverHeaderTimeout},
		{"first_output", 2 * time.Second, 0, 0, 1, errFailoverFirstOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newFailoverBudget(config.GatewayFailoverPolicyConfig{
				Enabled: true, Budget: config.FailoverBudgetConfig{TotalTimeout: tc.total, PerAccountTimeout: tc.account},
				FirstOutputTimeoutSeconds: tc.outputSeconds,
			})
			now := time.Now()
			b.StartClock(now)
			parent := context.Background()
			a, err := b.beginAttempt(parent, 5, now, tc.header, true)
			if err != nil {
				t.Fatal(err)
			}
			defer a.close()
			select {
			case <-a.ctx.Done():
				if got := context.Cause(a.ctx); !errors.Is(got, tc.want) {
					t.Fatalf("cause = %v, want %v", got, tc.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("upstream attempt did not time out")
			}
			if parent.Err() != nil {
				t.Fatal("failover canceled inbound context")
			}
		})
	}
}

func TestFailoverBudgetSemanticOutputStopsPreOutputTimer(t *testing.T) {
	b := testFailoverBudget(config.FailoverBudgetConfig{TotalTimeout: 350 * time.Millisecond, PerAccountTimeout: 300 * time.Millisecond, StopClockOnStream: true})
	now := time.Now()
	b.StartClock(now)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := b.beginAttempt(parent, 5, now, 125*time.Millisecond, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	a.headersReceived(http.StatusOK)
	select {
	case <-a.ctx.Done():
		t.Fatalf("header clock was kept after headers: %v", context.Cause(a.ctx))
	case <-time.After(160 * time.Millisecond):
	}
	if err := a.semanticOutput(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.ctx.Done():
		t.Fatalf("pre-output timers truncated a live stream: %v", context.Cause(a.ctx))
	case <-time.After(360 * time.Millisecond):
	}
	cancel()
	select {
	case <-a.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation was lost")
	}
	attempt, err := b.beginAttempt(parent, 6, time.Now(), 0, true)
	if attempt != nil || !errors.Is(err, context.Canceled) || b.outcome().Attempts != 1 {
		t.Fatalf("canceled request admitted another send: %v, %v", attempt, err)
	}
}

// Exercise actual Gemini retry calls through the service adapter instead of
// using the handler's selected-account count as a substitute.
type failoverBudgetGeminiUpstream struct {
	service.HTTPUpstream
	sends int
	hang  bool
}

func (u *failoverBudgetGeminiUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.sends++
	if u.hang {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unavailable"}}`))}, nil
}

func TestFailoverBudgetGeminiActualAttempts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     int
		sameLimit int
		hang      bool
		wantSends int
		wantErr   error
	}{
		{"service_retry_rejected_at_total_cap", 2, 0, false, 2, errFailoverBudget},
		{"service_retry_rejected_at_same_account_cap", 4, 1, false, 2, errFailoverSameAccount},
		{"hanging_upstream_canceled", 4, 0, true, 1, errFailoverBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.MaxSameAccountRetriesGemini = 4
			upstream := &failoverBudgetGeminiUpstream{hang: tc.hang}
			gemini := service.NewGeminiMessagesCompatService(nil, nil, nil, nil, nil, nil, upstream, nil, cfg)
			account := &service.Account{ID: 4, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}}
			fs := NewFailoverState(3, false)
			budget := config.FailoverBudgetConfig{MaxTotalUpstreamAttempts: tc.limit, MaxSameAccountAttemptsTotal: tc.sameLimit}
			if tc.hang {
				budget.TotalTimeout = 75 * time.Millisecond
			}
			fs.Budget = testFailoverBudget(budget)
			fs.AppendHop(SwitchHop{Kind: SwitchHopAccountSwitch, FromAccountID: 1})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:generateContent", nil)
			ctx, finish := fs.beginForward(c.Request.Context(), c, false, 0)
			if fs.Budget.outcome().Attempts != 0 || fs.SwitchChain[0].ToAccountID != 0 {
				t.Fatal("selection without a send affected count or switch target")
			}
			_, _ = gemini.ForwardNative(ctx, c, account, "gemini-test", "generateContent", false, []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
			if got := finish(); !errors.Is(got, tc.wantErr) {
				t.Fatalf("budget result = %v, want %v; sends = %d, response = %q", got, tc.wantErr, upstream.sends, recorder.Body.String())
			}
			if upstream.sends != tc.wantSends || fs.Budget.outcome().Attempts != tc.wantSends || fs.SwitchChain[0].ToAccountID != account.ID {
				t.Fatalf("sends = %d, outcome = %+v, hop = %+v", upstream.sends, fs.Budget.outcome(), fs.SwitchChain[0])
			}
			if recorder.Body.Len() != 0 || c.Request.Context().Err() != nil {
				t.Fatalf("local budget failure committed before terminal mapping or canceled inbound request: %q", recorder.Body.String())
			}
		})
	}
}
