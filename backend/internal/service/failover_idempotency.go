package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 本文件实现「幂等禁令开关 + 侧效工具识别」（design.md §3.6.4(b) /
// implementation.md T6.3）。
//
// 要解决的问题：网关路由**没有** `Idempotency-Key` 中间件
// （server/routes/gateway.go:180-189），所以跨账号换号必然产生**真实重复上游调用**。
// 若请求里含「有副作用的工具调用」（写文件、发消息、下单……），重试会让副作用执行两次。
//
// 本模块只做一件事：判定「这次请求是否允许跨账号换号」，作为**叠加过滤器**使用
// （risks.md R-48：判定器不能替代分类器的语义字段）：
//
//	switch = 分类器给出的 switch && gate.AllowAccountSwitch
//
// 三条硬约束：
//  1. **默认关闭**：`require_key_for_side_effecting=false` 时 `Evaluate` 直接返回
//     「允许换号」，**不解析请求体**，行为与改造前逐位一致（零默认行为变更）。
//  2. **只新增文件、不接调用点**：接口通过 `FailoverIdempotencyEvaluator` 注入，
//     不 import repository/redis（depguard service-no-repository）。
//  3. **与分类器解耦**：不依赖也不修改 failover_classifier.go；识别逻辑由
//     `SideEffectingToolDetector` 接口承载，可替换/mock。

// FailoverIdempotencySuppressedReasonSideEffectNoKey 是「命中侧效工具且无幂等键」
// 的抑制原因（日志/指标字段 failover_suppressed{reason=...}）。
const FailoverIdempotencySuppressedReasonSideEffectNoKey = "side_effect_no_key"

// failoverIdempotencyKeyHeader 是客户侧的幂等键请求头。
//
// 网关目前**不消费**它（只透传给上游）；本模块只把它当作「客户声明重试安全」的
// 凭据：带了它才允许跨账号换号。头名大小写不敏感由 http.Header 的规范化保证。
const failoverIdempotencyKeyHeader = "Idempotency-Key"

// FailoverIdempotencyDecision 是闸门对一次请求的判定结果。
//
// 语义边界：本结果**只**影响「是否跨账号换号」；同账号重试不受影响
// （design §3.6.4(b)：命中后 Switch=false，仍允许同账号重试）。
type FailoverIdempotencyDecision struct {
	// Enabled 闸门是否开启（= 配置 require_key_for_side_effecting）。
	Enabled bool
	// SideEffecting 请求是否被判定为「含侧效工具调用」。
	SideEffecting bool
	// MatchedTools 命中的侧效工具名（去重，保持出现顺序）；SideEffecting 的明细。
	MatchedTools []string
	// HasIdempotencyKey 请求是否带 Idempotency-Key。
	HasIdempotencyKey bool
	// AllowAccountSwitch 是否允许跨账号换号。false = 只能同账号重试或直接失败。
	AllowAccountSwitch bool
	// SuppressedReason 抑制原因；空表示未被抑制。
	SuppressedReason string
	// ParseFailed 请求体解析失败（已按 fail-closed 保守判为有副作用）。
	ParseFailed bool
}

// Suppressed 报告本次判定是否抑制了换号。
func (d FailoverIdempotencyDecision) Suppressed() bool {
	return d.SuppressedReason != ""
}

// ApplyToSwitchDecision 把闸门结果叠加到分类器的「是否换号」判定上。
//
// 这是**叠加**而不是替代：分类器说不能换号时，闸门无法让它变回可换
// （risks.md R-48：判定器只能收窄，不能放宽既有语义）。
func (d FailoverIdempotencyDecision) ApplyToSwitchDecision(classifierAllowsSwitch bool) bool {
	return classifierAllowsSwitch && d.AllowAccountSwitch
}

