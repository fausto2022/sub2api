package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 本文件实现「账号级容灾」的 L1 分类层（design.md §3.3.2 / implementation.md T0.4）。
//
// # 为什么需要它
//
// 改造前「是否换号」由 4 处各自硬编码的状态码集合决定，语义分散且互相不可推导：
//
//	gateway_forward.go:47-54                 {401,403,429,529} ∪ [500,∞)
//	antigravity_gateway_retry.go:780-787     同上
//	gemini_messages_compat_service.go:1706   同上（+ 服务内重试集合 :1686-1704）
//	openai_gateway_upstream_errors.go:303    {401,402,403,405,429,529} ∪ [500,∞)
//
// 状态码只是分类的**输入之一**：同一个 429 可能是瞬时限流，也可能是配额耗尽；同一个
// 503 可能是账号容量问题，也可能是上游整体降载（`RequestScopedTransient`）；同一个 400
// 既可能是请求本身错误，也可能是「Google 间歇性 400」这种**上游抖动**（`RetryableOnSameAccount`）。
// 把「状态码 → 类别 → 处置建议」显式建模，才能让 P1 的接线保持逐项等价、让 P2 之后的
// 冷却/写库/粘性策略有单一判定源。
//
// # 纯函数契约（不可破坏）
//
// ClassifyUpstreamFailure 不做任何 I/O：不读 Redis/DB、不读全局配置、不看 *gin.Context、
// 不写日志、不修改入参。所有输入来自 ClassifyInput（其中策略由调用方注入 `Policy`），
// 因此可以对任意输入穷举单测。`Body`/`Headers` 只做只读解析。
//
// P0 阶段本文件是**死代码**：不接任何调用点（接线在 T1.1）。先有等价表、后接线，
// 否则「等价」永远说不清（implementation.md T0.4 注意事项）。
//
// # 判定顺序是契约
//
// 顺序固定为（每一步都必须是前一步的「否」才继续）：
//
//	1. Canceled                 客户端取消 → 不换号、不标记、不冷却
//	2. BoundUpstreamPassthrough 绑定缓存的上游账号响应 → 原样回写，不换号
//	2b. PolicyBlocked           本地策略拒绝 → 不换号（与 2 同属「既有语义短路」）
//	3. RetryableOnSameAccount   既有语义要求先同账号重试 → 归 upstream_5xx
//	4. RequestScopedTransient   故障与账号无关 → 归 capacity_shed（不冷却、不写库）
//	5. 状态码类                  查规则表（缺省表见 defaultFailoverClassRules）
//	6. 兜底                      request_invalid
//
// 顺序不能重排，每一段都有既有测试或既有行为锁定：
//   - 1 在 5 之前：`gateway_upstream_transport_error_test.go:182` 锁定「客户端取消不标记」，
//     若让 deadline/canceled 落进 network/upstream_timeout，取消会被写成账号冷却（R-15 类事故）。
//   - 3 在 5 之前：Google 间歇性 400 是 `RetryableOnSameAccount=true` 的 400
//     （gateway_service.go:766-770），若先查状态码会落进 request_invalid → **不再重试**（回归）。
//   - 4 在 5 之前：`RequestScopedTransient` 的 429/503 是 provider 级降载，按状态码会算成
//     rate_limited/capacity 冷却 → 把与故障无关的账号一起摘掉。
//
// 三个语义字段（Stage/Scope/NextAccountAction）在这里是**叠加过滤器**，不是替代：
// 分类器给出建议后，`NextAccountStop` 仍然拥有否决权（R-48：decision = 既有语义 AND configRule）。

// FailureClass 是失败类别（design.md §3.3.2）。取值是稳定的小写下划线字符串，
// 会出现在配置（`gateway.failover_policy.status_classes[].class`）与日志里，
// 因此直接复用配置层的常量，避免两处字面量漂移。
type FailureClass string

