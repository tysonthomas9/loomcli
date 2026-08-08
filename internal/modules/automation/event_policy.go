package automation

import (
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// EventPolicy is Automation's canonical stateless event-provenance policy.
// Its method form satisfies the module's consumer-owned EventTrustPolicy port.
type EventPolicy struct{}

func (EventPolicy) EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID string) bool {
	return EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID)
}

// IsReservedSystemActorRef reports whether actor occupies the server-owned
// system namespace. External identities may not use these values.
func IsReservedSystemActorRef(actor string) bool {
	return actor == execution.RunFinishedActorRef || strings.HasPrefix(actor, execution.RunFinishedActorRef+":")
}

// IsTrustedRunFinished reports whether the complete provenance tuple belongs
// to one of Loom's two genuine run-outcome journal lanes: Execution's base
// event or Automation's optional internal copy.
func IsTrustedRunFinished(origin, sourceKind, actorRef, sourceEventID string) bool {
	return origin == string(EventOriginSystem) &&
		(sourceKind == execution.RunFinishedSourceKind || sourceKind == SourceKindInternal) &&
		actorRef == execution.RunFinishedActorRef &&
		strings.HasPrefix(sourceEventID, execution.RunFinishedSourceEventIDPrefix)
}

// EligibleForAwait prevents non-system sources from occupying Loom's reserved
// system actor namespace, then applies the stronger run.finished provenance
// rule everywhere an event can satisfy an await. This protects historical
// catch-up and live dispatch even when an old row predates admission policy.
func EligibleForAwait(eventType, origin, sourceKind, actorRef, sourceEventID string) bool {
	if origin != string(EventOriginSystem) && IsReservedSystemActorRef(actorRef) {
		return false
	}
	return eventType != execution.RunFinishedEventType || IsTrustedRunFinished(origin, sourceKind, actorRef, sourceEventID)
}

// EligibleForAdmission applies the same trust policy before matching,
// reservation, journal writes, or delivery dispatch.
func EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID string) bool {
	return EligibleForAwait(eventType, origin, sourceKind, actorRef, sourceEventID)
}
