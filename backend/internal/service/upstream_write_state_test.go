package service

// T2.1 的判据单测：语义字节 vs 心跳字节（修 R-01），以及每请求独立计数（C-01）。
//
// 无 build tag：`go test ./internal/service/...` 与 `-tags=unit` 两套都要跑。

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newWriteStateGin(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	return c
}

// TestClassifyUpstreamWriteChunk 锁定「什么算语义字节」（design §3.6.2）。
func TestClassifyUpstreamWriteChunk(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    UpstreamWriteKind
	}{
		{"空内容", "", UpstreamWriteNone},
		{"纯空行", "\n\n\n", UpstreamWriteNone},
		{"SSE 注释帧", ": keepalive\n\n", UpstreamWriteHeartbeat},
		{"retry 行", "retry: 3000\n\n", UpstreamWriteHeartbeat},
		{"标准 keepalive（event+data）", "event: ping\ndata: {\"type\": \"ping\"}\n\n", UpstreamWriteHeartbeat},
		{"keepalive 事件名", "event: keepalive\ndata: {}\n\n", UpstreamWriteHeartbeat},
		{"message_start 事件", "event: message_start\ndata: {\"type\":\"message_start\"}\n\n", UpstreamWriteSemantic},
		{"裸 data 行", "data: {\"delta\":\"hi\"}\n\n", UpstreamWriteSemantic},
		{"纯文本", "hello", UpstreamWriteSemantic},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ClassifyUpstreamWriteChunk([]byte(tc.payload)))
		})
	}
}

// TestUpstreamWriteState_R01_PingDoesNotBlockFailover 是 R-01 的回归锁定：
// 排队期写 ping 之后，上游断流仍然必须允许换号；写过语义字节之后必须禁止。
func TestUpstreamWriteState_R01_PingDoesNotBlockFailover(t *testing.T) {
	c := newWriteStateGin(t)
	state := UpstreamWriteStateFor(c)
	attempt := state.BeginAttempt(c, true)

	// 1) 排队期 ping：客户端只收到心跳，换号仍然安全。
	keepalive := "event: ping\ndata: {\"type\": \"ping\"}\n\n"
	_, err := c.Writer.WriteString(keepalive)
	require.NoError(t, err)
	state.WriteHeartbeatBytes(len(keepalive))

	require.Truef(t, attempt.CanFailover(false),
		"R-01：写 ping 之后仍必须允许换号（改造前 c.Writer.Written() 会永久禁止）；诊断：semanticDelta=%d heartbeatDelta=%d bytesDelta=%d size=%d",
		attempt.SemanticDelta(), attempt.HeartbeatDelta(), attempt.BytesDelta(), c.Writer.Size())
	require.Zero(t, attempt.SemanticDelta())
	require.Equal(t, int64(len(keepalive)), attempt.HeartbeatDelta())

	// 2) 写出语义字节：SSE 无法回滚，必须禁止换号。
	semantic := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	_, err = c.Writer.WriteString(semantic)
	require.NoError(t, err)
	state.WriteSemanticBytes(len(semantic))

	require.False(t, attempt.CanFailover(false), "已写语义字节后禁止换号（R-11：不做语义字节后的重放）")
	require.Equal(t, FailoverSuppressedSemanticBytes, attempt.BlockReason())
}

// TestUpstreamWriteState_UnclassifiedBytesCountAsSemantic 锁定保守方向：
// 没有显式记成心跳的写入，一律算语义字节（宁可少换号，不可拼接腐化）。
func TestUpstreamWriteState_UnclassifiedBytesCountAsSemantic(t *testing.T) {
	c := newWriteStateGin(t)
	state := UpstreamWriteStateFor(c)
	attempt := state.BeginAttempt(c, true)

	// 直接写，不调用任何记账函数（模拟「未来新增、尚未插桩」的写入点）。
	_, err := c.Writer.WriteString(`{"error":"boom"}`)
	require.NoError(t, err)

	require.False(t, attempt.CanFailover(false), "未分类写入必须保守算语义字节")
	require.Positive(t, attempt.SemanticDelta())
}

// TestUpstreamWriteState_LegacyMode 锁定回滚开关：use_semantic_bytes=false 时回到
// 改造前语义（连 ping 都阻止换号）。
func TestUpstreamWriteState_LegacyMode(t *testing.T) {
	c := newWriteStateGin(t)
	state := UpstreamWriteStateFor(c)
	attempt := state.BeginAttempt(c, false)

	keepalive := "event: ping\ndata: {\"type\": \"ping\"}\n\n"
	_, err := c.Writer.WriteString(keepalive)
	require.NoError(t, err)
	state.WriteHeartbeatBytes(len(keepalive))

	require.False(t, attempt.CanFailover(false),
		"回滚模式必须等价改造前：任何字节（含 ping）都不许换号")
}

// TestUpstreamWriteState_SafeToFailoverAfterWrite 锁定显式白名单。
func TestUpstreamWriteState_SafeToFailoverAfterWrite(t *testing.T) {
	c := newWriteStateGin(t)
	state := UpstreamWriteStateFor(c)
	attempt := state.BeginAttempt(c, true)

	_, err := c.Writer.WriteString("event: message_start\ndata: {}\n\n")
	require.NoError(t, err)
	state.WriteSemanticBytes(10)

	require.False(t, attempt.CanFailover(false))
	require.True(t, attempt.CanFailover(true), "SafeToFailoverAfterWrite 是显式白名单")
}

