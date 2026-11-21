package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 本文件锁定 T6.3 的契约（implementation.md §三·T6.3）：
//   - 侧效工具识别：写操作类 → 不可换号；读操作类 → 允许换号
//   - 开关默认关闭：enabled=false 时**完全不解析请求体**，行为等价现状
//   - 解析失败 fail-closed：保守判为有副作用
//   - 显式 blocked_tools 优先级最高（可封禁只读工具，如 design 示例的 web_fetch）
//   - 与分类器解耦：只作为叠加过滤器
//
// 测试文件不加 build tag：-tags=unit 与默认两种模式都会执行本文件。

// countingSideEffectingToolDetector 是计数型识别器：既用来断言「开关关闭时 0 次
// 调用」，也用来模拟解析失败。
type countingSideEffectingToolDetector struct {
	calls   int
	matched []string
	err     error
}

func (d *countingSideEffectingToolDetector) DetectSideEffectingTools(
	_ []byte, _ []string,
) ([]string, error) {
	d.calls++
	return d.matched, d.err
}

// ---------------------------------------------------------------------------
// 1) 开关默认关闭 = 零行为变更
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyGateDisabledDoesNotInspectRequestBody(t *testing.T) {
	detector := &countingSideEffectingToolDetector{matched: []string{"Bash"}}
	gate := NewFailoverIdempotencyGateWithDetector(
		config.FailoverIdempotencyConfig{RequireKeyForSideEffecting: false, BlockedTools: []string{"Bash"}},
		detector,
	)

	require.False(t, gate.Enabled(), "默认配置必须关闭闸门")

	body := []byte(`{"tools":[{"name":"Bash"}],"messages":[{"role":"assistant","content":"hi"}]}`)
	decision, err := gate.Evaluate(http.Header{}, body)

	require.NoError(t, err)
	require.Equal(t, 0, detector.calls, "开关关闭时不得解析请求体（等价现状：识别逻辑 0 次调用）")
	require.True(t, decision.AllowAccountSwitch, "开关关闭时必须允许换号")
	require.False(t, decision.SideEffecting)
	require.False(t, decision.Suppressed())
	require.Equal(t, "", decision.SuppressedReason)
	require.False(t, decision.Enabled)
	require.Empty(t, decision.MatchedTools)
}

func TestFailoverIdempotencyZeroValueGateIsDisabled(t *testing.T) {
	var gate FailoverIdempotencyGate // 零值 = 配置默认形态（关闭）

	require.False(t, gate.Enabled())
	decision, err := gate.Evaluate(http.Header{"Idempotency-Key": {"k"}}, []byte(`{not json`))

	require.NoError(t, err, "关闭时连解析都不做，因此非法 JSON 也不报错")
	require.True(t, decision.AllowAccountSwitch)
	require.False(t, decision.SideEffecting)
	require.Zero(t, decision.MatchedTools)
	require.Empty(t, decision.SuppressedReason)
}

func TestFailoverIdempotencyDefaultGateFromEmptyConfigIsDisabled(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{})
	decision, err := gate.Evaluate(http.Header{}, []byte(`{"tools":[{"name":"Bash"}]}`))

	require.NoError(t, err)
	require.True(t, decision.AllowAccountSwitch)
	require.False(t, decision.Enabled)
}

// ---------------------------------------------------------------------------
// 2) 侧效工具识别（表驱动）
// ---------------------------------------------------------------------------