const (
	// ClassCanceled 客户端侧取消：err == context.Canceled，或 deadline 且**请求上下文
	// 自身**已超时（见 IsClientCanceledFailure）。这不是上游故障，因此既不换号也不
	// 标记账号（gateway_upstream_transport_error_test.go:182）。
	ClassCanceled FailureClass = "canceled"

	// ClassAuthInvalid 凭据失效：401 / 403。
	ClassAuthInvalid FailureClass = FailureClass(config.FailoverClassAuthInvalid)
	// ClassRateLimited 瞬时限流：429。
	ClassRateLimited FailureClass = FailureClass(config.FailoverClassRateLimited)
	// ClassQuotaExhausted 窗口/配额耗尽：529（以及上游 reset 时间很远的 429）。
	ClassQuotaExhausted FailureClass = FailureClass(config.FailoverClassQuotaExhausted)
	// ClassCapacityShed 与账号无关的降载（`RequestScopedTransient`）。
	ClassCapacityShed FailureClass = FailureClass(config.FailoverClassCapacityShed)
	// ClassUpstream5xx 上游 5xx：[500,599]，以及 `RetryableOnSameAccount` 的抖动类 4xx。
	ClassUpstream5xx FailureClass = FailureClass(config.FailoverClassUpstream5xx)
	// ClassUpstreamTimeout 首字节 / 响应头 / 上游侧 deadline 超时。
	ClassUpstreamTimeout FailureClass = FailureClass(config.FailoverClassUpstreamTimeout)
	// ClassNetwork 拨号 / TLS / 连接重置 / EOF 等传输层失败。
	ClassNetwork FailureClass = FailureClass(config.FailoverClassNetwork)
	// ClassRequestInvalid 请求本身的问题：400 / 404 / 409 / 413 / 422（默认不换号）。
	ClassRequestInvalid FailureClass = FailureClass(config.FailoverClassRequestInvalid)
	// ClassPolicyBlocked 本地策略拒绝（如 BetaBlockedError）：禁止换号。
	ClassPolicyBlocked FailureClass = FailureClass(config.FailoverClassPolicyBlocked)
	// ClassBindingPassthru 绑定显式缓存的上游网关账号收到 429/5xx：原样回写，禁止换号。
	ClassBindingPassthru FailureClass = FailureClass(config.FailoverClassBindingPassthru)
)

// failoverAllClasses 是 11 个类别的完整清单（顺序无含义，仅用于穷举测试与排障）。
var failoverAllClasses = []FailureClass{
	ClassCanceled,
	ClassAuthInvalid,
	ClassRateLimited,
	ClassQuotaExhausted,
	ClassCapacityShed,
	ClassUpstream5xx,
	ClassUpstreamTimeout,
	ClassNetwork,
	ClassRequestInvalid,
	ClassPolicyBlocked,
	ClassBindingPassthru,
}

// FailoverProfile 标识「同一状态码在不同网关族下的旧语义」。
//
// 4 处旧实现并不完全一致：OpenAI 网关把 402（余额/配额）与 405 也视为「换号可能成功」，
// 而 Anthropic/Gemini/Antigravity 三处只看 {401,403,429,529} ∪ [500,∞)。这个差异是
// **既有事实**，F-01 等价表要求逐项相等，因此它必须成为分类器的输入，而不是藏进调用方。
type FailoverProfile string

const (
	// FailoverProfileGeneric 未指定网关族：只用通用规则（= Anthropic/Antigravity/Gemini 的集合）。
	FailoverProfileGeneric FailoverProfile = ""
	// FailoverProfileAnthropic gateway_forward.go 的 Anthropic / Bedrock 路径。
	FailoverProfileAnthropic FailoverProfile = "anthropic"
	// FailoverProfileAntigravity antigravity_gateway_retry.go 的路径。
	FailoverProfileAntigravity FailoverProfile = "antigravity"
	// FailoverProfileGemini gemini_messages_compat_service.go 的路径。
	FailoverProfileGemini FailoverProfile = "gemini"
	// FailoverProfileOpenAI openai_gateway_upstream_errors.go 的路径（402/405 也换号）。
	FailoverProfileOpenAI FailoverProfile = "openai"
)

// failoverProfileExtraClasses 记录**只有 OpenAI 网关**才视为可换号的额外状态码。
//
//	402 Payment Required → 余额/配额耗尽，换号有意义 → quota_exhausted
//	405 Method Not Allowed → OpenAI 兼容端点/账号配置不匹配，换号可能成功 → upstream_5xx
//	                          （借 upstream_5xx 的处置：换号 + 同账号沿用 + 60s 冷却 + account）
//
// 不要把它并入 defaultFailoverClassRules：那会让 Anthropic/Gemini/Antigravity 三处在
// 402/405 上从「不换号」变成「换号」，直接破坏 F-01 等价表。
var failoverProfileExtraClasses = map[FailoverProfile]map[int]FailureClass{
	FailoverProfileOpenAI: {
		http.StatusPaymentRequired:  ClassQuotaExhausted,
		http.StatusMethodNotAllowed: ClassUpstream5xx,
	},
}

// TransportFailureKind 是无 HTTP 响应时（纯传输层失败）的失败形态。
// 它只回答「像哪一类网络故障」，**不回答**「是否持久」——后者仍由既有的
// classifyUpstreamTransportError(...).Persistent 负责（openai_upstream_transport_error.go:68），
// 两者的区别是：kind 决定类别与处置建议，Persistent 决定是否写 T3 临时不可调度。
type TransportFailureKind string

