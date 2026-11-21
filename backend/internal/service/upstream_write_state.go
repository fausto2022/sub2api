package service

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

// 本文件是 P2/T2.1 的写状态判据：全链只用一个「已写字节」定义（修 R-01 / R-02）。
//
// # 为什么需要它
//
// 改造前「能不能换号」有两套互相矛盾的判据：
//
//	service  gateway_upstream_response.go:1066   if !c.Writer.Written()   // 任何字节（含 ping）
//	handler  gateway_handler.go:500/523/919/1061 c.Writer.Size() 增量     // 任何字节（含 ping）
//
// 而排队期（等上游首字节）为了防代理空闲断连，会周期性向下游写 `event: ping`。ping 是
// **无意义字节**（客户端拿不到任何语义），但它一旦写出：
//   - service 的 `!Written()` 永久为假 → 上游随后断流时不再换号（R-01）
//   - handler 的 `Size()` 增量非零 → 放弃换号，直接向客户端硬失败
//
// 于是「ping 之后断流」这一条常见路径退化为不可换号。本文件把判据改为
// **语义字节（semantic bytes）**：只有客户端能观察到语义的字节才阻止换号，ping/注释帧/
// 空行等心跳字节不阻止。
//
// # 判据（唯一入口，禁止旁路）
//
//	CanFailover(safeToFailoverAfterWrite) =
//	     safeToFailoverAfterWrite                                // 显式白名单
//	  || (语义字节数 == 0 && 未写状态行)                          // 语义模式（B1，默认）
//	  || (字节数 == 0 && 未写状态行)                              // 旧模式（use_semantic_bytes=false）
//
// 关键实现取巧（也是唯一安全的做法）：**未分类的字节一律算语义字节**。
//
//	semanticDelta = (writer.size 增量) - (heartbeat 累计增量)
//
// 这样只有「显式标记为心跳」的写入会被豁免，其余任何写入（含未来新增的、我们没有
// 逐一插桩的路径）都保守地算语义字节。反过来做（白名单语义字节）会漏插桩 → 悄悄
// 允许换号 → 客户端收到拼接腐化的流，代价远大于保守。
//
// # 每请求实例（C-01）
//
// 状态对象每请求一个（挂在 gin.Context 上），**不得**用包级 atomic：包级计数会让并发
// 请求互相污染（A 请求的心跳替 B 请求的语义字节背书）。
//
// # 边界（R-11，本任务不做）
//
// 本文件只回答「还能不能换号」。**不做**「已写语义字节后重放」——那会让同一次请求产生
// 第二条账单，以 ErrUsageBillingRequestConflict 失败（usage_billing_repo.go:86-88 +
// gateway_handler.go:944），属 P6/T6.2 的范围。

// upstreamWriteStateKey 是 gin.Context 里写状态对象的键。
const upstreamWriteStateKey = "service.upstream_write_state"

// UpstreamWriteKind 是一段待写出字节的语义分类（design §3.6.2）。
type UpstreamWriteKind string

const (
	// UpstreamWriteNone 空写入：不需要计任何账。
	UpstreamWriteNone UpstreamWriteKind = "none"
	// UpstreamWriteHeartbeat 心跳字节：SSE 注释帧、`event: ping`/`keepalive`、`retry:`、
	// 以及组合出它们的空行。客户端拿不到语义，**不阻止换号**。
	UpstreamWriteHeartbeat UpstreamWriteKind = "heartbeat"
	// UpstreamWriteSemantic 语义字节：任何 `data:` 行、非 ping 事件、非流式 JSON body。
	// 写出后**禁止换号**（SSE 无断点续传，拼接会腐化客户端视图）。
	UpstreamWriteSemantic UpstreamWriteKind = "semantic"
)

