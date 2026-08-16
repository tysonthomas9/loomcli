package execution

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

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
	ParentRunID string
	// TriggerBindingID stamps the run with the binding whose trigger-dispatch
	// leg admitted it (mirrors fleet-db's dispatchTriggerRouteLeg). Empty for
	// manually-created runs, which belong to no binding.
	TriggerBindingID string
	IdempotencyKey   string
	Payload          json.RawMessage
}

type DriverRunFilter struct {
	DriverID        string
	DriverVersionID string
	EpicID          string
	NodeID          string
	// BindingID filters to runs a trigger-dispatch leg stamped with this
	// binding id (DriverRunRecord.TriggerBindingID), sent to fleet-db as the
	// trigger_binding_id query param. Empty means no binding constraint.
	BindingID string
	// AgentServiceID filters to runs fleet-db stamped with this agent service
	// at dispatch, sent as the agent_service_id query param. Empty means no
	// agent-service constraint.
	AgentServiceID string
	Status         DriverRunStatus
	Limit          int
}

type DriverRunFinish struct {
	NodeID       string
	LeaseID      string
	FencingToken int64
	Status       DriverRunStatus
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
	Create(ctx context.Context, in DriverRunCreate) (*DriverRunRecord, error)
	CreateEpic(ctx context.Context, workspaceKey, epicID string, in EpicRunCreate) (*DriverRunRecord, error)
	Get(ctx context.Context, workspaceKey, runID string) (*DriverRunRecord, error)
	List(ctx context.Context, workspaceKey string, filter DriverRunFilter) ([]*DriverRunRecord, error)
	Claim(ctx context.Context, workspaceKey, runID, nodeID, leaseID string) (*DriverRunRecord, error)
	Heartbeat(ctx context.Context, workspaceKey, runID, nodeID, leaseID string, fencingToken int64) (*DriverRunRecord, error)
	Finish(ctx context.Context, workspaceKey, runID string, finish DriverRunFinish) (*DriverRunRecord, error)
	RecoverStale(ctx context.Context, workspaceKey string, recover StaleDriverRunRecovery) (*StaleDriverRunRecoveryResult, error)
	RecoverStaleTaskRuns(ctx context.Context, workspaceKey, runID string, recover StaleTaskRunRecovery) (*StaleTaskRunRecoveryResult, error)

	// Suspend suspends a running run on its await instance
	// (running -> suspended_awaiting_event), owner-fenced with the same
	// node+lease+token guard as Finish, releasing the executor slot
	// (node/lease cleared). awaitInstanceKey names the await cycle the run
	// suspends on (runID#await-{n}) and is required. Idempotent on re-suspend.
	// A backend that recorded a pending resume for this await cycle (the
	// accepted pending->suspend window) returns
	// ErrAlreadyResumed: do not suspend, continue inline.
	Suspend(ctx context.Context, workspaceKey, runID, nodeID, leaseID string, fencingToken int64, awaitInstanceKey string) (*DriverRunRecord, error)

	// ResumeAwaiting re-queues a suspended run
	// (suspended_awaiting_event -> queued) after the await cycle named by
	// awaitInstanceKey resolved, recording resumeSourceEventID (a trigger
	// event or the sweeper's synthetic timeout event) for the resumed
	// execution's replay fetch. Of two racing resumes exactly one wins; the
	// loser gets persistence.ErrInvalidTransition, which resume callers (AW7)
	// tolerate.
	ResumeAwaiting(ctx context.Context, workspaceKey, runID, awaitInstanceKey, resumeSourceEventID string) (*DriverRunRecord, error)
}

// DriverRunOutcomeStore is an optional DriverRunStore capability. Production
// FleetDB and the in-memory backend implement it; wrappers must forward it so
// the registered Execution reconciler cannot silently lose durability.
type DriverRunOutcomeStore interface {
	ClaimDriverRunOutcomes(context.Context, DriverRunOutcomeLease) ([]DriverRunOutcome, error)
	CompleteDriverRunOutcome(context.Context, DriverRunOutcomeCompletion) error
	RetryDriverRunOutcome(context.Context, DriverRunOutcomeRetry) error
}

