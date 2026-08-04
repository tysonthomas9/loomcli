package automation

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	DefaultTriggerRetryMaxAttempts    = 5
	DefaultTriggerRetryBackoffSeconds = 30
	DefaultEventHopDepthCap           = 4

	// Short aliases keep command logic readable while the Trigger-prefixed
	// names preserve domain-model compatibility.
	DefaultRetryMaxAttempts    = DefaultTriggerRetryMaxAttempts
	DefaultRetryBackoffSeconds = DefaultTriggerRetryBackoffSeconds

	SourceKindCron     = "cron"
	SourceKindInternal = "internal"

	SignatureStatusVerified = "verified"
	SignatureStatusInternal = "internal"
	SignatureStatusSession  = "session"

	DropReasonHopDepthExceeded = "hop_depth_exceeded"
	RejectionReasonActorFilter = "actor_filtered"
	RejectionConcurrencyForbid = "concurrency_forbid"

	DeliveryErrorDispatchFailed          = "execution_dispatch_failed"
	TriggerDeliveryErrorRetriesExhausted = "retries_exhausted"
	DeliveryErrorRetriesExhausted        = TriggerDeliveryErrorRetriesExhausted
)

// ActorFilter scopes which event actors a binding reacts to. Exclusions win
// over allow-list entries.
type ActorFilter struct {
	ExcludeActorKinds []string `json:"exclude_actor_kinds,omitempty"`
	AllowActors       []string `json:"allow_actors,omitempty"`
}

func (f *ActorFilter) IsZero() bool {
	return f == nil || (len(f.ExcludeActorKinds) == 0 && len(f.AllowActors) == 0)
}

func (f *ActorFilter) Clone() *ActorFilter { return cloneActorFilter(f) }

type BindingConcurrencyPolicy string

const (
	ConcurrencyAllow            BindingConcurrencyPolicy = "allow"
	ConcurrencyForbid           BindingConcurrencyPolicy = "forbid"
	ConcurrencyReplace          BindingConcurrencyPolicy = "replace"
	ConcurrencyQueue            BindingConcurrencyPolicy = "queue"
	ConcurrencyOneActivePerEpic BindingConcurrencyPolicy = "one_active_per_epic"
)

