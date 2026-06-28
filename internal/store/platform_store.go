package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type DriverCreate struct {
	WorkspaceKey    string
	DriverID        string
	Name            string
	OwnerType       domain.DriverOwnerType
	OwnerRef        string
	Description     string
	ActiveVersionID string
	Status          domain.DriverStatus
	// TrustLevel gates sandbox placement (§7 step 9). Stamped by the
	// registration path server-side, never from client input; empty means
	// untrusted (fail closed).
	TrustLevel domain.DriverTrustLevel
	Metadata   map[string]string
}

type DriverFilter struct {
	Name   string
	Status domain.DriverStatus
	Limit  int
}

type DriverUpdate struct {
	Name            *string
	OwnerType       *domain.DriverOwnerType
	OwnerRef        *string
	Description     *string
	ActiveVersionID *string
	Status          *domain.DriverStatus
	// TrustLevel is the explicit ops elevation/demotion path; workflow
	// runtimes never reach a surface that sets it.
	TrustLevel *domain.DriverTrustLevel
	Metadata   *map[string]string
}

type DriverStore interface {
	Create(ctx context.Context, in DriverCreate) (*domain.Driver, error)
	Get(ctx context.Context, workspaceKey, driverID string) (*domain.Driver, error)
	List(ctx context.Context, workspaceKey string, filter DriverFilter) ([]*domain.Driver, error)
	Update(ctx context.Context, workspaceKey, driverID string, patch DriverUpdate) (*domain.Driver, error)
}

type DriverVersionCreate struct {
	WorkspaceKey     string
	VersionID        string
	DriverID         string
	Version          int
	SourceRef        string
	SourceDigest     string
	BundleRef        string
	BundleDigest     string
	Runtime          string
	Manifest         map[string]string
	BuildDiagnostics string
	ValidationStatus domain.DriverVersionValidationStatus
	CreatedBy        string
}

type DriverVersionFilter struct {
	DriverID         string
	ValidationStatus domain.DriverVersionValidationStatus
	Limit            int
}

type DriverVersionStore interface {
	Create(ctx context.Context, in DriverVersionCreate) (*domain.DriverVersion, error)
	Get(ctx context.Context, workspaceKey, versionID string) (*domain.DriverVersion, error)
	List(ctx context.Context, workspaceKey string, filter DriverVersionFilter) ([]*domain.DriverVersion, error)
}

type WorkerProfileCreate struct {
	WorkspaceKey  string
	ProfileID     string
	Name          string
	Role          string
	Backend       string
	RuntimePolicy map[string]string
	Repos         []string
	MaxPriority   *int
	MaxParallel   int
	ParentEpic    string
	Labels        []string
	Capabilities  []string
	Enabled       *bool
	Metadata      map[string]string
}

type WorkerProfileFilter struct {
	Role    string
	Backend string
	Enabled *bool
	Limit   int
}

type WorkerProfileUpdate struct {
	Name             *string
	Role             *string
	Backend          *string
	RuntimePolicy    *map[string]string
	Repos            *[]string
	MaxPriority      *int
	MaxParallel      *int
	ClearMaxPriority bool
	ParentEpic       *string
	Labels           *[]string
	Capabilities     *[]string
	Enabled          *bool
	Metadata         *map[string]string
}

type WorkerProfileStore interface {
	Create(ctx context.Context, in WorkerProfileCreate) (*domain.WorkerProfile, error)
	Get(ctx context.Context, workspaceKey, profileID string) (*domain.WorkerProfile, error)
	List(ctx context.Context, workspaceKey string, filter WorkerProfileFilter) ([]*domain.WorkerProfile, error)
	Update(ctx context.Context, workspaceKey, profileID string, patch WorkerProfileUpdate) (*domain.WorkerProfile, error)
	Delete(ctx context.Context, workspaceKey, profileID string) error
}

type AgentServiceCreate struct {
	WorkspaceKey    string
	ServiceID       string
	Name            string
	Kind            domain.AgentServiceKind
	DesiredState    domain.AgentServiceDesiredState
	RoleName        string
	ProfileName     string
	ScheduleID      string
	EventSources    []string
	TriggerRefs     []string
	PlacementPolicy string
	MaxInstances    int
	LeaseID         string
	RestartPolicy   string
	Permissions     []string
	BudgetPolicy    string
	StateRef        string
	Metadata        map[string]string
}

