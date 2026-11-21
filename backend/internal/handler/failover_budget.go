package handler

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

var (
	errFailoverBudget         = errors.New("failover request budget exhausted")
	errFailoverAccountTimeout = errors.New("failover account timeout")
	errFailoverSameAccount    = errors.New("failover same-account budget exhausted")
	errFailoverHeaderTimeout  = errors.New("failover response header timeout")
	errFailoverFirstOutput    = errors.New("failover first output timeout")
)

// FailoverOutcomeError carries the P3 terminal result for the P5 response mapper.
// Attempts counts outbound sends, including in-service retries.
type FailoverOutcomeError struct {
	Reason     string
	Attempts   int
	LastStatus int
}

func (e *FailoverOutcomeError) Error() string { return "failover " + e.Reason + " exhausted" }

const failoverReasonBudget = "budget"

// FailoverBudget is shared by all forwards in a gateway request, including a
// fallback group. Its clock starts after the first concurrency slot is acquired.
// The inbound request context is never assigned a failover deadline.
type FailoverBudget struct {
	config              config.FailoverBudgetConfig
	firstOutputTimeout  time.Duration
	mu                  sync.Mutex
	started             bool
	deadline            time.Time
	clockStopped        bool
	accounts            map[int64]failoverAccountBudget
	attempts            int
	sameAccountAttempts int
	lastStatus          int
}

type failoverAccountBudget struct {
	firstAttempt time.Time
	attempts     int
}

func newFailoverBudget(policy config.GatewayFailoverPolicyConfig) *FailoverBudget {
	cfg := policy.Budget
	if !policy.Enabled || (cfg.TotalTimeout <= 0 && cfg.PerAccountTimeout <= 0 &&
		cfg.MaxTotalUpstreamAttempts <= 0 && cfg.MaxSameAccountAttemptsTotal <= 0 && policy.FirstOutputTimeoutSeconds <= 0) {
		return nil
	}
	return &FailoverBudget{
		config:             cfg,
		firstOutputTimeout: time.Duration(policy.FirstOutputTimeoutSeconds) * time.Second,
		accounts:           make(map[int64]failoverAccountBudget),
	}
}

func newResolvedFailoverBudget(policy *service.FailoverPolicy) *FailoverBudget {
	var p config.GatewayFailoverPolicyConfig
	p.Enabled = !policy.Legacy()
	if policy.BudgetTotalTimeout != nil {
		p.Budget.TotalTimeout = *policy.BudgetTotalTimeout
	}
	if policy.BudgetPerAccountTimeout != nil {
		p.Budget.PerAccountTimeout = *policy.BudgetPerAccountTimeout
	}
	if policy.BudgetMaxTotalUpstreamAttempts != nil {
		p.Budget.MaxTotalUpstreamAttempts = *policy.BudgetMaxTotalUpstreamAttempts
	}
	if policy.BudgetMaxSameAccountAttemptsTotal != nil {
		p.Budget.MaxSameAccountAttemptsTotal = *policy.BudgetMaxSameAccountAttemptsTotal
	}
	if policy.BudgetMinSwitchCost != nil {
		p.Budget.MinSwitchCost = *policy.BudgetMinSwitchCost
	}
	if policy.BudgetStopClockOnStream != nil {
		p.Budget.StopClockOnStream = *policy.BudgetStopClockOnStream
	}
	if policy.FirstOutputTimeoutSeconds != nil {
		p.FirstOutputTimeoutSeconds = *policy.FirstOutputTimeoutSeconds
	}
	return newFailoverBudget(p)
}

func (b *FailoverBudget) StartClock(now time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return
	}
	b.started = true
	b.deadline = deadlineAfter(now, b.config.TotalTimeout)
}

func deadlineAfter(now time.Time, duration time.Duration) time.Time {
	if duration <= 0 {
		return time.Time{}
	}
	return now.Add(duration)
}

// Remaining returns whether a total-time constraint is active, as well as its
// remaining duration. Turning off the clock leaves attempt caps in force.
func (b *FailoverBudget) Remaining(now time.Time) (time.Duration, bool) {
	if b == nil {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clockStopped || b.deadline.IsZero() {
		return 0, false
	}
	return b.deadline.Sub(now), true
}

func (b *FailoverBudget) canAttemptLocked(now time.Time) bool {
	if b.config.MaxTotalUpstreamAttempts > 0 && b.attempts >= b.config.MaxTotalUpstreamAttempts {
		return false
	}
	return b.clockStopped || b.deadline.IsZero() || now.Before(b.deadline)
}

func (b *FailoverBudget) CanAttempt(now time.Time) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.canAttemptLocked(now)
}

