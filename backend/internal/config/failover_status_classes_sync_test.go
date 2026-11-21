// 默认规则表的跨包一致性守卫（t8）。
//
// 背景：failover 的「默认规则表」存在**两份同构实现**——
//
//	config.DefaultFailoverStatusClasses()   backend/internal/config/config.go
//	                                        （生产路径：load() 把它填进 status_classes）
//	service.defaultFailoverClassRules()     backend/internal/service/failover_classifier.go
//	                                        （调用方没注入策略时的兜底）
//
// 两者必须人工保持同步，却没有任何编译期约束；两侧各自单测都绿，漂移不会被发现
// （例如把 503 从 capacity_shed 挪到别的类，只改一侧，两边测试仍然全过）。
//
// 本文件放在**外部测试包** `config_test`：Go 允许外部测试包 import「反向依赖被测包的包」，
// 因此可以在完全不改生产代码分层的前提下，同时拿到 config 与 service 的导出面
// （标准库先例：net/http_test 里 import net/http/httptest，而 httptest 又 import net/http）。
//
// service 侧的规则表是未导出的，所以这里不"读表"，而是用**行为等价**比对：对 [100,599]
// 全状态码逐一要求「config 表声明的类别 == 分类器给出的类别」，并要求类别/建议与契约一致。
// 这样无论哪一侧被单边修改（挪码、改类别、加码、改 advice），本测试都会变红。
package config_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestFailoverStatusClassesSyncWithClassifier 是「双同构表」的同步守卫。
//
// 断言三件事：
//  1. config 默认表的结构契约：10 条规则、类别不重复、advice 全为 nil（配置层不承载处置表）；
//  2. 对 [100,599] 每一个状态码：config 表声明的类别 == 分类器实际给出的类别；
//  3. 对 config 表**没有**声明的状态码：分类器必须回落到 request_invalid
//     （若 service 侧单边加了一个码，这一条会红）。
func TestFailoverStatusClassesSyncWithClassifier(t *testing.T) {
	rules := config.DefaultFailoverStatusClasses()
	require.Len(t, rules, 10, "默认规则表一项一类（与分类器 defaultFailoverClassRules 同构）")

	// —— 1. 结构契约 ——
	knownClasses := map[string]struct{}{
		config.FailoverClassAuthInvalid:     {},
		config.FailoverClassRateLimited:     {},
		config.FailoverClassQuotaExhausted:  {},
		config.FailoverClassCapacityShed:    {},
		config.FailoverClassUpstream5xx:     {},
		config.FailoverClassUpstreamTimeout: {},
		config.FailoverClassNetwork:         {},
		config.FailoverClassRequestInvalid:  {},
		config.FailoverClassPolicyBlocked:   {},
		config.FailoverClassBindingPassthru: {},
	}
	seen := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		require.NotEmpty(t, strings.TrimSpace(rule.Class), "第 %d 条规则的 class 不得为空", index)
		_, ok := knownClasses[rule.Class]
		require.True(t, ok, "第 %d 条规则的 class %q 不在已知类别集合内", index, rule.Class)
		_, duplicated := seen[rule.Class]
		require.False(t, duplicated, "类别 %q 在默认表里出现了两次", rule.Class)
		seen[rule.Class] = struct{}{}
		// 配置层只承载「状态码 → 类别」。一旦有人在这里写 advice，生产路径
		// （load() 用本表填 status_classes）就会与分类器的内置建议分叉——
		// 这是「双同构表」漂移的第二种形态，必须在这里拦住。
		require.Nil(t, rule.Advice, "config 默认表第 %d 条（%s）不得携带 advice 覆盖", index, rule.Class)
	}

	// —— 2 & 3. 全状态码行为等价 ——
	codeClasses := failoverDefaultCodeClasses(t, rules)
	require.NotEmpty(t, codeClasses, "默认表必须至少声明一个状态码")
	for code := 100; code <= 599; code++ {
		wantClass, declared := codeClasses[code]
		in := service.ClassifyInput{StatusCode: code}
		if declared && wantClass == config.FailoverClassCapacityShed {
			// design §3.3.4：没有 RequestScopedTransient 的 503 归 upstream_5xx，
			// 只有「与账号无关的上游降载」才是 capacity_shed。表里同时存在这两条，
			// 因此按该分支的语义构造输入（分类器在命中表前会先看这个标志）。
			in.RequestScopedTransient = true
		}
		gotClass, _ := service.ClassifyUpstreamFailure(in)
		if declared {
			require.Equal(t, wantClass, string(gotClass),
				"状态码 %d：config 默认表声明 %s，分类器给出 %s —— 两份默认表已漂移", code, wantClass, gotClass)
			continue
		}
		require.Equal(t, string(service.ClassRequestInvalid), string(gotClass),
			"状态码 %d 不在 config 默认表里，分类器必须回落 request_invalid（否则说明有人在 service 侧单边加了码）",
			code)
	}
}

