package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// ErrFailoverPolicyReaderUnavailable reports that a real group could not be
// looked up because no reader was configured.
var ErrFailoverPolicyReaderUnavailable = errors.New("group failover policy reader unavailable")

type failoverRequestPolicyKey struct{}

// FailoverPolicySnapshot identifies the resolved policy for a routing group.
// GroupID is zero for routes without a group. Source is group or global.
type FailoverPolicySnapshot struct {
	GroupID int64
	Source  string
	policy  FailoverPolicy
}

// Policy returns a copy so one consumer cannot change the request snapshot
// used by classification, budgeting or scheduling.
func (s FailoverPolicySnapshot) Policy() FailoverPolicy { return cloneRequestFailoverPolicy(s.policy) }

// FailoverRequestPolicy memoizes group policy resolutions within one request.
type FailoverRequestPolicy struct {
	reader GroupFailoverPolicyReader
	global FailoverPolicy
	mu     sync.Mutex
	groups map[int64]*failoverGroupPolicyEntry
}

type failoverGroupPolicyEntry struct {
	done     chan struct{}
	snapshot FailoverPolicySnapshot
	err      error
}

// WithFailoverRequestPolicy installs a fresh request cache. Install it once,
// then share the derived context for initial routing and authorized fallback.
// The global base is frozen here, including any compatibility decisions made
// by the caller before installation.
func WithFailoverRequestPolicy(ctx context.Context, reader GroupFailoverPolicyReader, global FailoverPolicy) context.Context {
	return context.WithValue(ctx, failoverRequestPolicyKey{}, &FailoverRequestPolicy{
		reader: reader,
		global: cloneRequestFailoverPolicy(global),
		groups: make(map[int64]*failoverGroupPolicyEntry),
	})
}

// FailoverRequestPolicyFromContext retrieves the shared cache. Nil means the
// entry point has not installed the request policy.
func FailoverRequestPolicyFromContext(ctx context.Context) *FailoverRequestPolicy {
	if ctx == nil {
		return nil
	}
	policy, _ := ctx.Value(failoverRequestPolicyKey{}).(*FailoverRequestPolicy)
	return policy
}

// Resolve returns the snapshot for the effective, already authorized routing
// group. Group authorization belongs to the caller. A nil or nonpositive ID
// uses the global base. A missing group is a successful global fallback under
// the reader contract. Read failures return the global snapshot with a wrapped
// error and freeze that result for this group. Cancellation is returned as an
// error even on cache hits and never poisons the group's cache.
func (p *FailoverRequestPolicy) Resolve(ctx context.Context, groupID *int64) (FailoverPolicySnapshot, error) {
	var id int64
	if groupID != nil && *groupID > 0 {
		id = *groupID
	}
	fallback := FailoverPolicySnapshot{GroupID: id, Source: FailoverPolicySourceGlobal, policy: p.global}
	if err := ctx.Err(); err != nil {
		return fallback, err
	}
	if id == 0 {
		return fallback, nil
	}
	if p.reader == nil {
		return fallback, ErrFailoverPolicyReaderUnavailable
	}

	p.mu.Lock()
	if entry := p.groups[id]; entry != nil {
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return fallback, ctx.Err()
		case <-entry.done:
			if err := ctx.Err(); err != nil {
				return fallback, err
			}
			return entry.snapshot, entry.err
		}
	}
	entry := &failoverGroupPolicyEntry{done: make(chan struct{}), snapshot: fallback}
	p.groups[id] = entry
	p.mu.Unlock()

	row, err := p.reader.LoadGroupFailoverPolicy(ctx, id)
	if err == nil && row != nil && row.GroupID != 0 && row.GroupID != id {
		err = fmt.Errorf("reader returned group %d", row.GroupID)
	}
	if err == nil && row != nil {
		effective, source := ResolveFailoverPolicy(nil, row, p.global)
		entry.snapshot = FailoverPolicySnapshot{GroupID: id, Source: source, policy: cloneRequestFailoverPolicy(effective)}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	if err != nil {
		entry.snapshot = fallback
		entry.err = fmt.Errorf("resolve failover policy for group %d: %w", id, err)
	}
	p.mu.Lock()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		delete(p.groups, id)
	}
	close(entry.done)
	p.mu.Unlock()
	return entry.snapshot, entry.err
}

func cloneFailoverPolicyValue[T any](ptr *T) *T {
	if ptr == nil {
		return nil
	}
	value := *ptr
	return &value
}

