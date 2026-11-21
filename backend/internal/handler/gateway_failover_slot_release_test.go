package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type failoverSlotCache struct {
	helperConcurrencyCacheStub
	active  atomic.Int64
	waiting atomic.Int64
}

func (s *failoverSlotCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	s.active.Add(1)
	return true, nil
}

func (s *failoverSlotCache) ReleaseAccountSlot(context.Context, int64, string) error {
	s.active.Add(-1)
	return nil
}

func (s *failoverSlotCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	s.waiting.Add(1)
	return true, nil
}

func (s *failoverSlotCache) DecrementAccountWaitCount(context.Context, int64) error {
	s.waiting.Add(-1)
	return nil
}

func TestGatewayFailoverReturnsAccountSlotsAndWaitCounts(t *testing.T) {
	for _, terminal := range []string{"success", "budget", "cancellation"} {
		t.Run(terminal, func(t *testing.T) {
			cache := &failoverSlotCache{}
			helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0)
			fs := NewFailoverState(3, false)
			limit := 4
			if terminal == "budget" {
				limit = 2
			}
			fs.Budget = testFailoverBudget(config.FailoverBudgetConfig{MaxTotalUpstreamAttempts: limit})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
			for id := int64(1); id <= 3; id++ {
				if allowed, err := helper.IncrementAccountWaitCount(ctx, id, 1); !allowed || err != nil {
					t.Fatalf("account %d wait admission: %v, %v", id, allowed, err)
				}
				started := false
				release, err := helper.AcquireAccountSlotWithWaitTimeout(c, id, 1, time.Second, false, &started)
				helper.DecrementAccountWaitCount(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				release = wrapReleaseOnDone(ctx, release)
				now := time.Now()
				fs.Budget.StartClock(now)
				if id == 3 && terminal == "cancellation" {
					cancel()
				}
				attempt, attemptErr := fs.Budget.beginAttempt(ctx, id, now, 0, false)
				if attempt != nil {
					attempt.close()
				}
				release()
				release()
				if got := cache.active.Load(); got != 0 {
					t.Fatalf("account %d left %d active slots", id, got)
				}
				if got := cache.waiting.Load(); got != 0 {
					t.Fatalf("account %d left %d wait units", id, got)
				}
				if id == 3 {
					var want error
					switch terminal {
					case "budget":
						want = errFailoverBudget
					case "cancellation":
						want = context.Canceled
					}
					if attemptErr != want {
						t.Fatalf("last attempt = %v, want %v", attemptErr, want)
					}
				} else if attemptErr != nil {
					t.Fatalf("unexpected refusal for account %d: %v", id, attemptErr)
				}
				if id < 3 {
					err := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
					want := FailoverContinue
					if id == 2 && terminal == "budget" {
						want = FailoverExhausted
					}
					if got := fs.HandleFailoverError(ctx, nil, id, service.PlatformAnthropic, 0, err); got != want {
						t.Fatalf("switch from %d = %v, want %v", id, got, want)
					}
				}
			}
		})
	}
}