// TerminalDriverRunWorkRecoveryQueueStore is the optional durable queue that
// drives the atomic RecoverTerminalDriverRunWork command. Attempt belongs to
// this lane and is independent from ordinary run-outcome delivery attempts.
type TerminalDriverRunWorkRecoveryQueueStore interface {
	ClaimTerminalDriverRunWorkRecoveries(context.Context, TerminalDriverRunWorkRecoveryLease) ([]DriverRunOutcome, error)
	CompleteTerminalDriverRunWorkRecovery(context.Context, TerminalDriverRunWorkRecoveryCompletion) error
	RetryTerminalDriverRunWorkRecovery(context.Context, TerminalDriverRunWorkRecoveryRetry) error
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
	// persistence.ErrInvalidTransition so a run claimed in the race window is
	// never terminalized under its executor.
	CancelQueuedRun(ctx context.Context, workspaceKey, runID, summary, errorClass string) (*DriverRunRecord, error)
	// RequestCancel stamps CancelRequestedAt on a RUNNING run. The owning
	// executor observes the marker on its next heartbeat and cancels the
	// runner, which then reports cancelled through the normal fenced Finish.
	// Idempotent once requested; non-running runs return
	// persistence.ErrInvalidTransition.
	RequestCancel(ctx context.Context, workspaceKey, runID, reason string) (*DriverRunRecord, error)
}

var ErrDriverRunEventsUnavailable = errors.New("driver run event reader unsupported")

type DriverRunEventsReader interface {
	Events(ctx context.Context, workspaceKey, runID, after string, limit int) (*AuditPage, error)
}

type DriverStepCreate struct {
	WorkspaceKey   string
	StepID         string
	DriverRunID    string
	StepKind       string
	Status         DriverStepStatus
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
	Status         DriverStepStatus
	Limit          int
}