// ClassifyUpstreamWriteChunk 把一段待写出的字节归类成 UpstreamWriteKind（纯函数）。
//
// 规则（design §3.6.2「什么算语义字节」）：
//   - 空内容 / 纯空白 / 纯 SSE 空行 → none
//   - 只含 `:` 注释帧、`event: ping|keepalive`、`retry:` 行 → heartbeat
//   - 其它一切（含 `data:` 行、`event: message_start` 等）→ semantic
//
// 保守方向是故意的：认不出来的内容算 semantic。
func ClassifyUpstreamWriteChunk(payload []byte) UpstreamWriteKind {
	if len(payload) == 0 {
		return UpstreamWriteNone
	}
	// 先看 event 名：`event: ping` 帧的 data 行也属于心跳（典型 keepalive 块是
	// `event: ping` + `data: {"type":"ping"}` 两行），不能因为看到 data: 就算语义。
	for _, rawLine := range bytes.Split(payload, []byte("\n")) {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "event:") {
			switch strings.TrimSpace(strings.TrimPrefix(lower, "event:")) {
			case "ping", "keepalive", "heartbeat":
				return UpstreamWriteHeartbeat
			}
		}
	}

	sawMeaningfulLine := false
	for _, rawLine := range bytes.Split(payload, []byte("\n")) {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}
		sawMeaningfulLine = true
		if strings.HasPrefix(line, ":") { // SSE 注释帧（含 ": ping"）
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "retry:") { // 重连间隔，无业务语义
			continue
		}
		if strings.HasPrefix(lower, "event:") {
			name := strings.TrimSpace(strings.TrimPrefix(lower, "event:"))
			if name == "ping" || name == "keepalive" || name == "heartbeat" {
				continue
			}
			return UpstreamWriteSemantic
		}
		if strings.HasPrefix(lower, "data:") {
			// `data:` 是语义通道；哪怕是空 data 行，也保守算语义（客户端会收到事件）。
			return UpstreamWriteSemantic
		}
		return UpstreamWriteSemantic
	}
	if sawMeaningfulLine {
		return UpstreamWriteHeartbeat
	}
	return UpstreamWriteNone
}

// UpstreamWriteState 是**每请求一个**的写状态（C-01：禁止包级计数）。
type UpstreamWriteState struct {
	semanticBytes  atomic.Int64
	heartbeatBytes atomic.Int64
	// statusCommitted 记录「已经给客户端定过状态行」，仅供观测与日志。
	statusCommitted atomic.Bool
}

// NewUpstreamWriteState 创建一个空状态（测试与无 gin.Context 的调用点用）。
func NewUpstreamWriteState() *UpstreamWriteState { return &UpstreamWriteState{} }

// UpstreamWriteStateFor 返回当前请求的写状态对象，必要时惰性创建。
//
// c == nil 时返回一个临时对象（不共享）：调用方拿不到请求上下文说明它也不在
// 真实请求链路上，此时**不能**退化成包级单例。
func UpstreamWriteStateFor(c *gin.Context) *UpstreamWriteState {
	if c == nil {
		return NewUpstreamWriteState()
	}
	if v, ok := c.Get(upstreamWriteStateKey); ok {
		if state, ok := v.(*UpstreamWriteState); ok && state != nil {
			return state
		}
	}
	state := NewUpstreamWriteState()
	c.Set(upstreamWriteStateKey, state)
	return state
}

// WriteSemanticBytes 记入 n 个语义字节。
func (s *UpstreamWriteState) WriteSemanticBytes(n int) {
	if s == nil || n <= 0 {
		return
	}
	s.semanticBytes.Add(int64(n))
}

// WriteHeartbeatBytes 记入 n 个心跳字节（ping / 注释帧 / 空行）。
func (s *UpstreamWriteState) WriteHeartbeatBytes(n int) {
	if s == nil || n <= 0 {
		return
	}
	s.heartbeatBytes.Add(int64(n))
}

// ObserveChunk 按分类结果记账，返回分类（便于调用方日志）。
func (s *UpstreamWriteState) ObserveChunk(payload []byte) UpstreamWriteKind {
	kind := ClassifyUpstreamWriteChunk(payload)
	if s == nil {
		return kind
	}
	switch kind {
	case UpstreamWriteSemantic:
		s.WriteSemanticBytes(len(payload))
	case UpstreamWriteHeartbeat:
		s.WriteHeartbeatBytes(len(payload))
	}
	return kind
}

// MarkStatusCommitted 记录「状态行已写给客户端」。
func (s *UpstreamWriteState) MarkStatusCommitted() {
	if s == nil {
		return
	}
	s.statusCommitted.Store(true)
}

// SemanticBytes 返回累计语义字节数。
func (s *UpstreamWriteState) SemanticBytes() int64 {
	if s == nil {
		return 0
	}
	return s.semanticBytes.Load()
}

// HeartbeatBytes 返回累计心跳字节数。
func (s *UpstreamWriteState) HeartbeatBytes() int64 {
	if s == nil {
		return 0
	}
	return s.heartbeatBytes.Load()
}

