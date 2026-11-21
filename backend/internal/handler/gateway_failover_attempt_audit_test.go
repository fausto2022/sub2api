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
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type costAuditGatewayWriter struct {
	rows    []service.UpstreamCostAttempt
	blocked chan struct{}
}

func (w *costAuditGatewayWriter) InsertUpstreamCostAttempts(ctx context.Context, rows []service.UpstreamCostAttempt) (service.UpstreamCostAttemptWriteResult, error) {
	if w.blocked != nil {
		select {
		case <-w.blocked:
			return service.UpstreamCostAttemptWriteResult{}, errors.New("audit write failed")
		case <-ctx.Done():
			return service.UpstreamCostAttemptWriteResult{}, ctx.Err()
		}
	}
	w.rows = append(w.rows, rows...)
	return service.UpstreamCostAttemptWriteResult{Inserted: len(rows)}, nil
}

type costAuditGatewaySched struct {
	service.SchedulerCache
	accounts []*service.Account
}

func (f costAuditGatewaySched) GetSnapshot(context.Context, service.SchedulerBucket) ([]*service.Account, bool, error) {
	return f.accounts, true, nil
}

func (f costAuditGatewaySched) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	for _, account := range f.accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return nil, nil
}

type costAuditGatewayGroup struct {
	service.GroupRepository
	group *service.Group
}

func (f costAuditGatewayGroup) GetByID(context.Context, int64) (*service.Group, error) {
	return f.group, nil
}
func (f costAuditGatewayGroup) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return f.group, nil
}

type costAuditGatewaySlots struct{ service.ConcurrencyCache }

func (costAuditGatewaySlots) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (costAuditGatewaySlots) ReleaseAccountSlot(context.Context, int64, string) error { return nil }
func (costAuditGatewaySlots) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (costAuditGatewaySlots) ReleaseUserSlot(context.Context, int64, string) error { return nil }
func (costAuditGatewaySlots) IncrementUserWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (costAuditGatewaySlots) DecrementUserWaitCount(context.Context, int64) error { return nil }
func (costAuditGatewaySlots) GetAccountsLoadBatch(context.Context, []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	return nil, nil
}

type costAuditGatewayBilling struct {
	service.UsageBillingRepository
	commands []*service.UsageBillingCommand
}

func (b *costAuditGatewayBilling) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	b.commands = append(b.commands, cmd)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type costAuditGatewayUpstream struct {
	service.HTTPUpstream
	accounts []int64
	failures int
	partial  bool
}

const costAuditPartialSSE = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","usage":{"input_tokens":12,"output_tokens":1}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}` + "\n\n"

const costAuditFinalSSE = "event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

func (u *costAuditGatewayUpstream) DoWithTLS(_ *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.accounts = append(u.accounts, accountID)
	status, body := http.StatusOK, costAuditPartialSSE
	if u.failures > 0 {
		u.failures--
		status, body = http.StatusBadGateway, `{"error":{"type":"api_error","message":"unavailable"}}`
	} else if !u.partial {
		body += costAuditFinalSSE
	}
	header := make(http.Header)
	header.Set("Content-Type", "text/event-stream")
	header.Set("X-Request-Id", "same-upstream-id")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func newCostAuditGateway(t *testing.T, upstream *costAuditGatewayUpstream, billing *costAuditGatewayBilling) (*GatewayHandler, *service.Group) {
	t.Helper()
	cfg := &config.Config{}
	group := &service.Group{ID: 9501, Hydrated: true, Status: service.StatusActive, Platform: service.PlatformAnthropic, RateMultiplier: 1}
	var accounts []*service.Account
	for i := int64(1); i <= 4; i++ {
		accounts = append(accounts, &service.Account{ID: i, Platform: group.Platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive,
			Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test"},
			AccountGroups: []service.AccountGroup{{AccountID: i, GroupID: group.ID}}})
	}
	snapshot := service.NewSchedulerSnapshotService(costAuditGatewaySched{accounts: accounts}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(newHandlerInflightCache(100), nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gw := service.NewGatewayService(nil, costAuditGatewayGroup{group: group}, nil, billing, nil, nil, nil, nil, cfg,
		snapshot, nil, service.NewBillingService(cfg, nil), nil, nil, nil, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &GatewayHandler{gatewayService: gw, billingCacheService: billingCache, cfg: cfg, maxAccountSwitches: 3,
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(costAuditGatewaySlots{}), SSEPingFormatClaude, 0)}
	return h, group
}

func serveCostAuditResponses(h *GatewayHandler, group *service.Group) *httptest.ResponseRecorder {
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-sonnet-4-5","stream":true,"input":"hello"}`))
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "shared-client-id"))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 9502, UserID: 9503, GroupID: &group.ID, Group: group, User: &service.User{ID: 9503, Balance: 100}})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9503})
	h.Responses(c)
	return out
}

func stopCostAuditGatewayRecorder(t *testing.T, recorder *service.UpstreamCostAttemptRecorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, recorder.Stop(ctx))
}