// SideEffectingToolDetector 抽象「从请求体里找出有副作用的工具」。
//
// 独立成接口是为了两件事：
//   - 与分类器/网关解耦（本任务不修改 failover_classifier.go）；
//   - 测试可注入计数/失败实现，从而断言「开关关闭时识别逻辑 0 次调用」。
type SideEffectingToolDetector interface {
	// DetectSideEffectingTools 返回请求体中**有副作用**的工具名（去重、保持出现顺序）。
	// blockedTools 是运维显式封禁名单（与内置写动词规则取并集，且优先级最高）。
	// 返回 error 表示请求体无法解析（调用方应 fail-closed）。
	DetectSideEffectingTools(body []byte, blockedTools []string) ([]string, error)
}

// FailoverIdempotencyEvaluator 是决策入口依赖的窄接口（P2 的 handler 侧注入）。
type FailoverIdempotencyEvaluator interface {
	Evaluate(headers http.Header, body []byte) (FailoverIdempotencyDecision, error)
}

// FailoverIdempotencyGate 是幂等禁令闸门。
//
// 零值 = 关闭（enabled=false）→ 完全不改行为，这也是配置默认值下的形态。
type FailoverIdempotencyGate struct {
	enabled      bool
	blockedTools []string
	detector     SideEffectingToolDetector
}

var _ FailoverIdempotencyEvaluator = FailoverIdempotencyGate{}

// NewFailoverIdempotencyGate 按全局配置构造闸门（使用内置的请求体识别器）。
func NewFailoverIdempotencyGate(cfg config.FailoverIdempotencyConfig) FailoverIdempotencyGate {
	return NewFailoverIdempotencyGateWithDetector(cfg, nil)
}

// NewFailoverIdempotencyGateWithDetector 按配置 + 自定义识别器构造闸门。
// detector 为 nil 时回落到内置识别器（生产路径）。
func NewFailoverIdempotencyGateWithDetector(
	cfg config.FailoverIdempotencyConfig,
	detector SideEffectingToolDetector,
) FailoverIdempotencyGate {
	if detector == nil {
		detector = failoverRequestBodyToolDetector{}
	}
	return FailoverIdempotencyGate{
		enabled:      cfg.RequireKeyForSideEffecting,
		blockedTools: append([]string(nil), cfg.BlockedTools...),
		detector:     detector,
	}
}

// NewFailoverIdempotencyGateFromPolicy 从三级覆盖后的生效策略构造闸门。
//
// 生产路径：`ResolveFailoverPolicy` 每请求解析一次并缓存，再把生效策略交给本函数。
// 指针字段为 nil 时按「未配置」处理（= 关闭 / 空名单）。
func NewFailoverIdempotencyGateFromPolicy(policy FailoverPolicy) FailoverIdempotencyGate {
	cfg := config.FailoverIdempotencyConfig{}
	if policy.IdempotencyRequireKeyForSideEffecting != nil {
		cfg.RequireKeyForSideEffecting = *policy.IdempotencyRequireKeyForSideEffecting
	}
	if policy.IdempotencyBlockedTools != nil {
		cfg.BlockedTools = *policy.IdempotencyBlockedTools
	}
	return NewFailoverIdempotencyGate(cfg)
}

// Enabled 报告闸门是否开启。
func (g FailoverIdempotencyGate) Enabled() bool {
	return g.enabled
}

