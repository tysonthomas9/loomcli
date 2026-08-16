package execution

import (
	"encoding/json"
	"strconv"
	"time"
)

// RuntimeProvider identifies the execution environment that owns a worker
// node. Providers are deliberately opaque to Execution's scheduling policy.
type RuntimeProvider string

const (
	RuntimeProviderLocal      RuntimeProvider = "local"
	RuntimeProviderE2B        RuntimeProvider = "e2b"
	RuntimeProviderKubernetes RuntimeProvider = "kubernetes"
	RuntimeProviderCI         RuntimeProvider = "ci"
	RuntimeProviderOther      RuntimeProvider = "other"
)

// DriverRunRecord is the complete durable Execution record. DriverRun is the
// smaller public projection returned by owner APIs; adapters map between the
// two at the Execution seam.
type DriverRunRecord struct {
	WorkspaceKey          string            `json:"workspace_key"`
	RunID                 string            `json:"run_id"`
	DriverID              string            `json:"driver_id"`
	DriverVersionID       string            `json:"driver_version_id"`
	Entrypoint            string            `json:"entrypoint,omitempty"`
	SourceKind            string            `json:"source_kind,omitempty"`
	SourceRef             string            `json:"source_ref,omitempty"`
	EpicID                string            `json:"epic_id,omitempty"`
	TriggerBindingID      string            `json:"trigger_binding_id,omitempty"`
	AgentServiceID        string            `json:"agent_service_id,omitempty"`
	SubjectKey            string            `json:"subject_key,omitempty"`
	Status                DriverRunStatus   `json:"status"`
	NodeID                string            `json:"node_id,omitempty"`
	LeaseID               string            `json:"lease_id,omitempty"`
	FencingToken          int64             `json:"fencing_token,omitempty"`
	IdempotencyKey        string            `json:"idempotency_key,omitempty"`
	Payload               json.RawMessage   `json:"payload,omitempty"`
	Output                map[string]string `json:"output,omitempty"`
	Summary               string            `json:"summary,omitempty"`
	ErrorClass            string            `json:"error_class,omitempty"`
	StartedAt             time.Time         `json:"started_at,omitempty"`
	LastHeartbeat         time.Time         `json:"last_heartbeat,omitempty"`
	FinishedAt            *time.Time        `json:"finished_at,omitempty"`
	ParentRunID           string            `json:"parent_run_id,omitempty"`
	AwaitInstanceKey      string            `json:"await_instance_key,omitempty"`
	SuspendedAt           *time.Time        `json:"suspended_at,omitempty"`
	CancelRequestedAt     *time.Time        `json:"cancel_requested_at,omitempty"`
	CancelRequestedReason string            `json:"cancel_requested_reason,omitempty"`
	ResumeSourceEventID   string            `json:"resume_source_event_id,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
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

func (status DriverStepStatus) IsTerminal() bool {
	return status == DriverStepCompleted || status == DriverStepFailed || status == DriverStepSkipped
}

type DriverStepRecord struct {
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

type TaskRunRecordStatus string

const (
	TaskRunRecordQueued    TaskRunRecordStatus = "queued"
	TaskRunRecordRunning   TaskRunRecordStatus = "running"
	TaskRunRecordCompleted TaskRunRecordStatus = "completed"
	TaskRunRecordFailed    TaskRunRecordStatus = "failed"
	TaskRunRecordCancelled TaskRunRecordStatus = "cancelled"
)

func (status TaskRunRecordStatus) IsTerminal() bool {
	return status == TaskRunRecordCompleted || status == TaskRunRecordFailed || status == TaskRunRecordCancelled
}

type TaskRunPlacementRecord struct {
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

func (placement TaskRunPlacementRecord) Empty() bool {
	return placement.Provider == "" && placement.NodeID == "" && placement.RunnerID == "" &&
		placement.ProcessRef == "" && placement.SandboxID == "" && placement.ImageOrSnapshot == "" &&
		placement.CWD == "" && placement.RepoRef == "" && placement.CleanupPolicy == "" &&
		placement.EgressMode == "" && placement.EgressMechanism == "" && placement.StartedAt.IsZero() &&
		placement.HeartbeatAt.IsZero() && placement.RetainedUntil == nil
}

type TaskRunRecord struct {
	WorkspaceKey               string                 `json:"workspace_key"`
	TaskRunID                  string                 `json:"task_run_id"`
	DriverRunID                string                 `json:"driver_run_id,omitempty"`
	DriverStepID               string                 `json:"driver_step_id,omitempty"`
	TaskID                     string                 `json:"task_id"`
	WorkerProfileID            string                 `json:"worker_profile_id,omitempty"`
	Runner                     string                 `json:"runner,omitempty"`
	RunnerRef                  string                 `json:"runner_ref,omitempty"`
	RunnerKind                 string                 `json:"runner_kind,omitempty"`
	RunnerEntrypoint           string                 `json:"runner_entrypoint,omitempty"`
	RunnerVersionID            string                 `json:"runner_driver_version_id,omitempty"`
	ProviderProfile            string                 `json:"provider_profile,omitempty"`
	TargetNodeID               string                 `json:"target_node_id,omitempty"`
	Status                     TaskRunRecordStatus    `json:"status"`
	NodeID                     string                 `json:"node_id,omitempty"`
	LeaseID                    string                 `json:"lease_id,omitempty"`
	FencingToken               int64                  `json:"fencing_token,omitempty"`
	RunnerPlacement            TaskRunPlacementRecord `json:"runner_placement,omitempty"`
	SandboxPlacement           TaskRunPlacementRecord `json:"sandbox_placement,omitempty"`
	Input                      json.RawMessage        `json:"input,omitempty"`
	ExitCode                   *int                   `json:"exit_code,omitempty"`
	LogsRef                    string                 `json:"logs_ref,omitempty"`
	ArtifactsRef               string                 `json:"artifacts_ref,omitempty"`
	InputTokens                int64                  `json:"input_tokens,omitempty"`
	OutputTokens               int64                  `json:"output_tokens,omitempty"`
	CacheReadTokens            int64                  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens           int64                  `json:"cache_write_tokens,omitempty"`
	EstimatedCostUSD           float64                `json:"estimated_cost_usd,omitempty"`
	RuntimeMetadata            map[string]string      `json:"runtime_metadata,omitempty"`
	NextEligibleAt             time.Time              `json:"next_eligible_at,omitempty"`
	StartedAt                  time.Time              `json:"started_at,omitempty"`
	LastHeartbeat              time.Time              `json:"last_heartbeat,omitempty"`
	FinishedAt                 *time.Time             `json:"finished_at,omitempty"`
	TerminalConvergenceVersion int                    `json:"terminal_convergence_version,omitempty"`
	TerminalConvergedAt        *time.Time             `json:"terminal_converged_at,omitempty"`
	ErrorClass                 string                 `json:"error_class,omitempty"`
	ErrorMessage               string                 `json:"error_message,omitempty"`
	CreatedAt                  time.Time              `json:"created_at"`
	UpdatedAt                  time.Time              `json:"updated_at"`
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

type TaskRunEventType string

const (
	TaskRunEventQueued    TaskRunEventType = "taskRunQueued"
	TaskRunEventClaimed   TaskRunEventType = "taskRunClaimed"
	TaskRunEventRequeued  TaskRunEventType = "taskRunRequeued"
	TaskRunEventCompleted TaskRunEventType = "taskRunCompleted"
	TaskRunEventFailed    TaskRunEventType = "taskRunFailed"
	TaskRunEventCancelled TaskRunEventType = "taskRunCancelled"
)

func TaskRunEventID(taskRunID string, attempt int, eventType TaskRunEventType) string {
	return taskRunID + "#" + strconv.Itoa(attempt) + "#" + string(eventType)
}

type TaskRunJournalEvent struct {
	WorkspaceKey   string              `json:"workspaceKey"`
	EventID        string              `json:"eventID"`
	Seq            int64               `json:"seq"`
	EpicID         string              `json:"epicID,omitempty"`
	DriverRunID    string              `json:"driverRunID,omitempty"`
	TaskID         string              `json:"taskID,omitempty"`
	TaskRunID      string              `json:"taskRunID"`
	Type           TaskRunEventType    `json:"type"`
	Status         TaskRunRecordStatus `json:"status,omitempty"`
	SchedulerState string              `json:"schedulerState,omitempty"`
	Attempt        int                 `json:"attempt"`
	ErrorClass     string              `json:"errorClass,omitempty"`
	ErrorMessage   string              `json:"errorMessage,omitempty"`
	LogsRef        string              `json:"logsRef,omitempty"`
	ArtifactsRef   string              `json:"artifactsRef,omitempty"`
	NextEligibleAt *time.Time          `json:"nextEligibleAt,omitempty"`
	OccurredAt     time.Time           `json:"occurredAt"`
}