// TestFailoverStatusClassesAdviceMatchesClassifierContract 锁定每个类别的处置建议。
//
// 为什么放在这个文件：处置建议（advice）只存在于分类器侧；config 表按契约一律留 nil。
// 一旦有人改了某类的 advice 字段（例如把 capacity_shed 的 Scope 从 provider 改成 account，
// 或给 request_invalid 打开 Failover），配置面与行为面的契约就断了——这里逐字段比对。
//
// 输入刻意保持「零值 + 仅状态码/传输层」：不带 Retry-After 头、不带 body、不注入策略，
// 因此拿到的是**内置默认建议**本身（429/529 的 cooldownSource 会在无 reset 头时回落 fixed，
// 这是 failoverAdviceFor 的既有行为，不是漂移）。
func TestFailoverStatusClassesAdviceMatchesClassifierContract(t *testing.T) {
	type adviceWant struct {
		failover       bool
		sameAccount    service.RetryPolicy
		cooldown       time.Duration
		cooldownSource string
		tempUnschedule bool
		clearSticky    bool
		scope          service.GatewayFailureScope
	}

	cases := []struct {
		name  string
		input service.ClassifyInput
		want  adviceWant
	}{
		{
			name:  "auth_invalid（401）",
			input: service.ClassifyInput{StatusCode: http.StatusUnauthorized},
			want: adviceWant{
				failover: true, sameAccount: service.RetryInherit, cooldown: 60 * time.Second,
				cooldownSource: "fixed", tempUnschedule: false, clearSticky: true,
				scope: service.GatewayFailureScopeAccount,
			},
		},
		{
			name:  "rate_limited（429，无 reset 头 → 回落 fixed）",
			input: service.ClassifyInput{StatusCode: http.StatusTooManyRequests},
			want: adviceWant{
				failover: true, sameAccount: service.RetryInherit, cooldown: 60 * time.Second,
				cooldownSource: "fixed", tempUnschedule: false, clearSticky: true,
				scope: service.GatewayFailureScopeAccount,
			},
		},
		{
			name:  "quota_exhausted（529，无 reset 头 → 回落 fixed）",
			input: service.ClassifyInput{StatusCode: 529},
			want: adviceWant{
				failover: true, sameAccount: service.RetryInherit, cooldown: 30 * time.Minute,
				cooldownSource: "fixed", tempUnschedule: false, clearSticky: true,
				scope: service.GatewayFailureScopeAccount,
			},
		},
		{
			name:  "capacity_shed（503 + RequestScopedTransient：不冷却、provider 归属）",
			input: service.ClassifyInput{StatusCode: http.StatusServiceUnavailable, RequestScopedTransient: true},
			want: adviceWant{
				failover: true, sameAccount: service.RetryInherit, cooldown: 0,
				cooldownSource: "none", tempUnschedule: false, clearSticky: true,
				scope: service.GatewayFailureScopeProvider,
			},
		},
		{
			name:  "upstream_5xx（503 无 RequestScopedTransient → 普通 5xx）",
			input: service.ClassifyInput{StatusCode: http.StatusInternalServerError},
			want: adviceWant{
				failover: true, sameAccount: service.RetryInherit, cooldown: 60 * time.Second,
				cooldownSource: "fixed", tempUnschedule: false, clearSticky: true,
				scope: service.GatewayFailureScopeAccount,
			},
		},
		{
			name:  "upstream_timeout（传输层超时）",
			input: service.ClassifyInput{TransportKind: service.TransportFailureTimeout},
			want: adviceWant{
				failover: true, sameAccount: service.RetryInherit, cooldown: 60 * time.Second,
				cooldownSource: "fixed", tempUnschedule: false, clearSticky: true,
				scope: service.GatewayFailureScopeAccount,
			},
		},
		{
			name:  "network（传输层连接重置）",
			input: service.ClassifyInput{TransportKind: service.TransportFailureConnectionReset},
			want: adviceWant{
				failover: true, sameAccount: service.RetryInherit, cooldown: 60 * time.Second,
				cooldownSource: "fixed", tempUnschedule: false, clearSticky: true,
				scope: service.GatewayFailureScopeAccount,
			},
		},
		{
			name:  "request_invalid（400 非白名单：不换号、不冷却）",
			input: service.ClassifyInput{StatusCode: http.StatusBadRequest},
			want: adviceWant{
				failover: false, sameAccount: service.RetryInherit, cooldown: 0,
				cooldownSource: "none", tempUnschedule: false, clearSticky: false,
				scope: service.GatewayFailureScopeRequest,
			},
		},
		{
			name:  "policy_blocked（本地策略拒绝；类别由标志位决定，不来自状态码表）",
			input: service.ClassifyInput{StatusCode: http.StatusBadRequest, PolicyBlocked: true},
			want: adviceWant{
				failover: false, sameAccount: service.RetryOff, cooldown: 0,
				cooldownSource: "none", tempUnschedule: false, clearSticky: false,
				scope: service.GatewayFailureScopeRequest,
			},
		},
		{
			name:  "binding_passthru（绑定缓存透传；类别由标志位决定）",
			input: service.ClassifyInput{StatusCode: http.StatusTooManyRequests, BoundUpstreamPassthrough: true},
			want: adviceWant{
				failover: false, sameAccount: service.RetryOff, cooldown: 0,
				cooldownSource: "none", tempUnschedule: false, clearSticky: false,
				scope: service.GatewayFailureScopeAccount,
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, advice := service.ClassifyUpstreamFailure(tc.input)
			require.Equal(t, tc.want.failover, advice.Failover, "Failover 漂移")
			require.Equal(t, tc.want.sameAccount, advice.SameAccount, "SameAccount 漂移")
			require.Equal(t, 0, advice.SameAccountRetries, "默认建议不得指定错误级重试次数")
			require.Equal(t, tc.want.cooldown, advice.Cooldown, "Cooldown 漂移")
			require.Equal(t, tc.want.cooldownSource, advice.CooldownSource, "CooldownSource 漂移")
			require.Equal(t, tc.want.tempUnschedule, advice.TempUnschedule,
				"TempUnschedule 漂移：P0/P1 不接管 T3，内置建议必须全为 false")
			require.Equal(t, tc.want.clearSticky, advice.ClearSticky, "ClearSticky 漂移")
			require.Equal(t, tc.want.scope, advice.Scope, "Scope 漂移")
		})
	}
}

