package domain

import "time"

// DrainDecision classifies an agent's drain metadata against the supervisor
// that is asking. A drain is one-shot: it is honored only while it is
// addressed to the currently running supervisor and still inside its TTL.
// That is what makes `draining` different from `stopped`, which stays an
// indefinite, explicit park with no TTL and no supersession.
type DrainDecision int

const (
	// DrainNotApplicable means desired_state is not "draining", so no drain
	// is in effect and the drain metadata carries no meaning.
	DrainNotApplicable DrainDecision = iota
	// DrainActive means the drain is addressed to the asking supervisor (or
	// carries only a TTL) and has not expired.
	DrainActive
	// DrainExpired means the drain carried a deadline that has passed.
	DrainExpired
	// DrainSuperseded means the drain was addressed to a different supervisor
	// node than the one asking — i.e. the supervisor it was issued to is gone.
	DrainSuperseded
	// DrainUntargeted means desired_state is "draining" but neither a node ID
	// nor an expiry was ever stamped, so the drain cannot be attributed to any
	// supervisor. Both a just-issued yield (not yet stamped) and every agent
	// parked by a pre-one-shot-drain release have this shape.
	DrainUntargeted
)

// String renders the decision for logs and test failures.
func (d DrainDecision) String() string {
	switch d {
	case DrainNotApplicable:
		return "not_applicable"
	case DrainActive:
		return "active"
	case DrainExpired:
		return "expired"
	case DrainSuperseded:
		return "superseded"
	case DrainUntargeted:
		return "untargeted"
	default:
		return "unknown"
	}
}

// ResolveDrain classifies a drain. It is pure and total: it performs no I/O,
// reads no clock of its own, and returns a decision for every input.
//
// The order of the checks is load-bearing. Supersession is tested before
// expiry so a drain belonging to a dead supervisor reads as superseded even
// when it also happens to have expired — the two are cleared alike, but the
// superseded reason is the one worth logging. An empty currentNodeID (the
// caller could not resolve its own identity) never produces DrainSuperseded,
// because "I don't know who I am" is not evidence that the drain belongs to
// someone else.
func ResolveDrain(desired AgentDesiredState, drainNodeID string, drainExpiresAt *time.Time, currentNodeID string, now time.Time) DrainDecision {
	if desired != AgentDesiredDraining {
		return DrainNotApplicable
	}
	if drainNodeID == "" && drainExpiresAt == nil {
		return DrainUntargeted
	}
	if drainNodeID != "" && currentNodeID != "" && drainNodeID != currentNodeID {
		return DrainSuperseded
	}
	if drainExpiresAt != nil && !now.Before(*drainExpiresAt) {
		return DrainExpired
	}
	return DrainActive
}

// DrainParks reports whether a decision should keep the agent out of
// supervision. It is evaluated on every supervision decision.
//
// DrainUntargeted parks: a yield issued moments ago may not have been stamped
// yet (a sub-second race), and silently supervising through that window would
// regress `yield` outright.
func DrainParks(d DrainDecision) bool {
	switch d {
	case DrainActive, DrainUntargeted:
		return true
	default:
		return false
	}
}

// DrainClearableAtStartup reports whether a decision should have its drain
// metadata cleared during startup reconciliation. It runs once, at daemon
// start, and never on a reconcile tick.
//
// DrainUntargeted is clearable here even though DrainParks parks on it: a
// freshly started daemon has no reason to honor a drain it cannot attribute
// to any supervisor, and every agent parked by a pre-change yield has exactly
// this shape — so the first start after deploy releases the parked fleet by
// itself. Collapsing this predicate into DrainParks would reintroduce either
// the permanent park or a silent regression of `yield`.
func DrainClearableAtStartup(d DrainDecision) bool {
	switch d {
	case DrainUntargeted, DrainSuperseded, DrainExpired:
		return true
	default:
		return false
	}
}