func (b *FailoverBudget) CanSwitch(now time.Time) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.canAttemptLocked(now) {
		return false
	}
	return b.clockStopped || b.deadline.IsZero() || b.deadline.Sub(now) >= b.config.MinSwitchCost
}

func (b *FailoverBudget) accountDenialLocked(account failoverAccountBudget, now time.Time) error {
	if account.attempts == 0 {
		return nil
	}
	if b.config.MaxSameAccountAttemptsTotal > 0 && b.sameAccountAttempts >= b.config.MaxSameAccountAttemptsTotal {
		return errFailoverSameAccount
	}
	if !b.clockStopped && b.config.PerAccountTimeout > 0 && !now.Before(account.firstAttempt.Add(b.config.PerAccountTimeout)) {
		return errFailoverAccountTimeout
	}
	return nil
}

func (b *FailoverBudget) allowsAccount(accountID int64, now time.Time) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.accountDenialLocked(b.accounts[accountID], now) == nil
}

func (b *FailoverBudget) outcome() *FailoverOutcomeError {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &FailoverOutcomeError{Reason: failoverReasonBudget, Attempts: b.attempts, LastStatus: b.lastStatus}
}

// budgetAttempt owns the outbound cancellation timer. HTTP headers and first
// semantic output are distinct milestones. A mutex serializes state changes with
// timer expiry so a stopped timer cannot cancel a stream later.
type budgetAttempt struct {
	budget                              *FailoverBudget
	ctx                                 context.Context
	cancel                              context.CancelCauseFunc
	mu                                  sync.Mutex
	timer                               *time.Timer
	total, account, header, firstOutput time.Time
	closed                              bool
	onTimeout                           context.CancelCauseFunc
	stopParent                          func() bool
	semantic                            bool
}

// beginAttempt must be called directly before an actual HTTP send. Local
// validation, selection and an attempted retry rejected here consume no count.
func (b *FailoverBudget) beginAttempt(parent context.Context, accountID int64, now time.Time, headerTimeout time.Duration, stream bool) (*budgetAttempt, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.canAttemptLocked(now) {
		return nil, errFailoverBudget
	}
	account := b.accounts[accountID]
	if err := b.accountDenialLocked(account, now); err != nil {
		return nil, err
	}
	if account.attempts > 0 {
		b.sameAccountAttempts++
	} else {
		account.firstAttempt = now
	}
	account.attempts++
	b.accounts[accountID] = account
	b.attempts++
	ctx, cancel := context.WithCancelCause(parent)
	a := &budgetAttempt{budget: b, ctx: ctx, cancel: cancel, header: deadlineAfter(now, headerTimeout)}
	if !b.clockStopped {
		a.total = b.deadline
		a.account = deadlineAfter(account.firstAttempt, b.config.PerAccountTimeout)
	}
	if stream {
		a.firstOutput = deadlineAfter(now, b.firstOutputTimeout)
	}
	a.mu.Lock()
	a.scheduleLocked(now)
	a.mu.Unlock()
	return a, nil
}

func (a *budgetAttempt) nextDeadlineLocked() (time.Time, error) {
	var earliest time.Time
	var cause error
	for _, limit := range [...]struct {
		at    time.Time
		cause error
	}{
		{a.total, errFailoverBudget}, {a.account, errFailoverAccountTimeout},
		{a.header, errFailoverHeaderTimeout}, {a.firstOutput, errFailoverFirstOutput},
	} {
		if !limit.at.IsZero() && (earliest.IsZero() || limit.at.Before(earliest)) {
			earliest, cause = limit.at, limit.cause
		}
	}
	return earliest, cause
}

func (a *budgetAttempt) scheduleLocked(now time.Time) {
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	at, cause := a.nextDeadlineLocked()
	if at.IsZero() || a.closed || a.ctx.Err() != nil {
		return
	}
	if !now.Before(at) {
		a.expireLocked(cause)
		return
	}
	// A callback from a retired timer may run after Stop. Recheck identity
	// under the same lock used by header/stream transitions.
	var timer *time.Timer
	timer = time.AfterFunc(at.Sub(now), func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.timer == timer {
			a.scheduleLocked(time.Now())
		}
	})
	a.timer = timer
}

