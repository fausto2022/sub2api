package service

// T2.2 的门控单测：决策（决定换不换号 / 标不标记）与副作用执行分离（修 R-17）。
//
// 无 build tag：两套 tag 模式都跑。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestDecideFailover_T17_MarkAccountTriState 锁定「算不算账号问题」三态（T-17）。
func TestDecideFailover_T17_MarkAccountTriState(t *testing.T) {
	accountAdvice := RetryAdvice{Failover: true, SameAccount: RetryInherit, Scope: GatewayFailureScopeAccount}
	providerAdvice := RetryAdvice{Failover: true, SameAccount: RetryInherit, Scope: GatewayFailureScopeProvider}
	requestAdvice := RetryAdvice{Failover: false, SameAccount: RetryOff, Scope: GatewayFailureScopeRequest}

	cases := []struct {
		name        string
		in          FailoverDecisionInput
		wantSwitch  bool
		wantMark    bool
		wantReason  FailoverSuppressedReason
		description string
	}{
		{
			name:        "provider 级 5xx：不算账号问题",
			in:          FailoverDecisionInput{Class: ClassUpstream5xx, Advice: accountAdvice, Scope: GatewayFailureScopeProvider, CanFailover: true},
			wantSwitch:  true,
			wantMark:    false,
			description: "上游整体故障不应记到被选中的账号头上",
		},
		{
			name:        "request-scoped 529：不算",
			in:          FailoverDecisionInput{Class: ClassCapacityShed, Advice: providerAdvice, Scope: GatewayFailureScopeProvider, RequestScopedTransient: true, CanFailover: true},
			wantSwitch:  true,
			wantMark:    false,
			description: "与账号无关的降载不写账号状态",
		},
		{
			name:        "凭据 401：算账号问题",
			in:          FailoverDecisionInput{Class: ClassAuthInvalid, Advice: accountAdvice, Scope: GatewayFailureScopeAccount, IsCredentialFailure: true, CanFailover: true},
			wantSwitch:  true,
			wantMark:    true,
			description: "凭据失效必须记账，否则坏账号不会被摘掉",
		},
		{
			name:        "普通 500：算",
			in:          FailoverDecisionInput{Class: ClassUpstream5xx, Advice: accountAdvice, Scope: GatewayFailureScopeAccount, CanFailover: true},
			wantSwitch:  true,
			wantMark:    true,
			description: "沿用现状：账号归属的 5xx 记账号",
		},
		{
			name:        "request_invalid 400：不算",
			in:          FailoverDecisionInput{Class: ClassRequestInvalid, Advice: requestAdvice, Scope: GatewayFailureScopeRequest, CanFailover: true},
			wantSwitch:  false,
			wantMark:    false,
			description: "请求本身有问题，换号与标记都无意义",
		},
		{
			name:        "绑定透传：不换号、不标记",
			in:          FailoverDecisionInput{Class: ClassBindingPassthru, Advice: accountAdvice, Scope: GatewayFailureScopeAccount, BoundUpstreamPassthrough: true, CanFailover: true},
			wantSwitch:  false,
			wantMark:    false,
			wantReason:  FailoverSuppressedBoundPassthru,
			description: "缓存绑定的上游账号状态要原样回写",
		},
		{
			name:        "NextAccountStop：不换号、不标记",
			in:          FailoverDecisionInput{Class: ClassUpstream5xx, Advice: accountAdvice, Scope: GatewayFailureScopeAccount, NextAccountAction: NextAccountStop, CanFailover: true},
			wantSwitch:  false,
			wantMark:    false,
			wantReason:  FailoverSuppressedNextAccountStop,
			description: "既有语义的否决权最高",
		},
		{
			name:        "已写语义字节：不换号（但仍可记账）",
			in:          FailoverDecisionInput{Class: ClassUpstream5xx, Advice: accountAdvice, Scope: GatewayFailureScopeAccount, CanFailover: false, WriteBlockReason: FailoverSuppressedSemanticBytes},
			wantSwitch:  false,
			wantMark:    true,
			wantReason:  FailoverSuppressedSemanticBytes,
			description: "R-01/R-11：写了语义字节不能换号，但失败本身仍算账号问题",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := DecideFailover(tc.in)
			require.Equal(t, tc.wantSwitch, got.Switch, "Switch（%s）", tc.description)
			require.Equal(t, tc.wantMark, got.MarkAccount, "MarkAccount（%s）", tc.description)
			require.Equal(t, tc.wantReason, got.SuppressedReason)
			if !got.MarkAccount {
				require.Zero(t, got.Cooldown, "不标记账号时不应产生冷却")
			}
		})
	}
}

// TestDecideFailover_NonAccountScopeSkipsStickyClear 锁定 B2 的类别级约束：
// provider/request 归属的失败不清粘性（粘性绑定代表「这个池的会话」，不是账号问题）。
func TestDecideFailover_NonAccountScopeSkipsStickyClear(t *testing.T) {
	advice := RetryAdvice{Failover: true, ClearSticky: true, Scope: GatewayFailureScopeProvider}
	decision := DecideFailover(FailoverDecisionInput{
		Class: ClassCapacityShed, Advice: advice, Scope: GatewayFailureScopeProvider, CanFailover: true,
	})
	require.False(t, decision.ClearSticky)
}

