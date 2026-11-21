package service

// 本文件锁定 P0/T0.4 的两件事：
//
//  1. **判定顺序**：Canceled → BoundUpstreamPassthrough → RetryableOnSameAccount →
//     RequestScopedTransient → 状态码。顺序不是实现细节，是契约：顺序错了会让某一类
//     错误走错分支（客户端取消被写成冷却、Google 间歇性 400 不再重试、provider 级降载
//     被算成账号问题），而这三类回归在既有测试里都有硬约束。
//  2. **F-01 等价表**：分类器对现有 4 处硬编码状态码集合的每个状态码，输出与原硬编码
//     语义逐项相等。等价表是「P1 接线不改行为」的唯一凭据。
//
// 本文件没有 build tag：`go test ./internal/service/...` 与 `-tags=unit` 两套都必须跑它。
// （既有 gateway_upstream_transport_error_test.go 是 unit-only，所以这里重新表述那三条
// 硬规则，而不是依赖它的编译标签。）

import (
	"context"
	"errors"
	"io"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// —— 旧实现的 test-only 副本 ————————————————————————————————————————————————
//
// 这 4 个函数在改造前各自硬编码状态码集合。副本只用于断言「分类器输出 == 旧语义」，
// 不进生产代码（P1/T1.2 会再写一份更完整的双跑副本，命名刻意不同以免冲突）。

// referenceLegacyFailoverShared 是下面三处逐字相同的实现：
//
//	gateway_forward.go:47-54              shouldFailoverUpstreamError
//	antigravity_gateway_retry.go:780-787  shouldFailoverUpstreamError
//	gemini_messages_compat_service.go:1706-1713 shouldFailoverGeminiUpstreamError
func referenceLegacyFailoverShared(statusCode int) bool {
	switch statusCode {
	case 401, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// referenceLegacyFailoverOpenAI 是 openai_gateway_upstream_errors.go:303-310 的副本。
func referenceLegacyFailoverOpenAI(statusCode int) bool {
	switch statusCode {
	case 401, 402, 403, 405, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// failoverTestSite 是「一个旧实现入口」：网关族 + 它的状态码集合。
type failoverTestSite struct {
	name    string
	profile FailoverProfile
	legacy  func(statusCode int) bool
}

// failoverTestSites 是 F-01 表的 4 个入口 + 通用兜底（不指定网关族时 == 三处共享集合）。
func failoverTestSites() []failoverTestSite {
	return []failoverTestSite{
		{"gateway_forward.go:47-54", FailoverProfileAnthropic, referenceLegacyFailoverShared},
		{"antigravity_gateway_retry.go:780-787", FailoverProfileAntigravity, referenceLegacyFailoverShared},
		{"gemini_messages_compat_service.go:1706-1713", FailoverProfileGemini, referenceLegacyFailoverShared},
		{"openai_gateway_upstream_errors.go:303-310", FailoverProfileOpenAI, referenceLegacyFailoverOpenAI},
		{"generic（未指定网关族）", FailoverProfileGeneric, referenceLegacyFailoverShared},
	}
}

func failoverTestBoolPtr(v bool) *bool { return &v }

func failoverTestIntPtr(v int) *int { return &v }

func failoverTestDurationPtr(v time.Duration) *time.Duration { return &v }

func failoverTestStringPtr(v string) *string { return &v }

// —— 类别清单 ————————————————————————————————————————————————————————————————

// TestFailoverClassifierClassInventory 锁定「11 个类别」这一数量与取值稳定性。
// 类别取值会出现在配置与日志里，改名/删类属于破坏性变更（配置校验与排障查询都会失效）。
func TestFailoverClassifierClassInventory(t *testing.T) {
	require.Len(t, failoverAllClasses, 11, "design §3.3.2 的 10 类 + canceled = 11 类")

	seen := make(map[FailureClass]struct{}, len(failoverAllClasses))
	for _, class := range failoverAllClasses {
		require.NotEmpty(t, string(class), "类别名不能为空")
		_, dup := seen[class]
		require.False(t, dup, "类别 %q 重复登记", class)
		seen[class] = struct{}{}
	}

	// 与改造前的字面量对齐：这些字符串是配置与日志的对外契约。
	require.Equal(t, FailureClass("auth_invalid"), ClassAuthInvalid)
	require.Equal(t, FailureClass("rate_limited"), ClassRateLimited)
	require.Equal(t, FailureClass("quota_exhausted"), ClassQuotaExhausted)
	require.Equal(t, FailureClass("capacity_shed"), ClassCapacityShed)
	require.Equal(t, FailureClass("upstream_5xx"), ClassUpstream5xx)
	require.Equal(t, FailureClass("upstream_timeout"), ClassUpstreamTimeout)
	require.Equal(t, FailureClass("network"), ClassNetwork)
	require.Equal(t, FailureClass("request_invalid"), ClassRequestInvalid)
	require.Equal(t, FailureClass("policy_blocked"), ClassPolicyBlocked)
	require.Equal(t, FailureClass("binding_passthru"), ClassBindingPassthru)
	require.Equal(t, FailureClass("canceled"), ClassCanceled)
}

// —— F-01 等价表 ————————————————————————————————————————————————————————————

// failoverEquivalenceRow 是 F-01 表的一行。
type failoverEquivalenceRow struct {
	name string
	in   ClassifyInput
	// wantClass 期望类别；wantClassOpenAI 非空时表示「只有 OpenAI 网关族不同」。
	wantClass       FailureClass
	wantClassOpenAI FailureClass
	// legacyComparable=true 时逐站点断言 advice.Failover == 该站点的旧实现。
	// 只有「带真实 HTTP 状态码、且输入不携带会改变判定的既有语义标志」的行才成立：
	// 4 处旧函数是纯 status→bool 映射，它们管不到传输层（status=0）与
	// RetryableOnSameAccount/绑定透传/策略拒绝这些由错误对象承载的语义。
	legacyComparable bool
}

// TestFailoverClassifierF01Equivalence 是 F-01 等价表主表（≥60 行断言）。
//
// 逐行 × 逐站点断言两件事：
//   - 类别与 design §3.3.2 的默认规则表一致；
//   - advice.Failover 与「该站点改造前的状态码集合」逐项相等（legacyComparable 行）。
func TestFailoverClassifierF01Equivalence(t *testing.T) {
	compat400Body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"anthropic-beta header is required for this model"}}`)
	plain400Body := []byte(`{"error":{"type":"invalid_request_error","message":"messages: field required"}}`)
	resetHeaders := http.Header{"Retry-After": []string{"30"}}

	rows := []failoverEquivalenceRow{
		// —— 显式集合成员 ——
		{name: "401 unauthorized", in: ClassifyInput{StatusCode: 401}, wantClass: ClassAuthInvalid, legacyComparable: true},
		{name: "403 forbidden", in: ClassifyInput{StatusCode: 403}, wantClass: ClassAuthInvalid, legacyComparable: true},
		{name: "429 too many requests", in: ClassifyInput{StatusCode: 429}, wantClass: ClassRateLimited, legacyComparable: true},
		{name: "429 + Retry-After", in: ClassifyInput{StatusCode: 429, ResponseHeaders: resetHeaders}, wantClass: ClassRateLimited, legacyComparable: true},
		{name: "529 overloaded", in: ClassifyInput{StatusCode: 529}, wantClass: ClassQuotaExhausted, legacyComparable: true},
		{name: "402 payment required", in: ClassifyInput{StatusCode: 402}, wantClass: ClassRequestInvalid, wantClassOpenAI: ClassQuotaExhausted, legacyComparable: true},
		{name: "405 method not allowed", in: ClassifyInput{StatusCode: 405}, wantClass: ClassRequestInvalid, wantClassOpenAI: ClassUpstream5xx, legacyComparable: true},

		// —— 5xx 段（default: statusCode >= 500）——
		{name: "500", in: ClassifyInput{StatusCode: 500}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "501（落在 [500,599] 区间）", in: ClassifyInput{StatusCode: 501}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "502", in: ClassifyInput{StatusCode: 502}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "503 without transient", in: ClassifyInput{StatusCode: 503}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "504", in: ClassifyInput{StatusCode: 504}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "505", in: ClassifyInput{StatusCode: 505}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "599 边界", in: ClassifyInput{StatusCode: 599}, wantClass: ClassUpstream5xx, legacyComparable: true},

		// —— 非集合 4xx（旧实现一律 false）——
		{name: "400 无关键词", in: ClassifyInput{StatusCode: 400}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "400 + 非白名单关键词", in: ClassifyInput{StatusCode: 400, ResponseBody: plain400Body}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "400 + 白名单关键词 + failover_on_400=false（默认）", in: ClassifyInput{StatusCode: 400, ResponseBody: compat400Body}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "404", in: ClassifyInput{StatusCode: 404}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "409", in: ClassifyInput{StatusCode: 409}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "413", in: ClassifyInput{StatusCode: 413}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "422", in: ClassifyInput{StatusCode: 422}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "418 未登记状态码", in: ClassifyInput{StatusCode: 418}, wantClass: ClassRequestInvalid, legacyComparable: true},

		// —— 传输层 5xx 语义：503 + provider 级降载 ——
		{name: "503 + RequestScopedTransient", in: ClassifyInput{StatusCode: 503, RequestScopedTransient: true}, wantClass: ClassCapacityShed, legacyComparable: true},
	}

	total := 0
	for _, row := range rows {
		row := row
		for _, site := range failoverTestSites() {
			site := site
			in := row.in
			in.Profile = site.profile
			t.Run(row.name+"/"+site.name, func(t *testing.T) {
				class, advice := ClassifyUpstreamFailure(in)

				wantClass := row.wantClass
				if site.profile == FailoverProfileOpenAI && row.wantClassOpenAI != "" {
					wantClass = row.wantClassOpenAI
				}
				require.Equal(t, wantClass, class, "类别")

				if row.legacyComparable {
					require.Equal(t, site.legacy(row.in.StatusCode), advice.Failover,
						"F-01：%s 对状态码 %d 的换号语义必须与旧实现相等", site.name, row.in.StatusCode)
				}

				// P0 不得改变同账号重试语义：凡是由状态码/传输层得出的类别，
				// SameAccount 必须是 inherit（= 调用方继续沿用既有口径）。
				if class != ClassCanceled && class != ClassBindingPassthru && class != ClassPolicyBlocked {
					require.Equal(t, RetryInherit, advice.SameAccount, "同账号重试语义在 P0 不得被改写")
				}
			})
			total++
		}
	}
	require.GreaterOrEqual(t, total, 60, "F-01 表行数下限（implementation.md T0.4 验收标准）")
}

// TestFailoverClassifierLegacySetBoundaries 单独穷举 4 处旧集合的**边界**语义，
// 防止「区间 500-599」被写成 [500,600) 之外的形态（例如 600 误命中）。
func TestFailoverClassifierLegacySetBoundaries(t *testing.T) {
	cases := []struct {
		statusCode int
		wantShared bool
		wantOpenAI bool
	}{
		{0, false, false},
		{100, false, false},
		{199, false, false},
		{200, false, false},
		{399, false, false},
		{400, false, false},
		{401, true, true},
		{402, false, true},
		{403, true, true},
		{404, false, false},
		{405, false, true},
		{429, true, true},
		{499, false, false},
		{500, true, true},
		{529, true, true},
		{599, true, true},
		{600, false, false},
		{1000, false, false},
	}
	for _, tc := range cases {
		_, sharedAdvice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: tc.statusCode, Profile: FailoverProfileAnthropic})
		require.Equal(t, tc.wantShared, sharedAdvice.Failover, "shared 集合 @%d", tc.statusCode)
		_, openAIAdvice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: tc.statusCode, Profile: FailoverProfileOpenAI})
		require.Equal(t, tc.wantOpenAI, openAIAdvice.Failover, "openai 集合 @%d", tc.statusCode)
	}
}

// —— 判定顺序 ————————————————————————————————————————————————————————————————

// TestFailoverClassifierJudgmentOrder 锁定判定顺序（顺序本身是契约，见文件头注释）。
//
// 每个子用例都构造「两个分支的条件同时成立」的输入，断言靠前的分支胜出：
// 顺序被重排时至少有一个子用例会红。
func TestFailoverClassifierJudgmentOrder(t *testing.T) {
	t.Run("1-canceled beats everything", func(t *testing.T) {
		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:               503,
			Canceled:                 true,
			BoundUpstreamPassthrough: true,
			RetryableOnSameAccount:   true,
			RequestScopedTransient:   true,
			PolicyBlocked:            true,
		})
		require.Equal(t, ClassCanceled, class)
		require.False(t, advice.Failover, "客户端取消不换号")
		require.False(t, advice.TempUnschedule, "客户端取消不标记账号")
		require.Zero(t, advice.Cooldown, "客户端取消不冷却")
		require.Equal(t, GatewayFailureScopeRequest, advice.Scope)
	})

	t.Run("1-canceled beats transport kind", func(t *testing.T) {
		// Canceled 字段与 TransportKind=canceled 都必须命中第一个分支。
		for _, in := range []ClassifyInput{
			{StatusCode: 503, Canceled: true},
			{TransportKind: TransportFailureCanceled},
			{Canceled: true, TransportKind: TransportFailureTimeout},
		} {
			class, _ := ClassifyUpstreamFailure(in)
			require.Equal(t, ClassCanceled, class, "输入 %+v", in)
		}
	})

	t.Run("2-passthrough beats retryable", func(t *testing.T) {
		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:               503,
			BoundUpstreamPassthrough: true,
			RetryableOnSameAccount:   true,
			RequestScopedTransient:   true,
		})
		require.Equal(t, ClassBindingPassthru, class)
		require.False(t, advice.Failover, "绑定透传不换号")
		require.Equal(t, RetryOff, advice.SameAccount)
	})

	t.Run("2b-policy blocked after passthrough before retryable", func(t *testing.T) {
		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:             500,
			PolicyBlocked:          true,
			RetryableOnSameAccount: true,
		})
		require.Equal(t, ClassPolicyBlocked, class)
		require.False(t, advice.Failover)

		// 透传优先于策略拒绝（两者都为真时以透传为准：缓存语义必须原样回写）。
		class, _ = ClassifyUpstreamFailure(ClassifyInput{StatusCode: 500, PolicyBlocked: true, BoundUpstreamPassthrough: true})
		require.Equal(t, ClassBindingPassthru, class)
	})

	t.Run("3-retryable beats status code (Google intermittent 400)", func(t *testing.T) {
		// 这是 design §3.3.4 点名的易错点：400 + RetryableOnSameAccount 若先查状态码
		// 会落进 request_invalid → 不再同账号重试（回归）。
		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:             400,
			RetryableOnSameAccount: true,
		})
		require.Equal(t, ClassUpstream5xx, class, "Google 间歇性 400 归 upstream_5xx")
		require.True(t, advice.Failover)
		require.Equal(t, RetryInherit, advice.SameAccount)
		require.NotEqual(t, ClassRequestInvalid, class)
	})

	t.Run("3-retryable beats status code (502 empty response)", func(t *testing.T) {
		class, advice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 502, RetryableOnSameAccount: true})
		require.Equal(t, ClassUpstream5xx, class)
		require.Equal(t, RetryInherit, advice.SameAccount)
	})

	t.Run("4-transient beats status code", func(t *testing.T) {
		// 429 + RequestScopedTransient 是 provider 级过载：按状态码会算成 rate_limited
		// 并给账号写冷却，把与故障无关的账号一起摘掉。
		class, advice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 429, RequestScopedTransient: true})
		require.Equal(t, ClassCapacityShed, class)
		require.True(t, advice.Failover)
		require.Zero(t, advice.Cooldown, "provider 级降载不冷却")
		require.False(t, advice.TempUnschedule)
		require.Equal(t, GatewayFailureScopeProvider, advice.Scope)
	})

	t.Run("3-before-4 when both flags set", func(t *testing.T) {
		// 两个标志同时成立：类别升级为 capacity_shed（provider 归属），
		// 但同账号重试语义仍由分支 3 保留（inherit）。
		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:             503,
			RetryableOnSameAccount: true,
			RequestScopedTransient: true,
		})
		require.Equal(t, ClassCapacityShed, class)
		require.Equal(t, RetryInherit, advice.SameAccount)
		require.Zero(t, advice.Cooldown)
		require.False(t, advice.TempUnschedule)
	})

	t.Run("5-status code when nothing else matches", func(t *testing.T) {
		class, _ := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 400})
		require.Equal(t, ClassRequestInvalid, class)
	})

	t.Run("6-default is request_invalid", func(t *testing.T) {
		// 既无状态码也无传输层信息：最保守的兜底（不换号）。
		class, advice := ClassifyUpstreamFailure(ClassifyInput{})
		require.Equal(t, ClassRequestInvalid, class)
		require.False(t, advice.Failover)
	})
}

// —— 三条硬规则（gateway_upstream_transport_error_test.go 的等价表述）————————————————

// TestFailoverClassifierLockedTransportRules 复述既有测试锁定的三条硬规则。
// 既有文件是 `//go:build unit`，因此这里用分类器的角度重新锁定，保证两套 tag 都覆盖。
func TestFailoverClassifierLockedTransportRules(t *testing.T) {
	t.Run("client canceled: no failover, no mark", func(t *testing.T) {
		// gateway_upstream_transport_error_test.go:165-184
		err := context.Canceled
		require.True(t, IsClientCanceledFailure(err, nil))

		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			Canceled:      IsClientCanceledFailure(err, nil),
			TransportKind: ClassifyTransportFailureKind(err),
		})
		require.Equal(t, ClassCanceled, class, "客户端取消不得归 network/upstream_timeout")
		require.False(t, advice.Failover)
		require.False(t, advice.TempUnschedule, "客户端取消不得写 T3")
		require.Equal(t, RetryOff, advice.SameAccount)
		require.Zero(t, advice.Cooldown)
	})

	t.Run("upstream deadline: still fails over, no mark", func(t *testing.T) {
		// gateway_upstream_transport_error_test.go:189-205
		err := context.DeadlineExceeded
		require.False(t, IsClientCanceledFailure(err, nil), "请求上下文还活着 → 是上游 deadline")

		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			Canceled:      IsClientCanceledFailure(err, nil),
			TransportKind: ClassifyTransportFailureKind(err),
		})
		require.Equal(t, ClassUpstreamTimeout, class)
		require.True(t, advice.Failover, "上游 deadline 仍要换号")
		require.False(t, advice.TempUnschedule, "上游 deadline 不得写 T3（repo.calls=0）")
	})

	t.Run("client deadline: treated as cancel", func(t *testing.T) {
		// err 是 DeadlineExceeded 且请求上下文自身已超时 → 客户端自己的 deadline。
		require.True(t, IsClientCanceledFailure(context.DeadlineExceeded, context.DeadlineExceeded))
	})

	t.Run("transient transport blip: fails over without eviction", func(t *testing.T) {
		// gateway_upstream_transport_error_test.go:45-73
		kind := ClassifyTransportFailureKind(errors.New(`Post "http://upstream/v1/messages?beta=true": EOF`))
		require.Equal(t, TransportFailureEOF, kind, "EOF 形态")

		class, advice := ClassifyUpstreamFailure(ClassifyInput{TransportKind: kind})
		require.Equal(t, ClassNetwork, class)
		require.True(t, advice.Failover, "瞬态传输错误要换号")
		require.False(t, advice.TempUnschedule, "瞬态传输错误不驱逐账号")
		require.Equal(t, RetryInherit, advice.SameAccount)
	})

	t.Run("conn reset maps to network and stays transient", func(t *testing.T) {
		kind := ClassifyTransportFailureKind(syscall.ECONNRESET)
		require.Equal(t, TransportFailureConnectionReset, kind)
		class, advice := ClassifyUpstreamFailure(ClassifyInput{TransportKind: kind})
		require.Equal(t, ClassNetwork, class)
		require.True(t, advice.Failover)
		require.False(t, advice.TempUnschedule)
	})
}

