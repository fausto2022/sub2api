package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const upstreamCostAttemptInsertPrefix = `INSERT INTO upstream_cost_attempts (execution_id, attempt_index, account_id, status, elapsed_ms, billed_usage, decision) VALUES `
const upstreamCostAttemptInsertSuffix = ` ON CONFLICT (execution_id, attempt_index) DO NOTHING`

func TestUpstreamCostAttemptRepositoryWriteBatch(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	firstExecution := uuid.MustParse("31d9ac5b-c41f-4f7e-bb64-8c55906638ed")
	secondExecution := uuid.MustParse("0e5ecfea-2b53-452b-86d1-a18c18f71862")
	status := 200
	noResponse := service.UpstreamCostAttempt{
		ExecutionID: firstExecution, AttemptIndex: 1, AccountID: 11, ElapsedMS: 120, Decision: "switch_account",
	}
	zeroUsage := service.UpstreamCostAttempt{
		ExecutionID: firstExecution, AttemptIndex: 2, AccountID: 12, Status: &status, BilledUsage: &service.UpstreamCostAttemptUsage{}, Decision: "succeeded",
	}
	otherRequest := service.UpstreamCostAttempt{
		ExecutionID: secondExecution, AttemptIndex: 1, AccountID: 11, Status: &status, ElapsedMS: 15,
		BilledUsage: &service.UpstreamCostAttemptUsage{InputTokens: 3, OutputTokens: 2}, Decision: "succeeded",
	}
	mock.ExpectExec(upstreamCostAttemptInsertPrefix+
		`($1, $2, $3, $4, $5, $6::jsonb, $7), ($8, $9, $10, $11, $12, $13::jsonb, $14), ($15, $16, $17, $18, $19, $20::jsonb, $21), ($22, $23, $24, $25, $26, $27::jsonb, $28)`+upstreamCostAttemptInsertSuffix).
		WithArgs(
			firstExecution.String(), 1, 11, nil, 120, nil, "switch_account",
			firstExecution.String(), 2, 12, 200, 0, `{"input_tokens":0,"output_tokens":0,"cache_creation_tokens":0,"cache_read_tokens":0,"image_count":0}`, "succeeded",
			secondExecution.String(), 1, 11, 200, 15, `{"input_tokens":3,"output_tokens":2,"cache_creation_tokens":0,"cache_read_tokens":0,"image_count":0}`, "succeeded",
			firstExecution.String(), 1, 11, nil, 120, nil, "switch_account",
		).WillReturnResult(sqlmock.NewResult(0, 3))

	writer := NewUpstreamCostAttemptRepository(db)
	result, err := writer.InsertUpstreamCostAttempts(context.Background(), []service.UpstreamCostAttempt{noResponse, zeroUsage, otherRequest, noResponse})
	require.NoError(t, err)
	require.Equal(t, service.UpstreamCostAttemptWriteResult{Inserted: 3, Ignored: 1}, result)

	mock.ExpectExec(upstreamCostAttemptInsertPrefix+`($1, $2, $3, $4, $5, $6::jsonb, $7)`+upstreamCostAttemptInsertSuffix).
		WithArgs(firstExecution.String(), 1, 11, nil, 120, nil, "switch_account").WillReturnResult(sqlmock.NewResult(0, 0))
	result, err = writer.InsertUpstreamCostAttempts(context.Background(), []service.UpstreamCostAttempt{noResponse})
	require.NoError(t, err)
	require.Equal(t, service.UpstreamCostAttemptWriteResult{Ignored: 1}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamCostAttemptRepositoryBatchBounds(t *testing.T) {
	writer := NewUpstreamCostAttemptRepository(nil)
	result, err := writer.InsertUpstreamCostAttempts(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, result)

	batch := make([]service.UpstreamCostAttempt, service.UpstreamCostAttemptBatchLimit+1)
	result, err = writer.InsertUpstreamCostAttempts(context.Background(), batch)
	require.ErrorContains(t, err, "limit is 256")
	require.Empty(t, result)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	batch = batch[:service.UpstreamCostAttemptBatchLimit]
	executionID := uuid.New()
	for i := range batch {
		batch[i] = service.UpstreamCostAttempt{ExecutionID: executionID, AttemptIndex: i + 1, AccountID: 1, Decision: "retry"}
	}
	mock.ExpectExec("^" + regexp.QuoteMeta(upstreamCostAttemptInsertPrefix) + ".+" +
		regexp.QuoteMeta(`($1786, $1787, $1788, $1789, $1790, $1791::jsonb, $1792)`+upstreamCostAttemptInsertSuffix) + "$").
		WillReturnResult(sqlmock.NewResult(0, int64(len(batch))))
	result, err = NewUpstreamCostAttemptRepository(db).InsertUpstreamCostAttempts(context.Background(), batch)
	require.NoError(t, err)
	require.Equal(t, service.UpstreamCostAttemptWriteResult{Inserted: len(batch)}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamCostAttemptRepositoryPropagatesWriteErrors(t *testing.T) {
	for _, operation := range []string{"exec", "rows_affected"} {
		t.Run(operation, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			failure := errors.New("database unavailable")
			exec := mock.ExpectExec(upstreamCostAttemptInsertPrefix + `($1, $2, $3, $4, $5, $6::jsonb, $7)` + upstreamCostAttemptInsertSuffix)
			if operation == "exec" {
				exec.WillReturnError(failure)
			} else {
				exec.WillReturnResult(sqlmock.NewErrorResult(failure))
			}
			attempt := service.UpstreamCostAttempt{ExecutionID: uuid.New(), AttemptIndex: 1, AccountID: 1, Decision: "retry"}
			result, err := NewUpstreamCostAttemptRepository(db).InsertUpstreamCostAttempts(context.Background(), []service.UpstreamCostAttempt{attempt})
			require.ErrorIs(t, err, failure)
			require.Empty(t, result)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
