package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const opsRecoveredFailoverEnqueuedKey = "ops_recovered_failover_enqueued"

// attachOpsFailoverTelemetry runs after legacy attribution and filtering. Only
// the new decisions gain an execution ID; original correlation fields stay put.
func attachOpsFailoverTelemetry(c *gin.Context, entry *service.OpsInsertErrorLogInput) {
	legacyCount := len(entry.UpstreamErrors)
	appendFailoverOpsTelemetry(c, entry)
	newCount := len(entry.UpstreamErrors) - legacyCount
	var executionID string
	for _, event := range entry.UpstreamErrors[:newCount] {
		if event.Kind != service.OpsFailoverSwitchEvent && event.Kind != service.OpsFailoverExhaustedEvent {
			continue
		}
		if executionID == "" {
			executionID = opsFailoverExecutionID(c)
		}
		event.ExecutionID = executionID
	}
}

func opsFailoverExecutionID(c *gin.Context) string {
	ctx, id := service.EnsureGatewayExecutionID(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	return id.String()
}

// Only called on the existing recovered path, after all legacy events have
// been considered. A synthetic row must not supply legacy failure fields.
func recoveredFailoverTelemetryEntry(c *gin.Context, status int) *service.OpsInsertErrorLogInput {
	if c == nil || c.Request == nil || status < http.StatusOK || status >= http.StatusMultipleChoices ||
		c.GetBool(opsRecoveredFailoverEnqueuedKey) || shouldSkipFinalOpsFailure(c) || isCountTokensRequest(c) {
		return nil
	}
	o := requestFailoverObservation(c)
	if o == nil || !o.logged || o.switches == 0 || o.reason != "" {
		return nil
	}
	apiKey, authenticated := middleware.GetAPIKeyFromContext(c)
	if !authenticated || apiKey == nil {
		return nil
	}
	ctx := c.Request.Context()
	platform := resolveOpsPlatform(ctx, apiKey, o.platform)
	var groupID *int64
	if o.groupID > 0 {
		id := o.groupID // Recorded from the authorized routing group, never the request body.
		groupID = &id
		if apiKey.Group != nil && apiKey.Group.ID == id && apiKey.Group.Platform != "" {
			platform = apiKey.Group.Platform
		} else if o.platform != "" {
			platform = o.platform
		}
	} else if apiKey.GroupID != nil && *apiKey.GroupID > 0 {
		id := *apiKey.GroupID
		groupID = &id
	}
	for _, attempt := range o.attemptsLog {
		if service.OpsObserverMaySkipMonitoring(c, o.platform, attempt.status) {
			return nil
		}
	}
	requestID, _ := ctx.Value(ctxkey.RequestID).(string)
	clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID = c.Writer.Header().Get("X-Request-Id")
	}
	return &service.OpsInsertErrorLogInput{
		RequestID: requestID, ClientRequestID: clientRequestID,
		GroupID: groupID, Platform: platform, StatusCode: status,
		ErrorPhase: service.OpsFailoverTelemetryPhase, ErrorType: service.OpsFailoverTelemetryPhase,
		ErrorSource: "gateway", Severity: "P3", CreatedAt: time.Now(),
		UpstreamErrors: []*service.OpsUpstreamErrorEvent{{Kind: service.OpsFailoverSwitchEvent,
			ExecutionID: opsFailoverExecutionID(c), Message: "Observed account switch"}},
	}
}