// TestDefaultFailoverStatusClassesReturnsFreshCopy 锁定导出函数的「不共享底层数组」性质。
//
// 导出之后任何调用方都可能改写返回值；如果实现改成返回包级共享切片，测试 A 的改写会
// 污染测试 B 与生产路径（load() 只在 StatusClasses==nil 时调用一次，但仍不该暴露可写
// 的内部状态）。这里显式断言每次调用都拿到独立副本。
func TestDefaultFailoverStatusClassesReturnsFreshCopy(t *testing.T) {
	first := config.DefaultFailoverStatusClasses()
	require.NotEmpty(t, first)
	require.NotEmpty(t, first[0].Codes)

	originalCode := first[0].Codes[0]
	first[0].Codes[0] = -1
	first[0].Class = "mutated"

	second := config.DefaultFailoverStatusClasses()
	require.Equal(t, originalCode, second[0].Codes[0], "导出函数不得返回内部共享切片")
	require.NotEqual(t, "mutated", second[0].Class, "导出函数不得返回内部共享结构")
}

// failoverDefaultCodeClasses 把 config 默认表展开成「状态码 → 类别」映射（先出现先命中，
// 与分类器的规则顺序语义一致）。
func failoverDefaultCodeClasses(
	t *testing.T,
	rules []config.FailoverStatusClassRuleConfig,
) map[int]string {
	t.Helper()
	classes := make(map[int]string, 128)
	assign := func(code int, class string) {
		if _, exists := classes[code]; exists {
			return // 先匹配先命中：重复码以更靠前的规则为准
		}
		classes[code] = class
	}
	for _, rule := range rules {
		for _, code := range rule.Codes {
			assign(code, rule.Class)
		}
		for _, raw := range rule.Ranges {
			low, high := parseFailoverStatusRange(t, raw)
			for code := low; code <= high; code++ {
				assign(code, rule.Class)
			}
		}
	}
	return classes
}

// parseFailoverStatusRange 解析 "500-599" 形式的闭区间。
func parseFailoverStatusRange(t *testing.T, raw string) (int, int) {
	t.Helper()
	lowText, highText, found := strings.Cut(strings.TrimSpace(raw), "-")
	require.True(t, found, "默认表里的码段 %q 必须是 \"min-max\" 形式", raw)
	low, err := strconv.Atoi(strings.TrimSpace(lowText))
	require.NoError(t, err, "码段 %q 的下界不可解析", raw)
	high, err := strconv.Atoi(strings.TrimSpace(highText))
	require.NoError(t, err, "码段 %q 的上界不可解析", raw)
	require.LessOrEqual(t, low, high, "码段 %q 必须 min <= max", raw)
	return low, high
}