// StatusCommitted 报告状态行是否已写出（观测用；不参与换号判据）。
func (s *UpstreamWriteState) StatusCommitted() bool {
	if s == nil {
		return false
	}
	return s.statusCommitted.Load()
}

// UpstreamWriteSnapshot 是某一时刻的只读快照（日志/测试/排障用）。
type UpstreamWriteSnapshot struct {
	SemanticBytes   int64 `json:"semantic_bytes"`
	HeartbeatBytes  int64 `json:"heartbeat_bytes"`
	StatusCommitted bool  `json:"status_committed"`
}

// Snapshot 返回当前快照。
func (s *UpstreamWriteState) Snapshot() UpstreamWriteSnapshot {
	if s == nil {
		return UpstreamWriteSnapshot{}
	}
	return UpstreamWriteSnapshot{
		SemanticBytes:   s.SemanticBytes(),
		HeartbeatBytes:  s.HeartbeatBytes(),
		StatusCommitted: s.StatusCommitted(),
	}
}

// UpstreamWriteAttempt 是一次「上游尝试」的写基线：从这次尝试开始到现在，客户端
// 到底拿到了多少**不可回滚**的内容。
//
// 每次发起上游尝试前用 BeginAttempt 取基线；错误发生时用 CanFailover 判定。
// 基线必须每尝试一次，否则同账号重试会继承上一次尝试的字节数（R-18 的反面）。
type UpstreamWriteAttempt struct {
	state *UpstreamWriteState
	// writer 用于读「当前真实字节数」。**只做基数校正**，不单独作为判据：
	// 未分类字节一律算语义字节（见文件头注释）。
	writer gin.ResponseWriter

	semanticBefore  int64
	heartbeatBefore int64
	sizeBefore      int
	writtenBefore   bool
	useSemantic     bool
}

// BeginAttempt 采集一次尝试的写基线。
//
// c 可以为 nil（无响应上下文的路径），此时只依赖心跳记账；判定会退化为
// 「心跳之外的任何已知写入算语义」。useSemanticBytes=false 时回退旧判据
// （任何字节都阻止换号，含 ping），用于 design §3.6.6 的回滚开关。
func (s *UpstreamWriteState) BeginAttempt(c *gin.Context, useSemanticBytes bool) UpstreamWriteAttempt {
	attempt := UpstreamWriteAttempt{state: s, useSemantic: useSemanticBytes}
	if s != nil {
		attempt.semanticBefore = s.SemanticBytes()
		attempt.heartbeatBefore = s.HeartbeatBytes()
	}
	if c != nil && c.Writer != nil {
		attempt.writer = c.Writer
		attempt.sizeBefore = c.Writer.Size()
		// gin 的 responseWriter 在首次写入前 Size() 返回 -1（内部 noWritten 哨兵）。
		// 不归零会让「写出量」多算 1 字节，足以把纯心跳误判成语义字节（R-01 回归）。
		if attempt.sizeBefore < 0 {
			attempt.sizeBefore = 0
		}
		attempt.writtenBefore = c.Writer.Written()
	}
	return attempt
}

// BeginUpstreamWriteAttempt 是给 handler 用的便捷入口：直接按 gin.Context 取状态并采集基线。
func BeginUpstreamWriteAttempt(c *gin.Context, useSemanticBytes bool) UpstreamWriteAttempt {
	return UpstreamWriteStateFor(c).BeginAttempt(c, useSemanticBytes)
}

// SemanticDelta 返回本次尝试期间写出的语义字节数（未分类字节一律计入语义）。
func (a UpstreamWriteAttempt) SemanticDelta() int64 {
	semantic := int64(0)
	if a.state != nil {
		semantic = a.state.SemanticBytes() - a.semanticBefore
	}
	heartbeat := a.HeartbeatDelta()
	if a.writer == nil {
		return semantic
	}
	// 真实写出量 = writer 增量（含我们没插桩的路径）；扣掉显式心跳后剩下的都算语义。
	written := int64(a.writer.Size() - a.sizeBefore)
	if written <= 0 {
		return semantic
	}
	unclassified := written - heartbeat
	if unclassified < 0 {
		unclassified = 0
	}
	// 取两者较大值：既覆盖「已插桩但没有经过 writer 记账」的计数，也覆盖未插桩写入。
	if semantic > unclassified {
		return semantic
	}
	return unclassified
}