// TestClassifyTransportFailureKind 覆盖传输层错误归一（纯函数，不做 IO）。
func TestClassifyTransportFailureKind(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want TransportFailureKind
	}{
		{"nil", nil, TransportFailureNone},
		{"canceled", context.Canceled, TransportFailureCanceled},
		{"deadline", context.DeadlineExceeded, TransportFailureTimeout},
		{"conn reset", syscall.ECONNRESET, TransportFailureConnectionReset},
		{"conn refused", syscall.ECONNREFUSED, TransportFailureConnectionRefused},
		{"plain EOF", io.EOF, TransportFailureEOF},
		{"wrapped EOF", errors.New(`Post "http://upstream/v1/messages": EOF`), TransportFailureEOF},
		{"unexpected EOF", io.ErrUnexpectedEOF, TransportFailureEOF},
		{"DNS", errors.New("no such host"), TransportFailureDNS},
		{"TLS", errors.New(`tls: failed to verify certificate`), TransportFailureTLS},
		{"unknown", errors.New("boom"), TransportFailureOther},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ClassifyTransportFailureKind(tc.err))
		})
	}
}

// —— 策略注入 ————————————————————————————————————————————————————————————————

// TestFailoverClassifierHonorsInjectedPolicy 证明「配置由调用方注入」这条约束真的生效：
// 同一个状态码，注入不同策略会得到不同建议；分类器自己不读任何全局配置。
func TestFailoverClassifierHonorsInjectedPolicy(t *testing.T) {
	compat400Body := []byte(`{"error":{"type":"invalid_request_error","message":"anthropic-beta header is required"}}`)

	t.Run("failover_on_400 gates the compat whitelist", func(t *testing.T) {
		_, disabled := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 400, ResponseBody: compat400Body})
		require.False(t, disabled.Failover, "默认 failover_on_400=false → 不换号（现状）")

		enabledPolicy := &FailoverPolicy{FailoverOn400: failoverTestBoolPtr(true)}
		_, enabled := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 400, ResponseBody: compat400Body, Policy: enabledPolicy})
		require.True(t, enabled.Failover, "failover_on_400=true + 白名单命中 → 换号")

		// 白名单未命中时，即使 failover_on_400=true 也不换号。
		plainBody := []byte(`{"error":{"type":"invalid_request_error","message":"messages: field required"}}`)
		_, plain := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 400, ResponseBody: plainBody, Policy: enabledPolicy})
		require.False(t, plain.Failover, "非白名单 400 永不换号")
	})

	t.Run("custom compat keywords replace the builtin list", func(t *testing.T) {
		policy := &FailoverPolicy{
			FailoverOn400:     failoverTestBoolPtr(true),
			Compat400Keywords: &[]string{"quota exceeded for beta"},
		}
		_, hit := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:   400,
			ResponseBody: []byte(`{"error":{"message":"quota exceeded for beta program"}}`),
			Policy:       policy,
		})
		require.True(t, hit.Failover)

		// 替换后内置关键词不再生效。
		_, miss := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 400, ResponseBody: compat400Body, Policy: policy})
		require.False(t, miss.Failover)
	})

	t.Run("status_classes is a whole-table replacement", func(t *testing.T) {
		// 把 418 配成 capacity_shed：命中配置表，而不是内置的 request_invalid。
		rules := []config.FailoverStatusClassRuleConfig{{Class: config.FailoverClassCapacityShed, Codes: []int{418}}}
		policy := &FailoverPolicy{StatusClasses: &rules}

		class, advice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 418, RequestScopedTransient: true, Policy: policy})
		require.Equal(t, ClassCapacityShed, class)
		require.True(t, advice.Failover)

		// 整体替换：内置表没了，501 不再命中 500-599 区间 → 兜底 request_invalid。
		class, _ = ClassifyUpstreamFailure(ClassifyInput{StatusCode: 501, Policy: policy})
		require.Equal(t, ClassRequestInvalid, class)
	})

	t.Run("rule advice overrides builtin defaults", func(t *testing.T) {
		off := config.FailoverSameAccountModeOff
		rules := []config.FailoverStatusClassRuleConfig{{
			Class:  config.FailoverClassQuotaExhausted,
			Codes:  []int{529},
			Advice: &config.FailoverRetryAdviceConfig{SameAccount: &off},
		}}
		policy := &FailoverPolicy{StatusClasses: &rules}
		_, advice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 529, Policy: policy})
		require.Equal(t, RetryOff, advice.SameAccount, "advice.same_account=off 可把默认 inherit 覆盖掉")
	})

	t.Run("global same_account.default_mode=off wins", func(t *testing.T) {
		policy := &FailoverPolicy{SameAccountDefaultMode: failoverTestStringPtr(config.FailoverSameAccountModeOff)}
		_, advice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 429, Policy: policy})
		require.Equal(t, RetryOff, advice.SameAccount)
	})

	t.Run("cooldown max_ttl caps upstream reset", func(t *testing.T) {
		// 529 兜底 30m，被 cooldown.max_ttl=1m 截断。
		policy := &FailoverPolicy{CooldownMaxTTL: failoverTestDurationPtr(time.Minute)}
		_, advice := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 529, Policy: policy})
		require.Equal(t, time.Minute, advice.Cooldown)

		// Retry-After=30s 时用上游值，而不是内置的 60s。
		_, advice = ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:      429,
			ResponseHeaders: http.Header{"Retry-After": []string{"30"}},
		})
		require.Equal(t, 30*time.Second, advice.Cooldown)
		require.Equal(t, cooldownSourceUpstreamReset, advice.CooldownSource)

		// 没有 Retry-After 时回落固定 60s，来源标记同步改成 fixed。
		_, advice = ClassifyUpstreamFailure(ClassifyInput{StatusCode: 429})
		require.Equal(t, 60*time.Second, advice.Cooldown)
		require.Equal(t, cooldownSourceFixed, advice.CooldownSource)
	})

	t.Run("legacy NextAccountStop still vetoes failover", func(t *testing.T) {
		// R-48：既有语义的否决权在分类之后、且不可被类别覆盖。
		class, advice := ClassifyUpstreamFailure(ClassifyInput{
			StatusCode:        500,
			NextAccountAction: NextAccountStop,
		})
		require.Equal(t, ClassUpstream5xx, class, "类别仍按顺序判定（观测要看真实类别）")
		require.False(t, advice.Failover, "既有语义 Stop 拥有最终否决权")
	})
}