type AgentServiceFilter struct {
	Kind         domain.AgentServiceKind
	DesiredState domain.AgentServiceDesiredState
	RoleName     string
	ProfileName  string
	Limit        int
}

type AgentServiceUpdate struct {
	Name            *string
	Kind            *domain.AgentServiceKind
	DesiredState    *domain.AgentServiceDesiredState
	RoleName        *string
	ProfileName     *string
	ScheduleID      *string
	EventSources    *[]string
	TriggerRefs     *[]string
	PlacementPolicy *string
	MaxInstances    *int
	LeaseID         *string
	RestartPolicy   *string
	Permissions     *[]string
	BudgetPolicy    *string
	StateRef        *string
	Metadata        *map[string]string
}

type AgentServiceStore interface {
	Create(ctx context.Context, in AgentServiceCreate) (*domain.AgentService, error)
	Get(ctx context.Context, workspaceKey, serviceID string) (*domain.AgentService, error)
	List(ctx context.Context, workspaceKey string, filter AgentServiceFilter) ([]*domain.AgentService, error)
	Update(ctx context.Context, workspaceKey, serviceID string, patch AgentServiceUpdate) (*domain.AgentService, error)
	Delete(ctx context.Context, workspaceKey, serviceID string) error
}

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
	ConcurrencyPolicy    domain.TriggerBindingConcurrencyPolicy
	IdempotencyPolicy    string
	AuthPolicy           string
	WebhookSecret        string
	SubjectKeyTemplate   string
	ActorFilter          *domain.TriggerActorFilter
	RetryMaxAttempts     int
	RetryBackoffSeconds  int
	Schedule             string
	ScheduleTimezone     string
	Permissions          []string
	Enabled              bool
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
	ConcurrencyPolicy    *domain.TriggerBindingConcurrencyPolicy
	IdempotencyPolicy    *string
	AuthPolicy           *string
	WebhookSecret        *string
	SubjectKeyTemplate   *string
	// ActorFilter replaces the whole filter when set; a zero-valued filter
	// (no constraints) clears it, mirroring fleet-db's patch semantics.
	ActorFilter         *domain.TriggerActorFilter
	RetryMaxAttempts    *int
	RetryBackoffSeconds *int
	Schedule            *string
	ScheduleTimezone    *string
	Permissions         *[]string
	Enabled             *bool
}

type TriggerBindingStore interface {
	Create(ctx context.Context, in TriggerBindingCreate) (*domain.TriggerBinding, error)
	Get(ctx context.Context, workspaceKey, bindingID string) (*domain.TriggerBinding, error)
	GetByRouteKey(ctx context.Context, workspaceKey, routeKey string) (*domain.TriggerBinding, error)
	List(ctx context.Context, workspaceKey string, filter TriggerBindingFilter) ([]*domain.TriggerBinding, error)
	Update(ctx context.Context, workspaceKey, bindingID string, patch TriggerBindingUpdate) (*domain.TriggerBinding, error)
	// ResolveWebhookSecret fetches a binding's plaintext webhook signing secret.
	// Read/Get/List return the binding with the secret redacted; this is the
	// privileged path the webhook verifier uses to check inbound signatures.
	ResolveWebhookSecret(ctx context.Context, workspaceKey, bindingID string) (string, error)
}

// TriggerEventFilter narrows TriggerEvent listings.
type TriggerEventFilter struct {
	SourceKind       string
	TriggerBindingID string
	Limit            int
}

// TriggerEventStore is a read-only view of persisted trigger events.
type TriggerEventStore interface {
	Get(ctx context.Context, workspaceKey, eventID string) (*domain.TriggerEvent, error)
	List(ctx context.Context, workspaceKey string, filter TriggerEventFilter) ([]*domain.TriggerEvent, error)
}