func TestFailoverIdempotencySideEffectingToolTable(t *testing.T) {
	cases := []struct {
		name          string
		tool          string
		sideEffecting bool
	}{
		// —— 写操作类：必须判为「不可重试」——
		{name: "shell 执行", tool: "Bash", sideEffecting: true},
		{name: "小写 shell", tool: "bash", sideEffecting: true},
		{name: "通用命令执行", tool: "shell_exec", sideEffecting: true},
		{name: "run_command", tool: "run_command", sideEffecting: true},
		{name: "终端", tool: "terminal", sideEffecting: true},
		{name: "文件编辑（design 示例）", tool: "str_replace_editor", sideEffecting: true},
		{name: "写文件", tool: "write_file", sideEffecting: true},
		{name: "camelCase 写文件", tool: "writeFile", sideEffecting: true},
		{name: "大小写混合", tool: "Write_File", sideEffecting: true},
		{name: "删文件", tool: "delete_file", sideEffecting: true},
		{name: "移动文件", tool: "move_file", sideEffecting: true},
		{name: "MCP 命名空间（含写动词）", tool: "mcp__filesystem__write_file", sideEffecting: true},
		{name: "斜杠命名空间（末段写动词）", tool: "fs/delete", sideEffecting: true},
		{name: "发消息", tool: "send_email", sideEffecting: true},
		{name: "发帖", tool: "post_message", sideEffecting: true},
		{name: "下单", tool: "place_order", sideEffecting: true},
		{name: "支付", tool: "create_payment", sideEffecting: true},
		{name: "执行 SQL", tool: "execute_sql", sideEffecting: true},
		{name: "更新记录", tool: "update_record", sideEffecting: true},
		{name: "部署", tool: "kubectl_apply", sideEffecting: true},
		{name: "推送代码", tool: "git_push", sideEffecting: true},
		{name: "HTTP POST", tool: "http_post", sideEffecting: true},
		{name: "上传", tool: "upload_file", sideEffecting: true},

		// —— 读操作类：必须保持「允许重试」——
		{name: "读文件", tool: "read_file", sideEffecting: false},
		{name: "Read", tool: "Read", sideEffecting: false},
		{name: "cat", tool: "cat", sideEffecting: false},
		{name: "ls", tool: "ls", sideEffecting: false},
		{name: "列目录", tool: "list_directory", sideEffecting: false},
		{name: "glob", tool: "glob", sideEffecting: false},
		{name: "grep", tool: "grep", sideEffecting: false},
		{name: "代码搜索", tool: "search_code", sideEffecting: false},
		{name: "取文件信息", tool: "get_file_info", sideEffecting: false},
		{name: "view", tool: "view", sideEffecting: false},
		{name: "diff", tool: "diff", sideEffecting: false},
		{name: "tree", tool: "directory_tree", sideEffecting: false},
		{name: "网页搜索", tool: "web_search", sideEffecting: false},
		{name: "取当前时间", tool: "get_current_time", sideEffecting: false},
		{name: "MCP 只读", tool: "mcp__filesystem__read_file", sideEffecting: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			matched := matchFailoverSideEffectingTools([]string{tc.tool}, nil)
			if tc.sideEffecting {
				require.Equal(t, []string{tc.tool}, matched, "写操作类工具必须被识别为不可重试")
				return
			}
			require.Empty(t, matched, "读操作类工具必须允许重试")
		})
	}
}

func TestFailoverIdempotencyNameNormalization(t *testing.T) {
	cases := []struct {
		raw        string
		wantTokens []string
	}{
		{raw: "Bash", wantTokens: []string{"bash"}},
		{raw: "writeFile", wantTokens: []string{"write", "file"}},
		{raw: "Str_Replace_Editor", wantTokens: []string{"str", "replace", "editor"}},
		{raw: "mcp__fs__Write_File", wantTokens: []string{"mcp", "fs", "write", "file"}},
		{raw: "  read_file  ", wantTokens: []string{"read", "file"}},
	}
	for _, tc := range cases {
		require.Equal(t, tc.wantTokens, failoverToolNameTokens(normalizeFailoverToolName(tc.raw)),
			"工具名 %q 的 token 切分不符合预期", tc.raw)
	}

	// 空名/纯空白：规范化后为空，不产生任何 token（因此不会被判为侧效）。
	require.Equal(t, "", normalizeFailoverToolName("   "))
	require.Empty(t, failoverToolNameTokens(normalizeFailoverToolName("")))
	require.False(t, failoverToolNameIsSideEffecting("", nil))
}

// ---------------------------------------------------------------------------
// 3) 闸门判定：命中 + 无 key → 禁止换号
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyGateSuppressesSwitchWithoutKey(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
	})
	require.True(t, gate.Enabled())

	body := []byte(`{"tools":[{"name":"Bash"},{"name":"read_file"}],
		"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"write_file","input":{}}]}]}`)
	decision, err := gate.Evaluate(http.Header{}, body)

	require.NoError(t, err)
	require.True(t, decision.SideEffecting)
	require.Equal(t, []string{"Bash", "write_file"}, decision.MatchedTools,
		"命中明细必须去重且保持出现顺序（read_file 是只读，不该入选）")
	require.False(t, decision.AllowAccountSwitch, "命中侧效工具且无幂等键时必须禁止跨账号换号")
	require.True(t, decision.Suppressed())
	require.Equal(t, FailoverIdempotencySuppressedReasonSideEffectNoKey, decision.SuppressedReason)
	require.False(t, decision.ParseFailed)
	require.False(t, decision.HasIdempotencyKey)
}

