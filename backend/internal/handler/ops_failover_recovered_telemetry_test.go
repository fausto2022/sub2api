package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRecoveredFailoverTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		account                                int64
		legacy, hidden, skip, noSend, disabled bool
		ingress, fallbackOnly                  bool
		wantRow                                bool
	}{
		{name: "503 then another account 200", account: 2, wantRow: true},
		{name: "normal recovered row", account: 2, legacy: true, wantRow: true},
		{name: "hidden legacy attempt", account: 2, legacy: true, hidden: true},
		{name: "explicit skip", account: 2, skip: true},
		{name: "same account", account: 1},
		{name: "candidate never sends", account: 2, noSend: true},
		{name: "monitoring disabled", account: 2, disabled: true},
		{name: "ingress rejection", account: 2, ingress: true},
		{name: "unauthenticated fallback key", account: 2, fallbackOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 3)
			var cfg *config.Config
			if tc.disabled {
				cfg = &config.Config{}
			}
			ops := service.NewOpsService(&ingressRejectOpsRepo{}, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			groupID := int64(42)
			router.POST("/v1/messages", func(c *gin.Context) {
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.RequestID, "shared-id"))
				key := &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformGemini}}
				if tc.fallbackOnly {
					middleware.SetOpsFallbackAPIKey(c, key)
				} else {
					c.Set(string(middleware.ContextKeyAPIKey), key)
				}
				if tc.ingress {
					middleware.MarkIngressRejected(c, middleware.IngressRejectInvalidAPIKey)
				}
				state := NewFailoverState(3, false)
				state.observeForward(c, &service.Account{ID: 1, Platform: service.PlatformAntigravity}, &groupID, "gemini")
				requestFailoverObservation(c).beginSend(1)(&http.Response{StatusCode: 503}, nil)
				state.observeForward(c, &service.Account{ID: tc.account, Platform: service.PlatformAntigravity}, &groupID, "gemini")
				if !tc.noSend {
					requestFailoverObservation(c).beginSend(tc.account)(&http.Response{StatusCode: 200}, nil)
				}
				if tc.legacy {
					c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{{Kind: "failover", UpstreamStatusCode: 503, Message: "legacy", SkipMonitoring: tc.hidden}})
				}
				if tc.skip {
					c.Set(service.OpsSkipPassthroughKey, true)
				}
				c.Status(http.StatusOK)
				finishFailoverObservation(c)
			})
			var previous uuid.UUID
			for range 2 {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
				require.Equal(t, http.StatusOK, w.Code)
				if !tc.wantRow {
					require.Zero(t, OpsErrorLogQueueLength())
					continue
				}
				require.EqualValues(t, 1, OpsErrorLogQueueLength())
				entry := (<-opsErrorLogQueue).entry
				require.Equal(t, "shared-id", entry.RequestID)
				require.Equal(t, groupID, *entry.GroupID)
				require.Equal(t, service.PlatformGemini, entry.Platform)
				if tc.legacy {
					require.Equal(t, "upstream", entry.ErrorPhase)
					require.Equal(t, "provider", entry.ErrorOwner)
					require.Equal(t, http.StatusServiceUnavailable, *entry.UpstreamStatusCode)
				} else {
					require.Equal(t, service.OpsFailoverTelemetryPhase, entry.ErrorPhase)
					require.Empty(t, entry.ErrorOwner)
					require.Empty(t, entry.ErrorBody)
					require.Nil(t, entry.UpstreamStatusCode)
				}
				require.NotNil(t, entry.UpstreamErrorsJSON)
				events, err := service.ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
				require.NoError(t, err)
				if !tc.legacy {
					require.Len(t, events, 1)
				}
				var eventID uuid.UUID
				for _, event := range events {
					if event.Kind == service.OpsFailoverSwitchEvent {
						eventID, err = uuid.Parse(event.ExecutionID)
						require.NoError(t, err)
					}
				}
				require.NotEqual(t, uuid.Nil, eventID)
				require.NotEqual(t, previous, eventID)
				previous = eventID
				// Drain the test queue accounting before serving the next request.
				opsErrorLogQueueLen.Add(-1)
			}
		})
	}
}
