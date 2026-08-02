package agenterr

import "github.com/olesho/harness-wrapper/pkg/wrapper"

// DomainOutcome is the loom-domain half of an Outcome: failure modes that
// are NOT harness output and therefore cannot be classified by the wrapper
// (no agent subprocess produced them). They are decided by loom's own
// coordination logic — task claiming, epic exhaustion, spawn mechanics,
// backend-binary availability.
type DomainOutcome int

const (
	DomainNone                DomainOutcome = iota
	NoWorkOutcome                           // no claimable task / epic exhausted
	LockConflictOutcome                     // fleet-db task locked by another agent
	SpawnFailureOutcome                     // supervisor could not exec the agent subprocess
	BackendUnavailableOutcome               // backend CLI binary not on PATH (folded from wrapper ErrBinaryNotFound)
	IncompleteRunOutcome                    // agent exited 0 but never released its task claim (turn ended before the task did)
)

func (d DomainOutcome) String() string {
	switch d {
	case NoWorkOutcome:
		return "NoWork"
	case LockConflictOutcome:
		return "LockConflict"
	case SpawnFailureOutcome:
		return "SpawnFailure"
	case BackendUnavailableOutcome:
		return "BackendUnavailable"
	case IncompleteRunOutcome:
		return "IncompleteRun"
	default:
		return "None"
	}
}

// Outcome is the carrier the decision policy keys on: a single value that
// is EITHER a harness-output class (owned by the wrapper) OR a loom-domain
// outcome (owned by loom). Exactly one side is meaningful for an error; the
// zero value Outcome{ErrNone, DomainNone} means "no error / clean success",
// which callers handle before consulting policy.
//
// It exists because a harness class and a domain outcome must travel through
// the same AgentError.Class field, telemetry, and policy lookup, but the
// wrapper cannot represent the domain outcomes.
type Outcome struct {
	Harness wrapper.ErrorClass
	Domain  DomainOutcome
}

// OutcomeFromHarness wraps a wrapper-classified harness error.
func OutcomeFromHarness(c wrapper.ErrorClass) Outcome { return Outcome{Harness: c} }

// OutcomeFromDomain wraps a loom-domain outcome.
func OutcomeFromDomain(d DomainOutcome) Outcome { return Outcome{Domain: d} }

// IsDomain reports whether this Outcome carries a loom-domain outcome.
func (o Outcome) IsDomain() bool { return o.Domain != DomainNone }

// IsHarness reports whether this Outcome carries a harness-output class.
func (o Outcome) IsHarness() bool { return o.Harness != wrapper.ErrNone }

// Is reports whether this Outcome is the given domain outcome.
func (o Outcome) Is(d DomainOutcome) bool { return o.Domain == d }

// IsClass reports whether this Outcome is the given harness class.
func (o Outcome) IsClass(c wrapper.ErrorClass) bool { return o.Harness == c }

// String returns the canonical wire/display name, preserving the existing
// serialized strings (e.g. "AuthFailure", "NoWork") so daemon-agents.json
// last_error_class, events, and checkpoints stay byte-stable. A domain
// outcome wins when set; otherwise the harness class' canonical name.
func (o Outcome) String() string {
	if o.IsDomain() {
		return o.Domain.String()
	}
	return o.Harness.String()
}
