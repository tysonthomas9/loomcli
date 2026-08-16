package automation

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// EventTrustPolicy is Automation's consumer-owned provenance gate. Admission
// invokes it before binding matching, reservation, or execution dispatch. The
// primitive tuple keeps the port independent of legacy Execution types while
// allowing composition to supply the one canonical platform policy.
type EventTrustPolicy interface {
	EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID string) bool
}

// BindingStore is Automation's consumer-owned persistence port for bindings.
// Implementations must enforce workspace scoping and route uniqueness.
type BindingStore interface {
	CreateBinding(ctx context.Context, binding *Binding) (*Binding, error)
	GetBinding(ctx context.Context, workspace, bindingID string) (*Binding, error)
	ListBindings(ctx context.Context, workspace string, filter BindingFilter) ([]*Binding, error)
}

// UnmanagedBindingSnapshot is the exact ordinary-binding generation and
// revision observed by an ordinary lifecycle command. The store must compare
// these fields and assert that the persisted target_agent_service_id is empty
// in the same atomic operation as the replacement/delete.
type UnmanagedBindingSnapshot struct {
	WorkspaceKey      string
	BindingID         string
	ExpectedRouteKey  string
	ExpectedCreatedAt time.Time
	ExpectedUpdatedAt time.Time
}

type UnmanagedBindingReplacement struct {
	Expected UnmanagedBindingSnapshot
	Binding  *Binding
}

// UnmanagedBindingStore prevents an ordinary command from crossing the
// agent-service ownership boundary after its initial read. It deliberately
// has no create operation: ordinary creation must carry an empty owner.
type UnmanagedBindingStore interface {
	ReplaceUnmanagedBinding(ctx context.Context, replacement UnmanagedBindingReplacement) (*Binding, error)
	DeleteUnmanagedBindingIfUnchanged(ctx context.Context, expected UnmanagedBindingSnapshot) error
}

// ManagedBindingSnapshot is the exact identity and revision a managed-binding
// command observed before constructing its replacement. Implementations must
// compare every field in the same atomic mutation as the write. CreatedAt
// prevents delete/recreate ABA, while UpdatedAt prevents retargeting a
// concurrent update.
type ManagedBindingSnapshot struct {
	WorkspaceKey                 string
	BindingID                    string
	ExpectedTargetAgentServiceID string
	ExpectedRouteKey             string
	ExpectedCreatedAt            time.Time
	ExpectedUpdatedAt            time.Time
}

// ManagedBindingReplacement is a full, validated replacement paired with the
// exact snapshot it was derived from. The store owns the committed UpdatedAt
// value; callers must use the returned binding.
type ManagedBindingReplacement struct {
	Expected ManagedBindingSnapshot
	Binding  *Binding
}

// ManagedBindingStore is deliberately separate from ordinary BindingStore.
// A managed replace/delete must fail closed when its exact owner, generation,
// or revision changed; implementations must never retry against a newer row.
type ManagedBindingStore interface {
	CreateManagedBinding(ctx context.Context, binding *Binding) (*Binding, error)
	ReplaceManagedBinding(ctx context.Context, replacement ManagedBindingReplacement) (*Binding, error)
	DeleteManagedBindingIfUnchanged(ctx context.Context, expected ManagedBindingSnapshot) error
}

// BindingMatchSnapshot is one optimistic, workspace-scoped view of enabled
// binding candidates for a route. ReserveEvent revalidates the exact revision
// atomically, so same-ID target/filter edits cannot slip between match and
// reservation.
type BindingMatchSnapshot struct {
	WorkspaceKey       string
	RouteKey           string
	BindingSetRevision uint64
	Bindings           []*Binding
}

// BindingMatcher is separate from CRUD/query persistence so admission cannot
// accidentally compose a list read without its optimistic revision.
type BindingMatcher interface {
	MatchBindings(ctx context.Context, workspace, routeKey string) (*BindingMatchSnapshot, error)
}

type EventReader interface {
	GetEvent(ctx context.Context, workspace, eventID string) (*Event, error)
	ListEvents(ctx context.Context, workspace string, filter EventFilter) ([]*Event, error)
}

// ApprovalEventStore appends one already-authorized session decision to the
// durable trigger-event journal without binding fanout. The legacy Store and
// FleetDB implementations satisfy this narrow port directly because Event is
// the canonical type behind the compatibility alias.
type ApprovalEventStore interface {
	AppendTriggerEvent(context.Context, *Event) (*Event, error)
}

type DeliveryReader interface {
	GetDelivery(ctx context.Context, workspace, deliveryID string) (*Delivery, error)
	ListDeliveries(ctx context.Context, workspace string, filter DeliveryFilter) ([]*Delivery, error)
}

