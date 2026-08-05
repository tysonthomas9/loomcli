package automation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionCreateBinding         authority.Action = "automation.create-binding"
	ActionUpdateBinding         authority.Action = "automation.update-binding"
	ActionEnableBinding         authority.Action = "automation.enable-binding"
	ActionDisableBinding        authority.Action = "automation.disable-binding"
	ActionDeleteBinding         authority.Action = "automation.delete-binding"
	ActionCreateManagedBinding  authority.Action = "automation.create-managed-binding"
	ActionUpdateManagedBinding  authority.Action = "automation.update-managed-binding"
	ActionEnableManagedBinding  authority.Action = "automation.enable-managed-binding"
	ActionDisableManagedBinding authority.Action = "automation.disable-managed-binding"
	ActionDeleteManagedBinding  authority.Action = "automation.delete-managed-binding"
	ActionEnsureManagedBinding  authority.Action = "automation.ensure-managed-binding"
	ActionAdmitEvent            authority.Action = "automation.admit-event"
	ActionDispatchBinding       authority.Action = "automation.dispatch-binding"
	ActionSweepCron             authority.Action = "automation.sweep-cron"
	ActionRetryDeliveries       authority.Action = "automation.retry-deliveries"
)

// API is the complete public Automation surface for the Phase 3 core.
type API interface {
	BindingCommands
	ManagedBindingCommands
	ProvisioningBindingCommands
	BindingQueries
	EventQueries
	DeliveryQueries
	EventAdmission
	ManualDispatch
	RuntimeCommands
}

// BindingOperations is the complete management surface needed by Automation
// transports. Keeping it here lets composition pass one narrow capability
// without exposing event admission or runtime commands.
type BindingOperations interface {
	BindingCommands
	ManagedBindingCommands
	ProvisioningBindingCommands
	BindingQueries
	ManualDispatch
}

// ProvisioningBindingCommands is the exact system-only convergence surface
// used by AgentProvisioning. It cannot update or adopt a divergent binding.
type ProvisioningBindingCommands interface {
	EnsureManagedBinding(context.Context, authority.SystemAuthority, EnsureManagedBindingCommand) (*Binding, error)
}

// AuditQueries is the read-only Event/Delivery surface used by audit HTTP
// routes. It intentionally has no mutation or admission methods.
type AuditQueries interface {
	EventQueries
	DeliveryQueries
}

type BindingCommands interface {
	CreateBinding(ctx context.Context, auth authority.OperatorAuthority, command CreateBindingCommand) (*Binding, error)
	UpdateBinding(ctx context.Context, auth authority.OperatorAuthority, command UpdateBindingCommand) (*Binding, error)
	EnableBinding(ctx context.Context, auth authority.OperatorAuthority, command BindingCommand) (*Binding, error)
	DisableBinding(ctx context.Context, auth authority.OperatorAuthority, command BindingCommand) (*Binding, error)
	DeleteBinding(ctx context.Context, auth authority.OperatorAuthority, command BindingCommand) error
}

// ManagedBindingCommands is the only mutation surface for bindings attached
// to an AgentService record. Every command carries the expected owning service
// ID, and the core verifies that exact identity before writing. Ordinary
// BindingCommands continue to reject managed bindings.
type ManagedBindingCommands interface {
	CreateManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command CreateManagedBindingCommand) (*Binding, error)
	UpdateManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command UpdateManagedBindingCommand) (*Binding, error)
	EnableManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command ManagedBindingCommand) (*Binding, error)
	DisableManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command ManagedBindingCommand) (*Binding, error)
	DeleteManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command ManagedBindingCommand) error
}

type BindingQueries interface {
	GetBinding(ctx context.Context, workspace, bindingID string) (*Binding, error)
	ListBindings(ctx context.Context, workspace string, filter BindingFilter) ([]*Binding, error)
}

type EventQueries interface {
	GetEvent(ctx context.Context, workspace, eventID string) (*Event, error)
	ListEvents(ctx context.Context, workspace string, filter EventFilter) ([]*Event, error)
}

type DeliveryQueries interface {
	GetDelivery(ctx context.Context, workspace, deliveryID string) (*Delivery, error)
	ListDeliveries(ctx context.Context, workspace string, filter DeliveryFilter) ([]*Delivery, error)
}

type EventAdmission interface {
	AdmitEvent(ctx context.Context, auth EventAuthority, command AdmitEventCommand) (*AdmissionResult, error)
}

type ManualDispatch interface {
	DispatchBinding(ctx context.Context, auth authority.OperatorAuthority, command DispatchBindingCommand) (*DispatchBindingResult, error)
}

// RuntimeCommands is invoked only by registered server runtime components.
// Implementations live separately from the core API/model contracts.
type RuntimeCommands interface {
	SweepCron(ctx context.Context, auth authority.SystemAuthority, command SweepCronCommand) (*SweepCronResult, error)
	RetryDeliveries(ctx context.Context, auth authority.SystemAuthority, command RetryDeliveriesCommand) (*RetryDeliveriesResult, error)
}