const (
	// TransportFailureNone 有 HTTP 响应（或未提供传输层信息）。
	TransportFailureNone TransportFailureKind = ""
	// TransportFailureCanceled 客户端取消（与 Canceled 字段等价，便于调用方只传 kind）。
	TransportFailureCanceled TransportFailureKind = "canceled"
	// TransportFailureTimeout 超时：上游 deadline、首字节超时、响应头超时。
	TransportFailureTimeout TransportFailureKind = "timeout"
	// TransportFailureConnectionReset 连接被重置（ECONNRESET / ECONNABORTED / EPIPE）。
	TransportFailureConnectionReset TransportFailureKind = "connection_reset"
	// TransportFailureEOF 连接提前结束（EOF / unexpected EOF）。
	TransportFailureEOF TransportFailureKind = "eof"
	// TransportFailureConnectionRefused 连接被拒绝（ECONNREFUSED）。
	TransportFailureConnectionRefused TransportFailureKind = "connection_refused"
	// TransportFailureDNS DNS 解析失败。
	TransportFailureDNS TransportFailureKind = "dns"
	// TransportFailureTLS TLS 握手/证书失败。
	TransportFailureTLS TransportFailureKind = "tls"
	// TransportFailureOther 其它传输层失败（未知）。
	TransportFailureOther TransportFailureKind = "other"
)

// RetryPolicy 是同账号重试策略（design.md §3.3.2 的 "inherit | off | n"）。
type RetryPolicy string

const (
	// RetryInherit 沿用账号凭据的 pool_mode_retry_count / pool_mode_retry_status_codes
	// 与错误对象上的 RetryableOnSameAccount 标志——行为与改造前完全一致。
	// 这是所有「账号级」类别的默认值：分类器不覆盖既有同账号重试语义，
	// 因此 P1 接线时不会出现「换号对了、重试不对」的半接状态。
	RetryInherit RetryPolicy = "inherit"
	// RetryOff 关闭同账号重试（只换号）。
	RetryOff RetryPolicy = "off"
)

// 冷却时长来源（RetryAdvice.CooldownSource，design.md §3.3.2 的取值）。
const (
	// cooldownSourceNone 不冷却。
	cooldownSourceNone = "none"
	// cooldownSourceFixed 用类别内置的固定时长。
	cooldownSourceFixed = "fixed"
	// cooldownSourceUpstreamReset 用上游给的 reset/Retry-After；解析不到时回落固定时长。
	cooldownSourceUpstreamReset = "upstream_reset"
)

// RetryAdvice 是某个失败类别的「处置建议」：可执行、可断言、可被策略层覆盖。
//
// 它是**建议**而不是执行：T2（冷却）/T3（写库）/粘性清理的执行在 P2/P3 落地，
// 本阶段只保证同一输入的建议稳定。
type RetryAdvice struct {
	// Failover 是否允许换到同组下一个账号。
	Failover bool
	// SameAccount 同账号重试策略：inherit（沿用既有语义）/ off（关闭）。
	SameAccount RetryPolicy
	// SameAccountRetries 错误级同账号重试上限；0 = 未指定（沿用 handler 默认预算）。
	SameAccountRetries int
	// Cooldown 该类别的建议冷却时长（T2 层还要看 policy.cooldown.enabled 才真正写）。
	Cooldown time.Duration
	// CooldownSource Cooldown 的来源：none / fixed / upstream_reset。
	CooldownSource string
	// TempUnschedule 是否**要求**写 T3 临时不可调度（true = 必须写）。
	//
	// 内置默认表里全部为 false：现状的写库全部来自既有路径
	// （TempUnscheduleRetryableError / classifyUpstreamTransportError().Persistent /
	// rate limit service），分类器只做判定、不接管执行，因此 P0 保持零行为变更。
	// 「禁止写库」不靠本字段表达，而靠 Scope：request/provider 归属的失败不得落在
	// 被选中的账号头上（openai_apikey_health_breaker.go:34 也是这个口径）。
	TempUnschedule bool
	// ClearSticky 是否清粘性绑定（B2 的类别级开关；总开关是
	// gateway.failover_policy.switch_order.clear_sticky_on_account_failure）。
	ClearSticky bool
	// Scope 失败归属：account / provider / request。
	Scope GatewayFailureScope
}

