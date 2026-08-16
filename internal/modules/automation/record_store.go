package automation

import (
	"context"
	"strings"
	"time"
)

type TriggerBindingCreate struct {
	WorkspaceKey         string
	BindingID            string
	Name                 string
	SourceKind           string
	SourceRef            string
	SourceConfigRef      string
	RouteKey             string
	Method               string
	PathTemplate         string
	Topic                string
	EventTypePatterns    []string
	FilterRef            string
	DriverID             string
	DriverVersionID      string
	TargetEntrypoint     string
	TargetAgentServiceID string
	ConcurrencyPolicy    BindingConcurrencyPolicy
	IdempotencyPolicy    string
	AuthPolicy           string
	SubjectKeyTemplate   string
	ActorFilter          *ActorFilter
	RetryMaxAttempts     int
	RetryBackoffSeconds  int
	Schedule             string
	ScheduleTimezone     string
	Permissions          []string
	Enabled              bool
}

// CronSourceKind is the trigger-binding source kind swept by the cron scheduler
// (trigger.CronSourceKind aliases it). Its bindings fire by schedule, not by an
// external event route.
const CronSourceKind = "cron"

// InternalSourceKind is the trigger-binding source kind that fires off loopback
// internal events (the issue-journal bridge's internal.task.ready, run.finished,
// etc.). Like cron it has no external route the caller must supply: it matches
// events by event_type_patterns, so its route_key is a derived, unique 1:1
// address (WithDerivedRoute) rather than a shared event route — otherwise two
// pattern-matched siblings on the same event (e.g. a planner and a coder both
// bound to internal.task.ready) would collide on the exact-owner route slot.
const InternalSourceKind = "internal"

// DefaultBindingID derives a binding's id from its route key when the caller
// did not pick one. The id is wire-visible (a cron binding's derived route is
// "cron:<binding_id>"), so every create surface (CLI, webui) must share this
// derivation.
func DefaultBindingID(routeKey string) string {
	return "binding-" + strings.ReplaceAll(routeKey, ".", "-")
}

// WithDerivedRoute fills a cron or internal binding's route_key from its
// (unique) binding_id when the caller left it empty. route_key is a binding's
// internal 1:1 routing address — the scheduler stamps it on each cron.tick and
// the router resolves it via GetByRouteKey — but neither a scheduled binding
// (fires by schedule) nor an internal-event binding (matches by
// event_type_patterns) has an external route to own, so deriving it from
// binding_id keeps every such binding's address unique without callers
// hand-picking a shared, collision-prone route string. The prefix records the
// source kind ("cron:" / "internal:") and never collides with a real event route
// (those use dots, e.g. internal.task.ready). Applied by every store Create so
// all callers (webui, CLI) get it uniformly.
func (in TriggerBindingCreate) WithDerivedRoute() TriggerBindingCreate {
	if in.RouteKey != "" || in.BindingID == "" {
		return in
	}
	switch in.SourceKind {
	case CronSourceKind:
		in.RouteKey = "cron:" + in.BindingID
	case InternalSourceKind:
		in.RouteKey = "internal:" + in.BindingID
	}
	return in
}

type TriggerBindingFilter struct {
	SourceKind           string
	RouteKey             string
	DriverID             string
	TargetAgentServiceID string
	Enabled              *bool
	Limit                int
}

type TriggerBindingUpdate struct {
	Name                 *string
	SourceKind           *string
	SourceRef            *string
	SourceConfigRef      *string
	RouteKey             *string
	Method               *string
	PathTemplate         *string
	Topic                *string
	EventTypePatterns    *[]string
	FilterRef            *string
	DriverID             *string
	DriverVersionID      *string
	TargetEntrypoint     *string
	TargetAgentServiceID *string
	ConcurrencyPolicy    *BindingConcurrencyPolicy
	IdempotencyPolicy    *string
	AuthPolicy           *string
	SubjectKeyTemplate   *string
	// ActorFilter replaces the whole filter when set; a zero-valued filter
	// (no constraints) clears it, mirroring fleet-db's patch semantics.
	ActorFilter         *ActorFilter
	RetryMaxAttempts    *int
	RetryBackoffSeconds *int
	Schedule            *string
	ScheduleTimezone    *string
	Permissions         *[]string
	Enabled             *bool
}

