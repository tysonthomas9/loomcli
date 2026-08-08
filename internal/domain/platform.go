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
	GenerationID    string                   `json:"generation_id"`
	Name            string                   `json:"name"`
	Kind            AgentServiceKind         `json:"kind"`
	DesiredState    AgentServiceDesiredState `json:"desired_state"`
	RoleName        string                   `json:"role_name"`
	DriverID        string                   `json:"driver_id,omitempty"`
	DriverVersionID string                   `json:"driver_version_id,omitempty"`
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
	CreatedBy       string                   `json:"created_by,omitempty"`
	DeletedAt       *time.Time               `json:"deleted_at,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	UpdatedAt       time.Time                `json:"updated_at"`
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
	WorkspaceKey    string `json:"workspace_key"`
	RunID           string `json:"run_id"`
	DriverID        string `json:"driver_id"`
	DriverVersionID string `json:"driver_version_id"`
	Entrypoint      string `json:"entrypoint,omitempty"`
	SourceKind      string `json:"source_kind,omitempty"`
	SourceRef       string `json:"source_ref,omitempty"`
	EpicID          string `json:"epic_id,omitempty"`
	// TriggerBindingID is the binding whose trigger-dispatch leg admitted this
	// run (empty for non-trigger runs). The server (fleet-db) stamps and sends
	// it; decoded here for a tag-identical round-trip (AW5). It scopes a
	// binding's run history and failure health to that binding, so bindings
	// sharing a driver do not bleed metrics into each other.
	TriggerBindingID string `json:"trigger_binding_id,omitempty"`
	AgentServiceID   string `json:"agent_service_id,omitempty"`
	// SubjectKey is Fleet's rendered trigger-concurrency subject snapshot.
	// It is output-only for Loom's generic run store and lets atomic dispatch
	// responses round-trip the complete DriverRun contract.
	SubjectKey     string            `json:"subject_key,omitempty"`
	Status         DriverRunStatus   `json:"status"`
	NodeID         string            `json:"node_id,omitempty"`
	LeaseID        string            `json:"lease_id,omitempty"`
	FencingToken   int64             `json:"fencing_token,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Payload        json.RawMessage   `json:"payload,omitempty"`
	Output         map[string]string `json:"output,omitempty"`
	Summary        string            `json:"summary,omitempty"`
	ErrorClass     string            `json:"error_class,omitempty"`
	StartedAt      time.Time         `json:"started_at,omitempty"`
	LastHeartbeat  time.Time         `json:"last_heartbeat,omitempty"`
	FinishedAt     *time.Time        `json:"finished_at,omitempty"`
	// Composition + await fields (Phase D). snake_case tags like the rest
	// of this struct: the fleet-db client decodes v1 responses directly
	// into DriverRun (tag-identical round-trip, AW5); the driver/watch wire
	// carries runs through its own DTOs (internal/driver/run_events.go).
	//
	// ParentRunID links a child run spawned by a parent workflow run.
	// Empty means detached/root (no cancel cascade). Orthogonal to EpicID:
	// a run can belong to an epic, a parent run, both, or neither.
	ParentRunID string `json:"parent_run_id,omitempty"`
	// AwaitInstanceKey identifies the current await cycle while suspended or
	// while a matching event has won the pending-to-suspend race.
	AwaitInstanceKey string `json:"await_instance_key,omitempty"`
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
	WorkerProfileID  string           `json:"worker_profile_id,omitempty"`
	Runner           string           `json:"runner,omitempty"`
	RunnerRef        string           `json:"runner_ref,omitempty"`
	RunnerKind       string           `json:"runner_kind,omitempty"`
	RunnerEntrypoint string           `json:"runner_entrypoint,omitempty"`
	RunnerVersionID  string           `json:"runner_driver_version_id,omitempty"`
	ProviderProfile  string           `json:"provider_profile,omitempty"`
	TargetNodeID     string           `json:"target_node_id,omitempty"`
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
	// TerminalConvergenceVersion is the highest Execution-owned projection
	// protocol version durably completed for this terminal TaskRun. It is
	// advanced only through the typed convergence completion command.
	TerminalConvergenceVersion int        `json:"terminal_convergence_version,omitempty"`
	TerminalConvergedAt        *time.Time `json:"terminal_converged_at,omitempty"`
	ErrorClass                 string     `json:"error_class,omitempty"`
	ErrorMessage               string     `json:"error_message,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
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