func (a *budgetAttempt) expireLocked(cause error) {
	if a.ctx.Err() == nil {
		a.cancel(cause)
		if a.onTimeout != nil {
			a.onTimeout(cause)
		}
	}
}

func (a *budgetAttempt) headersReceived(status int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if at, cause := a.nextDeadlineLocked(); !at.IsZero() && !now.Before(at) {
		a.expireLocked(cause)
	}
	if status >= http.StatusBadRequest {
		a.budget.mu.Lock()
		a.budget.lastStatus = status
		a.budget.mu.Unlock()
	}
	a.header = time.Time{}
	a.scheduleLocked(now)
}

// semanticOutput transitions a streaming attempt out of the pre-output phase.
// It races with timer expiry under the lock before the first semantic write.
func (a *budgetAttempt) semanticOutput() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if at, cause := a.nextDeadlineLocked(); !at.IsZero() && !now.Before(at) {
		a.expireLocked(cause)
	}
	if cause := context.Cause(a.ctx); cause != nil && (!a.closed || isFailoverBudgetError(cause)) {
		return cause
	}
	a.semantic = true
	if a.stopParent != nil {
		a.stopParent()
		a.stopParent = nil
	}
	a.firstOutput = time.Time{}
	a.account = time.Time{}
	if a.budget.config.StopClockOnStream {
		a.total = time.Time{}
		a.budget.mu.Lock()
		a.budget.clockStopped = true
		a.budget.mu.Unlock()
	}
	a.scheduleLocked(now)
	return nil
}

func (a *budgetAttempt) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.stopParent != nil {
		a.stopParent()
		a.stopParent = nil
	}
	a.cancel(nil)
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
}

// budgetForward owns the pre-output phase of one Forward call, including local
// retry backoff. Individual send timers additionally cover headers and output.
type budgetForward struct {
	state         *FailoverState
	observation   *failoverObservation
	parent        context.Context
	phase         *budgetAttempt
	latest        *budgetAttempt
	rejection     error
	headerTimeout time.Duration
	stream        bool
	semantic      bool
}

func (s *FailoverState) beginForward(ctx context.Context, c *gin.Context, stream bool, headerTimeout time.Duration) (context.Context, func() error) {
	f := &budgetForward{state: s, stream: stream, headerTimeout: headerTimeout, observation: requestFailoverObservation(c), parent: ctx}
	if s.Budget == nil {
		if s.SwitchCount == 0 && f.observation == nil && service.FailoverSideEffectSinkFrom(ctx) == nil {
			return ctx, noFailoverBudgetError
		}
		return service.WithFailoverAttemptObserver(ctx, f.beginSend), noFailoverBudgetError
	}
	b := s.Budget
	now := time.Now()
	b.StartClock(now)
	phaseCtx, cancel := context.WithCancelCause(ctx)
	f.phase = &budgetAttempt{budget: b, ctx: phaseCtx, cancel: cancel}
	if remaining, active := b.Remaining(now); active {
		f.phase.total = now.Add(remaining)
	}
	f.phase.mu.Lock()
	f.phase.scheduleLocked(now)
	f.phase.mu.Unlock()
	original := c.Writer
	c.Writer = &failoverBudgetWriter{ResponseWriter: original, forward: f}
	return service.WithFailoverAttemptObserver(phaseCtx, f.beginSend), func() error {
		c.Writer = original
		err := f.failure()
		f.phase.close()
		return err
	}
}

func noFailoverBudgetError() error { return nil }

