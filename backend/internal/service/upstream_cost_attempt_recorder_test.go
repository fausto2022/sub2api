package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type upstreamCostAttemptRecorderTestWriter func(context.Context, []UpstreamCostAttempt) (UpstreamCostAttemptWriteResult, error)

func (w upstreamCostAttemptRecorderTestWriter) InsertUpstreamCostAttempts(ctx context.Context, rows []UpstreamCostAttempt) (UpstreamCostAttemptWriteResult, error) {
	return w(ctx, rows)
}

func recorderTestAttempt(index int) UpstreamCostAttempt {
	return UpstreamCostAttempt{
		ExecutionID: uuid.MustParse("70551d7d-1ff0-4713-bae8-3a99947e26b4"), AttemptIndex: index, AccountID: 19, Decision: "fallback",
	}
}

func stopCostRecorderForTest(t *testing.T, recorder *UpstreamCostAttemptRecorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, recorder.Stop(ctx))
}

func awaitCostRecorderTestValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for recorder")
		var zero T
		return zero
	}
}

func TestUpstreamCostAttemptRecorderDisabled(t *testing.T) {
	var writes atomic.Int64
	writer := upstreamCostAttemptRecorderTestWriter(func(context.Context, []UpstreamCostAttempt) (UpstreamCostAttemptWriteResult, error) {
		writes.Add(1)
		return UpstreamCostAttemptWriteResult{}, nil
	})
	recorder, err := NewUpstreamCostAttemptRecorder(false, writer, UpstreamCostAttemptRecorderOptions{})
	require.NoError(t, err)
	require.Nil(t, recorder) // No queue, worker, or retained writer.
	require.False(t, recorder.Submit(recorderTestAttempt(1)))
	require.NoError(t, recorder.Stop(context.Background()))
	require.Empty(t, recorder.Stats())
	require.Zero(t, writes.Load())
	_, err = NewUpstreamCostAttemptRecorder(true, nil, UpstreamCostAttemptRecorderOptions{})
	require.ErrorContains(t, err, "requires a writer")
}

func TestUpstreamCostAttemptRecorderSnapshotsAndBounds(t *testing.T) {
	const queuedRows = 2*UpstreamCostAttemptBatchLimit + 3
	firstWriteStarted := make(chan struct{})
	releaseFirstWrite := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseFirstWrite) }) }
	var batches [][]UpstreamCostAttempt
	writer := upstreamCostAttemptRecorderTestWriter(func(ctx context.Context, rows []UpstreamCostAttempt) (UpstreamCostAttemptWriteResult, error) {
		batches = append(batches, append([]UpstreamCostAttempt(nil), rows...))
		if len(batches) == 1 {
			close(firstWriteStarted)
			select {
			case <-releaseFirstWrite:
			case <-ctx.Done():
				return UpstreamCostAttemptWriteResult{}, ctx.Err()
			}
		}
		ignored := 0
		if len(batches) == 2 {
			ignored = 1 // A database duplicate, not a queue rejection.
		}
		return UpstreamCostAttemptWriteResult{Inserted: len(rows) - ignored, Ignored: ignored}, nil
	})
	recorder, err := NewUpstreamCostAttemptRecorder(true, writer, UpstreamCostAttemptRecorderOptions{QueueSize: queuedRows, WriteTimeout: 10 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { stopCostRecorderForTest(t, recorder) })
	defer release()

	require.True(t, recorder.Submit(recorderTestAttempt(1)))
	awaitCostRecorderTestValue(t, firstWriteStarted)
	status := 503
	usage := UpstreamCostAttemptUsage{InputTokens: 5}
	attempt := recorderTestAttempt(2)
	attempt.Status, attempt.BilledUsage = &status, &usage
	require.True(t, recorder.Submit(attempt))
	status, usage.InputTokens, attempt.Decision = 200, 999, "changed"
	for i := 3; i <= queuedRows+1; i++ {
		require.True(t, recorder.Submit(recorderTestAttempt(i)))
	}

	// A full queue must return before the writer is unblocked.
	admitted := make(chan bool, 1)
	go func() { admitted <- recorder.Submit(recorderTestAttempt(queuedRows + 2)) }()
	require.False(t, awaitCostRecorderTestValue(t, admitted))
	invalid := recorderTestAttempt(queuedRows + 3)
	invalid.Decision = strings.Repeat("x", 65)
	require.False(t, recorder.Submit(invalid))
	require.EqualValues(t, 1, recorder.Stats().DroppedFull)
	require.EqualValues(t, 1, recorder.Stats().DroppedInvalid)

	// Start shutdown while the first write is blocked. Admission closes even
	// if the caller cannot wait, and a later Stop can wait for the drain.
	stopCtx, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	require.ErrorIs(t, recorder.Stop(stopCtx), context.Canceled)
	require.False(t, recorder.Submit(recorderTestAttempt(queuedRows+4)))
	release()
	stopCostRecorderForTest(t, recorder)

	require.Len(t, batches, 4)
	require.Len(t, batches[0], 1)
	require.Len(t, batches[1], UpstreamCostAttemptBatchLimit)
	require.Len(t, batches[2], UpstreamCostAttemptBatchLimit)
	require.Len(t, batches[3], 3)
	require.Nil(t, batches[0][0].Status)
	require.Nil(t, batches[0][0].BilledUsage)
	require.Equal(t, 503, *batches[1][0].Status)
	require.EqualValues(t, 5, batches[1][0].BilledUsage.InputTokens)
	require.Equal(t, "fallback", batches[1][0].Decision)
	index := 1
	for _, batch := range batches {
		for _, row := range batch {
			require.Equal(t, index, row.AttemptIndex)
			index++
		}
	}
	require.Equal(t, UpstreamCostAttemptRecorderStats{
		Enqueued: queuedRows + 1, Inserted: queuedRows, Ignored: 1, DroppedFull: 1, DroppedStop: 1, DroppedInvalid: 1,
	}, recorder.Stats())
}

