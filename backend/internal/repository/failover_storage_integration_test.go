//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFailoverStoragePostgresMigration254(t *testing.T) {
	tx := testTx(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// The temporary groups table shadows the migrated table on this transaction's
	// connection. It lets the embedded migration run against a pre-254 row.
	_, err := tx.ExecContext(ctx, `CREATE TEMP TABLE groups (id bigint PRIMARY KEY) ON COMMIT DROP`)
	require.NoError(t, err)
	const groupID int64 = 1
	_, err = tx.ExecContext(ctx, `INSERT INTO pg_temp.groups (id) VALUES ($1)`, groupID)
	require.NoError(t, err)

	migrationSQL, err := dbmigrations.FS.ReadFile("254_group_failover_policy.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	rows, err := tx.QueryContext(ctx, `
SELECT attname, format_type(atttypid, atttypmod), attnotnull, atthasdef
FROM pg_attribute
WHERE attrelid = 'pg_temp.groups'::regclass AND attnum > 0 AND NOT attisdropped
ORDER BY attnum`)
	require.NoError(t, err)
	var columns []string
	for rows.Next() {
		var name, kind string
		var notNull, hasDefault bool
		require.NoError(t, rows.Scan(&name, &kind, &notNull, &hasDefault))
		columns = append(columns, name+":"+kind)
		if name != "id" {
			require.False(t, notNull, "%s must allow NULL", name)
			require.False(t, hasDefault, "%s must have no default", name)
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, []string{"id:bigint", "failover_on_400:boolean", "failover_max_account_switches:integer", "failover_policy:jsonb"}, columns)

	const readPolicy = `SELECT failover_on_400, failover_max_account_switches, failover_policy::text FROM pg_temp.groups WHERE id = $1`
	var on400 sql.NullBool
	var switches sql.NullInt64
	var policy sql.NullString
	require.NoError(t, tx.QueryRowContext(ctx, readPolicy, groupID).Scan(&on400, &switches, &policy))
	require.Equal(t, sql.NullBool{}, on400)
	require.Equal(t, sql.NullInt64{}, switches)
	require.Equal(t, sql.NullString{}, policy)

	const override = `{"cooldown":{"enabled":true}}`
	_, err = tx.ExecContext(ctx, `UPDATE pg_temp.groups SET failover_on_400 = false,
failover_max_account_switches = 0, failover_policy = $1::jsonb WHERE id = $2`, override, groupID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, readPolicy, groupID).Scan(&on400, &switches, &policy))
	require.Equal(t, sql.NullBool{Bool: false, Valid: true}, on400)
	require.Equal(t, sql.NullInt64{Int64: 0, Valid: true}, switches)
	require.True(t, policy.Valid)
	require.JSONEq(t, override, policy.String)
}

func TestFailoverStoragePostgresGroupPolicyRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO groups (name, platform) VALUES ($1, $2) RETURNING id`,
		"failover-policy-"+uuid.NewString(), service.PlatformAnthropic,
	).Scan(&id))
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(cleanupCtx, `DELETE FROM groups WHERE id = $1`, id)
		require.NoError(t, err)
	})

	repo := NewGroupFailoverPolicyRepository(integrationDB)
	globalOn400, globalSwitches := true, 3
	global := service.FailoverPolicy{FailoverOn400: &globalOn400, MaxAccountSwitches: &globalSwitches}
	row, err := repo.LoadGroupFailoverPolicy(ctx, id)
	require.NoError(t, err)
	require.Equal(t, &service.GroupFailoverPolicyRow{GroupID: id}, row)
	effective, source := service.ResolveFailoverPolicy(nil, row, global)
	require.Equal(t, service.FailoverPolicySourceGlobal, source)
	require.NotNil(t, effective.FailoverOn400)
	require.True(t, *effective.FailoverOn400)
	require.NotNil(t, effective.MaxAccountSwitches)
	require.Equal(t, 3, *effective.MaxAccountSwitches)

	// A real JSONB override deliberately disagrees with the scalar columns.
	_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET failover_on_400 = false,
