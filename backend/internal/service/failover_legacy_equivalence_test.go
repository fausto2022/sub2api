package service

// 本文件是 P1/T1.2 的「双跑对比」（shadow dual-run）：
//
//	同一批输入 → ① P1 已接线的生产函数（4 处旧硬编码已被替换为分类器调用）
//	           → ② 旧实现的 test-only 影子副本（逐字抄自改造前代码）
//	断言两者输出**完全一致**。这是「P1 零行为变更」的凭据，也是后续阶段
//	（P2 起真的会改判定语义）唯一还能回溯的基线。
//
// 影子副本**不进生产代码**：本文件的 legacy* 函数只允许出现在 _test.go 里
// （TestFailoverLegacyEquivalence_ShadowCopiesAreTestOnly 会静态核对）。
//
// 本文件没有 build tag：契约要求 `go test` 与 `-tags=unit` 两套都必须执行双跑，
// 加 unit tag 会让非 tag 那条命令变成「0 tests to run」的假绿。

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// —— 旧实现的影子副本（逐字抄自改造前代码，禁止修改其语义）————————————————

// legacyShouldFailoverShared 对应改造前下面三处**逐字相同**的实现：
//
//	gateway_forward.go:47-54                 GatewayService.shouldFailoverUpstreamError
//	antigravity_gateway_retry.go:780-787     AntigravityGatewayService.shouldFailoverUpstreamError
//	gemini_messages_compat_service.go:1706-1713 GeminiMessagesCompatService.shouldFailoverGeminiUpstreamError
func legacyShouldFailoverShared(statusCode int) bool {
	switch statusCode {
	case 401, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// legacyShouldFailoverOpenAI 对应改造前 openai_gateway_upstream_errors.go:303-310。
// 它与上面三处的唯一差异是多了 402 / 405。
func legacyShouldFailoverOpenAI(statusCode int) bool {
	switch statusCode {
	case 401, 402, 403, 405, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// legacyShouldRetryUpstreamError 对应改造前 gateway_forward.go:36-44（**本轮未接线**，
// 双跑只是证明它没被动过：它是账号凭据口径，不是状态码集合）。
func legacyShouldRetryUpstreamError(account *Account, statusCode int) bool {
	if account.IsOAuth() {
		return statusCode == 403
	}
	return !account.ShouldHandleErrorCode(statusCode)
}

// legacyShouldRetryGeminiUpstreamError 对应改造前 gemini_messages_compat_service.go:1686-1704
// （**本轮未接线**，同上）。
func legacyShouldRetryGeminiUpstreamError(account *Account, statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	case 403:
		if account == nil || account.Type != AccountTypeOAuth {
			return false
		}
		oauthType := strings.ToLower(strings.TrimSpace(account.GetCredential("oauth_type")))
		if oauthType == "" && strings.TrimSpace(account.GetCredential("project_id")) != "" {
			oauthType = "code_assist"
		}
		return oauthType == "code_assist"
	default:
		return false
	}
}

// legacyShouldRetryAntigravityError 对应改造前 antigravity_gateway_retry.go:719-726
// （**本轮未接线**，同上）。
func legacyShouldRetryAntigravityError(statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

// legacyClientCanceledFailure 对应改造前 gateway_upstream_transport_error.go:59 的判定：
//
//	err == context.Canceled                                 → 客户端走了（不换号、不标记）
//	err == context.DeadlineExceeded && ctx.Err()==Deadline  → 客户端自己的 deadline 到了
//
// 注意「上游 deadline」（err 是 DeadlineExceeded 但请求 ctx 还活着）**不**命中这里。
func legacyClientCanceledFailure(err, requestCtxErr error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) && errors.Is(requestCtxErr, context.DeadlineExceeded)
}

// legacyTransportFailover 对应改造前 handleUpstreamTransportError 的「是否换号」结论：
// 客户端取消 → 原样返回错误（不换号）；其余传输层失败（含上游 deadline、瞬态 EOF/
// 连接重置）→ 构造 UpstreamFailoverError（换号）。是否写 T3 另由
// classifyUpstreamTransportError().Persistent 决定，不在本函数里。
func legacyTransportFailover(err, requestCtxErr error) bool {
	return !legacyClientCanceledFailure(err, requestCtxErr)
}

// —— 双跑入口 ————————————————————————————————————————————————————————————————

// failoverWiringSite 描述一个已接线的生产入口：它的调用方式 + 对应的影子副本。
type failoverWiringSite struct {
	name       string
	profile    FailoverProfile
	legacy     func(statusCode int) bool
	wired      func(statusCode int) bool
	production string // 生产文件（静态核对用）
}

// failoverWiringSites 是 4 处硬编码替换后的生产入口。
func failoverWiringSites() []failoverWiringSite {
	gateway := &GatewayService{}
	antigravity := &AntigravityGatewayService{}
	gemini := &GeminiMessagesCompatService{}
	openai := &OpenAIGatewayService{}
	return []failoverWiringSite{
		{
			name:       "GatewayService.shouldFailoverUpstreamError",
			profile:    FailoverProfileAnthropic,
			legacy:     legacyShouldFailoverShared,
			wired:      gateway.shouldFailoverUpstreamError,
			production: "gateway_forward.go",
		},
		{
			name:       "AntigravityGatewayService.shouldFailoverUpstreamError",
			profile:    FailoverProfileAntigravity,
			legacy:     legacyShouldFailoverShared,
			wired:      antigravity.shouldFailoverUpstreamError,
			production: "antigravity_gateway_retry.go",
		},
		{
			name:       "GeminiMessagesCompatService.shouldFailoverGeminiUpstreamError",
			profile:    FailoverProfileGemini,
			legacy:     legacyShouldFailoverShared,
			wired:      gemini.shouldFailoverGeminiUpstreamError,
			production: "gemini_messages_compat_service.go",
		},
		{
			name:       "OpenAIGatewayService.shouldFailoverUpstreamError",
			profile:    FailoverProfileOpenAI,
			legacy:     legacyShouldFailoverOpenAI,
			wired:      openai.shouldFailoverUpstreamError,
			production: "openai_gateway_upstream_errors.go",
		},
	}
}

// TestFailoverLegacyEquivalence_EveryStatusCode 对 **每一个** int 状态码做双跑。
//
// 范围取 -1..700：既覆盖 HTTP 定义域 100–599，也覆盖定义域外的越界值
// （旧实现是 `statusCode >= 500`，600/700 在旧语义下**是**换号的；分类器的规则表只覆盖
// 100–599，所以接线时必须把定义域外的情况显式兜住，见 shouldFailoverStatus）。
func TestFailoverLegacyEquivalence_EveryStatusCode(t *testing.T) {
	const from, to = -1, 700
	for _, site := range failoverWiringSites() {
		for statusCode := from; statusCode <= to; statusCode++ {
			got := site.wired(statusCode)
			want := site.legacy(statusCode)
			require.Equal(t, want, got,
				"%s: 状态码 %d 的换号结果与旧实现不一致（接线改变了行为）", site.name, statusCode)
		}
	}
}

// legacyEquivalenceRow 是 F-01 等价表的一行（与 P0 的 failover_classifier_test.go 同表，
// 这里是「对影子副本双跑」的形态：期望值来自旧实现副本，而不是手写常量）。
type legacyEquivalenceRow struct {
	name string
	in   ClassifyInput
	// wantClass 期望类别（OpenAI 网关族在 402/405 上不同，用 wantClassOpenAI 覆盖）。
	wantClass       FailureClass
	wantClassOpenAI FailureClass
	// legacyComparable=true：该行是「纯状态码」输入，可以直接与影子副本比对换号结果。
	legacyComparable bool
}

func legacyEquivalenceRows() []legacyEquivalenceRow {
	compat400Body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"anthropic-beta header is required for this model"}}`)
	plain400Body := []byte(`{"error":{"type":"invalid_request_error","message":"messages: field required"}}`)
	return []legacyEquivalenceRow{
		{name: "401", in: ClassifyInput{StatusCode: 401}, wantClass: ClassAuthInvalid, legacyComparable: true},
		{name: "403", in: ClassifyInput{StatusCode: 403}, wantClass: ClassAuthInvalid, legacyComparable: true},
		{name: "429", in: ClassifyInput{StatusCode: 429}, wantClass: ClassRateLimited, legacyComparable: true},
		{name: "529", in: ClassifyInput{StatusCode: 529}, wantClass: ClassQuotaExhausted, legacyComparable: true},
		{name: "402", in: ClassifyInput{StatusCode: 402}, wantClass: ClassRequestInvalid, wantClassOpenAI: ClassQuotaExhausted, legacyComparable: true},
		{name: "405", in: ClassifyInput{StatusCode: 405}, wantClass: ClassRequestInvalid, wantClassOpenAI: ClassUpstream5xx, legacyComparable: true},
		{name: "500", in: ClassifyInput{StatusCode: 500}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "501", in: ClassifyInput{StatusCode: 501}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "502", in: ClassifyInput{StatusCode: 502}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "503", in: ClassifyInput{StatusCode: 503}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "503+transient", in: ClassifyInput{StatusCode: 503, RequestScopedTransient: true}, wantClass: ClassCapacityShed, legacyComparable: true},
		{name: "504", in: ClassifyInput{StatusCode: 504}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "599", in: ClassifyInput{StatusCode: 599}, wantClass: ClassUpstream5xx, legacyComparable: true},
		{name: "400", in: ClassifyInput{StatusCode: 400}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "400+非白名单关键词", in: ClassifyInput{StatusCode: 400, ResponseBody: plain400Body}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "400+白名单关键词（failover_on_400=false）", in: ClassifyInput{StatusCode: 400, ResponseBody: compat400Body}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "404", in: ClassifyInput{StatusCode: 404}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "409", in: ClassifyInput{StatusCode: 409}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "413", in: ClassifyInput{StatusCode: 413}, wantClass: ClassRequestInvalid, legacyComparable: true},
		{name: "422", in: ClassifyInput{StatusCode: 422}, wantClass: ClassRequestInvalid, legacyComparable: true},
		// 语义标志驱动（旧实现是纯 status→bool，覆盖不到这些维度，故不与影子副本比对）。
		{name: "400+RetryableOnSameAccount", in: ClassifyInput{StatusCode: 400, RetryableOnSameAccount: true}, wantClass: ClassUpstream5xx},
		{name: "502+RetryableOnSameAccount", in: ClassifyInput{StatusCode: 502, RetryableOnSameAccount: true}, wantClass: ClassUpstream5xx},
		{name: "429+RequestScopedTransient", in: ClassifyInput{StatusCode: 429, RequestScopedTransient: true}, wantClass: ClassCapacityShed},
		{name: "503+bound passthrough", in: ClassifyInput{StatusCode: 503, BoundUpstreamPassthrough: true}, wantClass: ClassBindingPassthru},
		{name: "500+policy blocked", in: ClassifyInput{StatusCode: 500, PolicyBlocked: true}, wantClass: ClassPolicyBlocked},
	}
}

// TestFailoverLegacyEquivalence_F01Table 是 F-01 等价表的双跑形态：
// 逐行 × 5 个网关族，把分类器结果与**旧实现副本**比对（P0 是跟手写常量比对，
// 这里跟影子副本比对，两者互为正交证据）。
func TestFailoverLegacyEquivalence_F01Table(t *testing.T) {
	profiles := []struct {
		name       string
		profile    FailoverProfile
		legacy     func(statusCode int) bool
		production string
	}{
		{"gateway_forward", FailoverProfileAnthropic, legacyShouldFailoverShared, "gateway_forward.go"},
		{"antigravity", FailoverProfileAntigravity, legacyShouldFailoverShared, "antigravity_gateway_retry.go"},
		{"gemini", FailoverProfileGemini, legacyShouldFailoverShared, "gemini_messages_compat_service.go"},
		{"openai", FailoverProfileOpenAI, legacyShouldFailoverOpenAI, "openai_gateway_upstream_errors.go"},
		{"generic", FailoverProfileGeneric, legacyShouldFailoverShared, ""},
	}

	total := 0
	for _, row := range legacyEquivalenceRows() {
		row := row
		for _, p := range profiles {
			p := p
			t.Run(row.name+"/"+p.name, func(t *testing.T) {
				in := row.in
				in.Profile = p.profile
				class, advice := ClassifyUpstreamFailure(in)

				wantClass := row.wantClass
				if p.profile == FailoverProfileOpenAI && row.wantClassOpenAI != "" {
					wantClass = row.wantClassOpenAI
				}
				require.Equal(t, wantClass, class, "类别")

				if row.legacyComparable {
					require.Equal(t, p.legacy(row.in.StatusCode), advice.Failover,
						"%s：状态码 %d 的换号结果必须与旧实现一致", p.name, row.in.StatusCode)
				}

				// 同账号重试语义在 P1 不得被改写：状态码/传输层得出的类别一律 inherit。
				if class != ClassCanceled && class != ClassBindingPassthru && class != ClassPolicyBlocked {
					require.Equal(t, RetryInherit, advice.SameAccount)
				}
			})
			total++
		}
	}
	require.GreaterOrEqual(t, total, 100, "双跑表格行数下限")
}

// TestFailoverLegacyEquivalence_TransportSemantics 双跑「客户端取消 / 上游 deadline /
// 瞬态传输错误」三种语义输入：分类器的换号建议必须与旧传输层决策一致。
//
// 这三条是 gateway_upstream_transport_error_test.go 锁定的硬规则在 P1 接线后的等价表述。
func TestFailoverLegacyEquivalence_TransportSemantics(t *testing.T) {
	eofString := errors.New(`Post "http://upstream/v1/messages?beta=true": EOF`)
	cases := []struct {
		name         string
		err          error
		requestCtx   error
		wantClass    FailureClass
		wantTransfer TransportFailureKind
	}{
		{"client cancel", context.Canceled, nil, ClassCanceled, TransportFailureCanceled},
		{"client deadline", context.DeadlineExceeded, context.DeadlineExceeded, ClassCanceled, TransportFailureTimeout},
		{"upstream deadline", context.DeadlineExceeded, nil, ClassUpstreamTimeout, TransportFailureTimeout},
		{"transient EOF", eofString, nil, ClassNetwork, TransportFailureEOF},
		{"connection reset", syscall.ECONNRESET, nil, ClassNetwork, TransportFailureConnectionReset},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			kind := ClassifyTransportFailureKind(tc.err)
			require.Equal(t, tc.wantTransfer, kind, "传输层形态归一")

			canceled := IsClientCanceledFailure(tc.err, tc.requestCtx)
			// 接线期的不变量：IsClientCanceledFailure 必须与旧判定逐字一致。
			require.Equal(t, legacyClientCanceledFailure(tc.err, tc.requestCtx), canceled)

			class, advice := ClassifyUpstreamFailure(ClassifyInput{Canceled: canceled, TransportKind: kind})
			require.Equal(t, tc.wantClass, class)
			require.Equal(t,
				legacyTransportFailover(tc.err, tc.requestCtx), advice.Failover,
				"换号结论必须与旧传输层决策一致")
			require.False(t, advice.TempUnschedule,
				"传输层失败不得由分类器要求写 T3（是否写库仍由 classifyUpstreamTransportError().Persistent 决定）")
		})
	}
}

// TestFailoverLegacyEquivalence_SameAccountFunctionsUntouched 证明本轮**没有**动
// 三处「同账号重试」函数（它们不是状态码集合，接线会改变 4xx 的重试语义，
// 按 implementation.md T1.1 注意事项留给 T2.2 的决策引擎）。
func TestFailoverLegacyEquivalence_SameAccountFunctionsUntouched(t *testing.T) {
	gateway := &GatewayService{}
	gemini := &GeminiMessagesCompatService{}

	oauthCodeAssist := &Account{
		ID: 1, Type: AccountTypeOAuth,
		Credentials: map[string]any{"oauth_type": "code_assist"},
	}
	oauthOther := &Account{
		ID: 2, Type: AccountTypeOAuth,
		Credentials: map[string]any{"oauth_type": ""},
	}
	apiKeyPlain := &Account{ID: 3, Type: AccountTypeAPIKey}
	apiKeyWithCustomCodes := &Account{
		ID: 4, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(400), float64(418)},
		},
	}

	for _, account := range []*Account{oauthCodeAssist, oauthOther, apiKeyPlain, apiKeyWithCustomCodes} {
		for statusCode := 100; statusCode <= 599; statusCode++ {
			require.Equal(t,
				legacyShouldRetryUpstreamError(account, statusCode),
				gateway.shouldRetryUpstreamError(account, statusCode),
				"GatewayService.shouldRetryUpstreamError 被改动了（account=%d, code=%d）", account.ID, statusCode)
			require.Equal(t,
				legacyShouldRetryGeminiUpstreamError(account, statusCode),
				gemini.shouldRetryGeminiUpstreamError(account, statusCode),
				"GeminiMessagesCompatService.shouldRetryGeminiUpstreamError 被改动了（account=%d, code=%d）", account.ID, statusCode)
		}
	}

	for statusCode := 100; statusCode <= 599; statusCode++ {
		require.Equal(t,
			legacyShouldRetryAntigravityError(statusCode),
			shouldRetryAntigravityError(statusCode),
			"shouldRetryAntigravityError 被改动了（code=%d）", statusCode)
	}
}

// TestFailoverLegacyEquivalence_ShadowCopiesAreTestOnly 静态核对两件事：
//  1. 4 个生产文件里的旧状态码集合已被删除（`case 401, 403, 429, 529:` 等不再出现在生产代码）；
//  2. 每个生产文件确实通过 shouldFailoverStatus 接线（而不是各自还留着 switch）。
//
// 这正是 implementation.md T1.1 验收里的「rg 只剩 1 处」检查，改成测试内断言后
// 不需要额外脚本，也不会被之后的阶段悄悄改回去。
func TestFailoverLegacyEquivalence_ShadowCopiesAreTestOnly(t *testing.T) {
	legacyPatterns := []string{
		"case 401, 403, 429, 529:",
		"case 401, 402, 403, 405, 429, 529:",
	}

	files := []string{
		"gateway_forward.go",
		"antigravity_gateway_retry.go",
		"gemini_messages_compat_service.go",
		"openai_gateway_upstream_errors.go",
	}

	for _, file := range files {
		raw, err := os.ReadFile(file) // go test 的 cwd 恒为包目录
		if err != nil {
			t.Skipf("读不到 %s（%v）：该断言只在包目录下运行时有意义", file, err)
		}
		content := string(raw)
		for _, pattern := range legacyPatterns {
			require.NotContains(t, content, pattern,
				"%s 里仍残留旧状态码集合 %q：T1.1 未完成（影子副本只允许存在于 _test.go）", file, pattern)
		}
		require.Contains(t, content, "shouldFailoverStatus(",
			"%s 没有走 shouldFailoverStatus 接线", file)
	}

	// 反向：影子副本必须仍然存在于测试代码里（否则双跑就失去意义）。
	for _, pattern := range legacyPatterns {
		require.Contains(t, shadowCopySource(), pattern,
			"影子副本丢失：%q 应只在本测试文件里出现", pattern)
	}
}

// shadowCopySource 返回本测试文件的源码，用于「影子副本仍在测试里」的反向核对。
func shadowCopySource() string {
	raw, err := os.ReadFile("failover_legacy_equivalence_test.go")
	if err != nil {
		return ""
	}
	return string(raw)
}