type TriggerBindingStore interface {
	Create(ctx context.Context, in TriggerBindingCreate) (*Binding, error)
	Get(ctx context.Context, workspaceKey, bindingID string) (*Binding, error)
	GetByRouteKey(ctx context.Context, workspaceKey, routeKey string) (*Binding, error)
	List(ctx context.Context, workspaceKey string, filter TriggerBindingFilter) ([]*Binding, error)
	Update(ctx context.Context, workspaceKey, bindingID string, patch TriggerBindingUpdate) (*Binding, error)
	// Delete removes a binding. Deleting is deliberately separate from grant
	// revocation (Decision 6): the caller revokes the binding's connector grants
	// so no credentials outlive it. A missing binding wraps persistence.ErrNotFound.
	Delete(ctx context.Context, workspaceKey, bindingID string) error
}

// TriggerEventFilter narrows TriggerEvent listings.
type TriggerEventFilter struct {
	SourceKind       string
	TriggerBindingID string
	// SubjectRef is the immutable subject association stamped at event
	// admission (for issue journal events, "issue:<task-id>"). Exact matching
	// lets read models join an event back to its task without inspecting the
	// event payload.
	SubjectRef string
	Limit      int
}

// TriggerEventStore is a read-only view of persisted trigger events.
type TriggerEventStore interface {
	Get(ctx context.Context, workspaceKey, eventID string) (*Event, error)
	List(ctx context.Context, workspaceKey string, filter TriggerEventFilter) ([]*Event, error)
}

// TriggerEventAppender is an OPTIONAL TriggerEventStore capability (detected
// by type assertion, like DriverRunEventsReader): append one trusted,
// server-attested event directly to the trigger-event journal without route
// dispatch. The base Execution runtime requires this for run.finished (AW6):
// journal-first so the await
// registration scan (AwaitStore.RegisterAwaitAndCheck) sees terminal runs
// even when no binding listens on the internal route — composition awaits can
// never be suppressed by binding configuration or the loop guard.
//
// Implementations preserve the caller's EventID (lifecycle event IDs are
// deterministic for idempotent re-emission) and dedup on both EventID and
// IdempotencyKey, returning the existing record unchanged on a replay. The
// Production FleetDB exposes this capability only through its service-auth
// producer route; human bearer requests cannot forge event provenance.
type TriggerEventAppender interface {
	AppendTriggerEvent(ctx context.Context, event *Event) (*Event, error)
}

// TriggerDeliveryFilter narrows TriggerDelivery listings.
type TriggerDeliveryFilter struct {
	TriggerEventID   string
	TriggerBindingID string
	Status           DeliveryStatus
	Limit            int
}

// TriggerDeliveryDueFilter narrows TriggerDeliveryStore.ListDue. A delivery
// is due when it is retry-sweeper work — held (queue-policy promotion rides
// the sweeper) or failed without error class retries_exhausted — and its
// NextRetryAt is nil (immediately due) or not after Now. A zero Now means
// the implementation's current time.
type TriggerDeliveryDueFilter struct {
	Now   time.Time
	Limit int
}

// TriggerDeliveryResultUpdate is the input to
// TriggerDeliveryStore.UpdateResult after one retry-sweeper (or dispatch)
// attempt. Status failed with a NextRetryAt reschedules the delivery; held
// keeps it held for queue promotion; dispatched/rejected/superseded (and
// failed once the binding's RetryMaxAttempts is reached) make it final.
// A zero Attempt keeps the stored attempt count.
type TriggerDeliveryResultUpdate struct {
	Status      DeliveryStatus
	Attempt     int
	NextRetryAt *time.Time
	ErrorClass  string
	// DriverRunID stamps the admitted run when a retry finally dispatches.
	DriverRunID string
}

// TriggerDeliveryStore reads persisted trigger deliveries and records
// Automation retry outcomes. Deliveries themselves are created only by
// Automation admission, never directly through this read/update projection.
type TriggerDeliveryStore interface {
	Get(ctx context.Context, workspaceKey, deliveryID string) (*Delivery, error)
	List(ctx context.Context, workspaceKey string, filter TriggerDeliveryFilter) ([]*Delivery, error)
	// ListDue returns deliveries awaiting the retry sweeper whose due time
	// is <= filter.Now, in due order (earliest first).
	ListDue(ctx context.Context, workspaceKey string, filter TriggerDeliveryDueFilter) ([]*Delivery, error)
	// UpdateResult records one attempt outcome. A failed result whose
	// Attempt reaches the binding's RetryMaxAttempts is forced terminal:
	// status stays failed, ErrorClass becomes
	// TriggerDeliveryErrorRetriesExhausted and NextRetryAt clears.
	// Final deliveries (dispatched, rejected, duplicate, superseded,
	// replayed, terminal failed) reject transitions to a different status
	// with persistence.ErrInvalidTransition; re-applying the same status is
	// idempotent.
	UpdateResult(ctx context.Context, workspaceKey, deliveryID string, update TriggerDeliveryResultUpdate) (*Delivery, error)
}
