package service

import (
	"context"

	"github.com/google/uuid"
)

// UpstreamCostAttemptBatchLimit 是一条批量写入语句允许的最大记录数。
const UpstreamCostAttemptBatchLimit = 256

// UpstreamCostAttempt 是一次实际发送的成本审计数据。ExecutionID 由服务端为
// 本次执行生成，AttemptIndex 从 1 开始，均用于审计去重。
type UpstreamCostAttempt struct {
	ExecutionID  uuid.UUID
	AttemptIndex int
	AccountID    int64
	Status       *int // 未收到 HTTP 响应时为 nil。
	ElapsedMS    int64
	BilledUsage  *UpstreamCostAttemptUsage // 上游用量未观测到时为 nil。
	Decision     string
}

// UpstreamCostAttemptUsage 记录实际观测到的上游用量，可表示已知的零用量。
type UpstreamCostAttemptUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	ImageCount          int64 `json:"image_count"`
}

// UpstreamCostAttemptWriteResult 报告成功执行的批次中写入和因唯一键重复而忽略的行数。
type UpstreamCostAttemptWriteResult struct {
	Inserted int
	Ignored  int
}

// UpstreamCostAttemptWriter 一次原子写入一个有界批次。空批次是空操作。
// 调用方负责分批并处理错误，返回错误时结果计数不可使用。
type UpstreamCostAttemptWriter interface {
	InsertUpstreamCostAttempts(ctx context.Context, attempts []UpstreamCostAttempt) (UpstreamCostAttemptWriteResult, error)
}