// TestUpstreamWriteState_C01_PerRequestIsolation 是 C-01：
// 并发请求各自计数互不污染（禁止包级 atomic）。
func TestUpstreamWriteState_C01_PerRequestIsolation(t *testing.T) {
	const requests = 100
	states := make([]*UpstreamWriteState, requests)
	for i := range states {
		states[i] = NewUpstreamWriteState()
	}

	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				states[idx].WriteSemanticBytes(idx + 1)
				states[idx].WriteHeartbeatBytes(1)
			}
		}(i)
	}
	wg.Wait()

	for i, state := range states {
		require.Equal(t, int64(10*(i+1)), state.SemanticBytes(), "第 %d 个请求的语义计数被污染", i)
		require.Equal(t, int64(10), state.HeartbeatBytes(), "第 %d 个请求的心跳计数被污染", i)
	}
}

// TestUpstreamWriteStateFor_SharesPerRequestState 锁定「同一 gin.Context 拿到同一对象」。
func TestUpstreamWriteStateFor_SharesPerRequestState(t *testing.T) {
	c := newWriteStateGin(t)
	first := UpstreamWriteStateFor(c)
	first.WriteSemanticBytes(7)
	second := UpstreamWriteStateFor(c)
	require.Same(t, first, second)
	require.Equal(t, int64(7), second.SemanticBytes())

	other := newWriteStateGin(t)
	require.NotSame(t, first, UpstreamWriteStateFor(other))
	require.Zero(t, UpstreamWriteStateFor(other).SemanticBytes())
}

// TestUpstreamWriteStateFor_NilContextSafe 锁定无请求上下文时不退化成全局单例。
func TestUpstreamWriteStateFor_NilContextSafe(t *testing.T) {
	a := UpstreamWriteStateFor(nil)
	b := UpstreamWriteStateFor(nil)
	require.NotSame(t, a, b)
	a.WriteSemanticBytes(1)
	require.Zero(t, b.SemanticBytes())
}

// TestFailoverBlockedForAttempt 锁定 handler 侧的统一入口。
func TestFailoverBlockedForAttempt(t *testing.T) {
	c := newWriteStateGin(t)
	attempt := BeginUpstreamWriteAttempt(c, true)

	blocked, _ := FailoverBlockedForAttempt(attempt)
	require.False(t, blocked, "什么都没写 → 可换号")

	keepalive := "event: ping\ndata: {\"type\": \"ping\"}\n\n"
	_, err := c.Writer.WriteString(keepalive)
	require.NoError(t, err)
	UpstreamWriteStateFor(c).WriteHeartbeatBytes(len(keepalive))

	blocked, _ = FailoverBlockedForAttempt(attempt)
	require.False(t, blocked, "R-01：只有 ping → 仍可换号")

	_, err = c.Writer.WriteString("data: {\"delta\":\"hi\"}\n\n")
	require.NoError(t, err)
	blocked, reason := FailoverBlockedForAttempt(attempt)
	require.True(t, blocked, "写出语义字节后禁止换号")
	require.Equal(t, FailoverSuppressedSemanticBytes, reason)

	// 旧判据（use_semantic_bytes=false）下 ping 就算「已写内容」。
	legacyCtx := newWriteStateGin(t)
	legacyAttempt := BeginUpstreamWriteAttempt(legacyCtx, false)
	_, err = legacyCtx.Writer.WriteString(keepalive)
	require.NoError(t, err)
	UpstreamWriteStateFor(legacyCtx).WriteHeartbeatBytes(len(keepalive))
	blocked, _ = FailoverBlockedForAttempt(legacyAttempt)
	require.True(t, blocked, "回滚模式：ping 也算已写内容")
}

// TestUseSemanticBytesFor 锁定配置读取（nil 安全 + B1 默认 true）。
func TestUseSemanticBytesFor(t *testing.T) {
	require.True(t, UseSemanticBytesFor(nil), "无配置时按 B1 默认（语义字节判据）")

	disabled := false
	cfg := &config.Config{}
	cfg.Gateway.FailoverPolicy.Switchable.UseSemanticBytes = disabled
	require.False(t, UseSemanticBytesFor(cfg))

	enabled := true
	cfg.Gateway.FailoverPolicy.Switchable.UseSemanticBytes = enabled
	require.True(t, UseSemanticBytesFor(cfg))
}

// TestUpstreamWriteState_Snapshot 锁定快照字段（观测/日志用）。
func TestUpstreamWriteState_Snapshot(t *testing.T) {
	state := NewUpstreamWriteState()
	state.WriteSemanticBytes(11)
	state.WriteHeartbeatBytes(3)
	state.MarkStatusCommitted()

	snapshot := state.Snapshot()
	require.Equal(t, int64(11), snapshot.SemanticBytes)
	require.Equal(t, int64(3), snapshot.HeartbeatBytes)
	require.True(t, snapshot.StatusCommitted)
	require.Equal(t, fmt.Sprintf("%+v", snapshot), fmt.Sprintf("%+v", state.Snapshot()))
}
