// Package agentpolicy is the single source of truth for "what do we DO
// about this error" across loom's three decision layers (in-invocation
// retry, the auto-loop, and the daemon supervisor). It maps an
// agenterr.Outcome (a wrapper harness class OR a loom-domain outcome) to a
// Disposition. The layers keep their own attempt counters and configured
// backoff values; they consult this table only for the verdict + the
// backoff bucket to use.
package agentpolicy

import (
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// Decision is the action a layer takes for an Outcome.
type Decision int

const (
	Retry          Decision = iota // restart; counts toward the layer's retry budget
	RetryUncounted                 // restart; does NOT erode the budget (rate-limit, no-work)
	Block                          // budget exhausted: fixed-interval re-attempt instead of giving up
	Failover                       // try the next configured backend
	FastFail                       // deterministic failure: stop now, surface as failed
	StopFatal                      // auth/billing: stop; needs human intervention
)

func (d Decision) String() string {
	switch d {
	case Retry:
		return "Retry"
	case RetryUncounted:
		return "RetryUncounted"
	case Block:
		return "Block"
	case Failover:
		return "Failover"
	case FastFail:
		return "FastFail"
	case StopFatal:
		return "StopFatal"
	default:
		return "Unknown"
	}
}

// BackoffProfile names the configured backoff bucket a layer should read
// (its initial/max/interval values stay in restart_policy / automode config).
// It is the SHAPE selector, not concrete durations.
type BackoffProfile int

const (
	BPDefault            BackoffProfile = iota // exponential: getBackoffInitial/getBackoffMax
	BPTimeout                                  // exponential: getTimeoutBackoff
	BPRateLimit                                // fixed + hint: getRateLimitBackoff/getRateLimitMaxWait
	BPNoWork                                   // fixed: getNoWorkBackoff
	BPBackendUnavailable                       // fixed recheck: backendRecheckBackoff
	BPBlock                                    // fixed: maxRetriesBlockBackoff
	BPClaimsHeld                               // fixed recheck: claimHoldRecheckBackoff
)

// Disposition is the policy verdict for an Outcome.
type Disposition struct {
	Decision Decision
	Backoff  BackoffProfile
	// HonorHint: prefer the wrapper's RetryAfter/ResumeAt over the schedule.
	HonorHint bool
	// OnExhaustion: what a counted Retry (or a budgeted Failover) becomes
	// once its budget is spent.
	OnExhaustion Decision
	// BlockBudget: for OnExhaustion==Block, the number of block cycles WITHOUT
	// progress before escalating to FastFail. 0 = block indefinitely.
	BlockBudget int
	// FailoverAfter: for RetryUncounted, fail over once the uncounted-retry
	// counter EXCEEDS this (e.g. 3 ⇒ failover on the 4th observation). 0 = never.
	FailoverAfter int
}

// defaultBlockBudget caps how long an ambiguous/deterministic-leaning class
// (Unknown, spawn/lock failures) blocks without making progress before it
// escalates to FastFail — so a deterministic crash does not block forever.
const defaultBlockBudget = 3

// Decide returns the Disposition for an Outcome. Domain outcomes win when
// set; otherwise the harness class governs. The zero Outcome (clean success)
// should be handled by callers before Decide; it falls through to a
// conservative Retry here.
func Decide(o agenterr.Outcome) Disposition {
	if o.IsDomain() {
		return decideDomain(o.Domain)
	}
	return decideHarness(o.Harness)
}

func decideHarness(c wrapper.ErrorClass) Disposition {
	switch c {
	case wrapper.ErrAuth, wrapper.ErrBilling:
		return Disposition{Decision: StopFatal}
	case wrapper.ErrModelNotFound:
		// A wrong model never self-heals: fail over to a fallback backend,
		// and once those are exhausted, fast-fail — never block.
		return Disposition{Decision: Failover, Backoff: BPDefault, OnExhaustion: FastFail}
	case wrapper.ErrContextOverflow:
		return Disposition{Decision: FastFail}
	case wrapper.ErrRateLimited:
		return Disposition{Decision: RetryUncounted, Backoff: BPRateLimit, HonorHint: true, FailoverAfter: 3, OnExhaustion: RetryUncounted}
	case wrapper.ErrTimeout:
		return Disposition{Decision: Retry, Backoff: BPTimeout, HonorHint: true, OnExhaustion: Block, BlockBudget: 0}
	case wrapper.ErrTransient:
		return Disposition{Decision: Retry, Backoff: BPDefault, HonorHint: true, OnExhaustion: Block, BlockBudget: 0}
	case wrapper.ErrUnknown:
		// Ambiguous (transient blip OR deterministic crash): block capped, so a
		// crash that never makes progress escalates to FastFail.
		return Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}
	default: // ErrNone / unrecognized: conservative bounded restart
		return Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}
	}
}

// QuarantineEligible reports whether an Outcome counts toward TASK-level
// quarantine (repeated no-progress kills of the same task, across agents).
// Declared here in the policy seat; counted by the supervisor (mirrors how
// BlockBudget is declared in the table and counted via ap.BlockCount).
//
// Eligible: the classes a watchdog/ownership kill of a silently-stalled
// backend actually produces via the exit-code fallback (-1 → Unknown,
// 137 → Timeout, 143 → Transient), plus ContextOverflow (FastFails the
// agent, but the task returns to open and boomerangs across siblings).
// Not eligible: domain outcomes (coordination signals, not task-fault),
// RateLimited (backend-wide, not task-specific), and Auth/Billing/
// ModelNotFound (operator-actionable; the agent stops anyway).
func QuarantineEligible(o agenterr.Outcome) bool {
	if o.IsDomain() {
		return false
	}
	switch o.Harness {
	case wrapper.ErrUnknown, wrapper.ErrTimeout, wrapper.ErrTransient, wrapper.ErrContextOverflow:
		return true
	default:
		return false
	}
}

func decideDomain(d agenterr.DomainOutcome) Disposition {
	switch d {
	case agenterr.NoWorkOutcome:
		return Disposition{Decision: RetryUncounted, Backoff: BPNoWork}
	case agenterr.BackendUnavailableOutcome:
		return Disposition{Decision: Block, Backoff: BPBackendUnavailable}
	case agenterr.LockConflictOutcome:
		return Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}
	case agenterr.SpawnFailureOutcome:
		return Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}
	case agenterr.CompletionHookFailureOutcome:
		// The turn itself succeeded; only the supervisor's issue write failed.
		// Transient write errors clear on retry, and a permanent one (bad label
		// permission) must still stop rather than spin — so bounded counted
		// retry with the default backoff, then Block.
		return Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}
	case agenterr.ClaimsHeldOutcome:
		// A claim hold is an operator decision to stop STARTING work, not a
		// failure of the agent, the task or the backend. Nothing may move:
		// the agent re-checks on a fixed interval and resumes exactly where
		// the fleet left off once the hold is released or expires.
		return Disposition{Decision: RetryUncounted, Backoff: BPClaimsHeld}
	case agenterr.IncompleteRunOutcome:
		// The turn ended before the task did. Retrying is the right move — the
		// worktree, the checkpoint and (across a daemon restart) the session id
		// are all preserved for it — but it is a COUNTED retry: an agent that
		// keeps handing back unfinished turns without ever signaling
		// completion is the exact spiral max_retries exists to catch, and the
		// clean-success reset would otherwise zero the budget every cycle.
		// Bounded block escalation (defaultBlockBudget) so a task that can
		// never be finished surfaces as failed instead of consuming turns
		// forever; a run that does complete resets the budget as usual.
		return Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}
	default:
		return Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}
	}
}