// DispatchTarget is the immutable target snapshot reserved with a delivery.
// It lets an idempotent admission retry heal a crash after event reservation
// without re-matching bindings or resolving a different catalog version.
type DispatchTarget struct {
	DriverID             string
	DriverVersionID      string
	DriverRevision       uint64
	SourceDigest         string
	BundleDigest         string
	Entrypoint           string
	TargetAgentServiceID string
	SourceKind           string
	SourceRef            string
	BindingID            string
	ConcurrencyPolicy    BindingConcurrencyPolicy
	RetryMaxAttempts     int
	RetryBackoff         time.Duration
}

// CatalogGuard is the immutable catalog fact set that durable reservation
// revalidates atomically before committing an accepted delivery.
type CatalogGuard struct {
	BindingID      string
	DriverID       string
	VersionID      string
	DriverRevision uint64
	SourceDigest   string
	BundleDigest   string
}

// DeliveryReservation describes a new delivery committed at attempt 1. Only
// DeliveryRetryPort.ClaimDueDeliveries advances the attempt for a retry.
type DeliveryReservation struct {
	BindingID       string
	Status          DeliveryStatus
	SubjectKey      string
	RejectionReason string
	Target          *DispatchTarget
}

// EventReservation carries one event and all fan-out legs. EventID,
// DeliveryID, TriggerEventID, and ReceivedAt are empty; OccurredAt is also
// empty when ingress did not supply one. The durable store assigns missing
// timestamps atomically on first reservation, so omitted-time replay stays
// stable while an explicitly changed occurrence time conflicts.
type EventReservation struct {
	Event              *Event
	ReplayOnly         bool
	Deliveries         []DeliveryReservation
	Payload            json.RawMessage
	SubjectAttrs       map[string]string
	EpicID             string
	MatchedBindingIDs  []string
	BindingSetRevision uint64
	CatalogGuards      []CatalogGuard
	// Execution ownership is a fresh workflow-reservation precondition. It is
	// carried to Fleet for atomic fencing but excluded from immutable Fingerprint.
	ExecutionNodeID  string
	ExecutionLeaseID string
	ExecutionFence   int64
	Fingerprint      string
}

type ReservedDelivery struct {
	Delivery *Delivery
	Target   *DispatchTarget
}

type ReservationResult struct {
	Event        *Event
	Deliveries   []ReservedDelivery
	Payload      json.RawMessage
	SubjectAttrs map[string]string
	EpicID       string
	Replayed     bool
}

type DeliveryTransition struct {
	WorkspaceKey    string
	DeliveryID      string
	ExpectedStatus  DeliveryStatus
	ExpectedAttempt int
	IdempotencyKey  string
	Status          DeliveryStatus
	RejectionReason string
	DriverRunID     string
	Attempt         int
	NextRetryAt     *time.Time
	ErrorClass      string
}

// AdmissionStore owns the atomic event/idempotency reservation and delivery
// state transitions. ReserveEvent must be first-writer-wins by workspace and
// Event.IdempotencyKey; a key reused with another Fingerprint returns
// ErrConflict. ReplayOnly must perform no writes and returns
// ErrAdmissionReplayNotFound when no committed reservation exists.
type AdmissionStore interface {
	ReserveEvent(ctx context.Context, reservation EventReservation) (*ReservationResult, error)
	TransitionDelivery(ctx context.Context, transition DeliveryTransition) (*Delivery, error)
}

// ExecutionEmissionContext is re-derived by Execution from an
// ExecutionAuthority. In particular, callers cannot choose their emitting run,
// parent event, actor, epic, or fencing token through WorkflowEvent.
type ExecutionEmissionContext struct {
	WorkspaceKey  string
	RunID         string
	NodeID        string
	LeaseID       string
	ParentEventID string
	ActorRef      string
	EpicID        string
	FencingToken  int64
}

type ExecutionDispatchRequest struct {
	WorkspaceKey            string
	IdempotencyKey          string
	ReplayOnly              bool
	DeliveryID              string
	ExpectedDeliveryStatus  DeliveryStatus
	ExpectedDeliveryAttempt int
	DriverID                string
	DriverVersionID         string
	DriverRevision          uint64
	SourceDigest            string
	BundleDigest            string
	Entrypoint              string
	TargetAgentServiceID    string
	SourceKind              string
	SourceRef               string
	SubjectRef              string
	TriggerBindingID        string
	SubjectKey              string
	ConcurrencyPolicy       BindingConcurrencyPolicy
	EpicID                  string
	ActorRef                string
	RawPayloadRef           string
	Payload                 json.RawMessage
	SubjectAttrs            map[string]string
}