// —— 纯函数 ————————————————————————————————————————————————————————————————

// TestFailoverClassifierIsPure 证明它没有隐藏状态、不改入参、不受策略总开关影响。
func TestFailoverClassifierIsPure(t *testing.T) {
	in := ClassifyInput{StatusCode: 503, RequestScopedTransient: true}

	class1, advice1 := ClassifyUpstreamFailure(in)
	class2, advice2 := ClassifyUpstreamFailure(in)
	require.Equal(t, class1, class2, "同一输入必须得到同一类别")
	require.Equal(t, advice1, advice2, "同一输入必须得到同一建议")

	// 策略总开关不参与分类：Enabled=false 只告诉调用方走旧路径（Policy.Legacy()），
	// 分类器本身不应因此改变输出（否则「回滚开关」会变成第二条判定链）。
	disabled := false
	inDisabled := in
	inDisabled.Policy = &FailoverPolicy{Enabled: &disabled}
	class3, advice3 := ClassifyUpstreamFailure(inDisabled)
	require.Equal(t, class1, class3)
	require.Equal(t, advice1, advice3)

	// 入参的 body/headers 只读。
	body := []byte(`{"error":{"message":"anthropic-beta required"}}`)
	bodyCopy := string(body)
	headers := http.Header{"Retry-After": []string{"30"}}
	policy := &FailoverPolicy{FailoverOn400: failoverTestBoolPtr(true)}
	_, _ = ClassifyUpstreamFailure(ClassifyInput{
		StatusCode:      400,
		ResponseBody:    body,
		ResponseHeaders: headers,
		Policy:          policy,
	})
	require.Equal(t, bodyCopy, string(body), "分类器不得修改响应体")
	require.Equal(t, []string{"30"}, headers["Retry-After"], "分类器不得修改响应头")
}

