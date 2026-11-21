package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayStreamTerminalFailoverIsWrittenOnce(t *testing.T) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, err := c.Writer.WriteString(partialMessageStartSSE)
	require.NoError(t, err)
	h := &GatewayHandler{}
	failure := &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	markFailoverSuppressed(c, "semantic_bytes", failure.StatusCode)
	h.handleFailoverExhausted(c, failure, service.PlatformAnthropic, true)
	h.handleFailoverExhausted(c, failure, service.PlatformAnthropic, true)
	output := r.Body.String()
	require.Equal(t, 1, strings.Count(output, "event: message_start\n"))
	require.Equal(t, 1, strings.Count(output, "event: error\n"))
	require.Equal(t, 1, strings.Count(output, "event: message_stop\n"))
	require.Contains(t, output, `"exhausted_reason":"stream_started"`)
	require.True(t, service.IsResponseCommitted(c))
}

func TestGatewayStreamTerminalBudgetHidesAttempts(t *testing.T) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set(failoverExposeDetailKey, true)
	_, err := c.Writer.WriteString(partialMessageStartSSE)
	require.NoError(t, err)

	h := &GatewayHandler{}
	outcome := &FailoverOutcomeError{Reason: failoverReasonBudget, LastStatus: http.StatusServiceUnavailable, Attempts: 3}
	h.handleFailoverBudgetExhausted(c, outcome, true)
	h.handleFailoverBudgetExhausted(c, outcome, true)

	require.Equal(t, http.StatusOK, r.Code)
	require.Empty(t, r.Result().Header.Get("X-Failover-Reason"))
	output := r.Body.String()
	require.Equal(t, 1, strings.Count(output, "event: message_start\n"))
	require.Equal(t, 1, strings.Count(output, "event: error\n"))
	require.Equal(t, 1, strings.Count(output, "event: message_stop\n"))
	require.NotContains(t, output, `"attempts"`)
	require.Contains(t, output, `"exhausted_reason":"budget"`)
	require.Equal(t, *outcome, c.MustGet(failoverOutcomeContextKey))
	require.True(t, service.IsResponseCommitted(c))
}