// ClassifyInput 是分类器的全部输入。所有字段都是**值**：分类器不读外部状态。
type ClassifyInput struct {
	// StatusCode 上游 HTTP 状态码；0 表示没拿到 HTTP 响应（纯传输层失败）。
	StatusCode int
	// Profile 网关族（决定 402/405 等「同码不同义」的差异）；空 = 按 Platform 推导。
	Profile FailoverProfile
	// Platform 平台标识（best-effort 推导 Profile；显式 Profile 优先）。
	Platform string
	// Canceled 本次失败是否由**客户端侧**取消引起；调用方用 IsClientCanceledFailure 计算。
	Canceled bool
	// TransportKind 传输层失败形态（StatusCode==0 时有意义）。
	TransportKind TransportFailureKind
	// ResponseBody 上游响应体（只读；用于 400 关键词白名单判定）。
	ResponseBody []byte
	// ResponseHeaders 上游响应头（只读；用于 Retry-After / reset 冷却时长）。
	ResponseHeaders http.Header

	// Stage 失败发生的阶段（既有语义）。
	Stage GatewayFailureStage
	// Scope 既有语义给出的失败归属（既有语义）。
	Scope GatewayFailureScope
	// NextAccountAction 既有语义给出的下一账号动作；NextAccountStop 拥有最终否决权。
	NextAccountAction NextAccountAction
	// IsCredentialFailure 既有语义判定为凭据失效（Stage == account_auth）。
	IsCredentialFailure bool
	// RetryableOnSameAccount 既有语义要求先在同账号重试（Google 间歇性 400 / 空响应 / 池模式）。
	RetryableOnSameAccount bool
	// RequestScopedTransient 故障因素与账号无关（上游按客户端身份/模型容量降载）。
	RequestScopedTransient bool
	// BoundUpstreamPassthrough 绑定显式缓存的上游账号收到 429/5xx（原样回写）。
	BoundUpstreamPassthrough bool
	// PolicyBlocked 本地策略拒绝（如 BetaBlockedError）。
	PolicyBlocked bool

	// Policy 生效策略（必选由调用方注入）。nil = 内置默认表 = 改造前现状。
	// 注意：`Policy.Legacy()` 为 true 时调用方应直接走旧硬编码路径，不要依赖本函数的建议。
	Policy *FailoverPolicy
}

// profile 返回生效的网关族：显式 Profile 优先，其次按 Platform 推导。
func (in ClassifyInput) profile() FailoverProfile {
	if in.Profile != FailoverProfileGeneric {
		return in.Profile
	}
	switch in.Platform {
	case PlatformAnthropic:
		return FailoverProfileAnthropic
	case PlatformAntigravity:
		return FailoverProfileAntigravity
	case PlatformGemini:
		return FailoverProfileGemini
	case PlatformOpenAI:
		return FailoverProfileOpenAI
	default:
		return FailoverProfileGeneric
	}
}

// ClassifyUpstreamFailure 是分类层的唯一入口（纯函数）。
//
// 返回 (类别, 处置建议)。判定顺序见文件头注释，顺序本身是契约。
func ClassifyUpstreamFailure(in ClassifyInput) (FailureClass, RetryAdvice) {
	// 1. 客户端取消：不是上游故障，不换号、不标记、不冷却。
	if in.Canceled || in.TransportKind == TransportFailureCanceled {
		return ClassCanceled, failoverAdviceFor(in, ClassCanceled, nil)
	}

	// 2. 绑定上游透传：响应反映的是持有缓存的上游账号，原样回写才有意义。
	if in.BoundUpstreamPassthrough {
		return ClassBindingPassthru, failoverAdviceFor(in, ClassBindingPassthru, nil)
	}

	// 2b. 本地策略拒绝：请求根本没被上游判定，换号只会把同一请求打到另一个账号。
	if in.PolicyBlocked {
		return ClassPolicyBlocked, failoverAdviceFor(in, ClassPolicyBlocked, nil)
	}

	// 3. 既有语义要求先同账号重试（Google 间歇性 400 / 502 空响应 / 池模式）。
	//
	// 归 upstream_5xx 而不是 request_invalid：这类 4xx 是上游抖动，不是请求错误
	// （design §3.3.4 的「易错点」）。若同时带 RequestScopedTransient，类别升级为
	// capacity_shed（provider 归属、不冷却、不写库），但同账号重试语义仍按本分支保留。
	if in.RetryableOnSameAccount {
		class := ClassUpstream5xx
		if in.RequestScopedTransient {
			class = ClassCapacityShed
		}
		advice := failoverAdviceFor(in, class, nil)
		advice.SameAccount = RetryInherit
		return class, advice
	}

	// 4. 请求级瞬时故障：与账号无关的上游降载。
	if in.RequestScopedTransient {
		return ClassCapacityShed, failoverAdviceFor(in, ClassCapacityShed, nil)
	}

	// 5. 状态码类（含 400 三分类与传输层类比）。
	class, rule := resolveFailoverClass(in)

	// 6. 兜底：没命中任何规则的类别一律按「请求本身的问题」处理（禁止换号，最保守）。
	advice := failoverAdviceFor(in, class, rule)
	return class, advice
}

// failoverClassRule 是分类器内部的规则项：`class + codes/ranges + advice 覆盖`。
// 它与 config.FailoverStatusClassRuleConfig 同构，但把 range 预解析成闭区间，
// 避免每次分类都做字符串解析。
type failoverClassRule struct {
	class  FailureClass
	codes  []int
	ranges [][2]int
	advice *config.FailoverRetryAdviceConfig
}

// matches 报告状态码是否命中该规则（精确 or 闭区间）。
func (r failoverClassRule) matches(statusCode int) bool {
	if statusCode <= 0 {
		return false
	}
	for _, code := range r.codes {
		if code == statusCode {
			return true
		}
	}
	for _, span := range r.ranges {
		if statusCode >= span[0] && statusCode <= span[1] {
			return true
		}
	}
	return false
}