// TestFailoverClassifierBuiltinDefaultsAreZeroBehaviorChange 锁定「P0 默认零行为变更」：
// 没有任何类别在默认表里要求写 T3 或强制关闭同账号重试。
func TestFailoverClassifierBuiltinDefaultsAreZeroBehaviorChange(t *testing.T) {
	for _, class := range failoverAllClasses {
		advice := builtinFailoverAdvice(class)
		require.False(t, advice.TempUnschedule, "类别 %q 的默认建议不得要求写 T3（现状写库全部来自既有路径）", class)

		switch class {
		case ClassCanceled, ClassBindingPassthru, ClassPolicyBlocked:
			// 「本地判定、与上游无关」的类别不重试同账号。
			require.Equal(t, RetryOff, advice.SameAccount, "类别 %q", class)
		default:
			// 其余类别必须 inherit：否则 P1 接线会改变 pool_mode / RetryableOnSameAccount 语义。
			require.Equal(t, RetryInherit, advice.SameAccount, "类别 %q", class)
		}
	}

	// capacity_shed 的关键约束（implementation.md T0.4 验收标准）。
	shed := builtinFailoverAdvice(ClassCapacityShed)
	require.True(t, shed.Failover)
	require.Zero(t, shed.Cooldown)
	require.False(t, shed.TempUnschedule)
	require.Equal(t, GatewayFailureScopeProvider, shed.Scope)
}
