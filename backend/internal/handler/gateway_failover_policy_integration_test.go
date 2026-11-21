package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type gatewayFailoverIntegrationReader struct {
	ids  []int64
	rows map[int64]*service.GroupFailoverPolicyRow
}

func (r *gatewayFailoverIntegrationReader) LoadGroupFailoverPolicy(_ context.Context, id int64) (*service.GroupFailoverPolicyRow, error) {
	r.ids = append(r.ids, id)
	return r.rows[id], nil
}

func TestGatewayFailoverPolicyAuthenticatedGroupOverride(t *testing.T) {
	upstream := &costAuditGatewayUpstream{failures: 1}
	h, group := newCostAuditGateway(t, upstream, &costAuditGatewayBilling{})
	h.cfg.Gateway.FailoverPolicy.Enabled = true
	h.cfg.Gateway.FailoverOn400 = true
	zero, disabled := 0, false
	reader := &gatewayFailoverIntegrationReader{rows: map[int64]*service.GroupFailoverPolicyRow{
		group.ID: {GroupID: group.ID, Policy: &service.FailoverPolicy{MaxAccountSwitches: &zero, FailoverOn400: &disabled}},
	}}
	h.gatewayService.ConfigureFailoverRouting(reader, nil)
	h.gatewayService.EnableHTTPFailoverObservation()
	response := serveCostAuditResponses(h, group)
	require.Equal(t, 502, response.Code)
	require.Len(t, upstream.accounts, 1, "explicit zero prevents a second upstream account")
	require.Equal(t, []int64{group.ID}, reader.ids)
	ctx := h.gatewayService.WithFailoverRouting(context.Background())
	_, policy, source := h.gatewayService.ResolveFailoverRoutingPolicy(ctx, &group.ID)
	require.Equal(t, service.FailoverPolicySourceGroup, source)
	require.NotNil(t, policy.FailoverOn400)
	require.False(t, *policy.FailoverOn400)
	_, policy, source = h.gatewayService.ResolveFailoverRoutingPolicy(ctx, nil)
	require.Equal(t, service.FailoverPolicySourceGlobal, source)
	require.True(t, *policy.FailoverOn400)
}
