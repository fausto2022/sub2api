package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type upstreamCostAttemptRepository struct {
	db *sql.DB
}

// NewUpstreamCostAttemptRepository constructs a writer for the attempt audit table.
func NewUpstreamCostAttemptRepository(db *sql.DB) service.UpstreamCostAttemptWriter {
	return &upstreamCostAttemptRepository{db: db}
}

func (r *upstreamCostAttemptRepository) InsertUpstreamCostAttempts(ctx context.Context, attempts []service.UpstreamCostAttempt) (service.UpstreamCostAttemptWriteResult, error) {
	var result service.UpstreamCostAttemptWriteResult
	if len(attempts) == 0 {
		return result, nil
	}
	if len(attempts) > service.UpstreamCostAttemptBatchLimit {
		return result, fmt.Errorf("upstream cost attempt batch has %d rows, limit is %d", len(attempts), service.UpstreamCostAttemptBatchLimit)
	}
	if r == nil || r.db == nil {
		return result, fmt.Errorf("upstream cost attempt repository database is missing")
	}

	const fieldsPerAttempt = 7
	args := make([]any, 0, len(attempts)*fieldsPerAttempt)
	var query strings.Builder
	query.WriteString(`INSERT INTO upstream_cost_attempts (execution_id, attempt_index, account_id, status, elapsed_ms, billed_usage, decision) VALUES `)
	for i, attempt := range attempts {
		if i > 0 {
			query.WriteString(", ")
		}
		p := len(args) + 1
		fmt.Fprintf(&query, "($%d, $%d, $%d, $%d, $%d, $%d::jsonb, $%d)", p, p+1, p+2, p+3, p+4, p+5, p+6)
		var usage any
		if attempt.BilledUsage != nil {
			encoded, err := json.Marshal(attempt.BilledUsage)
			if err != nil {
				return result, fmt.Errorf("encode upstream cost attempt usage: %w", err)
			}
			usage = string(encoded)
		}
		args = append(args, attempt.ExecutionID.String(), attempt.AttemptIndex, attempt.AccountID, attempt.Status, attempt.ElapsedMS, usage, attempt.Decision)
	}
	query.WriteString(` ON CONFLICT (execution_id, attempt_index) DO NOTHING`)

	execResult, err := r.db.ExecContext(ctx, query.String(), args...)
	if err != nil {
		return result, fmt.Errorf("insert upstream cost attempts: %w", err)
	}
	inserted, err := execResult.RowsAffected()
	if err != nil {
		return result, fmt.Errorf("count inserted upstream cost attempts: %w", err)
	}
	return service.UpstreamCostAttemptWriteResult{Inserted: int(inserted), Ignored: len(attempts) - int(inserted)}, nil
}
