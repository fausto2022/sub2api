package repository

import (
	"context"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type tempUnschedulableSnapshotSpy struct {
	service.SchedulerCache
	account *service.Account
}

func (s *tempUnschedulableSnapshotSpy) SetAccount(_ context.Context, account *service.Account) error {
	s.account = account
	return nil
}

func TestAccountRepositoryTempUnschedulableIfLater(t *testing.T) {
	const id int64 = 42
	until := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		prior     time.Time
		priorWhy  string
		extended  bool
		changed   bool
		storedEnd time.Time
		storedWhy string
	}{
		{"shorter preserves effective deadline and reason", until.Add(9 * time.Minute), "existing", false, false, until.Add(9 * time.Minute), "existing"},
		{"equal window refreshes reason", until, "existing", false, true, until, "new"},
		{"identical window and reason avoid row mutation", until, "new", false, false, until, "new"},
		{"later deadline replaces window and reason", until.Add(-time.Minute), "existing", true, true, until, "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			cache := &tempUnschedulableSnapshotSpy{}
			repo := newAccountRepositoryWithSQL(client, db, cache)

			// Assert the row lock, shorter guard, identical-window check and
			// otherwise single UPDATE using the production PostgreSQL query.
			predicate := regexp.QuoteMeta("(temp_unschedulable_until IS NULL OR temp_unschedulable_until < $1)") +
				".*" + regexp.QuoteMeta("FOR UPDATE") + ".*" +
				regexp.QuoteMeta("SET temp_unschedulable_until = $1, temp_unschedulable_reason = $2") + ".*" +
				regexp.QuoteMeta("(w.extended OR (w.temp_unschedulable_until = $1 AND a.temp_unschedulable_reason IS DISTINCT FROM $2))")
			extended := tc.prior.Before(until)
			changed := extended || (tc.prior.Equal(until) && tc.priorWhy != "new")
			mock.ExpectQuery(predicate).WithArgs(until, "new", id).
				WillReturnRows(sqlmock.NewRows([]string{"extended", "changed"}).AddRow(extended, changed))
			if tc.changed {
				mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectQuery(`FROM "accounts"`).WillReturnRows(sqlmock.NewRows([]string{"id", "temp_unschedulable_until", "temp_unschedulable_reason"}).AddRow(id, tc.storedEnd, tc.storedWhy))
			mock.ExpectQuery(`FROM "account_groups"`).WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id"}))

			gotExtended, err := repo.SetTempUnschedulableIfLater(context.Background(), id, until, "new")
			require.NoError(t, err)
			require.Equal(t, tc.extended, gotExtended)
			require.NoError(t, mock.ExpectationsWereMet())
			require.NotNil(t, cache.account, "every existing row refreshes the snapshot")
			require.NotNil(t, cache.account.TempUnschedulableUntil)
			require.Equal(t, tc.storedEnd, *cache.account.TempUnschedulableUntil)
			require.Equal(t, tc.storedWhy, cache.account.TempUnschedulableReason)
		})
	}
}

func TestAccountRepository529OverloadDeadlineGuard(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	predicate := regexp.QuoteMeta("UPDATE accounts SET overload_until = $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL AND (overload_until IS NULL OR overload_until < $1)")
	const id int64 = 42
	long := time.Date(2026, 1, 2, 3, 14, 0, 0, time.UTC)
	mock.ExpectExec(predicate).WithArgs(long, id).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(0, 1))
	short := long.Add(-9 * time.Minute)
	mock.ExpectExec(predicate).WithArgs(short, id).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, repo.SetOverloaded(context.Background(), id, long))
	require.NoError(t, repo.SetOverloaded(context.Background(), id, short))
	require.NoError(t, mock.ExpectationsWereMet(), "a guarded no-op emits no outbox event")
}

type overloadDebounceRedisTestRepo struct {
	service.AccountRepository
	writes atomic.Int32
}

func (r *overloadDebounceRedisTestRepo) SetOverloaded(context.Context, int64, time.Time) error {
	r.writes.Add(1)
	return nil
}

func TestRateLimit529WriteAmplificationWithRedis(t *testing.T) {
	rds := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rds.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo := &overloadDebounceRedisTestRepo{}
	cfg := &config.Config{}
	cfg.Gateway.FailoverPolicy.Cooldown.RedisTimeout = 5 * time.Second
	svcs := [2]*service.RateLimitService{
		service.NewRateLimitService(repo, nil, cfg, nil, NewTempUnschedCache(client)),
		service.NewRateLimitService(repo, nil, cfg, nil, NewTempUnschedCache(client)),
	}
	account := &service.Account{ID: 42, Platform: service.PlatformAntigravity, Type: service.AccountTypeOAuth}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			svcs[i%len(svcs)].HandleUpstreamError(context.Background(), account, 529, nil, nil)
		}()
	}
	close(start)
	wg.Wait()
	require.EqualValues(t, 1, repo.writes.Load())
	rds.FastForward(time.Second)
	svcs[0].HandleUpstreamError(context.Background(), account, 529, nil, nil)
	require.EqualValues(t, 2, repo.writes.Load(), "the next window permits an extension")
}

func TestRateLimit529WriteReservationReleaseChecksOwner(t *testing.T) {
	rds := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rds.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewTempUnschedCache(client).(*tempUnschedCache)
	ctx := context.Background()
	acquired, releaseOld, err := cache.TryAcquireOverloadWrite(ctx, 42, time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	rds.FastForward(time.Second)
	acquired, releaseNew, err := cache.TryAcquireOverloadWrite(ctx, 42, time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, releaseOld(ctx))
	acquired, _, err = cache.TryAcquireOverloadWrite(ctx, 42, time.Second)
	require.NoError(t, err)
	require.False(t, acquired, "an expired owner cannot remove the new reservation")
	require.NoError(t, releaseNew(ctx))
	acquired, _, err = cache.TryAcquireOverloadWrite(ctx, 42, time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
}