// HeartbeatDelta 返回本次尝试期间写出的心跳字节数。
func (a UpstreamWriteAttempt) HeartbeatDelta() int64 {
	if a.state == nil {
		return 0
	}
	return a.state.HeartbeatBytes() - a.heartbeatBefore
}

// BytesDelta 返回本次尝试期间写出的全部字节数（旧判据口径，含心跳）。
func (a UpstreamWriteAttempt) BytesDelta() int64 {
	if a.writer == nil {
		if a.state == nil {
			return 0
		}
		return a.state.SemanticBytes() - a.semanticBefore + a.HeartbeatDelta()
	}
	delta := int64(a.writer.Size() - a.sizeBefore)
	if delta < 0 {
		return 0
	}
	return delta
}

// CanFailover 是**唯一**的换号判据（service 与 handler 共用，禁止任何路径再直接读
// `c.Writer.Written()` / `c.Writer.Size()` 做换号决策）。
//
// safeToFailoverAfterWrite 来自 UpstreamFailoverError 的同名字段：该路径显式声明
// 「即使写过字节也能换号」（例如只写过 SSE 注释）。
func (a UpstreamWriteAttempt) CanFailover(safeToFailoverAfterWrite bool) bool {
	if safeToFailoverAfterWrite {
		return true
	}
	if !a.useSemantic {
		// 回退开关：等价于改造前的「任何字节都不许换号」。
		if a.BytesDelta() > 0 {
			return false
		}
		if a.writer != nil && a.writer.Written() && !a.writtenBefore {
			return false
		}
		return true
	}
	return a.SemanticDelta() == 0
}

// FailoverSuppressedReason 是「放弃换号」的结构化原因（日志字段 reason）。
type FailoverSuppressedReason string

const (
	// FailoverSuppressedSemanticBytes 已写出语义字节，SSE 无法回滚。
	FailoverSuppressedSemanticBytes FailoverSuppressedReason = "semantic_bytes"
	// FailoverSuppressedNextAccountStop 既有语义要求不再换号（NextAccountStop）。
	FailoverSuppressedNextAccountStop FailoverSuppressedReason = "next_account_stop"
	// FailoverSuppressedBoundPassthru 绑定缓存的上游账号响应必须原样回写。
	FailoverSuppressedBoundPassthru FailoverSuppressedReason = "bound_passthru"
	// FailoverSuppressedClientGone 客户端已断开。
	FailoverSuppressedClientGone FailoverSuppressedReason = "client_gone"
)

// BlockReason 返回「不能换号」的原因；能换号时返回空串。
func (a UpstreamWriteAttempt) BlockReason() FailoverSuppressedReason {
	if a.CanFailover(false) {
		return ""
	}
	return FailoverSuppressedSemanticBytes
}

// FailoverBlockedForAttempt 报告「本次尝试之后还能不能换号」，并给出不能换号的原因。
//
// 它把 handler 过去四处 `c.Writer.Size() != writerSizeBeforeForward` 的判断收敛成一次
// 调用，避免出现「service 认为能换、handler 认为不能」的第二套判据（R-01）。
//
// 必须在**发起 Forward 之前**用 BeginUpstreamWriteAttempt 采集基线（attempt 自带基线），
// 否则多次尝试之间会互相污染（第二次尝试继承第一次的字节数）。
func FailoverBlockedForAttempt(attempt UpstreamWriteAttempt) (bool, FailoverSuppressedReason) {
	if attempt.CanFailover(false) {
		return false, ""
	}
	return true, FailoverSuppressedSemanticBytes
}

// UseSemanticBytesFor 从配置里读 `gateway.failover_policy.switchable.use_semantic_bytes`。
//
// nil 配置返回 true：B1 是显式默认变更（ping 之后允许换号，这正是 R-01 的修复收益），
// 配置层默认值同样是 true。需要回滚时配成 false，即回到「任何字节都不换号」。
func UseSemanticBytesFor(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	return cfg.Gateway.FailoverPolicy.Switchable.UseSemanticBytes
}

// upstreamWriteStateMutex 仅用于测试中的夹具（避免多测试并发创建同名 gin key 时的
// 数据竞争提示）；生产路径不使用。
var upstreamWriteStateMutex sync.Mutex