func TestFailoverAttemptAuditFourSendsOneBilling(t *testing.T) {
	writer := &costAuditGatewayWriter{}
	recorder, err := service.NewUpstreamCostAttemptRecorder(true, writer, service.UpstreamCostAttemptRecorderOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { stopCostAuditGatewayRecorder(t, recorder) })
	upstream, billing := &costAuditGatewayUpstream{failures: 3}, &costAuditGatewayBilling{}
	h, group := newCostAuditGateway(t, upstream, billing)
	h.InstallUpstreamCostAttemptRecorder(recorder)
	first := serveCostAuditResponses(h, group)
	require.Contains(t, first.Body.String(), "event: response.completed")
	require.Len(t, billing.commands, 1)
	require.Len(t, upstream.accounts, 4)
	require.Positive(t, billing.commands[0].BalanceCost)
	second := serveCostAuditResponses(h, group)
	require.Contains(t, second.Body.String(), "event: response.completed")
	require.Len(t, billing.commands, 2)
	require.Equal(t, billing.commands[0].RequestID, billing.commands[1].RequestID)
	stopCostAuditGatewayRecorder(t, recorder)
	require.Len(t, writer.rows, 5)
	for i, row := range writer.rows[:4] {
		require.Equal(t, writer.rows[0].ExecutionID, row.ExecutionID)
		require.Equal(t, i+1, row.AttemptIndex)
		require.Equal(t, upstream.accounts[i], row.AccountID)
		if i < 3 {
			require.Equal(t, http.StatusBadGateway, *row.Status)
			require.Equal(t, "switch_account", row.Decision)
			require.Nil(t, row.BilledUsage)
		} else {
			require.Equal(t, "succeeded", row.Decision)
			require.NotNil(t, row.BilledUsage)
			require.EqualValues(t, 7, row.BilledUsage.OutputTokens)
		}
	}
	require.NotEqual(t, writer.rows[0].ExecutionID, writer.rows[4].ExecutionID)
	require.Equal(t, 1, writer.rows[4].AttemptIndex)
}

func TestFailoverAttemptAuditSendAdmission(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	writer := &costAuditGatewayWriter{}
	recorder, err := service.NewUpstreamCostAttemptRecorder(true, writer, service.UpstreamCostAttemptRecorderOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { stopCostAuditGatewayRecorder(t, recorder) })
	installCostAttemptAudit(c, recorder)
	state := NewFailoverState(3, false)
	state.Budget = newFailoverBudget(config.GatewayFailoverPolicyConfig{Enabled: true, Budget: config.FailoverBudgetConfig{MaxTotalUpstreamAttempts: 2}})
	upstream := &costAuditGatewayUpstream{failures: 3}
	h, _ := newCostAuditGateway(t, upstream, &costAuditGatewayBilling{})
	gw := h.gatewayService
	account := &service.Account{ID: 1, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}}
	for i := 0; i < 3; i++ {
		body := service.NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-5","max_tokens":5,"messages":[{"role":"user","content":"hello"}]}`))
		parsed, err := service.ParseGatewayRequest(body, service.PlatformAnthropic)
		require.NoError(t, err)
		state.observeForward(c, account, nil, "claude-sonnet-4-5")
		ctx, finish := state.beginForward(c.Request.Context(), c, false, 0)
		result, forwardErr := gw.Forward(ctx, c, account, parsed)
		recordGatewayForwardCost(c, result, forwardErr)
		budgetErr := finish()
		if i < 2 {
			require.NoError(t, budgetErr)
		} else {
			require.ErrorIs(t, budgetErr, errFailoverBudget)
			setFailoverTerminal(c, FailoverOutcomeError{Reason: failoverReasonBudget})
		}
	}
	finishFailoverObservation(c)
	stopCostAuditGatewayRecorder(t, recorder)
	require.Equal(t, []int64{1, 1}, upstream.accounts)
	require.Len(t, writer.rows, 2)
	require.Equal(t, "retry_same_account", writer.rows[0].Decision)
	require.Equal(t, "budget_exhausted", writer.rows[1].Decision)
	require.Nil(t, writer.rows[1].BilledUsage)
}

func TestFailoverAttemptAuditPartialUsageAndDefaultOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		writer := &costAuditGatewayWriter{}
		recorder, err := service.NewUpstreamCostAttemptRecorder(enabled, writer, service.UpstreamCostAttemptRecorderOptions{})
		require.NoError(t, err)
		t.Cleanup(func() { stopCostAuditGatewayRecorder(t, recorder) })
		upstream, billing := &costAuditGatewayUpstream{partial: true}, &costAuditGatewayBilling{}
		h, group := newCostAuditGateway(t, upstream, billing)
		h.InstallUpstreamCostAttemptRecorder(recorder)
		response := serveCostAuditResponses(h, group)
		require.Contains(t, response.Body.String(), "event: response.failed")
		require.Len(t, billing.commands, 1)
		stopCostAuditGatewayRecorder(t, recorder)
		if !enabled {
			require.Empty(t, writer.rows)
			continue
		}
		require.Len(t, writer.rows, 1)
		require.Equal(t, "partial_usage", writer.rows[0].Decision)
		require.NotNil(t, writer.rows[0].BilledUsage)
		require.EqualValues(t, 12, writer.rows[0].BilledUsage.InputTokens)
	}
}

func TestFailoverAttemptAuditWriterFailureDoesNotAffectBilling(t *testing.T) {
	blocked := make(chan struct{})
	writer := &costAuditGatewayWriter{blocked: blocked}
	recorder, err := service.NewUpstreamCostAttemptRecorder(true, writer, service.UpstreamCostAttemptRecorderOptions{WriteTimeout: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { stopCostAuditGatewayRecorder(t, recorder) })
	upstream, billing := &costAuditGatewayUpstream{}, &costAuditGatewayBilling{}
	h, group := newCostAuditGateway(t, upstream, billing)
	h.InstallUpstreamCostAttemptRecorder(recorder)
	response := serveCostAuditResponses(h, group)
	require.Contains(t, response.Body.String(), "event: response.completed")
	require.Len(t, billing.commands, 1)
	require.Zero(t, recorder.Stats().Failed)
	close(blocked)
	stopCostAuditGatewayRecorder(t, recorder)
	require.EqualValues(t, 1, recorder.Stats().Failed)
}