func TestUpstreamCostAttemptRecorderWriteFailureDoesNotRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
	}{
		{name: "database_error", reason: "write_error"},
		{name: "query_timeout", reason: "write_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			executionStarted := make(chan struct{})
			writer := upstreamCostAttemptRecorderTestWriter(func(ctx context.Context, rows []UpstreamCostAttempt) (UpstreamCostAttemptWriteResult, error) {
				if calls.Add(1) == 1 {
					close(executionStarted)
				}
				if tc.name == "query_timeout" {
					<-ctx.Done()
				}
				return UpstreamCostAttemptWriteResult{Inserted: len(rows)}, errors.New("secret=value private raw request body")
			})
			var logs bytes.Buffer
			recorder, err := NewUpstreamCostAttemptRecorder(true, writer, UpstreamCostAttemptRecorderOptions{WriteTimeout: 20 * time.Millisecond})
			require.NoError(t, err)
			t.Cleanup(func() { stopCostRecorderForTest(t, recorder) })
			recorder.log = slog.New(slog.NewTextHandler(&logs, nil))
			require.True(t, recorder.Submit(recorderTestAttempt(1)))
			awaitCostRecorderTestValue(t, executionStarted)
			stopCostRecorderForTest(t, recorder)
			require.EqualValues(t, 1, calls.Load())
			require.Equal(t, UpstreamCostAttemptRecorderStats{Enqueued: 1, Failed: 1}, recorder.Stats())
			require.Contains(t, logs.String(), "gateway.upstream_cost_attempt_write_failed")
			require.Contains(t, logs.String(), "reason="+tc.reason)
			require.NotContains(t, logs.String(), "secret=value")
			require.NotContains(t, logs.String(), "raw request body")
		})
	}
}

func TestUpstreamCostAttemptRecorderConcurrentStopAndSubmit(t *testing.T) {
	writer := upstreamCostAttemptRecorderTestWriter(func(_ context.Context, rows []UpstreamCostAttempt) (UpstreamCostAttemptWriteResult, error) {
		return UpstreamCostAttemptWriteResult{Inserted: len(rows)}, nil
	})
	recorder, err := NewUpstreamCostAttemptRecorder(true, writer, UpstreamCostAttemptRecorderOptions{QueueSize: 4})
	require.NoError(t, err)
	t.Cleanup(func() { stopCostRecorderForTest(t, recorder) })
	const attempts = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 1; i <= attempts; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			recorder.Submit(recorderTestAttempt(index))
		}(i)
	}
	close(start)
	stopCostRecorderForTest(t, recorder)
	wg.Wait()
	stats := recorder.Stats()
	require.Equal(t, stats.Enqueued, stats.Inserted)
	require.EqualValues(t, attempts, stats.Enqueued+stats.DroppedFull+stats.DroppedStop)
}