// ResolveFailoverPolicy shares pointers with its inputs. Detach those values
// when storing or handing a policy out from the request cache.
func cloneRequestFailoverPolicy(p FailoverPolicy) FailoverPolicy {
	p.Enabled = cloneFailoverPolicyValue(p.Enabled)
	p.NetworkErrorsEnabled = cloneFailoverPolicyValue(p.NetworkErrorsEnabled)
	p.FailoverOn400 = cloneFailoverPolicyValue(p.FailoverOn400)
	p.SameAccountDefaultMode = cloneFailoverPolicyValue(p.SameAccountDefaultMode)
	p.BudgetTotalTimeout = cloneFailoverPolicyValue(p.BudgetTotalTimeout)
	p.BudgetPerAccountTimeout = cloneFailoverPolicyValue(p.BudgetPerAccountTimeout)
	p.BudgetMaxTotalUpstreamAttempts = cloneFailoverPolicyValue(p.BudgetMaxTotalUpstreamAttempts)
	p.BudgetMaxSameAccountAttemptsTotal = cloneFailoverPolicyValue(p.BudgetMaxSameAccountAttemptsTotal)
	p.BudgetMinSwitchCost = cloneFailoverPolicyValue(p.BudgetMinSwitchCost)
	p.BudgetStopClockOnStream = cloneFailoverPolicyValue(p.BudgetStopClockOnStream)
	p.FirstOutputTimeoutSeconds = cloneFailoverPolicyValue(p.FirstOutputTimeoutSeconds)
	p.CooldownEnabled = cloneFailoverPolicyValue(p.CooldownEnabled)
	p.CooldownConsecutiveFailuresThreshold = cloneFailoverPolicyValue(p.CooldownConsecutiveFailuresThreshold)
	p.CooldownStreakWindow = cloneFailoverPolicyValue(p.CooldownStreakWindow)
	p.CooldownDefaultTTL = cloneFailoverPolicyValue(p.CooldownDefaultTTL)
	p.CooldownMaxTTL = cloneFailoverPolicyValue(p.CooldownMaxTTL)
	p.CooldownPoolModeEnabled = cloneFailoverPolicyValue(p.CooldownPoolModeEnabled)
	p.CooldownProbeEnabled = cloneFailoverPolicyValue(p.CooldownProbeEnabled)
	p.CooldownProbeMaxAttempts = cloneFailoverPolicyValue(p.CooldownProbeMaxAttempts)
	p.CooldownRedisTimeout = cloneFailoverPolicyValue(p.CooldownRedisTimeout)
	p.CooldownFailOpen = cloneFailoverPolicyValue(p.CooldownFailOpen)
	p.CooldownBatchReadMax = cloneFailoverPolicyValue(p.CooldownBatchReadMax)
	p.SwitchOrderRecordChain = cloneFailoverPolicyValue(p.SwitchOrderRecordChain)
	p.SwitchOrderClearStickyOnAccountFailure = cloneFailoverPolicyValue(p.SwitchOrderClearStickyOnAccountFailure)
	p.MaxSelectionRetries = cloneFailoverPolicyValue(p.MaxSelectionRetries)
	p.MaxAccountSwitches = cloneFailoverPolicyValue(p.MaxAccountSwitches)
	p.SwitchableUseSemanticBytes = cloneFailoverPolicyValue(p.SwitchableUseSemanticBytes)
	p.SwitchableTreatKeepaliveAsSafe = cloneFailoverPolicyValue(p.SwitchableTreatKeepaliveAsSafe)
	p.IdempotencyRequireKeyForSideEffecting = cloneFailoverPolicyValue(p.IdempotencyRequireKeyForSideEffecting)
	p.T3OnlyExtend = cloneFailoverPolicyValue(p.T3OnlyExtend)
	p.ExposeFailoverDetail = cloneFailoverPolicyValue(p.ExposeFailoverDetail)
	p.TerminalMixedStatus503 = cloneFailoverPolicyValue(p.TerminalMixedStatus503)
	p.CostAttemptsEnabled = cloneFailoverPolicyValue(p.CostAttemptsEnabled)
	if p.Compat400Keywords != nil {
		keywords := slices.Clone(*p.Compat400Keywords)
		p.Compat400Keywords = &keywords
	}
	if p.IdempotencyBlockedTools != nil {
		tools := slices.Clone(*p.IdempotencyBlockedTools)
		p.IdempotencyBlockedTools = &tools
	}
	if p.StatusClasses != nil {
		rules := slices.Clone(*p.StatusClasses)
		for i := range rules {
			rule := &rules[i]
			rule.Codes = slices.Clone(rule.Codes)
			rule.Ranges = slices.Clone(rule.Ranges)
			if advice := cloneFailoverPolicyValue(rule.Advice); advice != nil {
				advice.Failover = cloneFailoverPolicyValue(advice.Failover)
				advice.SameAccount = cloneFailoverPolicyValue(advice.SameAccount)
				advice.Cooldown = cloneFailoverPolicyValue(advice.Cooldown)
				advice.CooldownSource = cloneFailoverPolicyValue(advice.CooldownSource)
				advice.TempUnschedule = cloneFailoverPolicyValue(advice.TempUnschedule)
				advice.ClearSticky = cloneFailoverPolicyValue(advice.ClearSticky)
				advice.Scope = cloneFailoverPolicyValue(advice.Scope)
				rule.Advice = advice
			}
		}
		p.StatusClasses = &rules
	}
	return p
}