func TestFailoverIdempotencyGateAllowsSwitchWithKey(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
	})

	body := []byte(`{"tools":[{"name":"place_order"}]}`)
	decision, err := gate.Evaluate(http.Header{"Idempotency-Key": {"order-42"}}, body)

	require.NoError(t, err)
	require.True(t, decision.SideEffecting, "仍然要识别出侧效工具（用于观测）")
	require.True(t, decision.HasIdempotencyKey)
	require.True(t, decision.AllowAccountSwitch, "带幂等键 = 客户声明重试安全 → 允许换号")
	require.False(t, decision.Suppressed())
}

func TestFailoverIdempotencyGateAllowsReadOnlyTools(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
	})

	body := []byte(`{"tools":[{"name":"read_file"},{"name":"grep"}],
		"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"list_directory"}]}]}`)
	decision, err := gate.Evaluate(http.Header{}, body)

	require.NoError(t, err)
	require.False(t, decision.SideEffecting, "只有只读工具时不应判定为有副作用")
	require.True(t, decision.AllowAccountSwitch)
	require.False(t, decision.Suppressed())
	require.Empty(t, decision.MatchedTools)
}

func TestFailoverIdempotencyGateIgnoresBodyWithoutTools(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
	})

	decision, err := gate.Evaluate(http.Header{}, []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.False(t, decision.SideEffecting)
	require.True(t, decision.AllowAccountSwitch)

	empty, err := gate.Evaluate(http.Header{}, nil)
	require.NoError(t, err)
	require.True(t, empty.AllowAccountSwitch, "空 body（如 GET /v1/models）必须允许换号")
}

// ---------------------------------------------------------------------------
// 4) 显式封禁名单优先级最高
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyExplicitBlockedToolsTakePrecedence(t *testing.T) {
	// design §3.6.4(b) 的示例名单含 web_fetch：内置规则认为它只读，但运维显式
	// 封禁时必须以运维为准。
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
		BlockedTools:               []string{"web_fetch", "read_file"},
	})

	body := []byte(`{"tools":[{"name":"web_fetch"},{"name":"read_file"},{"name":"grep"}]}`)
	decision, err := gate.Evaluate(http.Header{}, body)

	require.NoError(t, err)
	require.True(t, decision.SideEffecting)
	require.Equal(t, []string{"web_fetch", "read_file"}, decision.MatchedTools)
	require.False(t, decision.AllowAccountSwitch)

	// 只读工具没有被显式封禁时仍然允许。
	allowed, err := gate.Evaluate(http.Header{}, []byte(`{"tools":[{"name":"grep"}]}`))
	require.NoError(t, err)
	require.True(t, allowed.AllowAccountSwitch)
}

func TestFailoverIdempotencyBlockedToolNamespacedMatch(t *testing.T) {
	cases := []struct {
		name    string
		blocked string
		tool    string
		want    bool
	}{
		{name: "精确", blocked: "write_file", tool: "write_file", want: true},
		{name: "双下划线命名空间", blocked: "write_file", tool: "mcp__fs__write_file", want: true},
		{name: "斜杠命名空间", blocked: "write_file", tool: "fs/write_file", want: true},
		{name: "双冒号命名空间", blocked: "write_file", tool: "vendor::write_file", want: true},
		{name: "末段匹配", blocked: "tool", tool: "server/tool", want: true},
		{name: "前缀不误命中", blocked: "tool", tool: "toolbox_server", want: false},
		{name: "大小写不敏感", blocked: "Web_Fetch", tool: "WEB_FETCH", want: true},
		{name: "空名单项", blocked: "  ", tool: "anything", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, failoverToolNameMatchesBlocked(
				normalizeFailoverToolName(tc.tool), tc.blocked))
		})
	}
}

