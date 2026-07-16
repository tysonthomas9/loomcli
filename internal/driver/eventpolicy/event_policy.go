// Package eventpolicy owns the legacy Execution provenance policy while that
// capability remains on its pre-module migration path. Consumers depend on
// their own narrow ports; serve composition injects Policy where required.
package eventpolicy

import "strings"

const (
	RunFinishedEventType           = "run.finished"
	RunFinishedActorRef            = "system"
	RunFinishedSourceEventIDPrefix = "run-finished:"
	SourceKindExecution            = "execution"
	SourceKindInternal             = "internal"
	OriginSystem                   = "system"
)

// Policy is the canonical stateless event-provenance policy. Its method form
// satisfies consumer-owned ports without making those consumers import this
// legacy implementation package.
type Policy struct{}

func (Policy) EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID string) bool {
	return EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID)
}

// IsReservedSystemActorRef reports whether actor occupies the server-owned
// system namespace. External identities may not use these values.
func IsReservedSystemActorRef(actor string) bool {
	return actor == RunFinishedActorRef || strings.HasPrefix(actor, RunFinishedActorRef+":")
}

// IsTrustedRunFinished reports whether the complete provenance tuple belongs
// to one of Loom's two genuine run-outcome journal lanes: Execution's base
// event or Automation's optional internal copy.
func IsTrustedRunFinished(origin, sourceKind, actorRef, sourceEventID string) bool {
	return origin == OriginSystem &&
		(sourceKind == SourceKindExecution || sourceKind == SourceKindInternal) &&
		actorRef == RunFinishedActorRef &&
		strings.HasPrefix(sourceEventID, RunFinishedSourceEventIDPrefix)
}

// EligibleForAwait prevents non-system sources from occupying Loom's reserved
// system actor namespace, then applies the stronger run.finished provenance
// rule everywhere an event can satisfy an await. This protects historical
// catch-up and live dispatch even when an old row predates admission policy.
func EligibleForAwait(eventType, origin, sourceKind, actorRef, sourceEventID string) bool {
	if origin != OriginSystem && IsReservedSystemActorRef(actorRef) {
		return false
	}
	return eventType != RunFinishedEventType || IsTrustedRunFinished(origin, sourceKind, actorRef, sourceEventID)
}

// EligibleForAdmission applies the same trust policy before matching,
// reservation, journal writes, or delivery dispatch.
func EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID string) bool {
	return EligibleForAwait(eventType, origin, sourceKind, actorRef, sourceEventID)
}
