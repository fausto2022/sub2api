package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type overloadWriteTestRepo struct {
	AccountRepository
	mu       sync.Mutex
	writes   []time.Time
	failNext bool
}

func (r *overloadWriteTestRepo) SetOverloaded(_ context.Context, _ int64, until time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, until)
	if r.failNext {
		r.failNext = false
		return errors.New("DB unavailable")
	}
	return nil
}

type overloadWriteTestCache struct {
	TempUnschedCache
	mu       sync.Mutex
	reserved map[int64]bool
	err      error
}

func (c *overloadWriteTestCache) TryAcquireOverloadWrite(_ context.Context, accountID int64, _ time.Duration) (bool, func(context.Context) error, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil || c.reserved[accountID] {
		return false, nil, c.err
	}
	c.reserved[accountID] = true
	return true, func(context.Context) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.reserved, accountID)
		return nil
	}, nil
}

func TestRateLimit529WriteAmplification(t *testing.T) {
	repo := &overloadWriteTestRepo{}
	cache := &overloadWriteTestCache{reserved: make(map[int64]bool)}
	// Two service instances share the same atomic cache, as in production.
	services := [2]*RateLimitService{
		NewRateLimitService(repo, nil, &config.Config{}, nil, cache),
		NewRateLimitService(repo, nil, &config.Config{}, nil, cache),
	}
	account := &Account{ID: 42, Platform: PlatformAntigravity, Type: AccountTypeOAuth}
	before := time.Now()
	start := make(chan struct{})
	var wg sync.WaitGroup
	const requests = 50
	shouldDisable := make(chan bool, requests)
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			shouldDisable <- services[i%len(services)].HandleUpstreamError(context.Background(), account, 529, nil, nil)
		}()
	}
	close(start)
	wg.Wait()
	close(shouldDisable)
	for disabled := range shouldDisable {
		require.False(t, disabled)
	}
	require.Len(t, repo.writes, 1)
	require.WithinDuration(t, before.Add(10*time.Minute), repo.writes[0], 2*time.Second)

	other := &Account{ID: 43, Platform: PlatformAntigravity, Type: AccountTypeOAuth}
	services[0].HandleUpstreamError(context.Background(), other, 529, nil, nil)
	require.Len(t, repo.writes, 2, "the debounce key is account-scoped")
}

func TestRateLimit529WriteAmplificationRedisUnavailable(t *testing.T) {
	repo := &overloadWriteTestRepo{}
	cache := &overloadWriteTestCache{err: errors.New("Redis unavailable")}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, cache)
	account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	for range 2 {
		require.False(t, svc.HandleUpstreamError(context.Background(), account, 529, nil, nil))
	}
	require.Len(t, repo.writes, 2, "each request falls back to the legacy DB path")
}

func TestRateLimit529WriteAmplificationRetriesFailedWrite(t *testing.T) {
	repo := &overloadWriteTestRepo{failNext: true}
	cache := &overloadWriteTestCache{reserved: make(map[int64]bool)}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, cache)
	account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	for range 3 {
		svc.HandleUpstreamError(context.Background(), account, 529, nil, nil)
	}
	require.Len(t, repo.writes, 2, "a failed write releases its reservation")
}

type retryTempUnscheduleTestRepo struct {
	AccountRepository
	accountID int64
	until     time.Time
	reason    string
}

func (r *retryTempUnscheduleTestRepo) SetTempUnschedulable(_ context.Context, accountID int64, until time.Time, reason string) error {
	r.accountID, r.until, r.reason = accountID, until, reason
	return nil
}

type retryTempUnscheduleIfLaterTestRepo struct {
	AccountRepository // traps calls to the legacy writer
	recorded          retryTempUnscheduleTestRepo
}

func (r *retryTempUnscheduleIfLaterTestRepo) SetTempUnschedulableIfLater(ctx context.Context, accountID int64, until time.Time, reason string) (bool, error) {
	return false, r.recorded.SetTempUnschedulable(ctx, accountID, until, reason)
}

func TestRetryTempUnschedulableUsesIfLater(t *testing.T) {
	for _, tc := range []struct {
		name   string
		apply  func(context.Context, AccountRepository, int64, string)
		reason string
	}{
		{"google config", tempUnscheduleGoogleConfigError, "400: invalid project resource name (auto temp-unschedule 1m)"},
		{"empty response", tempUnscheduleEmptyResponse, "empty stream response (auto temp-unschedule 1m)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallback := &retryTempUnscheduleTestRepo{}
			extender := &retryTempUnscheduleIfLaterTestRepo{}
			before := time.Now().Add(time.Minute)
			tc.apply(context.Background(), extender, 42, "test")
			tc.apply(context.Background(), fallback, 42, "test")
			for _, got := range []*retryTempUnscheduleTestRepo{&extender.recorded, fallback} {
				require.EqualValues(t, 42, got.accountID)
				require.Equal(t, tc.reason, got.reason)
				require.WithinDuration(t, before, got.until, 2*time.Second)
			}
		})
	}
}

func TestRateLimit529PreservesClassificationAndTransientPolicy(t *testing.T) {
	class, _ := ClassifyUpstreamFailure(ClassifyInput{StatusCode: 529, Platform: PlatformAntigravity})
	require.Equal(t, ClassQuotaExhausted, class)
	require.True(t, shouldRetryAntigravityError(529))

	// With no custom policy, these transient failures neither call overload nor
	// any other AccountRepository write. The embedded nil repo traps other calls.
	repo := &overloadWriteTestRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 42, Platform: PlatformAntigravity, Type: AccountTypeOAuth}
	for _, code := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		require.False(t, svc.HandleUpstreamError(context.Background(), account, code, nil, nil))
	}
	require.Empty(t, repo.writes)
}
