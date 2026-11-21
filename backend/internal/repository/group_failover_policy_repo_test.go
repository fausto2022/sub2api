package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// 迁移 254 的三列读取契约：原生 SQL、只读三列、NULL 与「显式 false/0」可区分。
var groupFailoverPolicyQuery = regexp.QuoteMeta(
	"SELECT failover_on_400, failover_max_account_switches, failover_policy FROM groups WHERE id = $1")

func TestLoadGroupFailoverPolicyAllNullFallsBackToGlobal(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(groupFailoverPolicyQuery).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"failover_on_400", "failover_max_account_switches", "failover_policy",
		}).AddRow(nil, nil, nil))

	repo := NewGroupFailoverPolicyRepository(db)
	row, err := repo.LoadGroupFailoverPolicy(context.Background(), 7)

	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, int64(7), row.GroupID)
	// 三列全 NULL = 该层没有任何覆盖，必须与「显式 false / 0」区分开。
	require.Nil(t, row.On400)
	require.Nil(t, row.MaxAccountSwitches)
	require.Nil(t, row.Policy)
	require.False(t, row.Override().HasAnyField())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadGroupFailoverPolicyReadsColumnsAndJSONB(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rawPolicy := []byte(`{
		"cooldown": {"enabled": true, "default_ttl": "90s"},
		"switchable": {"use_semantic_bytes": false},
		"max_selection_retries": 2
	}`)
	mock.ExpectQuery(groupFailoverPolicyQuery).
		WithArgs(int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{
			"failover_on_400", "failover_max_account_switches", "failover_policy",
		}).AddRow(false, int64(5), rawPolicy))

	repo := NewGroupFailoverPolicyRepository(db)
	row, err := repo.LoadGroupFailoverPolicy(context.Background(), 11)

	require.NoError(t, err)
	require.NotNil(t, row)
	require.NotNil(t, row.On400)
	require.False(t, *row.On400) // 显式 false 必须保留，不能退化成 nil
	require.NotNil(t, row.MaxAccountSwitches)
	require.Equal(t, 5, *row.MaxAccountSwitches)
	require.NotNil(t, row.Policy)
	require.NotNil(t, row.Policy.CooldownEnabled)
	require.True(t, *row.Policy.CooldownEnabled)
	require.NotNil(t, row.Policy.CooldownDefaultTTL)
	require.Equal(t, 90_000_000_000, int(*row.Policy.CooldownDefaultTTL)) // 90s
	require.NotNil(t, row.Policy.SwitchableUseSemanticBytes)
	require.False(t, *row.Policy.SwitchableUseSemanticBytes)
	require.NotNil(t, row.Policy.MaxSelectionRetries)
	require.Equal(t, 2, *row.Policy.MaxSelectionRetries)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadGroupFailoverPolicyMissingGroupReturnsNilRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(groupFailoverPolicyQuery).
		WithArgs(int64(404)).
		WillReturnRows(sqlmock.NewRows([]string{
			"failover_on_400", "failover_max_account_switches", "failover_policy",
		}))

	repo := NewGroupFailoverPolicyRepository(db)
	row, err := repo.LoadGroupFailoverPolicy(context.Background(), 404)

	require.NoError(t, err, "分组不存在不是错误：调用方回落到全局层")
	require.Nil(t, row)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadGroupFailoverPolicyRejectsMalformedJSONB(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// 键名写错（cooldwon）：静默忽略会变成「配了却不生效」的排障黑洞（R-49），必须报错。
	mock.ExpectQuery(groupFailoverPolicyQuery).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{
			"failover_on_400", "failover_max_account_switches", "failover_policy",
		}).AddRow(nil, nil, []byte(`{"cooldwon": {"enabled": true}}`)))

	repo := NewGroupFailoverPolicyRepository(db)
	row, err := repo.LoadGroupFailoverPolicy(context.Background(), 12)

	require.Error(t, err)
	require.Nil(t, row)
	require.ErrorContains(t, err, "load group failover policy")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadGroupFailoverPolicyRequiresDatabase(t *testing.T) {
	repo := NewGroupFailoverPolicyRepository(nil)
	row, err := repo.LoadGroupFailoverPolicy(context.Background(), 1)

	require.Error(t, err)
	require.Nil(t, row)
	require.ErrorContains(t, err, "not configured")
}