// ---------------------------------------------------------------------------
// 5) 解析失败 fail-closed
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyParseFailureIsFailClosed(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
	})

	decision, err := gate.Evaluate(http.Header{}, []byte(`{"tools":[{"name":`))

	require.Error(t, err, "解析失败必须把错误交给调用方记 warn")
	require.True(t, decision.SideEffecting, "fail-closed：解析失败保守判为有副作用")
	require.True(t, decision.ParseFailed)
	require.False(t, decision.AllowAccountSwitch)
	require.Equal(t, FailoverIdempotencySuppressedReasonSideEffectNoKey, decision.SuppressedReason)
}

func TestFailoverIdempotencyParseFailureWithKeyStillAllowsSwitch(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
	})

	decision, err := gate.Evaluate(http.Header{"Idempotency-Key": {"k-1"}}, []byte(`{`))

	require.Error(t, err)
	require.True(t, decision.ParseFailed)
	require.True(t, decision.AllowAccountSwitch, "客户已声明重试安全 → 解析失败也不阻断")
	require.False(t, decision.Suppressed())
}

func TestFailoverIdempotencyDetectorErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	detector := &countingSideEffectingToolDetector{err: sentinel}
	gate := NewFailoverIdempotencyGateWithDetector(
		config.FailoverIdempotencyConfig{RequireKeyForSideEffecting: true}, detector)

	decision, err := gate.Evaluate(http.Header{}, []byte(`{}`))

	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 1, detector.calls)
	require.True(t, decision.SideEffecting)
	require.False(t, decision.AllowAccountSwitch)
}

// ---------------------------------------------------------------------------
// 6) 四种报文形态都能识别
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyDetectsAllProviderShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "Anthropic Messages",
			body: `{"model":"claude","tools":[{"name":"Bash","description":"run shell"},{"name":"read_file"}],
				"messages":[{"role":"assistant","content":[
					{"type":"text","text":"ok"},
					{"type":"tool_use","id":"t1","name":"write_file","input":{}}]}]}`,
			want: []string{"Bash", "write_file"},
		},
		{
			name: "OpenAI Chat Completions",
			body: `{"model":"gpt","tools":[{"type":"function","function":{"name":"send_email","parameters":{}}}],
				"messages":[{"role":"assistant","tool_calls":[
					{"id":"c1","type":"function","function":{"name":"delete_file","arguments":"{}"}}]}]}`,
			want: []string{"send_email", "delete_file"},
		},
		{
			name: "OpenAI Responses",
			body: `{"model":"gpt-5","tools":[{"type":"function","name":"place_order"}],
				"input":[{"type":"function_call","name":"post_message","call_id":"c1"}]}`,
			want: []string{"place_order", "post_message"},
		},
		{
			name: "Gemini",
			body: `{"tools":[{"functionDeclarations":[{"name":"create_file","parameters":{}}]}],
				"contents":[{"parts":[{"functionCall":{"name":"delete_file","args":{}}}]}]}`,
			want: []string{"create_file", "delete_file"},
		},
		{
			name: "只读工具的四种形态都不命中",
			body: `{"tools":[{"name":"read_file"},{"type":"function","function":{"name":"grep"}},
					{"functionDeclarations":[{"name":"list_directory"}]}],
				"input":[{"type":"function_call","name":"search_code"}]}`,
			want: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			names, err := collectFailoverRequestToolNames([]byte(tc.body))
			require.NoError(t, err)
			matched := matchFailoverSideEffectingTools(names, nil)
			require.Equal(t, tc.want, matched)
		})
	}
}

// ---------------------------------------------------------------------------
// 7) Idempotency-Key 判定
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyHasIdempotencyKey(t *testing.T) {
	cases := []struct {
		name    string
		headers http.Header
		want    bool
	}{
		{name: "nil header", headers: nil, want: false},
		{name: "空 header", headers: http.Header{}, want: false},
		{name: "标准形式", headers: http.Header{"Idempotency-Key": {"abc"}}, want: true},
		{name: "小写头名", headers: http.Header{"idempotency-key": {"abc"}}, want: true},
		{name: "空值", headers: http.Header{"Idempotency-Key": {""}}, want: false},
		{name: "纯空白", headers: http.Header{"Idempotency-Key": {"   "}}, want: false},
		{name: "多值之一非空", headers: http.Header{"Idempotency-Key": {"", "k"}}, want: true},
		{name: "无关头", headers: http.Header{"X-Trace-Id": {"k"}}, want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, HasIdempotencyKey(tc.headers))
		})
	}
}