// Evaluate 判定一次请求是否允许跨账号换号。
//
// 开关关闭时**不看请求体、不调用识别器**，直接返回「允许换号」——这是
// 「enabled=false 时行为等价现状」的实现口径（测试用计数型识别器断言 0 次调用）。
func (g FailoverIdempotencyGate) Evaluate(
	headers http.Header,
	body []byte,
) (FailoverIdempotencyDecision, error) {
	decision := FailoverIdempotencyDecision{
		Enabled:            g.enabled,
		HasIdempotencyKey:  HasIdempotencyKey(headers),
		AllowAccountSwitch: true,
	}
	if !g.enabled {
		return decision, nil
	}

	matched, err := g.detector.DetectSideEffectingTools(body, g.blockedTools)
	if err != nil {
		// fail-closed：请求体解析失败时保守判为「有副作用」，宁可少换号也不重复
		// 执行副作用（implementation.md T6.3 验收第 3 条）。
		decision.SideEffecting = true
		decision.ParseFailed = true
		if !decision.HasIdempotencyKey {
			decision.AllowAccountSwitch = false
			decision.SuppressedReason = FailoverIdempotencySuppressedReasonSideEffectNoKey
		}
		return decision, err
	}

	decision.MatchedTools = matched
	decision.SideEffecting = len(matched) > 0
	if !decision.SideEffecting || decision.HasIdempotencyKey {
		return decision, nil
	}
	decision.AllowAccountSwitch = false
	decision.SuppressedReason = FailoverIdempotencySuppressedReasonSideEffectNoKey
	return decision, nil
}

// HasIdempotencyKey 报告请求是否带了有效的 Idempotency-Key（去空白后非空）。
//
// 头名按大小写不敏感比对而不是只走 http.Header.Values：生产路径里 net/http 已把
// 头名规范化，但手工构造/透传的 http.Header（测试、内部转发）可能保留原始大小写。
func HasIdempotencyKey(headers http.Header) bool {
	if len(headers) == 0 {
		return false
	}
	for name, values := range headers {
		if !strings.EqualFold(name, failoverIdempotencyKeyHeader) {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}

// HasSideEffectingTool 报告请求体是否含侧效工具调用（implementation.md T6.3 的
// 便捷入口）。解析失败按 fail-closed 返回 true —— 与 Gate 的口径保持一致，
// 避免两处行为分叉。
func HasSideEffectingTool(body []byte, blockedTools []string) bool {
	matched, err := (failoverRequestBodyToolDetector{}).DetectSideEffectingTools(body, blockedTools)
	if err != nil {
		return true
	}
	return len(matched) > 0
}

// ---------------------------------------------------------------------------
// 内置识别器：请求体解析 + 工具名判定
// ---------------------------------------------------------------------------

// failoverRequestBodyToolDetector 是生产用的识别器（无状态，可当零值使用）。
type failoverRequestBodyToolDetector struct{}

var _ SideEffectingToolDetector = failoverRequestBodyToolDetector{}

// DetectSideEffectingTools 收集请求体里出现的工具名，再按「显式名单 + 内置写动词」
// 判定哪些有副作用。
func (d failoverRequestBodyToolDetector) DetectSideEffectingTools(
	body []byte,
	blockedTools []string,
) ([]string, error) {
	names, err := collectFailoverRequestToolNames(body)
	if err != nil {
		return nil, err
	}
	return matchFailoverSideEffectingTools(names, blockedTools), nil
}

// collectFailoverRequestToolNames 从请求体里收集工具名（去重、保持出现顺序）。
//
// 只做**只读解析**：不读 ctx/DB/Redis，所以可以穷举单测。覆盖四类报文：
//   - Anthropic Messages：`tools[].name`、content block `{"type":"tool_use","name":...}`
//   - OpenAI Chat Completions：`tools[].function.name`、`tool_calls[].function.name`
//   - OpenAI Responses：`tools[].name`、`{"type":"function_call","name":...}`
//   - Gemini：`tools[].functionDeclarations[].name`、`{"functionCall":{"name":...}}`
//
// 空 body（例如 GET /v1/models）返回空列表且不报错。
func collectFailoverRequestToolNames(body []byte) ([]string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse request body for tool names: %w", err)
	}

	names := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	add := func(raw string) {
		name := strings.TrimSpace(raw)
		if name == "" {
			return
		}
		key := normalizeFailoverToolName(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}

	// addDeclared 处理工具声明数组（tools[] / functionDeclarations[]）。
	var addDeclared func(value any)
	addDeclared = func(value any) {
		items, ok := value.([]any)
		if !ok {
			return
		}
		for _, item := range items {
			node, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := node["name"].(string); ok {
				add(name)
			}
			if fn, ok := node["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok {
					add(name)
				}
			}
			// Gemini 把声明再嵌一层：tools[].functionDeclarations[].name
			for _, key := range []string{"functionDeclarations", "function_declarations"} {
				if decls, ok := node[key]; ok {
					addDeclared(decls)
				}
			}
		}
	}

	var walk func(value any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, item := range node {
				walk(item)
			}
		case map[string]any:
			// 工具调用块（Anthropic/OpenAI Responses 用 type 标记）。
			if blockType, ok := node["type"].(string); ok {
				if _, isCall := failoverToolCallBlockTypes[normalizeFailoverToolName(blockType)]; isCall {
					if name, ok := node["name"].(string); ok {
						add(name)
					}
				}
			}
			// OpenAI Chat 的 tool_calls[].function.name 与 tools[].function.name。
			if fn, ok := node["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok {
					add(name)
				}
			}
			// Gemini 的 functionCall / 部分兼容层的 function_call。
			for _, key := range []string{"functionCall", "function_call", "toolCall", "tool_call"} {
				if call, ok := node[key].(map[string]any); ok {
					if name, ok := call["name"].(string); ok {
						add(name)
					}
				}
			}
			// 工具声明数组。
			if tools, ok := node["tools"]; ok {
				addDeclared(tools)
			}
			for _, key := range []string{"functionDeclarations", "function_declarations"} {
				if decls, ok := node[key]; ok {
					addDeclared(decls)
				}
			}
			// 其余子节点按键名排序遍历：Go 的 map 迭代顺序是随机的，排序后同一报文
			// 多次解析的结果稳定（日志明细可对比、测试不 flaky）。
			keys := make([]string, 0, len(node))
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(node[key])
			}
		}
	}
	walk(payload)
	return names, nil
}

