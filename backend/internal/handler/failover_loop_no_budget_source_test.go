package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestFailoverSelectionExhaustedLimitsSources(t *testing.T) {
	ctx := context.Background()
	fs := NewFailoverState(3, false)
	fs.maxSelectionRetries = 1
	fs.LastFailoverErr = &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	fs.RecordProfitVeto(10)
	fs.FailedAccountIDs[20] = struct{}{}
	if got := fs.HandleSelectionExhausted(ctx); got != FailoverContinue {
		t.Fatalf("first 503 recovery = %v", got)
	}
	if _, ok := fs.FailedAccountIDs[10]; !ok {
		t.Fatal("503 recovery revived profit veto")
	}
	if _, ok := fs.FailedAccountIDs[20]; ok {
		t.Fatal("503 recovery kept a recoverable exclusion")
	}
	fs.FailedAccountIDs[20] = struct{}{}
	if got := fs.HandleSelectionExhausted(ctx); got != FailoverExhausted || fs.selectionRetryCount != 1 {
		t.Fatalf("bounded recovery = %v, retries = %d", got, fs.selectionRetryCount)
	}
}

func TestFailoverSelectionExhaustedBudgetAndCancellation(t *testing.T) {
	fs := NewFailoverState(3, false)
	fs.LastFailoverErr = &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	fs.Budget = testFailoverBudget(config.FailoverBudgetConfig{TotalTimeout: time.Second})
	fs.Budget.StartClock(time.Now().Add(-2 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := fs.HandleSelectionExhausted(ctx); got != FailoverCanceled || fs.Outcome != nil {
		t.Fatalf("cancellation yielded %v, %+v", got, fs.Outcome)
	}
	if got := fs.HandleSelectionExhausted(context.Background()); got != FailoverExhausted || fs.Outcome == nil || fs.Outcome.Reason != failoverReasonBudget {
		t.Fatalf("timed-out recovery yielded %v, %+v", got, fs.Outcome)
	}
}
