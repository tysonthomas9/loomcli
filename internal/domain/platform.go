package domain

import (
	"encoding/json"
	"time"
)

type PlatformEvent struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Actor       string            `json:"actor"`
	Action      string            `json:"action"`
	EntityType  string            `json:"entity_type"`
	EntityID    string            `json:"entity_id"`
	WorkspaceID string            `json:"workspace_id"`
	Before      string            `json:"before,omitempty"`
	After       string            `json:"after,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type PlatformEventsPage struct {
	Events []PlatformEvent `json:"events"`
	Cursor string          `json:"cursor"`
}

type DriverOwnerType string

const (
	DriverOwnerUser      DriverOwnerType = "user"
	DriverOwnerTeam      DriverOwnerType = "team"
	DriverOwnerLeadAgent DriverOwnerType = "lead_agent"
	DriverOwnerSystem    DriverOwnerType = "system"
)

type DriverStatus string

const (
	DriverStatusDraft    DriverStatus = "draft"
	DriverStatusActive   DriverStatus = "active"
	DriverStatusDisabled DriverStatus = "disabled"
	DriverStatusArchived DriverStatus = "archived"
)

// DriverTrustLevel classifies who vouches for a driver's bundle content and
// gates where its workflow runtimes may execute (§7 step 9 sandbox placement
// policy): trusted drivers (builtin/operator-registered) may run in a host
// process; untrusted drivers (externally submitted bundles) require an
// isolating sandbox launcher and the executor refuses anything else.
type DriverTrustLevel string

const (
	DriverTrustTrusted   DriverTrustLevel = "trusted"
	DriverTrustUntrusted DriverTrustLevel = "untrusted"
)

// Trusted reports whether the level grants host-process execution. Unknown or
// missing levels are untrusted — fail closed (step-9 locked decision: the
// one-time fleet-db backfill stamps pre-existing rows trusted; thereafter
// unknown/missing means sandbox).
func (t DriverTrustLevel) Trusted() bool {
	return t == DriverTrustTrusted
}

type Driver struct {
	WorkspaceKey    string            `json:"workspace_key"`
	DriverID        string            `json:"driver_id"`
	Name            string            `json:"name"`
	OwnerType       DriverOwnerType   `json:"owner_type"`
	OwnerRef        string            `json:"owner_ref,omitempty"`
	Description     string            `json:"description,omitempty"`
	ActiveVersionID string            `json:"active_version_id,omitempty"`
	Status          DriverStatus      `json:"status"`
	TrustLevel      DriverTrustLevel  `json:"trust_level,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type DriverVersionValidationStatus string

const (
	DriverVersionValidationPending DriverVersionValidationStatus = "pending"
	DriverVersionValidationPassed  DriverVersionValidationStatus = "passed"
	DriverVersionValidationFailed  DriverVersionValidationStatus = "failed"
)

type DriverVersion struct {
	WorkspaceKey     string                        `json:"workspace_key"`
	VersionID        string                        `json:"version_id"`
	DriverID         string                        `json:"driver_id"`
	Version          int                           `json:"version"`
	SourceRef        string                        `json:"source_ref"`
	SourceDigest     string                        `json:"source_digest"`
	BundleRef        string                        `json:"bundle_ref"`
	BundleDigest     string                        `json:"bundle_digest"`
	Runtime          string                        `json:"runtime,omitempty"`
	Manifest         map[string]string             `json:"manifest,omitempty"`
	BuildDiagnostics string                        `json:"build_diagnostics,omitempty"`
	ValidationStatus DriverVersionValidationStatus `json:"validation_status"`
	CreatedBy        string                        `json:"created_by,omitempty"`
	CreatedAt        time.Time                     `json:"created_at"`
}