func validateExecutionDispatchRequest(request ExecutionDispatchRequest) error {
	if request.ReplayOnly {
		if strings.TrimSpace(request.DeliveryID) != "" || request.ExpectedDeliveryStatus != "" || request.ExpectedDeliveryAttempt != 0 ||
			strings.TrimSpace(request.TriggerBindingID) == "" || request.TriggerBindingID != strings.TrimSpace(request.TriggerBindingID) ||
			strings.TrimSpace(request.WorkspaceKey) == "" || request.WorkspaceKey != strings.TrimSpace(request.WorkspaceKey) ||
			strings.TrimSpace(request.IdempotencyKey) == "" || request.IdempotencyKey != strings.TrimSpace(request.IdempotencyKey) {
			return ErrInvalidPersistedState
		}
		return nil
	}
	deliveryID := strings.TrimSpace(request.DeliveryID)
	if deliveryID == "" {
		if request.ExpectedDeliveryStatus != "" || request.ExpectedDeliveryAttempt != 0 {
			return ErrInvalidPersistedState
		}
		return nil
	}
	if deliveryID != request.DeliveryID || !request.ExpectedDeliveryStatus.IsValid() || request.ExpectedDeliveryAttempt < 1 {
		return ErrInvalidPersistedState
	}
	return nil
}

type ExecutionDispatchResult struct {
	RunID       string
	RunSnapshot json.RawMessage
	Replayed    bool
	Busy        bool
	BusyRunID   string
	// Delivery is the authoritative committed FleetDB snapshot for every
	// successful reserved/retry dispatch. It is nil only for manual dispatch.
	Delivery *Delivery
}

// ExecutionPort is the only Automation-to-Execution dependency. Dispatch
// must be idempotent by WorkspaceKey and IdempotencyKey.
type ExecutionPort interface {
	EmissionContext(ctx context.Context, auth authority.ExecutionAuthority) (*ExecutionEmissionContext, error)
	Dispatch(ctx context.Context, request ExecutionDispatchRequest) (*ExecutionDispatchResult, error)
}

// EffectiveVersionAuthorityProvider supplies a narrow server-owned capability
// for Workflow Catalog's effective-version resolver. Automation never receives
// an Issuer and cannot mint authority itself.
type EffectiveVersionAuthorityProvider interface {
	AuthorityForEffectiveVersion(ctx context.Context, workspace, reason string) (authority.SystemAuthority, error)
}

// CronOccurrence is a durably claimed scheduled fire. OccurrenceID must be
// stable across claim retries so admission can derive one idempotency key.
type CronOccurrence struct {
	WorkspaceKey string
	BindingID    string
	RouteKey     string
	OccurrenceID string
	OccurredAt   time.Time
}

// CronClaim is one idempotent, reclaimable due-occurrence sweep. Exact replay
// by IdempotencyKey returns the original claim without claiming an occurrence
// twice. A crashed claim becomes eligible again no later than ClaimUntil.
type CronClaim struct {
	WorkspaceKey   string
	Before         time.Time
	ClaimUntil     time.Time
	IdempotencyKey string
	Limit          int
}

type CronCompletionStatus string

const (
	CronCompletionAdmitted CronCompletionStatus = "admitted"
	CronCompletionDropped  CronCompletionStatus = "dropped"
	CronCompletionFailed   CronCompletionStatus = "failed"
)

type CronCompletion struct {
	WorkspaceKey string
	BindingID    string
	OccurrenceID string
	Status       CronCompletionStatus
	ErrorClass   string
}

// CronSweepPort owns durable due-time claiming and completion bookkeeping;
// schedule evaluation/admission remains Automation policy.
type CronSweepPort interface {
	ClaimDueCron(ctx context.Context, claim CronClaim) ([]CronOccurrence, error)
	// CompleteCron is idempotent by workspace and occurrence ID. Admitted and
	// Dropped advance the schedule window; Failed releases or retains the same
	// occurrence for a later reclaim without advancing the window.
	CompleteCron(ctx context.Context, completion CronCompletion) error
}

// RetryCandidate contains everything reserved with a delivery that is needed
// to retry after restart without resolving a new target or reading a legacy
// trigger package.
type RetryCandidate struct {
	Delivery     *Delivery
	Target       *DispatchTarget
	Event        *Event
	Payload      json.RawMessage
	SubjectAttrs map[string]string
	EpicID       string
}

// DeliveryRetryPort atomically claims due delivery retries. A claim retains
// Failed/Held status, increments Attempt exactly once, and persists
// NextRetryAt as a reclaimable lease no later than claimUntil. Delivery state
// is finalized through AdmissionStore.TransitionDelivery using the claimed
// status and attempt as CAS preconditions.
type DeliveryRetryPort interface {
	ClaimDueDeliveries(ctx context.Context, workspace string, before, claimUntil time.Time, limit int) ([]RetryCandidate, error)
}
