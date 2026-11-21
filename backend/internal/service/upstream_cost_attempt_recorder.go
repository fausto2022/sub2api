package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultUpstreamCostAttemptQueueSize    = 1024
	defaultUpstreamCostAttemptWriteTimeout = 2 * time.Second
	upstreamCostAttemptWarnInterval        = 5 * time.Second
)

// UpstreamCostAttemptRecorderOptions controls the independent audit queue.
// Nonpositive capacities or timeouts use the defaults. The recorder has one worker.
type UpstreamCostAttemptRecorderOptions struct {
	QueueSize    int
	WriteTimeout time.Duration
}

// UpstreamCostAttemptRecorderStats contains cumulative row counters, not batch
// counts. A snapshot taken during activity may see counters at different instants.
type UpstreamCostAttemptRecorderStats struct {
	Enqueued       uint64
	Inserted       uint64
	Ignored        uint64 // Duplicate keys reported by the database.
	Failed         uint64 // Admitted rows whose write did not report success.
	DroppedFull    uint64
	DroppedStop    uint64
	DroppedInvalid uint64
}

// UpstreamCostAttemptRecorder owns a queue for completed send snapshots. It has
// no relationship to the usage billing queue or the request's lifetime.
type UpstreamCostAttemptRecorder struct {
	writer       UpstreamCostAttemptWriter
	writeTimeout time.Duration
	log          *slog.Logger
	queue        chan UpstreamCostAttempt
	done         chan struct{}

	mu      sync.RWMutex // protects admission against closing queue; never held during I/O
	stopped bool

	enqueued       atomic.Uint64
	inserted       atomic.Uint64
	ignored        atomic.Uint64
	failed         atomic.Uint64
	droppedFull    atomic.Uint64
	droppedStop    atomic.Uint64
	droppedInvalid atomic.Uint64
}

// NewUpstreamCostAttemptRecorder returns nil without allocating a queue or
// starting a goroutine when disabled. An enabled recorder requires a writer.
func NewUpstreamCostAttemptRecorder(enabled bool, writer UpstreamCostAttemptWriter, opts UpstreamCostAttemptRecorderOptions) (*UpstreamCostAttemptRecorder, error) {
	if !enabled {
		return nil, nil
	}
	if writer == nil {
		return nil, errors.New("upstream cost attempt recorder requires a writer")
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultUpstreamCostAttemptQueueSize
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = defaultUpstreamCostAttemptWriteTimeout
	}
	r := &UpstreamCostAttemptRecorder{
		writer: writer, writeTimeout: opts.WriteTimeout, log: slog.Default(),
		queue: make(chan UpstreamCostAttempt, opts.QueueSize), done: make(chan struct{}),
	}
	go r.run()
	return r, nil
}

// Submit copies one completed send event and tries to enqueue it without
// waiting for space or a database write. false means it was not admitted.
// A nil recorder is disabled. Status and BilledUsage must remain stable until
// Submit returns; the caller may then reuse them. Decision is an internal label.
func (r *UpstreamCostAttemptRecorder) Submit(attempt UpstreamCostAttempt) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.stopped {
		r.droppedStop.Add(1)
		return false
	}
	// Prevent an invalid, oversized label from defeating the queue's memory bound.
	if len(attempt.Decision) > 64 || strings.TrimSpace(attempt.Decision) == "" {
		r.droppedInvalid.Add(1)
		return false
	}
	attempt.Decision = strings.Clone(attempt.Decision)
	if attempt.Status != nil {
		status := *attempt.Status
		attempt.Status = &status
	}
	if attempt.BilledUsage != nil {
		usage := *attempt.BilledUsage
		attempt.BilledUsage = &usage
	}
	select {
	case r.queue <- attempt:
		r.enqueued.Add(1)
		return true
	default:
		r.droppedFull.Add(1)
		return false
	}
}

// Stats returns the row counters. A disabled recorder has all-zero counters.
func (r *UpstreamCostAttemptRecorder) Stats() UpstreamCostAttemptRecorderStats {
	if r == nil {
		return UpstreamCostAttemptRecorderStats{}
	}
	return UpstreamCostAttemptRecorderStats{
		Enqueued: r.enqueued.Load(), Inserted: r.inserted.Load(), Ignored: r.ignored.Load(), Failed: r.failed.Load(),
		DroppedFull: r.droppedFull.Load(), DroppedStop: r.droppedStop.Load(), DroppedInvalid: r.droppedInvalid.Load(),
	}
}

// Stop closes admission, then waits for admitted records to drain. It is safe
// to call more than once or on a nil recorder. If ctx expires, the caller stops
// waiting; the bounded queue continues draining under per-write timeouts.
func (r *UpstreamCostAttemptRecorder) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		close(r.queue)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	default:
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *UpstreamCostAttemptRecorder) run() {
	defer close(r.done)
	batch := make([]UpstreamCostAttempt, 0, UpstreamCostAttemptBatchLimit)
	var lastWriteWarn, lastDropWarn time.Time
	var reportedDropped uint64
	for first := range r.queue {
		batch = append(batch[:0], first)
	collect:
		for len(batch) < cap(batch) {
			select {
			case attempt, ok := <-r.queue:
				if !ok {
					break collect
				}
				batch = append(batch, attempt)
			default:
				break collect
			}
		}
		if reason := r.writeBatch(batch); reason != "" && time.Since(lastWriteWarn) >= upstreamCostAttemptWarnInterval {
			lastWriteWarn = time.Now()
			// Do not emit raw driver errors (which can contain SQL or payload values).
			r.log.Warn("gateway.upstream_cost_attempt_write_failed", "reason", reason, "batch_rows", len(batch), "failed_rows_total", r.failed.Load())
		}
		clear(batch)
		if dropped := r.droppedFull.Load(); dropped > reportedDropped && time.Since(lastDropWarn) >= upstreamCostAttemptWarnInterval {
			lastDropWarn = time.Now()
			r.log.Warn("gateway.upstream_cost_attempt_queue_full", "dropped_rows_total", dropped)
			reportedDropped = dropped
		}
	}
}

func (r *UpstreamCostAttemptRecorder) writeBatch(batch []UpstreamCostAttempt) string {
	ctx, cancel := context.WithTimeout(context.Background(), r.writeTimeout)
	result, err := r.writer.InsertUpstreamCostAttempts(ctx, batch)
	ctxErr := ctx.Err()
	cancel()
	if err != nil {
		r.failed.Add(uint64(len(batch)))
		// A timeout cannot prove the transaction did not commit. Record the
		// unconfirmed audit write and do not replay it or touch user billing.
		if errors.Is(ctxErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return "write_timeout"
		}
		return "write_error"
	}
	r.inserted.Add(uint64(result.Inserted))
	r.ignored.Add(uint64(result.Ignored))
	return ""
}