func (f *budgetForward) beginSend(ctx context.Context, accountID int64) (service.FailoverHTTPAttempt, error) {
	var result service.FailoverHTTPAttempt
	if f.phase != nil {
		if err := f.phase.ctx.Err(); err != nil {
			return result, context.Cause(f.phase.ctx)
		}
		a, err := f.state.Budget.beginAttempt(ctx, accountID, time.Now(), f.headerTimeout, f.stream)
		if err != nil {
			f.rejection = err
			f.phase.cancel(err)
			return result, err
		}
		a.mu.Lock()
		a.onTimeout = f.phase.cancel
		if cause := context.Cause(a.ctx); isFailoverBudgetError(cause) {
			f.phase.cancel(cause)
		}
		accountDeadline := a.account
		// Most streaming transports detach for billing drain. Until the first
		// semantic output, still stop that detached send if the client leaves.
		a.stopParent = context.AfterFunc(f.phase.ctx, func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if !a.semantic && !a.closed {
				a.cancel(context.Cause(f.phase.ctx))
			}
		})
		a.mu.Unlock()
		f.latest = a
		f.phase.mu.Lock()
		f.phase.account = accountDeadline
		f.phase.scheduleLocked(time.Now())
		f.phase.mu.Unlock()
		result = service.FailoverHTTPAttempt{Context: a.ctx, Headers: a.headersReceived, Close: a.close}
	}
	if f.parent != nil && f.parent.Err() != nil {
		if result.Close != nil {
			result.Close()
		}
		return service.FailoverHTTPAttempt{}, f.parent.Err()
	}
	service.FailoverSideEffectSinkFrom(ctx).CommitCrossAccount(ctx, accountID)
	if f.state.retryAccountID == accountID {
		f.state.retryAccountID = 0
	}
	f.state.RecordSelectedAccount(accountID)
	result.Result = f.observation.beginSend(accountID)
	if f.observation != nil && f.observation.cost != nil {
		send := f.observation.cost.begin(f.observation.attempts, accountID)
		observedResult := result.Result
		result.Result = func(resp *http.Response, err error) {
			send.result(resp, err)
			observedResult(resp, err)
		}
		result.BodyEnd = send.end
	}
	isolationResult := gatewayIsolationFrom(ctx).onSend(accountID)
	if isolationResult != nil {
		observedResult := result.Result
		result.Result = func(resp *http.Response, err error) {
			isolationResult(resp, err)
			if observedResult != nil {
				observedResult(resp, err)
			}
		}
	}
	return result, nil
}

func isFailoverBudgetError(err error) bool {
	return errors.Is(err, errFailoverBudget) || errors.Is(err, errFailoverAccountTimeout) ||
		errors.Is(err, errFailoverSameAccount) || errors.Is(err, errFailoverHeaderTimeout) ||
		errors.Is(err, errFailoverFirstOutput)
}

func (f *budgetForward) failure() error {
	if isFailoverBudgetError(f.rejection) {
		return f.rejection
	}
	if cause := context.Cause(f.phase.ctx); isFailoverBudgetError(cause) {
		return cause
	}
	if f.latest != nil {
		if cause := context.Cause(f.latest.ctx); isFailoverBudgetError(cause) {
			return cause
		}
	}
	return nil
}

type failoverBudgetWriter struct {
	gin.ResponseWriter
	forward *budgetForward
}

func (w *failoverBudgetWriter) beforeWrite(payload []byte) error {
	if err := w.forward.failure(); err != nil {
		return err
	}
	f := w.forward
	if f.stream && !f.semantic && f.latest != nil && w.Status() < http.StatusBadRequest &&
		service.ClassifyUpstreamWriteChunk(payload) == service.UpstreamWriteSemantic {
		if err := f.latest.semanticOutput(); err != nil {
			return err
		}
		if err := f.phase.semanticOutput(); err != nil {
			return err
		}
		f.semantic = true
	}
	return nil
}

func (w *failoverBudgetWriter) Write(p []byte) (int, error) {
	if err := w.beforeWrite(p); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}

func (w *failoverBudgetWriter) WriteString(s string) (int, error) {
	if err := w.beforeWrite([]byte(s)); err != nil {
		return 0, err
	}
	return w.ResponseWriter.WriteString(s)
}

func (w *failoverBudgetWriter) Flush() {
	if w.forward.failure() == nil {
		w.ResponseWriter.Flush()
	}
}

func (w *failoverBudgetWriter) WriteHeaderNow() {
	if w.forward.failure() == nil {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (s *FailoverState) applyForwardBudgetError(err, budgetErr error) error {
	if budgetErr == nil {
		return err
	}
	var upstreamErr *service.UpstreamFailoverError
	if errors.As(err, &upstreamErr) && (!upstreamErr.ShouldRetryNextAccount() || upstreamErr.BoundUpstreamPassthrough) {
		return err
	}
	status := s.Budget.outcome().LastStatus
	if status == 0 {
		status = http.StatusGatewayTimeout
	}
	return &service.UpstreamFailoverError{StatusCode: status, Scope: service.GatewayFailureScopeRequest}
}