type WorkerProfile struct {
	WorkspaceKey  string            `json:"workspace_key"`
	ProfileID     string            `json:"profile_id"`
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	Backend       string            `json:"backend,omitempty"`
	RuntimePolicy map[string]string `json:"runtime_policy,omitempty"`
	Repos         []string          `json:"repos,omitempty"`
	MaxPriority   *int              `json:"max_priority,omitempty"`
	MaxParallel   int               `json:"max_parallel,omitempty"`
	ParentEpic    string            `json:"parent_epic,omitempty"`
	Labels        []string          `json:"labels,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	Enabled       bool              `json:"enabled"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type AgentServiceKind string

const (
	AgentServiceKindLead                 AgentServiceKind = "lead"
	AgentServiceKindSupport              AgentServiceKind = "support"
	AgentServiceKindTriage               AgentServiceKind = "triage"
	AgentServiceKindOnCall               AgentServiceKind = "on_call"
	AgentServiceKindScheduled            AgentServiceKind = "scheduled"
	AgentServiceKindMaintenance          AgentServiceKind = "maintenance"
	AgentServiceKindOrchestrator         AgentServiceKind = "orchestrator"
	AgentServiceKindAlwaysOn             AgentServiceKind = "always_on"
	AgentServiceKindCron                 AgentServiceKind = "cron"
	AgentServiceKindEvent                AgentServiceKind = "event"
	AgentServiceKindCampaignOrchestrator AgentServiceKind = "campaign_orchestrator"
)

type AgentServiceDesiredState string

const (
	AgentServiceDesiredRunning AgentServiceDesiredState = "running"
	AgentServiceDesiredStopped AgentServiceDesiredState = "stopped"
	AgentServiceDesiredPaused  AgentServiceDesiredState = "paused"
)

type AgentService struct {
	WorkspaceKey    string                   `json:"workspace_key"`
	ServiceID       string                   `json:"service_id"`
	Name            string                   `json:"name"`
	Kind            AgentServiceKind         `json:"kind"`
	DesiredState    AgentServiceDesiredState `json:"desired_state"`
	RoleName        string                   `json:"role_name"`
	ProfileName     string                   `json:"profile_name,omitempty"`
	ScheduleID      string                   `json:"schedule_id,omitempty"`
	EventSources    []string                 `json:"event_sources,omitempty"`
	TriggerRefs     []string                 `json:"trigger_refs,omitempty"`
	PlacementPolicy string                   `json:"placement_policy,omitempty"`
	MaxInstances    int                      `json:"max_instances"`
	LeaseID         string                   `json:"lease_id,omitempty"`
	RestartPolicy   string                   `json:"restart_policy,omitempty"`
	Permissions     []string                 `json:"permissions,omitempty"`
	BudgetPolicy    string                   `json:"budget_policy,omitempty"`
	StateRef        string                   `json:"state_ref,omitempty"`
	Metadata        map[string]string        `json:"metadata,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

// Retry defaults mirror fleet-db's write-time defaults for trigger binding
// retry fields: 5 attempts with 30s exponential backoff (the sweeper caps
// backoff at 1h).
const (
	DefaultTriggerRetryMaxAttempts    = 5
	DefaultTriggerRetryBackoffSeconds = 30
)

// TriggerActorFilter scopes which event actors a binding reacts to. It is most
// relevant for source_kind=internal bindings where workflow-emitted events
// could otherwise feed back into workflows. Loopback protection itself is
// structural (event origin + hop_depth), so the filter is advisory and a
// missing filter on an internal binding is accepted.
type TriggerActorFilter struct {
	ExcludeActorKinds []string `json:"exclude_actor_kinds,omitempty"`
	AllowActors       []string `json:"allow_actors,omitempty"`
}

// IsZero reports whether the filter is nil or carries no constraints.
func (f *TriggerActorFilter) IsZero() bool {
	return f == nil || (len(f.ExcludeActorKinds) == 0 && len(f.AllowActors) == 0)
}

// Clone returns a deep copy of the filter (nil-safe).
func (f *TriggerActorFilter) Clone() *TriggerActorFilter {
	if f == nil {
		return nil
	}
	return &TriggerActorFilter{
		ExcludeActorKinds: append([]string(nil), f.ExcludeActorKinds...),
		AllowActors:       append([]string(nil), f.AllowActors...),
	}
}

type TriggerBindingConcurrencyPolicy string

const (
	TriggerBindingConcurrencyAllow            TriggerBindingConcurrencyPolicy = "allow"
	TriggerBindingConcurrencyForbid           TriggerBindingConcurrencyPolicy = "forbid"
	TriggerBindingConcurrencyReplace          TriggerBindingConcurrencyPolicy = "replace"
	TriggerBindingConcurrencyQueue            TriggerBindingConcurrencyPolicy = "queue"
	TriggerBindingConcurrencyOneActivePerEpic TriggerBindingConcurrencyPolicy = "one_active_per_epic"
)

type TriggerBinding struct {
	WorkspaceKey         string                          `json:"workspace_key"`
	BindingID            string                          `json:"binding_id"`
	Name                 string                          `json:"name"`
	SourceKind           string                          `json:"source_kind"`
	SourceRef            string                          `json:"source_ref,omitempty"`
	SourceConfigRef      string                          `json:"source_config_ref,omitempty"`
	RouteKey             string                          `json:"route_key,omitempty"`
	Method               string                          `json:"method,omitempty"`
	PathTemplate         string                          `json:"path_template,omitempty"`
	Topic                string                          `json:"topic,omitempty"`
	EventTypePatterns    []string                        `json:"event_type_patterns,omitempty"`
	FilterRef            string                          `json:"filter_ref,omitempty"`
	DriverID             string                          `json:"driver_id"`
	DriverVersionID      string                          `json:"driver_version_id"`
	TargetEntrypoint     string                          `json:"target_entrypoint,omitempty"`
	TargetAgentServiceID string                          `json:"target_agent_service_id,omitempty"`
	ConcurrencyPolicy    TriggerBindingConcurrencyPolicy `json:"concurrency_policy"`
	IdempotencyPolicy    string                          `json:"idempotency_policy,omitempty"`
	AuthPolicy           string                          `json:"auth_policy,omitempty"`
	// WebhookSecret is the shared secret used to verify inbound webhook
	// signatures (e.g. GitHub's X-Hub-Signature-256 HMAC) for this route.
	WebhookSecret string `json:"webhook_secret,omitempty"`
	// SubjectKeyTemplate renders the concurrency subject key for deliveries
	// using {{subject_ref}}, {{event_type}} and {{attrs.X}} tokens (templates
	// never read the raw payload). Empty means the default key
	// binding_id|subject_ref.
	SubjectKeyTemplate string `json:"subject_key_template,omitempty"`
	// ActorFilter scopes which actors this binding reacts to; advisory for
	// source_kind=internal bindings (see TriggerActorFilter).
	ActorFilter *TriggerActorFilter `json:"actor_filter,omitempty"`
	// RetryMaxAttempts and RetryBackoffSeconds drive the delivery retry
	// sweeper; zero values are defaulted at write time (see
	// DefaultTriggerRetryMaxAttempts / DefaultTriggerRetryBackoffSeconds).
	RetryMaxAttempts    int `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds int `json:"retry_backoff_seconds,omitempty"`
	// Schedule is a standard 5-field cron expression (or @descriptor);
	// required when SourceKind is "cron". ScheduleTimezone is an IANA zone
	// name evaluated against Schedule (UTC when empty).
	Schedule         string    `json:"schedule,omitempty"`
	ScheduleTimezone string    `json:"schedule_timezone,omitempty"`
	Permissions      []string  `json:"permissions,omitempty"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TriggerEventOrigin mirrors fleet-db's structural event provenance: every
// trigger event carries a server-stamped origin so workflow-originated events
// are distinguishable from genuine external ones. Webhook ingest stamps
// external (hop depth 0), the workflow events API stamps workflow (hop depth
// parent+1, capped), and system schedulers stamp system.
type TriggerEventOrigin string

const (
	TriggerEventOriginExternal TriggerEventOrigin = "external"
	TriggerEventOriginWorkflow TriggerEventOrigin = "workflow"
	TriggerEventOriginSystem   TriggerEventOrigin = "system"
)

// TriggerEvent is a durable record of an external event ingested by the
// trigger layer (e.g. a verified GitHub webhook delivery), persisted before
// any dispatch happens.
type TriggerEvent struct {
	WorkspaceKey     string `json:"workspace_key"`
	EventID          string `json:"event_id"`
	TriggerBindingID string `json:"trigger_binding_id,omitempty"`
	SourceKind       string `json:"source_kind"`
	SourceEventID    string `json:"source_event_id,omitempty"`
	EventType        string `json:"event_type"`
	SubjectRef       string `json:"subject_ref,omitempty"`
	ActorRef         string `json:"actor_ref,omitempty"`
	// Origin is the server-stamped provenance (external|workflow|system).
	// Records persisted before provenance existed round-trip with an empty
	// origin and normalize to external on read (zero-value back-compat).
	Origin TriggerEventOrigin `json:"origin,omitempty"`
	// HopDepth counts workflow re-trigger hops from the originating
	// external or system event (which sit at depth 0).
	HopDepth         int       `json:"hop_depth,omitempty"`
	OccurredAt       time.Time `json:"occurred_at"`
	ReceivedAt       time.Time `json:"received_at"`
	IdempotencyKey   string    `json:"idempotency_key,omitempty"`
	RawPayloadRef    string    `json:"raw_payload_ref,omitempty"`
	RawPayloadDigest string    `json:"raw_payload_digest,omitempty"`
	SignatureStatus  string    `json:"signature_status,omitempty"`
	ReplayOfEventID  string    `json:"replay_of_event_id,omitempty"`
}

// NormalizeProvenance applies zero-value back-compat on read: events written
// before structural provenance existed were all externally ingested.
func (e *TriggerEvent) NormalizeProvenance() {
	if e.Origin == "" {
		e.Origin = TriggerEventOriginExternal
	}
}

// TriggerDeliveryStatus enumerates the lifecycle of a TriggerDelivery.
type TriggerDeliveryStatus string

const (
	TriggerDeliveryAccepted   TriggerDeliveryStatus = "accepted"
	TriggerDeliveryRejected   TriggerDeliveryStatus = "rejected"
	TriggerDeliveryDuplicate  TriggerDeliveryStatus = "duplicate"
	TriggerDeliveryQueued     TriggerDeliveryStatus = "queued"
	TriggerDeliveryDispatched TriggerDeliveryStatus = "dispatched"
	TriggerDeliveryFailed     TriggerDeliveryStatus = "failed"
	TriggerDeliveryReplayed   TriggerDeliveryStatus = "replayed"
	// TriggerDeliverySuperseded marks a delivery replaced by a newer event for
	// the same subject key (replace concurrency policy). TriggerDeliveryHeld
	// holds a delivery behind an active run for its subject key (queue
	// concurrency policy); the retry sweeper promotes it. Both are additive
	// enum values on the fleet-db v1 wire.
	TriggerDeliverySuperseded TriggerDeliveryStatus = "superseded"
	TriggerDeliveryHeld       TriggerDeliveryStatus = "held"
)

// TriggerDeliveryErrorRetriesExhausted is the terminal error class stamped on
// a failed delivery once its binding's RetryMaxAttempts budget is spent
// (mirrors fleet-db's write-time rule). A failed delivery carrying it is
// final and leaves the retry due-index.
const TriggerDeliveryErrorRetriesExhausted = "retries_exhausted"

// IsValid reports whether the status is a known TriggerDeliveryStatus
// (mirrors fleet-db's models.TriggerDeliveryStatus.IsValid).
func (s TriggerDeliveryStatus) IsValid() bool {
	switch s {
	case TriggerDeliveryAccepted, TriggerDeliveryRejected, TriggerDeliveryDuplicate,
		TriggerDeliveryQueued, TriggerDeliveryDispatched, TriggerDeliveryFailed,
		TriggerDeliveryReplayed, TriggerDeliverySuperseded, TriggerDeliveryHeld:
		return true
	}
	return false
}

// TriggerDelivery links a TriggerEvent to the binding that matched it and the
// DriverRun it enqueued.
type TriggerDelivery struct {
	WorkspaceKey     string                `json:"workspace_key"`
	DeliveryID       string                `json:"delivery_id"`
	TriggerEventID   string                `json:"trigger_event_id"`
	TriggerBindingID string                `json:"trigger_binding_id"`
	Status           TriggerDeliveryStatus `json:"status"`
	// SubjectKey is the rendered concurrency subject key for this delivery:
	// the binding's SubjectKeyTemplate output, or the default
	// binding_id|subject_ref when the binding has no template.
	SubjectKey      string     `json:"subject_key,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	DriverRunID     string     `json:"driver_run_id,omitempty"`
	Attempt         int        `json:"attempt"`
	NextRetryAt     *time.Time `json:"next_retry_at,omitempty"`
	ErrorClass      string     `json:"error_class,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type DriverRunStatus string

const (
	DriverRunQueued      DriverRunStatus = "queued"
	DriverRunRunning     DriverRunStatus = "running"
	DriverRunCompleted   DriverRunStatus = "completed"
	DriverRunFailed      DriverRunStatus = "failed"
	DriverRunNeedsReview DriverRunStatus = "needs_review"
	DriverRunCancelled   DriverRunStatus = "cancelled"
	// DriverRunSuspendedAwaitingEvent suspends a run that registered an
	// await-event and is waiting for a matching event (or its deadline).
	// Explicitly NOT terminal: the run resumes when its await resolves.
	DriverRunSuspendedAwaitingEvent DriverRunStatus = "suspended_awaiting_event"
)

func (s DriverRunStatus) IsTerminal() bool {
	switch s {
	case DriverRunCompleted, DriverRunFailed, DriverRunNeedsReview, DriverRunCancelled:
		return true
	case DriverRunSuspendedAwaitingEvent:
		// Suspended runs are alive — they resume on event/timeout.
		return false
	default:
		return false
	}
}

type DriverRun struct {
	WorkspaceKey    string            `json:"workspace_key"`
	RunID           string            `json:"run_id"`
	DriverID        string            `json:"driver_id"`
	DriverVersionID string            `json:"driver_version_id"`
	Entrypoint      string            `json:"entrypoint,omitempty"`
	SourceKind      string            `json:"source_kind,omitempty"`
	SourceRef       string            `json:"source_ref,omitempty"`
	EpicID          string            `json:"epic_id,omitempty"`
	Status          DriverRunStatus   `json:"status"`
	NodeID          string            `json:"node_id,omitempty"`
	LeaseID         string            `json:"lease_id,omitempty"`
	FencingToken    int64             `json:"fencing_token,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	Payload         json.RawMessage   `json:"payload,omitempty"`
	Output          map[string]string `json:"output,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	ErrorClass      string            `json:"error_class,omitempty"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	LastHeartbeat   time.Time         `json:"last_heartbeat,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
	// Composition + await fields (Phase D). snake_case tags like the rest
	// of this struct: the fleet-db client decodes v1 responses directly
	// into DriverRun (tag-identical round-trip, AW5); the driver/watch wire
	// carries runs through its own DTOs (internal/driver/run_events.go).
	//
	// ParentRunID links a child run spawned by a parent workflow run.
	// Empty means detached/root (no cancel cascade). Orthogonal to EpicID:
	// a run can belong to an epic, a parent run, both, or neither.
	ParentRunID string `json:"parent_run_id,omitempty"`
	// SuspendedAt is set when the run suspends in suspended_awaiting_event.
	SuspendedAt *time.Time `json:"suspended_at,omitempty"`
	// CancelRequestedAt records a cooperative cancel request against a
	// RUNNING run (composition cascade: parent terminal -> running children
	// cancel-requested). The owning executor observes it on heartbeat and
	// cancels its runner; the run still terminalizes through the normal
	// fenced Finish.
	CancelRequestedAt     *time.Time `json:"cancel_requested_at,omitempty"`
	CancelRequestedReason string     `json:"cancel_requested_reason,omitempty"`
	// ResumeSourceEventID records the trigger event that resolved the
	// await and resumed the run (or the synthetic timeout event).
	ResumeSourceEventID string    `json:"resume_source_event_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type DriverStepStatus string

const (
	DriverStepQueued    DriverStepStatus = "queued"
	DriverStepRunning   DriverStepStatus = "running"
	DriverStepWaiting   DriverStepStatus = "waiting"
	DriverStepCompleted DriverStepStatus = "completed"
	DriverStepFailed    DriverStepStatus = "failed"
	DriverStepSkipped   DriverStepStatus = "skipped"
)

func (s DriverStepStatus) IsTerminal() bool {
	switch s {
	case DriverStepCompleted, DriverStepFailed, DriverStepSkipped:
		return true
	default:
		return false
	}
}

type DriverStep struct {
	WorkspaceKey   string           `json:"workspace_key"`
	StepID         string           `json:"step_id"`
	DriverRunID    string           `json:"driver_run_id"`
	StepKind       string           `json:"step_kind"`
	Status         DriverStepStatus `json:"status"`
	TaskRunID      string           `json:"task_run_id,omitempty"`
	ActionLedgerID string           `json:"action_ledger_id,omitempty"`
	ExternalRef    string           `json:"external_ref,omitempty"`
	InputRef       string           `json:"input_ref,omitempty"`
	OutputRef      string           `json:"output_ref,omitempty"`
	StartedAt      time.Time        `json:"started_at,omitempty"`
	EndedAt        *time.Time       `json:"ended_at,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type TaskRunStatus string

const (
	TaskRunQueued    TaskRunStatus = "queued"
	TaskRunRunning   TaskRunStatus = "running"
	TaskRunCompleted TaskRunStatus = "completed"
	TaskRunFailed    TaskRunStatus = "failed"
	TaskRunCancelled TaskRunStatus = "cancelled"
)

func (s TaskRunStatus) IsTerminal() bool {
	switch s {
	case TaskRunCompleted, TaskRunFailed, TaskRunCancelled:
		return true
	default:
		return false
	}
}

type TaskRun struct {
	WorkspaceKey     string           `json:"workspace_key"`
	TaskRunID        string           `json:"task_run_id"`
	DriverRunID      string           `json:"driver_run_id,omitempty"`
	DriverStepID     string           `json:"driver_step_id,omitempty"`
	TaskID           string           `json:"task_id"`
	RepositorySet    []string         `json:"repository_set"`
	RootGeneration   int64            `json:"root_generation,omitempty"`
	WorkerProfileID  string           `json:"worker_profile_id,omitempty"`
	Runner           string           `json:"runner,omitempty"`
	RunnerRef        string           `json:"runner_ref,omitempty"`
	RunnerKind       string           `json:"runner_kind,omitempty"`
	RunnerEntrypoint string           `json:"runner_entrypoint,omitempty"`
	RunnerVersionID  string           `json:"runner_driver_version_id,omitempty"`
	ProviderProfile  string           `json:"provider_profile,omitempty"`
	Status           TaskRunStatus    `json:"status"`
	NodeID           string           `json:"node_id,omitempty"`
	LeaseID          string           `json:"lease_id,omitempty"`
	FencingToken     int64            `json:"fencing_token,omitempty"`
	RunnerPlacement  TaskRunPlacement `json:"runner_placement,omitempty"`
	SandboxPlacement TaskRunPlacement `json:"sandbox_placement,omitempty"`
	// Input is the optional task-run payload supplied by the requester
	// (e.g. a github-review-agent's diff+rubric). It is persisted verbatim
	// and delivered to the runner so the task harness can act on it.
	// Optional / back-compat: runs created without it behave as before.
	Input            json.RawMessage   `json:"input,omitempty"`
	ExitCode         *int              `json:"exit_code,omitempty"`
	LogsRef          string            `json:"logs_ref,omitempty"`
	ArtifactsRef     string            `json:"artifacts_ref,omitempty"`
	InputTokens      int64             `json:"input_tokens,omitempty"`
	OutputTokens     int64             `json:"output_tokens,omitempty"`
	CacheReadTokens  int64             `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64             `json:"cache_write_tokens,omitempty"`
	EstimatedCostUSD float64           `json:"estimated_cost_usd,omitempty"`
	RuntimeMetadata  map[string]string `json:"runtime_metadata,omitempty"`
	NextEligibleAt   time.Time         `json:"next_eligible_at,omitempty"`
	StartedAt        time.Time         `json:"started_at,omitempty"`
	LastHeartbeat    time.Time         `json:"last_heartbeat,omitempty"`
	FinishedAt       *time.Time        `json:"finished_at,omitempty"`
	ErrorClass       string            `json:"error_class,omitempty"`
	ErrorMessage     string            `json:"error_message,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type TaskRunPlacement struct {
	Provider        string     `json:"provider,omitempty"`
	NodeID          string     `json:"node_id,omitempty"`
	RunnerID        string     `json:"runner_id,omitempty"`
	ProcessRef      string     `json:"process_ref,omitempty"`
	SandboxID       string     `json:"sandbox_id,omitempty"`
	ImageOrSnapshot string     `json:"image_or_snapshot,omitempty"`
	CWD             string     `json:"cwd,omitempty"`
	RepoRef         string     `json:"repo_ref,omitempty"`
	CleanupPolicy   string     `json:"cleanup_policy,omitempty"`
	EgressMode      string     `json:"egress_mode,omitempty"`
	EgressMechanism string     `json:"egress_mechanism,omitempty"`
	StartedAt       time.Time  `json:"started_at,omitempty"`
	HeartbeatAt     time.Time  `json:"heartbeat_at,omitempty"`
	RetainedUntil   *time.Time `json:"retained_until,omitempty"`
}

func (p TaskRunPlacement) Empty() bool {
	return p.Provider == "" &&
		p.NodeID == "" &&
		p.RunnerID == "" &&
		p.ProcessRef == "" &&
		p.SandboxID == "" &&
		p.ImageOrSnapshot == "" &&
		p.CWD == "" &&
		p.RepoRef == "" &&
		p.CleanupPolicy == "" &&
		p.EgressMode == "" &&
		p.EgressMechanism == "" &&
		p.StartedAt.IsZero() &&
		p.HeartbeatAt.IsZero() &&
		p.RetainedUntil == nil
}

type TaskRunLogEntry struct {
	WorkspaceKey string    `json:"workspace_key"`
	TaskRunID    string    `json:"task_run_id"`
	Sequence     int64     `json:"sequence"`
	Stream       string    `json:"stream"`
	Text         string    `json:"text"`
	NodeID       string    `json:"node_id,omitempty"`
	LeaseID      string    `json:"lease_id,omitempty"`
	FencingToken int64     `json:"fencing_token,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
	CreatedAt    time.Time `json:"created_at"`
}