type DriverStepUpdate struct {
	Status         *DriverStepStatus `json:"status,omitempty"`
	TaskRunID      *string           `json:"task_run_id,omitempty"`
	ActionLedgerID *string           `json:"action_ledger_id,omitempty"`
	ExternalRef    *string           `json:"external_ref,omitempty"`
	InputRef       *string           `json:"input_ref,omitempty"`
	OutputRef      *string           `json:"output_ref,omitempty"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	ClearStartedAt bool              `json:"clear_started_at,omitempty"`
	EndedAt        *time.Time        `json:"ended_at,omitempty"`
	ClearEndedAt   bool              `json:"clear_ended_at,omitempty"`
	NodeID         string            `json:"node_id,omitempty"`
	LeaseID        string            `json:"lease_id,omitempty"`
	FencingToken   int64             `json:"fencing_token,omitempty"`
}

type DriverStepStore interface {
	Create(ctx context.Context, in DriverStepCreate) (*DriverStepRecord, error)
	CreateForRun(ctx context.Context, workspaceKey, runID string, in DriverStepCreate) (*DriverStepRecord, error)
	Get(ctx context.Context, workspaceKey, stepID string) (*DriverStepRecord, error)
	List(ctx context.Context, workspaceKey string, filter DriverStepFilter) ([]*DriverStepRecord, error)
	ListForRun(ctx context.Context, workspaceKey, runID string, filter DriverStepFilter) ([]*DriverStepRecord, error)
	Update(ctx context.Context, workspaceKey, stepID string, update DriverStepUpdate) (*DriverStepRecord, error)
}

type TaskRunCreate struct {
	WorkspaceKey     string
	TaskRunID        string
	DriverRunID      string
	DriverStepID     string
	TaskID           string
	WorkerProfileID  string
	Runner           string
	RunnerRef        string
	RunnerKind       string
	RunnerEntrypoint string
	RunnerVersionID  string
	ProviderProfile  string
	TargetNodeID     string
	Status           TaskRunRecordStatus
	NodeID           string
	LeaseID          string
	// LeaseToken is accepted only by compatibility stores that must seed a
	// pre-running fenced TaskRun. Production claim/start transports carry the
	// token as an opaque header and never persist or return it in the TaskRun.
	LeaseToken       string
	FencingToken     int64
	RunnerPlacement  TaskRunPlacementRecord
	SandboxPlacement TaskRunPlacementRecord
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
	Status          TaskRunRecordStatus
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
	RunnerPlacement    TaskRunPlacementRecord
	SandboxPlacement   TaskRunPlacementRecord
	ClaimedAt          time.Time
}

type TaskRunFinish struct {
	NodeID           string
	LeaseID          string
	LeaseToken       string
	FencingToken     int64
	Status           TaskRunRecordStatus
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
	// BlockTask marks the run's underlying task issue as blocked when the
	// run finishes failed with its retry budget exhausted. Only valid with
	// Status == TaskRunFailed. Server-side the issue update is fenced by
	// the same lease/fencing checks as the finish itself, idempotent, and
	// best-effort: a missing, already-blocked, or terminal issue is skipped
	// without failing the finish. Blocking releases the issue claim; moving
	// the issue back to open makes it eligible for the ready queue again.
	BlockTask bool
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
	Status              TaskRunRecordStatus
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
	RequestID    string
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

// TaskRunTerminalConvergenceQuery selects terminal TaskRuns whose durable
// Execution convergence marker is older than RequiredVersion. After is an
// exclusive TaskRun-ID cursor; each periodic pass starts from an empty cursor.
type TaskRunTerminalConvergenceQuery struct {
	WorkspaceKey    string
	RequiredVersion int
	After           string
	Limit           int
}

type TaskRunTerminalConvergencePage struct {
	TaskRunIDs []string `json:"task_run_ids"`
	Next       string   `json:"next,omitempty"`
}

type TaskRunTerminalConvergenceComplete struct {
	WorkspaceKey    string
	TaskRunID       string
	RequiredVersion int
	CompletedAt     time.Time
}

type TaskRunTerminalConvergenceResult struct {
	TaskRun  *TaskRunRecord `json:"task_run"`
	Replayed bool           `json:"replayed"`
}

// TaskRunTerminalConvergenceStore is the narrow Execution-owned checkpoint
// port. It is intentionally separate from the general TaskRun lifecycle store
// so callers cannot spoof the marker through runtime metadata.
type TaskRunTerminalConvergenceStore interface {
	ListTaskRunTerminalConvergenceCandidates(context.Context, TaskRunTerminalConvergenceQuery) (TaskRunTerminalConvergencePage, error)
	CompleteTaskRunTerminalConvergence(context.Context, TaskRunTerminalConvergenceComplete) (*TaskRunTerminalConvergenceResult, error)
}

type TaskRunStore interface {
	Create(ctx context.Context, in TaskRunCreate) (*TaskRunRecord, error)
	ClaimQueued(ctx context.Context, workspaceKey string, claim TaskRunClaim) (*TaskRunRecord, error)
	Get(ctx context.Context, workspaceKey, taskRunID string) (*TaskRunRecord, error)
	List(ctx context.Context, workspaceKey string, filter TaskRunFilter) ([]*TaskRunRecord, error)
	Heartbeat(ctx context.Context, workspaceKey, taskRunID string, heartbeat TaskRunHeartbeat) (*TaskRunRecord, error)
	Requeue(ctx context.Context, workspaceKey, taskRunID string, requeue TaskRunRequeue) (*TaskRunRecord, error)
	Finish(ctx context.Context, workspaceKey, taskRunID string, finish TaskRunFinish) (*TaskRunRecord, error)
	Complete(ctx context.Context, workspaceKey, taskRunID string, complete TaskRunComplete) (*TaskRunRecord, error)
	AppendLog(ctx context.Context, workspaceKey, taskRunID string, appendLog TaskRunLogAppend) (*TaskRunLogEntry, error)
	ListLogs(ctx context.Context, workspaceKey, taskRunID string, filter TaskRunLogFilter) ([]*TaskRunLogEntry, error)
}