// defaultFailoverClassRules 是分类器的内置默认规则表。
//
// 「顺序即判定顺序（先匹配先命中）」：429 必须排在 [500,599] 之前，否则 5xx 段不会
// 抢走 429；503 排在 [500,599] 之前，否则 capacity_shed 永远不会命中。
//
// 与 config.defaultFailoverStatusClasses()（config.go:1901）保持一致：配置层在 load()
// 后会把默认表填进 `status_classes`，因此生产路径用的是配置表；这里只是「调用方没注入
// 策略」时的兜底，两者**不得漂移**（改一处必须改另一处，F-01 表会同时覆盖两者）。
func defaultFailoverClassRules() []failoverClassRule {
	return []failoverClassRule{
		{class: ClassAuthInvalid, codes: []int{http.StatusUnauthorized, http.StatusForbidden}},
		{class: ClassRateLimited, codes: []int{http.StatusTooManyRequests}},
		{class: ClassQuotaExhausted, codes: []int{529}},
		{class: ClassCapacityShed, codes: []int{http.StatusServiceUnavailable}},
		{
			class:  ClassUpstream5xx,
			codes:  []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout},
			ranges: [][2]int{{500, 599}},
		},
		{class: ClassUpstreamTimeout},
		{class: ClassNetwork},
		{
			class: ClassRequestInvalid,
			codes: []int{
				http.StatusBadRequest,
				http.StatusNotFound,
				http.StatusConflict,
				http.StatusRequestEntityTooLarge,
				http.StatusUnprocessableEntity,
			},
		},
		{class: ClassPolicyBlocked},
		{class: ClassBindingPassthru},
	}
}

// builtinCompat400Keywords 是 400 兼容性关键词白名单的内置默认值（9 个），
// 与 config.go 的 `gateway.failover_policy.compat_400_keywords` 默认值、
// 以及改造前 shouldFailoverOn400（gateway_upstream_response.go:212-237）的判断集合一致。
var builtinCompat400Keywords = []string{
	"anthropic-beta",
	"beta feature",
	"requires beta",
	"thinking",
	"thought_signature",
	"signature",
	"tool_use",
	"tool_result",
	"tools",
}

// resolveFailoverClass 只做「状态码 / 传输层 → 类别」这一段，返回命中的规则
// （便于把规则上的 advice 覆盖带出去）。
func resolveFailoverClass(in ClassifyInput) (FailureClass, *config.FailoverRetryAdviceConfig) {
	if in.StatusCode <= 0 {
		return failoverClassForTransport(in.TransportKind), nil
	}

	// 网关族差异（402/405）优先于通用表：它是同一个状态码的不同既有语义。
	if extras, ok := failoverProfileExtraClasses[in.profile()]; ok {
		if class, ok := extras[in.StatusCode]; ok {
			return class, nil
		}
	}

	for _, rule := range failoverClassRules(in.Policy) {
		if rule.matches(in.StatusCode) {
			class := rule.class
			// capacity_shed 只对「与账号无关的降载」成立（design §3.3.4）：
			// 没有 RequestScopedTransient 的 503 是普通上游 5xx（会冷却、会算账号健康）。
			if class == ClassCapacityShed && !in.RequestScopedTransient {
				class = ClassUpstream5xx
			}
			return class, rule.advice
		}
	}
	return ClassRequestInvalid, nil
}

// failoverClassRules 返回生效的规则表：调用方注入了 status_classes 就用它（整体替换），
// 否则用内置默认表。
func failoverClassRules(policy *FailoverPolicy) []failoverClassRule {
	if policy == nil || policy.StatusClasses == nil || len(*policy.StatusClasses) == 0 {
		return defaultFailoverClassRules()
	}
	configured := *policy.StatusClasses
	rules := make([]failoverClassRule, 0, len(configured))
	for _, item := range configured {
		class := FailureClass(strings.TrimSpace(item.Class))
		if class == "" {
			continue
		}
		rules = append(rules, failoverClassRule{
			class:  class,
			codes:  item.Codes,
			ranges: parseFailoverStatusRanges(item.Ranges),
			advice: item.Advice,
		})
	}
	if len(rules) == 0 {
		return defaultFailoverClassRules()
	}
	return rules
}

// parseFailoverStatusRanges 把 "500-599" 解析成闭区间；非法项忽略
// （配置层 validateGatewayFailoverPolicy 已做范围校验，这里只做容错）。
func parseFailoverStatusRanges(raw []string) [][2]int {
	if len(raw) == 0 {
		return nil
	}
	out := make([][2]int, 0, len(raw))
	for _, item := range raw {
		parts := strings.SplitN(strings.TrimSpace(item), "-", 2)
		if len(parts) != 2 {
			continue
		}
		low, errLow := strconv.Atoi(strings.TrimSpace(parts[0]))
		high, errHigh := strconv.Atoi(strings.TrimSpace(parts[1]))
		if errLow != nil || errHigh != nil || low <= 0 || high < low {
			continue
		}
		out = append(out, [2]int{low, high})
	}
	return out
}