// failoverToolCallBlockTypes 是「工具调用块」的 type 取值（小写下划线形式）。
//
// 刻意**不含** `web_search_call` / `file_search_call`：它们只读，不产生副作用，
// 列入会导致只读场景被误判为不可重试。
var failoverToolCallBlockTypes = map[string]struct{}{
	"tool_use":              {},
	"server_tool_use":       {},
	"mcp_tool_use":          {},
	"computer_tool_use":     {},
	"function_call":         {},
	"custom_tool_call":      {},
	"tool_call":             {},
	"computer_call":         {},
	"code_interpreter_call": {},
}

// failoverSideEffectingToolTokens 是内置的「写动词」词表（按工具名 token 匹配）。
//
// 判定规则：工具名规范化后按非字母数字切分成 token，任一 token 命中本表即视为
// **有副作用**。例：`write_file` → {write,file} → 命中 `write`；
// `read_file` → {read,file} → 未命中 → 只读；`str_replace_editor` → {str,replace,editor}。
//
// 为什么用 token 而不是子串：子串匹配会让 `read` 命中 `thread`、`create` 命中
// `created_at` 之类的假阳性；token 边界可解释、可表驱动测试。
//
// 保守取向：本闸门默认关闭，开启后「多拦」的代价是少换号（可用性下降），
// 「漏拦」的代价是副作用重复执行（正确性事故），因此词表偏向宁多不少。
var failoverSideEffectingToolTokens = map[string]struct{}{
	// 文件系统写入
	"write": {}, "edit": {}, "replace": {}, "create": {}, "delete": {}, "remove": {},
	"move": {}, "rename": {}, "mkdir": {}, "touch": {}, "truncate": {}, "chmod": {}, "chown": {},
	// 数据库写入
	"insert": {}, "update": {}, "upsert": {}, "drop": {}, "alter": {},
	// 网络/SDK 写方法
	"post": {}, "put": {}, "patch": {}, "upload": {},
	// 命令执行
	"exec": {}, "execute": {}, "run": {}, "shell": {}, "bash": {}, "powershell": {}, "terminal": {},
	// 版本控制与发布
	"commit": {}, "push": {}, "merge": {}, "rebase": {}, "deploy": {}, "publish": {},
	"release": {}, "install": {}, "uninstall": {}, "apply": {}, "restart": {},
	// 消息/交易
	"send": {}, "submit": {}, "order": {}, "pay": {}, "payment": {}, "refund": {},
	"transfer": {}, "charge": {}, "purchase": {}, "checkout": {},
}