// ---------------------------------------------------------------------------
// 8) 从生效策略构造 / 叠加过滤器语义
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyGateFromPolicy(t *testing.T) {
	// 指针全 nil（未配置）→ 关闭。
	disabled := NewFailoverIdempotencyGateFromPolicy(FailoverPolicy{})
	require.False(t, disabled.Enabled())

	enabled := NewFailoverIdempotencyGateFromPolicy(FailoverPolicy{
		IdempotencyRequireKeyForSideEffecting: fpBoolPtr(true),
		IdempotencyBlockedTools:               &[]string{"web_fetch"},
	})
	require.True(t, enabled.Enabled())

	decision, err := enabled.Evaluate(http.Header{}, []byte(`{"tools":[{"name":"web_fetch"}]}`))
	require.NoError(t, err)
	require.False(t, decision.AllowAccountSwitch, "策略里的 blocked_tools 必须生效")
}

func TestFailoverIdempotencyApplyToSwitchDecisionIsIntersection(t *testing.T) {
	gate := NewFailoverIdempotencyGate(config.FailoverIdempotencyConfig{
		RequireKeyForSideEffecting: true,
	})
	blocked, err := gate.Evaluate(http.Header{}, []byte(`{"tools":[{"name":"Bash"}]}`))
	require.NoError(t, err)

	// 闸门只能收窄：分类器允许 + 闸门禁止 → 禁止。
	require.False(t, blocked.ApplyToSwitchDecision(true))
	// 分类器禁止时闸门无法放开。
	require.False(t, blocked.ApplyToSwitchDecision(false))

	allowed, err := gate.Evaluate(http.Header{"Idempotency-Key": {"k"}}, []byte(`{"tools":[{"name":"Bash"}]}`))
	require.NoError(t, err)
	require.True(t, allowed.ApplyToSwitchDecision(true))
	require.False(t, allowed.ApplyToSwitchDecision(false))
}

func TestFailoverIdempotencyHasSideEffectingToolHelper(t *testing.T) {
	require.True(t, HasSideEffectingTool([]byte(`{"tools":[{"name":"Bash"}]}`), nil))
	require.False(t, HasSideEffectingTool([]byte(`{"tools":[{"name":"read_file"}]}`), nil))
	require.True(t, HasSideEffectingTool([]byte(`{"tools":[{"name":"read_file"}]}`), []string{"read_file"}))
	require.False(t, HasSideEffectingTool(nil, nil))
	require.True(t, HasSideEffectingTool([]byte(`{`), nil), "解析失败按 fail-closed 返回 true")
}

// ---------------------------------------------------------------------------
// 9) 可注入性：自定义识别器 + nil 回落默认实现
// ---------------------------------------------------------------------------

func TestFailoverIdempotencyInjectedDetectorDrivesDecision(t *testing.T) {
	detector := &countingSideEffectingToolDetector{matched: []string{"custom_tool"}}
	gate := NewFailoverIdempotencyGateWithDetector(
		config.FailoverIdempotencyConfig{RequireKeyForSideEffecting: true}, detector)

	decision, err := gate.Evaluate(http.Header{}, []byte(`{"tools":[{"name":"read_file"}]}`))

	require.NoError(t, err)
	require.Equal(t, 1, detector.calls)
	require.Equal(t, []string{"custom_tool"}, decision.MatchedTools, "识别结果由注入的实现决定（与分类器解耦）")
	require.False(t, decision.AllowAccountSwitch)
}

func TestFailoverIdempotencyNilDetectorFallsBackToBuiltin(t *testing.T) {
	gate := NewFailoverIdempotencyGateWithDetector(
		config.FailoverIdempotencyConfig{RequireKeyForSideEffecting: true}, nil)

	decision, err := gate.Evaluate(http.Header{}, []byte(`{"tools":[{"name":"Bash"}]}`))
	require.NoError(t, err)
	require.True(t, decision.SideEffecting, "nil 识别器必须回落到内置实现而不是 panic")
	require.False(t, decision.AllowAccountSwitch)
}