// BindingDefinition contains every caller-controlled field on a binding. The
// service owns workspace, timestamps, retry defaults, and activated-version
// resolution.
type BindingDefinition struct {
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
	DriverVersionID      string                   `json:"driver_version_id,omitempty"`
	TargetEntrypoint     string                   `json:"target_entrypoint,omitempty"`
	TargetAgentServiceID string                   `json:"target_agent_service_id,omitempty"`
	ConcurrencyPolicy    BindingConcurrencyPolicy `json:"concurrency_policy,omitempty"`
	IdempotencyPolicy    string                   `json:"idempotency_policy,omitempty"`
	AuthPolicy           string                   `json:"auth_policy,omitempty"`
	SubjectKeyTemplate   string                   `json:"subject_key_template,omitempty"`
	ActorFilter          *ActorFilter             `json:"actor_filter,omitempty"`
	RetryMaxAttempts     int                      `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds  int                      `json:"retry_backoff_seconds,omitempty"`
	Schedule             string                   `json:"schedule,omitempty"`
	ScheduleTimezone     string                   `json:"schedule_timezone,omitempty"`
	Permissions          []string                 `json:"permissions,omitempty"`
	Enabled              bool                     `json:"enabled"`
}

type CreateBindingCommand struct {
	WorkspaceKey string            `json:"workspace_key"`
	Definition   BindingDefinition `json:"binding"`
}

type CreateManagedBindingCommand struct {
	WorkspaceKey   string            `json:"workspace_key"`
	AgentServiceID string            `json:"agent_service_id"`
	Definition     BindingDefinition `json:"binding"`
}

type EnsureManagedBindingCommand struct {
	RequestID      string            `json:"request_id"`
	WorkspaceKey   string            `json:"workspace_key"`
	AgentServiceID string            `json:"agent_service_id"`
	Definition     BindingDefinition `json:"binding"`
}

// BindingPatch uses pointers so omitted and explicit zero values remain
// distinct on update.
type BindingPatch struct {
	Name                 *string                   `json:"name,omitempty"`
	SourceKind           *string                   `json:"source_kind,omitempty"`
	SourceRef            *string                   `json:"source_ref,omitempty"`
	SourceConfigRef      *string                   `json:"source_config_ref,omitempty"`
	RouteKey             *string                   `json:"route_key,omitempty"`
	Method               *string                   `json:"method,omitempty"`
	PathTemplate         *string                   `json:"path_template,omitempty"`
	Topic                *string                   `json:"topic,omitempty"`
	EventTypePatterns    *[]string                 `json:"event_type_patterns,omitempty"`
	FilterRef            *string                   `json:"filter_ref,omitempty"`
	DriverID             *string                   `json:"driver_id,omitempty"`
	DriverVersionID      *string                   `json:"driver_version_id,omitempty"`
	TargetEntrypoint     *string                   `json:"target_entrypoint,omitempty"`
	TargetAgentServiceID *string                   `json:"target_agent_service_id,omitempty"`
	ConcurrencyPolicy    *BindingConcurrencyPolicy `json:"concurrency_policy,omitempty"`
	IdempotencyPolicy    *string                   `json:"idempotency_policy,omitempty"`
	AuthPolicy           *string                   `json:"auth_policy,omitempty"`
	SubjectKeyTemplate   *string                   `json:"subject_key_template,omitempty"`
	ActorFilter          *ActorFilter              `json:"actor_filter,omitempty"`
	ClearActorFilter     bool                      `json:"clear_actor_filter,omitempty"`
	RetryMaxAttempts     *int                      `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds  *int                      `json:"retry_backoff_seconds,omitempty"`
	Schedule             *string                   `json:"schedule,omitempty"`
	ScheduleTimezone     *string                   `json:"schedule_timezone,omitempty"`
	Permissions          *[]string                 `json:"permissions,omitempty"`
}

type UpdateBindingCommand struct {
	WorkspaceKey string       `json:"workspace_key"`
	BindingID    string       `json:"binding_id"`
	Patch        BindingPatch `json:"patch"`
}

type UpdateManagedBindingCommand struct {
	WorkspaceKey   string       `json:"workspace_key"`
	BindingID      string       `json:"binding_id"`
	AgentServiceID string       `json:"agent_service_id"`
	Patch          BindingPatch `json:"patch"`
}

type BindingCommand struct {
	WorkspaceKey string `json:"workspace_key"`
	BindingID    string `json:"binding_id"`
}

type ManagedBindingCommand struct {
	WorkspaceKey   string `json:"workspace_key"`
	BindingID      string `json:"binding_id"`
	AgentServiceID string `json:"agent_service_id"`
}

type BindingFilter struct {
	SourceKind           string `json:"source_kind,omitempty"`
	RouteKey             string `json:"route_key,omitempty"`
	DriverID             string `json:"driver_id,omitempty"`
	TargetAgentServiceID string `json:"target_agent_service_id,omitempty"`
	Enabled              *bool  `json:"enabled,omitempty"`
	Limit                int    `json:"limit,omitempty"`
}