// failoverClassForTransport 把传输层形态映射成类别。
// deadline 归 upstream_timeout、连接重置/EOF 归 network：两者都「换号但不驱逐」，
// 区别只体现在观测与冷却口径（gateway_upstream_transport_error_test.go:189 锁定 deadline 仍换号）。
func failoverClassForTransport(kind TransportFailureKind) FailureClass {
	switch kind {
	case TransportFailureTimeout:
		return ClassUpstreamTimeout
	case TransportFailureConnectionReset, TransportFailureEOF,
		TransportFailureConnectionRefused, TransportFailureDNS, TransportFailureTLS,
		TransportFailureOther:
		return ClassNetwork
	default:
		// 既没有状态码也没有传输层形态：按最保守的请求错误处理（不换号）。
		return ClassRequestInvalid
	}
}

// failoverAdviceFor 生成某类别的处置建议：内置默认表 → 规则上的 advice 覆盖 → 全局策略收敛。
func failoverAdviceFor(in ClassifyInput, class FailureClass, rule *config.FailoverRetryAdviceConfig) RetryAdvice {
	advice := builtinFailoverAdvice(class)

	// 规则级覆盖（status_classes[].advice）：只在显式配置时改变默认值。
	applyRetryAdviceConfig(&advice, rule)

	// 400 + 兼容性关键词白名单：只有 `failover_on_400=true` 时才换号（默认 false = 现状）。
	if in.StatusCode == http.StatusBadRequest && class == ClassRequestInvalid {
		advice.Failover = advice.Failover || (failoverOn400Enabled(in.Policy) && matchesCompat400Keywords(in.ResponseBody, compat400Keywords(in.Policy)))
	}

	// 全局策略收敛：同账号重试总开关、冷却上限。
	if policy := in.Policy; policy != nil {
		if policy.SameAccountDefaultMode != nil &&
			strings.EqualFold(strings.TrimSpace(*policy.SameAccountDefaultMode), config.FailoverSameAccountModeOff) {
			advice.SameAccount = RetryOff
		}
		if policy.CooldownMaxTTL != nil && *policy.CooldownMaxTTL > 0 &&
			advice.Cooldown > *policy.CooldownMaxTTL {
			advice.Cooldown = *policy.CooldownMaxTTL
		}
	}

	// 上游 reset 优先于固定时长；解析不到则回落固定时长（design §3.4.3）。
	if advice.CooldownSource == cooldownSourceUpstreamReset {
		if reset, ok := failoverUpstreamResetDelay(in.ResponseHeaders); ok {
			advice.Cooldown = reset
			if policy := in.Policy; policy != nil && policy.CooldownMaxTTL != nil && *policy.CooldownMaxTTL > 0 &&
				advice.Cooldown > *policy.CooldownMaxTTL {
				advice.Cooldown = *policy.CooldownMaxTTL
			}
		} else {
			advice.CooldownSource = cooldownSourceFixed
		}
	}

	// 既有语义的最终否决权（R-48：decision = 既有语义 AND configRule）。
	// 放在最后而不是最前：类别仍按顺序判定（日志/观测要看真实类别），只有「换号」被否决。
	if in.NextAccountAction == NextAccountStop {
		advice.Failover = false
	}
	return advice
}