// matchFailoverSideEffectingTools 判定候选工具名中哪些有副作用（去重、保持顺序）。
//
// 规则优先级：
//  1. 运维显式 `blockedTools` 命中 → 有副作用（**最高优先级**，可封禁只读工具，
//     例如 design §3.6.4(b) 把 `web_fetch` 列进示例名单）。
//  2. 工具名 token 命中内置写动词表 → 有副作用。
//  3. 其余（含纯只读、空名、非 ASCII 名）→ 允许换号。
func matchFailoverSideEffectingTools(candidates []string, blockedTools []string) []string {
	if len(candidates) == 0 {
		return nil
	}
	matched := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if failoverToolNameIsSideEffecting(candidate, blockedTools) {
			matched = append(matched, candidate)
		}
	}
	if len(matched) == 0 {
		return nil
	}
	return matched
}

// failoverToolNameIsSideEffecting 判定单个工具名是否有副作用。
func failoverToolNameIsSideEffecting(name string, blockedTools []string) bool {
	normalized := normalizeFailoverToolName(name)
	if normalized == "" {
		return false
	}
	for _, blocked := range blockedTools {
		if failoverToolNameMatchesBlocked(normalized, blocked) {
			return true
		}
	}
	for _, token := range failoverToolNameTokens(normalized) {
		if _, ok := failoverSideEffectingToolTokens[token]; ok {
			return true
		}
	}
	return false
}

// failoverToolNameMatchesBlocked 判断 name 是否命中显式封禁名单。
//
// 除精确匹配外，还支持带命名空间的工具名（MCP / 插件风格）：
//
//	blocked=write_file 命中 mcp__fs__write_file / fs/write_file / vendor::write_file
//	blocked=tool       命中 server/tool（末段匹配）
//
// 这样运维不必为每个 MCP server 前缀各写一条规则。
func failoverToolNameMatchesBlocked(normalizedName string, blocked string) bool {
	target := normalizeFailoverToolName(blocked)
	if target == "" {
		return false
	}
	if normalizedName == target {
		return true
	}
	for _, separator := range []string{"__", "/", "::", ":", "."} {
		if strings.HasSuffix(normalizedName, separator+target) {
			return true
		}
	}
	return false
}

// normalizeFailoverToolName 规范化工具名：去空白、camelCase 拆下划线、转小写。
//
// 例：`Str_Replace_Editor` → `str_replace_editor`；`writeFile` → `write_file`；
// `mcp__fs__Write_File` → `mcp__fs__write_file`。
func normalizeFailoverToolName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	var builder strings.Builder
	builder.Grow(len(trimmed) + 8)
	for index, char := range runes {
		if unicode.IsUpper(char) {
			if index > 0 {
				previous := runes[index-1]
				if !unicode.IsUpper(previous) && previous != '_' && previous != '-' {
					builder.WriteByte('_')
				}
			}
			builder.WriteRune(unicode.ToLower(char))
			continue
		}
		builder.WriteRune(unicode.ToLower(char))
	}
	return builder.String()
}

// failoverToolNameTokens 把规范化后的工具名切成 token（非字母数字均为分隔符）。
func failoverToolNameTokens(normalizedName string) []string {
	return strings.FieldsFunc(normalizedName, func(char rune) bool {
		return !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9')
	})
}