// TriggerEventAppender is an OPTIONAL TriggerEventStore capability (detected
// by type assertion, like DriverRunEventsReader): append one server-stamped
// event directly to the trigger-event journal without route dispatch. This is
// the run.finished lifecycle lane (AW6): journal-first so the await
// registration scan (AwaitStore.RegisterAwaitAndCheck) sees terminal runs
// even when no binding listens on the internal route — composition awaits can
// never be suppressed by binding configuration or the loop guard.
//
// Implementations preserve the caller's EventID (lifecycle event IDs are
// deterministic for idempotent re-emission) and dedup on both EventID and
// IdempotencyKey, returning the existing record unchanged on a replay. The
// fleet-db backend does not implement this client-side capability: there the
// journal append happens server-side in fleet-db's dispatch wiring
// (IndexAwaitEvent, AW2/AW7).
type TriggerEventAppender interface {
	AppendTriggerEvent(ctx context.Context, event *domain.TriggerEvent) (*domain.TriggerEvent, error)
}

// TriggerDeliveryFilter narrows TriggerDelivery listings.
type TriggerDeliveryFilter struct {
	TriggerEventID   string
	TriggerBindingID string
	Status           domain.TriggerDeliveryStatus
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
// keeps it parked for queue promotion; dispatched/rejected/superseded (and
// failed once the binding's RetryMaxAttempts is reached) make it final.
// A zero Attempt keeps the stored attempt count.
type TriggerDeliveryResultUpdate struct {
	Status      domain.TriggerDeliveryStatus
	Attempt     int
	NextRetryAt *time.Time
	ErrorClass  string
	// DriverRunID stamps the admitted run when a retry finally dispatches.
	DriverRunID string
}

// TriggerDeliveryStore reads persisted trigger deliveries and records
// retry-sweeper attempt outcomes. Deliveries themselves are created by the
// dispatch path (TriggerRouteDispatcher), never directly through this store.
type TriggerDeliveryStore interface {
	Get(ctx context.Context, workspaceKey, deliveryID string) (*domain.TriggerDelivery, error)
	List(ctx context.Context, workspaceKey string, filter TriggerDeliveryFilter) ([]*domain.TriggerDelivery, error)
	// ListDue returns deliveries awaiting the retry sweeper whose due time
	// is <= filter.Now, in due order (earliest first).
	ListDue(ctx context.Context, workspaceKey string, filter TriggerDeliveryDueFilter) ([]*domain.TriggerDelivery, error)
	// UpdateResult records one attempt outcome. A failed result whose
	// Attempt reaches the binding's RetryMaxAttempts is forced terminal:
	// status stays failed, ErrorClass becomes
	// domain.TriggerDeliveryErrorRetriesExhausted and NextRetryAt clears.
	// Final deliveries (dispatched, rejected, duplicate, superseded,
	// replayed, terminal failed) reject transitions to a different status
	// with domain.ErrInvalidTransition; re-applying the same status is
	// idempotent.
	UpdateResult(ctx context.Context, workspaceKey, deliveryID string, update TriggerDeliveryResultUpdate) (*domain.TriggerDelivery, error)
}

// TriggerRouteDispatch carries the normalized fields an adapter resolves from
// an inbound external event before handing off to the durable dispatch path.
type TriggerRouteDispatch struct {
	RunID            string
	IdempotencyKey   string
	SourceEventID    string
	EventType        string
	SubjectRef       string
	ActorRef         string
	EpicID           string
	RawPayloadRef    string
	RawPayloadDigest string
	SignatureStatus  string
	ReplayOfEventID  string
	Payload          json.RawMessage
	// SubjectAttrs carries adapter-enriched subject attributes consumed by
	// subject-key templating ({{attrs.X}}). Webhook adapters populate it
	// (C15); templates never read the raw payload. Not yet sent on the
	// fleet-db wire — the server-side templating lane lands separately.
	SubjectAttrs map[string]string
}

// TriggerRouteDelivery is one fan-out leg of a trigger-route dispatch. The
// JSON tags pin fleet-db's BREAKING router-v2 webhook wire: the response
// carries deliveries[] only, with no top-level driver_run_id.
type TriggerRouteDelivery struct {
	DeliveryID      string                       `json:"delivery_id"`
	BindingID       string                       `json:"trigger_binding_id"`
	RunID           string                       `json:"driver_run_id"`
	Status          domain.TriggerDeliveryStatus `json:"status"`
	RejectionReason string                       `json:"rejection_reason,omitempty"`
}

// TriggerRouteDispatchResult collects the fan-out legs of one dispatch in
// dispatch order: the exact RouteKey owner first (when present and enabled),
// then pattern matches in binding-id order.
type TriggerRouteDispatchResult struct {
	// PrimaryRun is the admitted run for the first matched binding. In-process
	// backends populate it directly; HTTP backends may leave it nil because the
	// router-v2 wire no longer returns run bodies (callers needing the run
	// fetch it by Deliveries[0].RunID).
	PrimaryRun *domain.DriverRun
	Deliveries []TriggerRouteDelivery
}

// TriggerRouteDispatcher resolves the matched binding set for a route key —
// the exact-RouteKey binding unioned with enabled bindings whose
// event_type_patterns match the key — persists ONE TriggerEvent, then per
// matched binding enqueues a queued DriverRun and records a TriggerDelivery,
// in that order. It fronts fleet-db's trigger-routes endpoint.
//
// Each write is individually idempotent: the event dedups on the dispatch
// idempotency key, each leg's run dedups on a per-binding composite
// {idempotencyKey}#{bindingID} key (the legacy single-binding exact path keeps
// the bare key), and each leg's delivery id is deterministic. The sequence is
// NOT a single transaction: a failure after earlier legs are durable surfaces
// as an error, and the caller's redelivery re-runs the sequence, deduping the
// event and every already-admitted run while writing only the missing
// deliveries — redelivery heals each leg independently. Callers should treat
// dispatch as durable and eventually-consistent, not transactional, and retry
// on error.
type TriggerRouteDispatcher interface {
	// DispatchTriggerRoute is the legacy single-run lane: it runs the same
	// fan-out dispatch and returns only the primary run. Kept so existing
	// webhook callers compile until they move to DispatchTriggerRouteV2.
	DispatchTriggerRoute(ctx context.Context, workspaceKey, routeKey string, in TriggerRouteDispatch) (*domain.DriverRun, error)
	// DispatchTriggerRouteV2 surfaces every fan-out leg of the dispatch.
	DispatchTriggerRouteV2(ctx context.Context, workspaceKey, routeKey string, in TriggerRouteDispatch) (*TriggerRouteDispatchResult, error)
}

type EpicRunCreate struct {
	RunID          string
	IdempotencyKey string
	Payload        json.RawMessage
}

type DriverRunCreate struct {
	WorkspaceKey    string
	RunID           string
	DriverID        string
	DriverVersionID string
	Entrypoint      string
	SourceKind      string
	SourceRef       string
	EpicID          string
	// ParentRunID links a child run to the workflow run that spawned it
	// (Phase D composition). Empty means detached/root — no cancel cascade.
	// Orthogonal to EpicID: a run may carry an epic, a parent, both, or
	// neither.
	ParentRunID    string
	IdempotencyKey string
	Payload        json.RawMessage
}

type DriverRunFilter struct {
	DriverID        string
	DriverVersionID string
	EpicID          string
	NodeID          string
	Status          domain.DriverRunStatus
	Limit           int
}

type DriverRunFinish struct {
	NodeID       string
	LeaseID      string
	FencingToken int64
	Status       domain.DriverRunStatus
	Summary      string
	ErrorClass   string
	Output       map[string]string
}

type StaleDriverRunRecovery struct {
	StaleBefore   time.Time `json:"stale_before,omitempty"`
	MaxAgeSeconds int64     `json:"max_age_seconds,omitempty"`
	ErrorClass    string    `json:"error_class,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Limit         int       `json:"limit,omitempty"`
}

type StaleDriverRunRecoveryResult struct {
	WorkspaceKey       string    `json:"workspace_key"`
	StaleBefore        time.Time `json:"stale_before"`
	RecoveredAt        time.Time `json:"recovered_at"`
	Recovered          int       `json:"recovered"`
	SkippedFresh       int       `json:"skipped_fresh"`
	RecoveredRunIDs    []string  `json:"recovered_run_ids,omitempty"`
	SkippedFreshRunIDs []string  `json:"skipped_fresh_run_ids,omitempty"`
}

type StaleTaskRunRecovery struct {
	StaleBefore   time.Time `json:"stale_before,omitempty"`
	MaxAgeSeconds int64     `json:"max_age_seconds,omitempty"`
	ErrorClass    string    `json:"error_class,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
}

type StaleTaskRunRecoveryResult struct {
	WorkspaceKey         string    `json:"workspace_key"`
	DriverRunID          string    `json:"driver_run_id"`
	StaleBefore          time.Time `json:"stale_before"`
	RecoveredAt          time.Time `json:"recovered_at"`
	Recovered            int       `json:"recovered"`
	Released             int       `json:"released"`
	SkippedFresh         int       `json:"skipped_fresh"`
	SkippedActorMismatch int       `json:"skipped_actor_mismatch"`
	SkippedIssueNotFound int       `json:"skipped_issue_not_found"`
	RecoveredTaskRunIDs  []string  `json:"recovered_task_run_ids,omitempty"`
	ReleasedTaskIDs      []string  `json:"released_task_ids,omitempty"`
	ActorMismatchTaskIDs []string  `json:"actor_mismatch_task_ids,omitempty"`
	IssueNotFoundTaskIDs []string  `json:"issue_not_found_task_ids,omitempty"`
}

type DriverRunStore interface {
	Create(ctx context.Context, in DriverRunCreate) (*domain.DriverRun, error)
	CreateEpic(ctx context.Context, workspaceKey, epicID string, in EpicRunCreate) (*domain.DriverRun, error)
	Get(ctx context.Context, workspaceKey, runID string) (*domain.DriverRun, error)
	List(ctx context.Context, workspaceKey string, filter DriverRunFilter) ([]*domain.DriverRun, error)
	Claim(ctx context.Context, workspaceKey, runID, nodeID, leaseID string) (*domain.DriverRun, error)
	Heartbeat(ctx context.Context, workspaceKey, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error)
	Finish(ctx context.Context, workspaceKey, runID string, finish DriverRunFinish) (*domain.DriverRun, error)
	RecoverStale(ctx context.Context, workspaceKey string, recover StaleDriverRunRecovery) (*StaleDriverRunRecoveryResult, error)
	RecoverStaleTaskRuns(ctx context.Context, workspaceKey, runID string, recover StaleTaskRunRecovery) (*StaleTaskRunRecoveryResult, error)

	// Suspend parks a running run on its await instance
	// (running -> suspended_awaiting_event), owner-fenced with the same
	// node+lease+token guard as Finish, releasing the executor slot
	// (node/lease cleared). awaitInstanceKey names the await cycle the run
	// parks on (runID#await-{n}) and is required. Idempotent on re-suspend.
	// A backend that recorded a pending resume for this await cycle (the
	// accepted park->suspend window) returns
	// domain.ErrDriverRunAlreadyResumed: do not park, continue inline.
	Suspend(ctx context.Context, workspaceKey, runID, nodeID, leaseID string, fencingToken int64, awaitInstanceKey string) (*domain.DriverRun, error)

	// ResumeAwaiting re-queues a suspended run
	// (suspended_awaiting_event -> queued) after the await cycle named by
	// awaitInstanceKey resolved, recording resumeSourceEventID (a trigger
	// event or the sweeper's synthetic timeout event) for the resumed
	// execution's replay fetch. Of two racing resumes exactly one wins; the
	// loser gets domain.ErrInvalidTransition, which resume callers (AW7)
	// tolerate.
	ResumeAwaiting(ctx context.Context, workspaceKey, runID, awaitInstanceKey, resumeSourceEventID string) (*domain.DriverRun, error)
}

// DriverRunCancelSupport is an OPTIONAL DriverRunStore capability (detected
// via type assertion, like TriggerEventAppender) backing the composition
// cancel cascade (AW10): when a parent run reaches a terminal status its
// queued children are cancelled and its running children get a cooperative
// cancel request. Backends without the capability (the fleet-db client until
// its server-side cascade wiring lands; the CLI tracing wrapper) skip the
// cascade — children there are bounded by their own await deadlines and the
// stale sweeps.
type DriverRunCancelSupport interface {
	// CancelQueuedRun terminalizes a still-QUEUED run as cancelled with no
	// owner check (mirroring the supersede lane's CancelQueuedDriverRun).
	// Idempotent on an already-cancelled run; any other status returns
	// domain.ErrInvalidTransition so a run claimed in the race window is
	// never terminalized under its executor.
	CancelQueuedRun(ctx context.Context, workspaceKey, runID, summary, errorClass string) (*domain.DriverRun, error)
	// RequestCancel stamps CancelRequestedAt on a RUNNING run. The owning
	// executor observes the marker on its next heartbeat and cancels the
	// runner, which then reports cancelled through the normal fenced Finish.
	// Idempotent once requested; non-running runs return
	// domain.ErrInvalidTransition.
	RequestCancel(ctx context.Context, workspaceKey, runID, reason string) (*domain.DriverRun, error)
}

var ErrDriverRunEventsUnavailable = errors.New("driver run event reader unsupported")

type DriverRunEventsReader interface {
	Events(ctx context.Context, workspaceKey, runID, after string, limit int) (*domain.PlatformEventsPage, error)
}

type DriverStepCreate struct {
	WorkspaceKey   string
	StepID         string
	DriverRunID    string
	StepKind       string
	Status         domain.DriverStepStatus
	TaskRunID      string
	ActionLedgerID string
	ExternalRef    string
	InputRef       string
	OutputRef      string
	StartedAt      time.Time
	EndedAt        *time.Time
	NodeID         string
	LeaseID        string
	FencingToken   int64
}

type DriverStepFilter struct {
	DriverRunID    string
	TaskRunID      string
	ActionLedgerID string
	StepKind       string
	Status         domain.DriverStepStatus
	Limit          int
}

type DriverStepUpdate struct {
	Status         *domain.DriverStepStatus `json:"status,omitempty"`
	TaskRunID      *string                  `json:"task_run_id,omitempty"`
	ActionLedgerID *string                  `json:"action_ledger_id,omitempty"`
	ExternalRef    *string                  `json:"external_ref,omitempty"`
	InputRef       *string                  `json:"input_ref,omitempty"`
	OutputRef      *string                  `json:"output_ref,omitempty"`
	StartedAt      *time.Time               `json:"started_at,omitempty"`
	ClearStartedAt bool                     `json:"clear_started_at,omitempty"`
	EndedAt        *time.Time               `json:"ended_at,omitempty"`
	ClearEndedAt   bool                     `json:"clear_ended_at,omitempty"`
	NodeID         string                   `json:"node_id,omitempty"`
	LeaseID        string                   `json:"lease_id,omitempty"`
	FencingToken   int64                    `json:"fencing_token,omitempty"`
}

type DriverStepStore interface {
	Create(ctx context.Context, in DriverStepCreate) (*domain.DriverStep, error)
	CreateForRun(ctx context.Context, workspaceKey, runID string, in DriverStepCreate) (*domain.DriverStep, error)
	Get(ctx context.Context, workspaceKey, stepID string) (*domain.DriverStep, error)
	List(ctx context.Context, workspaceKey string, filter DriverStepFilter) ([]*domain.DriverStep, error)
	ListForRun(ctx context.Context, workspaceKey, runID string, filter DriverStepFilter) ([]*domain.DriverStep, error)
	Update(ctx context.Context, workspaceKey, stepID string, update DriverStepUpdate) (*domain.DriverStep, error)
}

type TaskRunCreate struct {
	WorkspaceKey     string
	TaskRunID        string
	DriverRunID      string
	DriverStepID     string
	TaskID           string
	WorkerProfileID  string
	ProviderProfile  string
	Status           domain.TaskRunStatus
	NodeID           string
	LeaseID          string
	FencingToken     int64
	RunnerPlacement  domain.TaskRunPlacement
	SandboxPlacement domain.TaskRunPlacement
	RuntimeMetadata  map[string]string
	// Input is the optional task-run payload persisted on the run and
	// delivered to the runner (omitempty / back-compat).
	Input json.RawMessage
}

type TaskRunFilter struct {
	DriverRunID     string
	DriverStepID    string
	TaskID          string
	WorkerProfileID string
	Status          domain.TaskRunStatus
	Limit           int
}

type TaskRunClaim struct {
	TaskRunID          string
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
	ClaimedAt          time.Time
}

type TaskRunFinish struct {
	NodeID           string
	LeaseID          string
	LeaseToken       string
	FencingToken     int64
	Status           domain.TaskRunStatus
	ExitCode         *int
	LogsRef          string
	ArtifactsRef     string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	RuntimeMetadata  map[string]string
	ErrorClass       string
	ErrorMessage     string
	FinishedAt       time.Time
	// ParkTask marks the run's underlying task issue as parked when the
	// run finishes failed with its retry budget exhausted. Only valid with
	// Status == TaskRunFailed. Server-side the issue update is fenced by
	// the same lease/fencing checks as the finish itself, idempotent, and
	// best-effort: a missing, already-parked, or terminal issue is skipped
	// without failing the finish. Parking releases the issue claim; the
	// parked status prevents re-claim until a human moves the issue back
	// to open.
	ParkTask bool
}

type TaskRunRequeue struct {
	NodeID          string
	LeaseID         string
	LeaseToken      string
	FencingToken    int64
	RuntimeMetadata map[string]string
	LogsRef         string
	ArtifactsRef    string
	ErrorClass      string
	ErrorMessage    string
	RequeuedAt      time.Time
	// NextEligibleAt delays the requeued run from being claimed again until
	// the given time. The zero value keeps the run immediately claimable.
	NextEligibleAt time.Time
}

type TaskRunHeartbeat struct {
	NodeID          string
	LeaseID         string
	LeaseToken      string
	FencingToken    int64
	RuntimeMetadata map[string]string
	LogsRef         string
	ArtifactsRef    string
	HeartbeatAt     time.Time
}

type TaskRunComplete struct {
	CompletionID        string
	NodeID              string
	LeaseID             string
	LeaseToken          string
	FencingToken        int64
	Status              domain.TaskRunStatus
	ExitCode            *int
	LogsRef             string
	ArtifactsRef        string
	RequiredArtifactIDs []string
	RequireArtifacts    bool
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheWriteTokens    int64
	EstimatedCostUSD    float64
	RuntimeMetadata     map[string]string
	ErrorClass          string
	ErrorMessage        string
	CloseTask           bool
	CloseReason         string
	FinishedAt          time.Time
}

type TaskRunLogAppend struct {
	NodeID       string
	LeaseID      string
	LeaseToken   string
	FencingToken int64
	Stream       string
	Text         string
	Timestamp    time.Time
}

type TaskRunLogFilter struct {
	AfterSequence int64
	Limit         int
}

type TaskRunStore interface {
	Create(ctx context.Context, in TaskRunCreate) (*domain.TaskRun, error)
	ClaimQueued(ctx context.Context, workspaceKey string, claim TaskRunClaim) (*domain.TaskRun, error)
	Get(ctx context.Context, workspaceKey, taskRunID string) (*domain.TaskRun, error)
	List(ctx context.Context, workspaceKey string, filter TaskRunFilter) ([]*domain.TaskRun, error)
	Heartbeat(ctx context.Context, workspaceKey, taskRunID string, heartbeat TaskRunHeartbeat) (*domain.TaskRun, error)
	Requeue(ctx context.Context, workspaceKey, taskRunID string, requeue TaskRunRequeue) (*domain.TaskRun, error)
	Finish(ctx context.Context, workspaceKey, taskRunID string, finish TaskRunFinish) (*domain.TaskRun, error)
	Complete(ctx context.Context, workspaceKey, taskRunID string, complete TaskRunComplete) (*domain.TaskRun, error)
	AppendLog(ctx context.Context, workspaceKey, taskRunID string, appendLog TaskRunLogAppend) (*domain.TaskRunLogEntry, error)
	ListLogs(ctx context.Context, workspaceKey, taskRunID string, filter TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error)
}