type EventFilter struct {
	BindingID  string      `json:"binding_id,omitempty"`
	SourceKind string      `json:"source_kind,omitempty"`
	Origin     EventOrigin `json:"origin,omitempty"`
	Limit      int         `json:"limit,omitempty"`
}

type DeliveryFilter struct {
	EventID   string         `json:"event_id,omitempty"`
	BindingID string         `json:"binding_id,omitempty"`
	Status    DeliveryStatus `json:"status,omitempty"`
	Limit     int            `json:"limit,omitempty"`
}

// EventAuthority is a sealed sum of the only authority classes allowed to
// enter trigger admission. Its zero value fails closed.
type EventAuthority struct {
	value  authority.Authority
	origin EventOrigin
}

func NewWebhookEventAuthority(auth authority.WebhookAuthority) EventAuthority {
	return EventAuthority{value: auth, origin: EventOriginExternal}
}

func NewExecutionEventAuthority(auth authority.ExecutionAuthority) EventAuthority {
	return EventAuthority{value: auth, origin: EventOriginWorkflow}
}

func NewSystemEventAuthority(auth authority.SystemAuthority) EventAuthority {
	return EventAuthority{value: auth, origin: EventOriginSystem}
}

// AdmitEventCommand intentionally omits origin, hop depth, idempotency key,
// signature status, and actor credentials. The service derives those values
// from EventAuthority and ExecutionPort.EmissionContext.
type AdmitEventCommand struct {
	WorkspaceKey          string            `json:"workspace_key"`
	SourceKind            string            `json:"source_kind,omitempty"`
	SourceRef             string            `json:"source_ref,omitempty"`
	RouteKey              string            `json:"route_key,omitempty"`
	SourceEventID         string            `json:"source_event_id"`
	EventType             string            `json:"event_type"`
	SubjectRef            string            `json:"subject_ref,omitempty"`
	ActorRef              string            `json:"actor_ref,omitempty"`
	ParentEventID         string            `json:"parent_event_id,omitempty"`
	EpicID                string            `json:"epic_id,omitempty"`
	ExecutionNodeID       string            `json:"execution_node_id,omitempty"`
	ExecutionLeaseID      string            `json:"execution_lease_id,omitempty"`
	ExecutionFencingToken int64             `json:"execution_fencing_token,omitempty"`
	OccurredAt            time.Time         `json:"occurred_at,omitempty"`
	RawPayloadRef         string            `json:"raw_payload_ref,omitempty"`
	RawPayloadDigest      string            `json:"raw_payload_digest,omitempty"`
	Payload               json.RawMessage   `json:"payload,omitempty"`
	SubjectAttrs          map[string]string `json:"subject_attrs,omitempty"`
}

type AdmissionResult struct {
	Event      *Event      `json:"event,omitempty"`
	Deliveries []*Delivery `json:"deliveries,omitempty"`
	Replayed   bool        `json:"replayed,omitempty"`
	Dropped    bool        `json:"dropped,omitempty"`
	DropReason string      `json:"drop_reason,omitempty"`
	EventType  string      `json:"event_type,omitempty"`
	RouteKey   string      `json:"route_key,omitempty"`
	Origin     EventOrigin `json:"origin,omitempty"`
	HopDepth   int         `json:"hop_depth,omitempty"`
}

type DispatchBindingCommand struct {
	WorkspaceKey   string            `json:"workspace_key"`
	BindingID      string            `json:"binding_id"`
	IdempotencyKey string            `json:"idempotency_key"`
	SubjectRef     string            `json:"subject_ref,omitempty"`
	EpicID         string            `json:"epic_id,omitempty"`
	RawPayloadRef  string            `json:"raw_payload_ref,omitempty"`
	Payload        json.RawMessage   `json:"payload,omitempty"`
	SubjectAttrs   map[string]string `json:"subject_attrs,omitempty"`
}

type DispatchBindingResult struct {
	BindingID string `json:"binding_id"`
	RunID     string `json:"run_id"`
	Replayed  bool   `json:"replayed,omitempty"`
	// RunSnapshot is the immutable committed Execution response used by the
	// compatibility HTTP handler. It is not part of this wrapper's JSON shape.
	RunSnapshot json.RawMessage `json:"-"`
}

type SweepCronCommand struct {
	WorkspaceKey string `json:"workspace_key"`
	Limit        int    `json:"limit,omitempty"`
}

type SweepCronResult struct {
	Claimed  int `json:"claimed"`
	Admitted int `json:"admitted"`
	Dropped  int `json:"dropped"`
	Failed   int `json:"failed"`
}

type RetryDeliveriesCommand struct {
	WorkspaceKey string `json:"workspace_key"`
	Limit        int    `json:"limit,omitempty"`
}

type RetryDeliveriesResult struct {
	Claimed      int `json:"claimed"`
	Dispatched   int `json:"dispatched"`
	Deduplicated int `json:"deduplicated"`
	Held         int `json:"held"`
	Failed       int `json:"failed"`
	Exhausted    int `json:"exhausted"`
}