// builtinFailoverAdvice 返回类别内置的默认建议（= 改造前现状，design §3.3.2 默认规则表）。
//
// 两处刻意的取舍（都有理由，不要「按 design 表格改回去」）：
//
//  1. SameAccount 默认为 inherit（不是 off）。design §3.3.2 表里 quota_exhausted / 白名单 400
//     写的是 off，但同一张表的标题是「必须与现状逐项等价」——529 在
//     shouldRetryGeminiUpstreamError（gemini_messages_compat_service.go:1686-1704）与
//     shouldRetryAntigravityError（antigravity_gateway_retry.go:719-726）里**确实**同账号重试。
//     把 off 写进内置默认会让 P1 的等价性直接破功。off 是改造目标态，用配置打开：
//     `status_classes[].advice.same_account: "off"`（本函数已支持）。
//  2. TempUnschedule 全部为 false：见 RetryAdvice.TempUnschedule 的注释（P0 不接管 T3）。
func builtinFailoverAdvice(class FailureClass) RetryAdvice {
	switch class {
	case ClassCanceled:
		// 客户端走了：换号、标记、冷却都没有意义。
		return RetryAdvice{
			Failover:       false,
			SameAccount:    RetryOff,
			CooldownSource: cooldownSourceNone,
			TempUnschedule: false,
			ClearSticky:    false,
			Scope:          GatewayFailureScopeRequest,
		}
	case ClassBindingPassthru:
		// 缓存绑定的账号状态要原样回写，换号会破坏缓存语义。
		return RetryAdvice{
			Failover:       false,
			SameAccount:    RetryOff,
			CooldownSource: cooldownSourceNone,
			TempUnschedule: false,
			ClearSticky:    false,
			Scope:          GatewayFailureScopeAccount,
		}
	case ClassPolicyBlocked:
		return RetryAdvice{
			Failover:       false,
			SameAccount:    RetryOff,
			CooldownSource: cooldownSourceNone,
			TempUnschedule: false,
			ClearSticky:    false,
			Scope:          GatewayFailureScopeRequest,
		}
	case ClassAuthInvalid:
		return RetryAdvice{
			Failover:       true,
			SameAccount:    RetryInherit,
			Cooldown:       60 * time.Second,
			CooldownSource: cooldownSourceFixed,
			TempUnschedule: false,
			ClearSticky:    true,
			Scope:          GatewayFailureScopeAccount,
		}
	case ClassRateLimited:
		return RetryAdvice{
			Failover:       true,
			SameAccount:    RetryInherit,
			Cooldown:       60 * time.Second,
			CooldownSource: cooldownSourceUpstreamReset,
			TempUnschedule: false,
			ClearSticky:    true,
			Scope:          GatewayFailureScopeAccount,
		}
	case ClassQuotaExhausted:
		// 窗口耗尽要等上游窗口重置，兜底 30 分钟（design §3.4.3）。
		return RetryAdvice{
			Failover:       true,
			SameAccount:    RetryInherit,
			Cooldown:       30 * time.Minute,
			CooldownSource: cooldownSourceUpstreamReset,
			TempUnschedule: false,
			ClearSticky:    true,
			Scope:          GatewayFailureScopeAccount,
		}
	case ClassCapacityShed:
		// 关键约束：provider 级降载**不冷却、不写库**，否则上游整体故障会把全池摘空。
		return RetryAdvice{
			Failover:       true,
			SameAccount:    RetryInherit,
			Cooldown:       0,
			CooldownSource: cooldownSourceNone,
			TempUnschedule: false,
			ClearSticky:    true,
			Scope:          GatewayFailureScopeProvider,
		}
	case ClassUpstream5xx:
		return RetryAdvice{
			Failover:       true,
			SameAccount:    RetryInherit,
			Cooldown:       60 * time.Second, // 连续失败达阈值才真正冷却（T2 层看 streak）
			CooldownSource: cooldownSourceFixed,
			TempUnschedule: false,
			ClearSticky:    true,
			Scope:          GatewayFailureScopeAccount,
		}
	case ClassUpstreamTimeout:
		return RetryAdvice{
			Failover:       true,
			SameAccount:    RetryInherit,
			Cooldown:       60 * time.Second,
			CooldownSource: cooldownSourceFixed,
			TempUnschedule: false,
			ClearSticky:    true,
			Scope:          GatewayFailureScopeAccount,
		}
	case ClassNetwork:
		return RetryAdvice{
			Failover:       true,
			SameAccount:    RetryInherit,
			Cooldown:       60 * time.Second,
			CooldownSource: cooldownSourceFixed,
			TempUnschedule: false,
			ClearSticky:    true,
			Scope:          GatewayFailureScopeAccount,
		}
	default: // ClassRequestInvalid 及未知类别
		return RetryAdvice{
			Failover:       false,
			SameAccount:    RetryInherit,
			Cooldown:       0,
			CooldownSource: cooldownSourceNone,
			TempUnschedule: false,
			ClearSticky:    false,
			Scope:          GatewayFailureScopeRequest,
		}
	}
}

// applyRetryAdviceConfig 把规则上的 advice 覆盖叠加到内置默认值上（nil = 未设置）。
func applyRetryAdviceConfig(advice *RetryAdvice, cfg *config.FailoverRetryAdviceConfig) {
	if advice == nil || cfg == nil {
		return
	}
	if cfg.Failover != nil {
		advice.Failover = *cfg.Failover
	}
	if cfg.SameAccount != nil {
		switch mode := strings.ToLower(strings.TrimSpace(*cfg.SameAccount)); {
		case mode == "":
			// 未设置：保持默认。
		case mode == config.FailoverSameAccountModeOff:
			advice.SameAccount = RetryOff
			advice.SameAccountRetries = 0
		case mode == config.FailoverSameAccountModeInherit:
			advice.SameAccount = RetryInherit
			advice.SameAccountRetries = 0
		default:
			// 具体次数："2" → 沿用 inherit 语义，但带上错误级次数上限。
			if n, err := strconv.Atoi(mode); err == nil && n >= 0 {
				advice.SameAccount = RetryInherit
				advice.SameAccountRetries = n
			}
		}
	}
	if cfg.Cooldown != nil {
		advice.Cooldown = *cfg.Cooldown
		if *cfg.Cooldown <= 0 {
			advice.CooldownSource = cooldownSourceNone
		}
	}
	if cfg.CooldownSource != nil {
		switch src := strings.ToLower(strings.TrimSpace(*cfg.CooldownSource)); src {
		case cooldownSourceNone, cooldownSourceFixed, cooldownSourceUpstreamReset:
			advice.CooldownSource = src
		}
	}
	if cfg.TempUnschedule != nil {
		advice.TempUnschedule = *cfg.TempUnschedule
	}
	if cfg.ClearSticky != nil {
		advice.ClearSticky = *cfg.ClearSticky
	}
	if cfg.Scope != nil {
		switch scope := strings.ToLower(strings.TrimSpace(*cfg.Scope)); GatewayFailureScope(scope) {
		case GatewayFailureScopeAccount, GatewayFailureScopeProvider, GatewayFailureScopeRequest:
			advice.Scope = GatewayFailureScope(scope)
		}
	}
}