failover_max_account_switches = 0, failover_policy = $1::jsonb WHERE id = $2`,
		`{"failover_on_400":true,"max_account_switches":9,"cooldown":{"enabled":true}}`, id)
	require.NoError(t, err)
	row, err = repo.LoadGroupFailoverPolicy(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.NotNil(t, row.Policy)
	require.NotNil(t, row.Policy.MaxAccountSwitches)
	require.Equal(t, 9, *row.Policy.MaxAccountSwitches)
	effective, source = service.ResolveFailoverPolicy(nil, row, global)
	require.Equal(t, service.FailoverPolicySourceGroup, source)
	require.NotNil(t, effective.FailoverOn400)
	require.False(t, *effective.FailoverOn400)
	require.NotNil(t, effective.MaxAccountSwitches)
	require.Zero(t, *effective.MaxAccountSwitches)
	require.NotNil(t, effective.CooldownEnabled)
	require.True(t, *effective.CooldownEnabled)
}

func TestFailoverStoragePostgresCostAttemptRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	execution, otherExecution := uuid.New(), uuid.New()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := integrationDB.ExecContext(cleanupCtx,
			`DELETE FROM upstream_cost_attempts WHERE execution_id IN ($1, $2)`, execution.String(), otherExecution.String())
		require.NoError(t, err)
	})

	writer := NewUpstreamCostAttemptRepository(integrationDB)
	okStatus := http.StatusOK
	initial := service.UpstreamCostAttempt{
		ExecutionID: execution, AttemptIndex: 1, AccountID: 501, ElapsedMS: 17, Decision: "switch_account",
	}
	knownZero := service.UpstreamCostAttempt{
		ExecutionID: execution, AttemptIndex: 2, AccountID: 502, Status: &okStatus,
		BilledUsage: &service.UpstreamCostAttemptUsage{}, Decision: "succeeded",
	}
	other := initial
	other.ExecutionID = otherExecution
	result, err := writer.InsertUpstreamCostAttempts(ctx, []service.UpstreamCostAttempt{initial, knownZero, other})
	require.NoError(t, err)
	require.Equal(t, service.UpstreamCostAttemptWriteResult{Inserted: 3}, result)

	// Each writer races for the same new key and also attempts to replace the
	// existing first row. Exactly one new insert may win.
	const workers = 8
	type completion struct {
		result service.UpstreamCostAttemptWriteResult
		err    error
	}
	start := make(chan struct{})
	done := make(chan completion, workers)
	for i := range workers {
		go func() {
			<-start
			status := http.StatusBadGateway
			replacement := service.UpstreamCostAttempt{
				ExecutionID: execution, AttemptIndex: 1, AccountID: int64(600 + i), Status: &status,
				ElapsedMS: 99, BilledUsage: &service.UpstreamCostAttemptUsage{InputTokens: 4}, Decision: "replacement",
			}
			candidate := replacement
			candidate.AttemptIndex = 3
			result, err := writer.InsertUpstreamCostAttempts(ctx, []service.UpstreamCostAttempt{replacement, candidate})
			done <- completion{result, err}
		}()
	}
	close(start)
	var results []completion
	for range workers {
		results = append(results, <-done)
	}
	var inserted, ignored int
	for _, completion := range results {
		require.NoError(t, completion.err)
		inserted += completion.result.Inserted
		ignored += completion.result.Ignored
	}
	require.Equal(t, 1, inserted)
	require.Equal(t, 2*workers-1, ignored)

	var rowsForExecution, rowsForOther int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM upstream_cost_attempts WHERE execution_id = $1`, execution.String()).Scan(&rowsForExecution))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM upstream_cost_attempts WHERE execution_id = $1 AND attempt_index = 1`, otherExecution.String()).Scan(&rowsForOther))
	require.Equal(t, 3, rowsForExecution)
	require.Equal(t, 1, rowsForOther)

	const readAttempt = `SELECT account_id, status, elapsed_ms, billed_usage::text, decision
FROM upstream_cost_attempts WHERE execution_id = $1 AND attempt_index = $2`
	var accountID, elapsed int64
	var status sql.NullInt64
	var usage sql.NullString
	var decision string
	require.NoError(t, integrationDB.QueryRowContext(ctx, readAttempt, execution.String(), 1).Scan(&accountID, &status, &elapsed, &usage, &decision))
	require.Equal(t, initial.AccountID, accountID)
	require.Equal(t, initial.ElapsedMS, elapsed)
	require.Equal(t, initial.Decision, decision)
	require.False(t, status.Valid, "unknown HTTP status must be SQL NULL")
	require.False(t, usage.Valid, "unknown usage must be SQL NULL")
	require.NoError(t, integrationDB.QueryRowContext(ctx, readAttempt, execution.String(), 2).Scan(&accountID, &status, &elapsed, &usage, &decision))
	require.Equal(t, sql.NullInt64{Int64: http.StatusOK, Valid: true}, status)
	require.True(t, usage.Valid, "observed zero usage must remain present")
	require.JSONEq(t, `{"input_tokens":0,"output_tokens":0,"cache_creation_tokens":0,"cache_read_tokens":0,"image_count":0}`, usage.String)
}