// Binding is Automation's canonical trigger-binding model. Its JSON field
// names intentionally retain the existing Loom/FleetDB wire contract.
type Binding struct {
	WorkspaceKey         string                   `json:"workspace_key"`
	BindingID            string                   `json:"binding_id"`
	Name                 string                   `json:"name"`
	SourceKind           string                   `json:"source_kind"`
	SourceRef            string                   `json:"source_ref,omitempty"`
	SourceConfigRef      string                   `json:"source_config_ref,omitempty"`
	RouteKey             string                   `json:"route_key,omitempty"`
	Method               string                   `json:"method,omitempty"`
	PathTemplate         string                   `json:"path_template,omitempty"`
	Topic                string                   `json:"topic,omitempty"`
	EventTypePatterns    []string                 `json:"event_type_patterns,omitempty"`
	FilterRef            string                   `json:"filter_ref,omitempty"`
	DriverID             string                   `json:"driver_id"`
	DriverVersionID      string                   `json:"driver_version_id"`
	TargetEntrypoint     string                   `json:"target_entrypoint,omitempty"`
	TargetAgentServiceID string                   `json:"target_agent_service_id,omitempty"`
	ConcurrencyPolicy    BindingConcurrencyPolicy `json:"concurrency_policy"`
	IdempotencyPolicy    string                   `json:"idempotency_policy,omitempty"`
	AuthPolicy           string                   `json:"auth_policy,omitempty"`
	// WebhookSecret is retained only as a redacted compatibility projection.
	// Automation rejects nonempty values; secret ownership stays in Connectors
	// and the webhook-ingestion workflow.
	WebhookSecret       string       `json:"webhook_secret,omitempty"`
	SubjectKeyTemplate  string       `json:"subject_key_template,omitempty"`
	ActorFilter         *ActorFilter `json:"actor_filter,omitempty"`
	RetryMaxAttempts    int          `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds int          `json:"retry_backoff_seconds,omitempty"`
	Schedule            string       `json:"schedule,omitempty"`
	ScheduleTimezone    string       `json:"schedule_timezone,omitempty"`
	Permissions         []string     `json:"permissions,omitempty"`
	Enabled             bool         `json:"enabled"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

type EventOrigin string

const (
	EventOriginExternal EventOrigin = "external"
	EventOriginWorkflow EventOrigin = "workflow"
	EventOriginSystem   EventOrigin = "system"
)

// Event is Automation's canonical durable trigger-event model.
type Event struct {
	WorkspaceKey     string            `json:"workspace_key"`
	EventID          string            `json:"event_id"`
	TriggerBindingID string            `json:"trigger_binding_id,omitempty"`
	SourceKind       string            `json:"source_kind"`
	SourceEventID    string            `json:"source_event_id,omitempty"`
	EventType        string            `json:"event_type"`
	RouteKey         string            `json:"route_key,omitempty"`
	SubjectRef       string            `json:"subject_ref,omitempty"`
	ActorRef         string            `json:"actor_ref,omitempty"`
	EmittingRunID    string            `json:"emitting_run_id,omitempty"`
	ParentEventID    string            `json:"parent_event_id,omitempty"`
	EpicID           string            `json:"epic_id,omitempty"`
	Origin           EventOrigin       `json:"origin,omitempty"`
	HopDepth         int               `json:"hop_depth,omitempty"`
	OccurredAt       time.Time         `json:"occurred_at"`
	ReceivedAt       time.Time         `json:"received_at"`
	IdempotencyKey   string            `json:"idempotency_key,omitempty"`
	RawPayloadRef    string            `json:"raw_payload_ref,omitempty"`
	RawPayloadDigest string            `json:"raw_payload_digest,omitempty"`
	SignatureStatus  string            `json:"signature_status,omitempty"`
	ReplayOfEventID  string            `json:"replay_of_event_id,omitempty"`
	Payload          json.RawMessage   `json:"payload,omitempty"`
	SubjectAttrs     map[string]string `json:"subject_attrs,omitempty"`
}

// CanonicalEventID returns the source identity used by await replay and live
// notification, plus whether both stored identity fields are already in
// canonical form. Admission must reject whitespace-only or padded identities
// rather than letting consumers trim them differently.
func (e *Event) CanonicalEventID() (string, bool) {
	if e == nil || e.EventID == "" || strings.TrimSpace(e.EventID) != e.EventID {
		return "", false
	}
	if e.SourceEventID != "" && strings.TrimSpace(e.SourceEventID) != e.SourceEventID {
		return "", false
	}
	if e.SourceEventID != "" {
		return e.SourceEventID, true
	}
	return e.EventID, true
}

// NormalizeProvenance retains read compatibility with events written before
// structural provenance was introduced.
func (e *Event) NormalizeProvenance() {
	if e != nil && e.Origin == "" {
		e.Origin = EventOriginExternal
	}
}

type DeliveryStatus string

const (
	DeliveryAccepted   DeliveryStatus = "accepted"
	DeliveryRejected   DeliveryStatus = "rejected"
	DeliveryDuplicate  DeliveryStatus = "duplicate"
	DeliveryQueued     DeliveryStatus = "queued"
	DeliveryDispatched DeliveryStatus = "dispatched"
	DeliveryFailed     DeliveryStatus = "failed"
	DeliveryReplayed   DeliveryStatus = "replayed"
	DeliverySuperseded DeliveryStatus = "superseded"
	DeliveryHeld       DeliveryStatus = "held"
)

func (s DeliveryStatus) IsValid() bool {
	switch s {
	case DeliveryAccepted, DeliveryRejected, DeliveryDuplicate, DeliveryQueued,
		DeliveryDispatched, DeliveryFailed, DeliveryReplayed, DeliverySuperseded,
		DeliveryHeld:
		return true
	default:
		return false
	}
}

// Delivery is Automation's canonical durable trigger-delivery model.
type Delivery struct {
	WorkspaceKey     string         `json:"workspace_key"`
	DeliveryID       string         `json:"delivery_id"`
	TriggerEventID   string         `json:"trigger_event_id"`
	TriggerBindingID string         `json:"trigger_binding_id"`
	Status           DeliveryStatus `json:"status"`
	SubjectKey       string         `json:"subject_key,omitempty"`
	RejectionReason  string         `json:"rejection_reason,omitempty"`
	DriverRunID      string         `json:"driver_run_id,omitempty"`
	// The target and retry fields are the immutable snapshot reserved for
	// restart-safe delivery retry. They are additive optional wire fields.
	DriverID             string                   `json:"driver_id,omitempty"`
	DriverVersionID      string                   `json:"driver_version_id,omitempty"`
	TargetEntrypoint     string                   `json:"target_entrypoint,omitempty"`
	TargetAgentServiceID string                   `json:"target_agent_service_id,omitempty"`
	SourceKind           string                   `json:"source_kind,omitempty"`
	ConcurrencyPolicy    BindingConcurrencyPolicy `json:"concurrency_policy,omitempty"`
	RetryMaxAttempts     int                      `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds  int                      `json:"retry_backoff_seconds,omitempty"`
	Attempt              int                      `json:"attempt"`
	NextRetryAt          *time.Time               `json:"next_retry_at,omitempty"`
	ErrorClass           string                   `json:"error_class,omitempty"`
	CreatedAt            time.Time                `json:"created_at"`
	UpdatedAt            time.Time                `json:"updated_at"`
}

func cloneBinding(in *Binding) *Binding {
	if in == nil {
		return nil
	}
	out := *in
	out.EventTypePatterns = append([]string(nil), in.EventTypePatterns...)
	out.Permissions = append([]string(nil), in.Permissions...)
	out.ActorFilter = cloneActorFilter(in.ActorFilter)
	return &out
}

func cloneActorFilter(in *ActorFilter) *ActorFilter {
	if in == nil {
		return nil
	}
	return &ActorFilter{
		ExcludeActorKinds: append([]string(nil), in.ExcludeActorKinds...),
		AllowActors:       append([]string(nil), in.AllowActors...),
	}
}

func cloneEvent(in *Event) *Event {
	if in == nil {
		return nil
	}
	out := *in
	out.Payload = cloneRawMessage(in.Payload)
	out.SubjectAttrs = cloneStringMap(in.SubjectAttrs)
	return &out
}

func cloneDelivery(in *Delivery) *Delivery {
	if in == nil {
		return nil
	}
	out := *in
	if in.NextRetryAt != nil {
		next := *in.NextRetryAt
		out.NextRetryAt = &next
	}
	return &out
}
