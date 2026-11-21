package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFailoverObservationTracksSendsAndPreservesOpsAttribution(t *testing.T) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	fs := NewFailoverState(3, false)
	for _, tc := range []struct {
		account int64
		status  int
	}{{1, 429}, {1, 502}, {2, 503}, {3, 200}} {
		fs.observeForward(c, &service.Account{ID: tc.account, Platform: service.PlatformAnthropic}, nil, "claude-test")
		requestFailoverObservation(c).beginSend(tc.account)(&http.Response{StatusCode: tc.status}, nil)
	}
	// An account that passes selection but never sends must not move the total.
	fs.observeForward(c, &service.Account{ID: 4, Platform: service.PlatformAnthropic}, nil, "claude-test")
	observation := requestFailoverObservation(c)
	require.Equal(t, 4, observation.attempts)
	require.Equal(t, 2, observation.switches)
	finishFailoverObservation(c)
	finishFailoverObservation(c)
	failure := &service.OpsUpstreamErrorEvent{Kind: "failover", UpstreamStatusCode: 503, SkipMonitoring: true}
	c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{failure})
	entry := &service.OpsInsertErrorLogInput{}
	applyOpsUpstreamFieldsFromContext(c, entry)
	appendFailoverOpsTelemetry(c, entry)
	require.True(t, shouldSkipFinalOpsFailure(c))
	require.EqualValues(t, 503, *entry.UpstreamStatusCode)
	require.Same(t, failure, entry.UpstreamErrors[len(entry.UpstreamErrors)-1])
	require.NoError(t, service.SanitizeOpsUpstreamErrorsForQueue(entry))
	require.NotNil(t, entry.UpstreamErrorsJSON)
	require.Contains(t, *entry.UpstreamErrorsJSON, `"kind":"failover_switch"`)
	require.Contains(t, *entry.UpstreamErrorsJSON, `"attempt_seq":4`)
}

func TestFailoverForwardContextInstallsSinkBeforeCapture(t *testing.T) {
	old := service.FailoverDecisionGateEnabled()
	service.SetFailoverDecisionGate(true)
	t.Cleanup(func() { service.SetFailoverDecisionGate(old) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx := gatewayFailoverForwardContext(c)
	sink := service.FailoverSideEffectSinkFrom(ctx)
	require.NotNil(t, sink)
	fs := NewFailoverState(2, false)
	fs.observeForward(c, &service.Account{ID: 1, Platform: service.PlatformAnthropic}, nil, "test")
	forwardCtx, finish := fs.beginForward(ctx, c, false, 0)
	defer finish()
	require.Same(t, sink, service.FailoverSideEffectSinkFrom(forwardCtx))

	upstream := &failoverObservedAnthropicUpstream{}
	cfg := &config.Config{}
	gw := service.NewGatewayService(nil, nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	account := &service.Account{ID: 1, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test"}}
	body := service.NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-5","max_tokens":5,"messages":[{"role":"user","content":"hello"}]}`))
	parsed, err := service.ParseGatewayRequest(body, service.PlatformAnthropic)
	require.NoError(t, err)
	_, err = gw.Forward(forwardCtx, c, account, parsed)
	var failoverErr *service.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Same(t, sink, upstream.sink)
	require.Equal(t, 1, sink.Len())
	require.Equal(t, 1, requestFailoverObservation(c).attempts)
}

type failoverObservedAnthropicUpstream struct {
	service.HTTPUpstream
	sink *service.FailoverSideEffectSink
}

func (u *failoverObservedAnthropicUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.sink = service.FailoverSideEffectSinkFrom(req.Context())
	return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"error":{"type":"api_error","message":"unavailable"}}`))}, nil
}