// TestFailoverSideEffectSink_ApplyOncePerAccount 锁定 R-18：同请求同账号只执行一次。
func TestFailoverSideEffectSink_ApplyOncePerAccount(t *testing.T) {
	sink := NewFailoverSideEffectSink()
	var calls atomic.Int32
	sink.Add(42, FailoverDecision{MarkAccount: true}, func() { calls.Add(1) })
	sink.Add(42, FailoverDecision{MarkAccount: true}, func() { calls.Add(1) })
	sink.Add(7, FailoverDecision{MarkAccount: true}, func() { calls.Add(1) })

	require.Equal(t, 2, sink.Apply(42), "同账号两条都执行")
	require.Equal(t, int32(2), calls.Load())
	require.Zero(t, sink.Apply(42), "重复 Apply 不再执行（R-18）")
	require.Equal(t, int32(2), calls.Load())

	require.Equal(t, 1, sink.Apply(7))
	require.Equal(t, int32(3), calls.Load())
}

// TestFailoverSideEffectSink_SuppressPreventsAccountRewrite 是 R-17 的核心断言：
// handler 放弃换号 → 抑制挂起的副作用 → 账号状态一次都不被改写。
func TestFailoverSideEffectSink_SuppressPreventsAccountRewrite(t *testing.T) {
	sink := NewFailoverSideEffectSink()
	var writes atomic.Int32
	sink.Add(149, FailoverDecision{MarkAccount: true}, func() { writes.Add(1) })
	require.Equal(t, 1, sink.Len())

	require.Equal(t, 1, sink.Suppress(149, FailoverSuppressedSemanticBytes))
	require.Zero(t, sink.Apply(149), "被抑制的副作用不得执行")
	require.Zero(t, writes.Load(), "账号状态必须零改写")
}

// TestApplyFailoverSideEffects_DeniedDecisionDoesNotTouchAccount 证明「决策否定 →
// 不进入写账号状态的代码路径」。用 nil RateLimitService 作探针：一旦真的去标记就会 panic。
func TestApplyFailoverSideEffects_DeniedDecisionDoesNotTouchAccount(t *testing.T) {
	svc := &GatewayService{} // rateLimitService == nil
	account := &Account{ID: 149, Platform: PlatformAnthropic}
	resp := &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}

	require.NotPanics(t, func() {
		svc.ApplyFailoverSideEffects(context.Background(), resp, account, FailoverDecision{
			Class:       ClassCapacityShed,
			Scope:       GatewayFailureScopeProvider,
			MarkAccount: false,
		})
	}, "MarkAccount=false 时不得进入 RateLimitService（那会改写账号状态）")
}

// TestHandleFailoverSideEffects_GateDefersSideEffects 证明门控开启后 service 不再就地标记：
// 同样用 nil RateLimitService 作探针 —— 一旦就地标记就 panic；同时断言副作用被挂起。
func TestHandleFailoverSideEffects_GateDefersSideEffects(t *testing.T) {
	SetFailoverDecisionGate(true)
	t.Cleanup(func() { SetFailoverDecisionGate(false) })

	svc := &GatewayService{} // rateLimitService == nil：任何就地标记都会 panic
	account := &Account{ID: 149, Platform: PlatformAnthropic}
	resp := &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}

	sink := NewFailoverSideEffectSink()
	ctx := WithFailoverSideEffectSink(context.Background(), sink)

	require.NotPanics(t, func() {
		svc.handleFailoverSideEffects(ctx, resp, account)
	}, "门控开启时 service 不得就地标记账号")
	require.Equal(t, 1, sink.Len(), "副作用应挂起到每请求收集器")

	// handler 放弃换号 → 抑制 → 一条都不执行（也不 panic）。
	require.Equal(t, 1, sink.Suppress(account.ID, FailoverSuppressedSemanticBytes))
	require.NotPanics(t, func() { sink.Apply(account.ID) })
}

// TestHandleFailoverSideEffects_GateVetoesNonAccountFailures 证明门控开启后
// 「不算账号问题」的失败一条副作用都不挂起（对照 R-17 的现状：无条件标记）。
func TestHandleFailoverSideEffects_GateVetoesNonAccountFailures(t *testing.T) {
	SetFailoverDecisionGate(true)
	t.Cleanup(func() { SetFailoverDecisionGate(false) })

	svc := &GatewayService{} // 探针：被标记就会 panic
	account := &Account{ID: 200, Platform: PlatformOpenAI}
	// 404 属 request_invalid（Scope=request）→ 不算账号问题。
	resp := &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}}

	sink := NewFailoverSideEffectSink()
	ctx := WithFailoverSideEffectSink(context.Background(), sink)

	require.NotPanics(t, func() { svc.handleFailoverSideEffects(ctx, resp, account) })
	require.Zero(t, sink.Len(), "request 级失败不应产生账号级副作用")
}

// TestFailoverDecisionGate_DefaultOff 锁定默认零行为变更：门控默认关闭。
func TestFailoverDecisionGate_DefaultOff(t *testing.T) {
	require.False(t, FailoverDecisionGateEnabled(), "门控默认必须关闭（等价现状）")
}

// TestFailoverSideEffectSinkForRequest 锁定 handler 侧的安装 / 取用 / 抑制链路。
func TestFailoverSideEffectSinkForRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	require.Nil(t, FailoverSideEffectSinkForRequest(c), "未安装时返回 nil")

	sink := InstallFailoverSideEffectSinkForRequest(c)
	require.NotNil(t, sink)
	require.Same(t, sink, FailoverSideEffectSinkForRequest(c))

	var writes atomic.Int32
	sink.Add(9, FailoverDecision{MarkAccount: true}, func() { writes.Add(1) })

	// service 侧从 ctx 拿到同一个收集器（不需要 import handler）。
	require.Same(t, sink, FailoverSideEffectSinkFrom(c.Request.Context()))

	require.Equal(t, 1, SuppressFailoverSideEffectsForRequest(c, 9, FailoverSuppressedSemanticBytes))
	require.Zero(t, ApplyFailoverSideEffectsForRequest(c, 9))
	require.Zero(t, writes.Load())
}