// failoverOn400Enabled 报告策略是否允许「400 + 兼容性关键词白名单」换号（默认 false = 现状）。
func failoverOn400Enabled(policy *FailoverPolicy) bool {
	return policy != nil && policy.FailoverOn400 != nil && *policy.FailoverOn400
}

// compat400Keywords 返回生效的关键词白名单：策略配置了就用它，否则用内置 9 个。
func compat400Keywords(policy *FailoverPolicy) []string {
	if policy != nil && policy.Compat400Keywords != nil && len(*policy.Compat400Keywords) > 0 {
		return *policy.Compat400Keywords
	}
	return builtinCompat400Keywords
}

// matchesCompat400Keywords 报告响应体里的错误消息是否命中兼容性关键词白名单。
//
// 复用既有 extractUpstreamErrorMessage（纯 JSON 解析），保证与改造前
// shouldFailoverOn400（gateway_upstream_response.go:212-237）的匹配面一致。
func matchesCompat400Keywords(body []byte, keywords []string) bool {
	if len(body) == 0 || len(keywords) == 0 {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if msg == "" {
		return false
	}
	for _, keyword := range keywords {
		needle := strings.ToLower(strings.TrimSpace(keyword))
		if needle == "" {
			continue
		}
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// failoverUpstreamResetDelay 从响应头解析上游建议的冷却时长（Retry-After）。
// 只做只读解析：秒数或 HTTP 日期；解析不出则 ok=false。
func failoverUpstreamResetDelay(headers http.Header) (time.Duration, bool) {
	if headers == nil {
		return 0, false
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(raw); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay, true
		}
	}
	return 0, false
}

// IsClientCanceledFailure 报告一次失败是否属于「客户端侧取消」。
//
// 判定与改造前 handleUpstreamTransportError（gateway_upstream_transport_error.go:59）逐字同构：
//
//	err == context.Canceled                                  → 客户端走了
//	err == context.DeadlineExceeded && ctx.Err()==Deadline   → 客户端自己的 deadline 到了
//
// 注意**上游** deadline（err 是 DeadlineExceeded 但请求上下文还活着）不在这里命中：
// 那是上游故障，必须继续换号（gateway_upstream_transport_error_test.go:189 锁定）。
// 参数是纯值（`requestCtxErr` 由调用方传 `ctx.Err()`），因此本函数仍是纯函数。
func IsClientCanceledFailure(err, requestCtxErr error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) && errors.Is(requestCtxErr, context.DeadlineExceeded)
}

// ClassifyTransportFailureKind 把传输层错误归一成 TransportFailureKind（纯函数，不做 I/O）。
//
// 它只回答「像哪一类网络故障」，与 classifyUpstreamTransportError 的
// `Persistent`（是否持久 → 是否写 T3）是两个正交维度，不要合并：
// deadline / EOF / 连接重置都是 kind 明确但**不持久**的失败（换号但不驱逐账号）。
func ClassifyTransportFailureKind(err error) TransportFailureKind {
	if err == nil {
		return TransportFailureNone
	}
	switch {
	case errors.Is(err, context.Canceled):
		return TransportFailureCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return TransportFailureTimeout
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED), errors.Is(err, syscall.EPIPE):
		return TransportFailureConnectionReset
	case errors.Is(err, syscall.ECONNREFUSED):
		return TransportFailureConnectionRefused
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return TransportFailureEOF
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return TransportFailureDNS
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return TransportFailureTimeout
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "tls:"), strings.Contains(msg, "x509:"):
		return TransportFailureTLS
	case strings.Contains(msg, "connection reset"):
		return TransportFailureConnectionReset
	case strings.Contains(msg, "unexpected eof"):
		return TransportFailureEOF
	case strings.Contains(msg, "no such host"):
		return TransportFailureDNS
	// 现实里的 EOF 常被 net/http 包成 *url.Error 的字符串形态（`Post "...": EOF`），
	// 这时 errors.Is(err, io.EOF) 不成立，只能按错误文本兜底。
	case strings.Contains(msg, "eof"):
		return TransportFailureEOF
	}
	return TransportFailureOther
}
