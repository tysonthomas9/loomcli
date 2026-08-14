package execution

import (
	"encoding/json"
	"strings"
	"time"
)

// Placement is Execution's transport-independent record of where a TaskRun
// runner or sandbox was selected. Provider-specific details remain opaque.
type Placement struct {
	Provider  string
	NodeID    string
	RunnerID  string
	SandboxID string
	CWD       string
	RepoRef   string
}

// TaskRun is the Execution-owned public snapshot. LeaseToken is deliberately
// absent: the credential remains in the claim command and worker process only.
type TaskRun struct {
	WorkspaceKey     string
	TaskRunID        string
	DriverRunID      string
	DriverStepID     string
	WorkItemID       string
	WorkerProfileID  string
	Runner           string
	RunnerRef        string
	RunnerKind       string
	RunnerEntrypoint string
	RunnerVersionID  string
	ProviderProfile  string
	TargetNodeID     string
	Status           Status
	Owner            Owner
	RunnerPlacement  Placement
	SandboxPlacement Placement
	RuntimeMetadata  map[string]string
	Input            json.RawMessage
	ExitCode         *int
	LogsRef          string
	ArtifactsRef     string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	ErrorClass       string
	ErrorMessage     string
	NextEligibleAt   *time.Time
	StartedAt        *time.Time
	LastHeartbeat    *time.Time
	FinishedAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// FlueTaskSessionIDPrefix preserves the public task-session identity that the
// historical Flue AgentSession projection exposed.
const FlueTaskSessionIDPrefix = "flue-"

// PublicTaskRunSessionID returns the session identity exposed by task-session
// and agent-history read projections for one canonical TaskRun.
func PublicTaskRunSessionID(run *TaskRun) string {
	if run == nil {
		return ""
	}
	taskRunID := strings.TrimSpace(run.TaskRunID)
	if taskRunID == "" {
		return ""
	}
	if strings.TrimSpace(run.RunnerKind) == "flue-workflow" ||
		strings.TrimSpace(run.RuntimeMetadata["runtime"]) == "flue" {
		return FlueTaskSessionIDPrefix + taskRunID
	}
	return taskRunID
}

// RequestTaskRunCommand is one parent-bound enqueue intent. The persistence
// port owns creation of the queued TaskRun, its deterministic queued event,
// and the DriverStep link as one idempotent application operation. Callers do
// not create or patch a DriverStep beside this command.
type RequestTaskRunCommand struct {
	WorkspaceKey string
	RequestID    string
	ParentOwner  Owner
	TaskRunID    string
	DriverRunID  string
	DriverStepID string
	WorkItemID   string
	// ClaimActionID binds the child TaskRun request to the exact owner-fenced
	// DriverRun Work Item claim that authorized execution of WorkItemID.
	ClaimActionID    string
	WorkerProfileID  string
	Runner           string
	RunnerRef        string
	RunnerKind       string
	RunnerEntrypoint string
	RunnerVersionID  string
	ProviderProfile  string
	TargetNodeID     string
	// RequiredCapabilities is the normalized capability envelope produced by
	// runner/provider preflight. Execution checks it against live workers
	// before any queued child state is written.
	RequiredCapabilities []string
	RunnerPlacement      Placement
	SandboxPlacement     Placement
	RuntimeMetadata      map[string]string
	Input                json.RawMessage
	RequestedAt          time.Time
}

type TaskRunDriverStep struct {
	WorkspaceKey   string
	StepID         string
	DriverRunID    string
	TaskRunID      string
	Status         string
	ActionLedgerID string
}

type RequestTaskRunResult struct {
	Run           *TaskRun
	Step          *TaskRunDriverStep
	ActionID      string
	ClaimActionID string
	Replay        bool
}

// ClaimTaskRunCommand invokes the backend's atomic claim-and-start command.
// LeaseToken is write-only and must never be copied into TaskRun or results.
type ClaimTaskRunCommand struct {
	WorkspaceKey       string
	RequestID          string
	TaskRunID          string
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string `json:"-"`
	LeaseTTL           time.Duration
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    Placement
	SandboxPlacement   Placement
	ClaimedAt          time.Time
}

type ClaimTaskRunResult struct {
	Run      *TaskRun
	Step     *TaskRunDriverStep
	ActionID string
	Replay   bool
}

// UpdateTaskRunWorkItemDesignCommand updates only the design fields of the
// Work Item durably bound to Owner's TaskRun. Design is a pointer so presence
// is explicit, but planner saves require nonblank content. A nil or blank
// DesignFormat canonicalizes to markdown.
type UpdateTaskRunWorkItemDesignCommand struct {
	WorkspaceKey string
	RequestID    string
	Owner        Owner
	Design       *string
	DesignFormat *string
}

// UpdateTaskRunWorkItemDesignResult exposes only the bounded mutation receipt.
// The Work Item projection and immutable commit remain inside the authoritative
// adapter, which validates that both belong to the fenced TaskRun.
type UpdateTaskRunWorkItemDesignResult struct {
	WorkItemID string
	ActionID   string
	Replay     bool
}

type RequeueTaskRunCommand struct {
	WorkspaceKey    string
	RequestID       string
	Owner           Owner
	RuntimeMetadata map[string]string
	LogsRef         string
	ArtifactsRef    string
	ErrorClass      string
	ErrorMessage    string
	RequeuedAt      time.Time
	NextEligibleAt  time.Time
}

type RequeueTaskRunResult struct {
	Run       *TaskRun
	Step      *TaskRunDriverStep
	Committed *RequeueTaskRunCommit
	ActionID  string
	Replay    bool
}

// RequeueTaskRunCommit is the immutable proof of the atomic requeue
// transition. Current Run/Step projections may already have advanced when a
// lost response is replayed; this receipt remains fixed to the original
// command and contains no owner or lease credential.
type RequeueTaskRunCommit struct {
	WorkspaceKey     string
	TaskRunID        string
	DriverRunID      string
	DriverStepID     string
	WorkItemID       string
	TaskRunStatus    Status
	DriverStepStatus string
	RuntimeMetadata  map[string]string
	LogsRef          string
	ArtifactsRef     string
	ErrorClass       string
	ErrorMessage     string
	RequeuedAt       time.Time
	NextEligibleAt   time.Time
}

// ExhaustTaskRunRetriesCommand is deliberately separate from ordinary
// Finalize. It asks the authoritative backend to atomically fail a running
// TaskRun and, when that TaskRun's durable Work Item generation is still
// current, block that exact generation after the retry budget is exhausted.
// A successor generation must be preserved; implementations must never
// degrade this to a TaskRun finish followed by a best-effort Work Item write.
type ExhaustTaskRunRetriesCommand struct {
	WorkspaceKey        string
	RequestID           string
	Owner               Owner
	Attempt             int
	MaxAttempts         int
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
	FinishedAt          time.Time
}

type ExhaustTaskRunRetriesResult struct {
	Run        *TaskRun
	WorkItemID string
	// WorkItemBlocked observes the current Work Item projection. The immutable
	// command outcome lives in Committed.WorkItemBlocked and can differ on
	// replay after legitimate Issue movement.
	WorkItemBlocked bool
	Committed       *ExhaustTaskRunRetriesCommit
	ActionID        string
	Replay          bool
}

// ExhaustTaskRunRetriesCommit is the immutable proof of which retry-budget
// branch committed: TaskRun failure plus either an exact-generation Work Item
// block or preservation of a successor/defensively absent Issue. The current
// Work Item may later move legitimately; replay validation relies on this
// receipt instead of treating today's Issue status as historical proof.
type ExhaustTaskRunRetriesCommit struct {
	WorkspaceKey        string
	TaskRunID           string
	WorkItemID          string
	TaskRunStatus       Status
	WorkItemBlocked     bool
	Attempt             int
	MaxAttempts         int
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
	FinishedAt          time.Time
}

type WorkerNodeDrainState string

const (
	WorkerNodeActive   WorkerNodeDrainState = "active"
	WorkerNodeDraining WorkerNodeDrainState = "draining"
	WorkerNodeDrained  WorkerNodeDrainState = "drained"
)

type WorkerNode struct {
	WorkspaceKey    string
	NodeID          string
	OwnerActor      string
	RuntimeProvider string
	Labels          []string
	Capabilities    []string
	ToolInventory   []string
	Version         string
	Capacity        int
	DrainState      WorkerNodeDrainState
	LastHeartbeat   time.Time
	ExpiresAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type RegisterWorkerNodeCommand struct {
	WorkspaceKey    string
	RequestID       string
	NodeID          string
	OwnerActor      string
	RuntimeProvider string
	Labels          []string
	Capabilities    []string
	ToolInventory   []string
	Version         string
	Capacity        int
	TTL             time.Duration
	RegisteredAt    time.Time
}

type HeartbeatWorkerNodeCommand struct {
	WorkspaceKey string
	RequestID    string
	NodeID       string
	TTL          time.Duration
	HeartbeatAt  time.Time
}

type SetWorkerNodeDrainCommand struct {
	WorkspaceKey string
	RequestID    string
	NodeID       string
	DrainState   WorkerNodeDrainState
	ChangedAt    time.Time
}

// TaskRunSchedulingQuery contains only the scheduling facts required by the
// request preflight. It is a read port; it cannot register or mutate a node or
// worker profile.
type TaskRunSchedulingQuery struct {
	WorkspaceKey     string
	TargetNodeID     string
	WorkerProfileID  string
	ProviderProfile  string
	RequiredFeatures []string
}

// ActiveTaskRunQuery is the bounded parent-run projection used by workflow
// status and watch transports. Execution defines which lifecycle states are
// active; callers cannot provide arbitrary status filters.
type ActiveTaskRunQuery struct {
	WorkspaceKey string
	DriverRunID  string
	Limit        int
}

// TaskRunArchiveQuery is Execution's bounded immutable history query for Run
// Capture composition. Empty WorkItemID means all TaskRuns in the workspace;
// Limit is mandatory so callers cannot request an unbounded archive scan.
type TaskRunArchiveQuery struct {
	WorkspaceKey string
	WorkItemID   string
	Limit        int
}

type TaskRunEventQuery struct {
	WorkspaceKey string
	EpicID       string
	DriverRunID  string
	AfterSeq     int64
	Limit        int
}

// TaskRunEvent is Execution's public append-only lifecycle observation. It
// intentionally contains no lease credential or persistence-specific cursor.
type TaskRunEvent struct {
	WorkspaceKey   string     `json:"workspaceKey"`
	EventID        string     `json:"eventID"`
	Seq            int64      `json:"seq"`
	EpicID         string     `json:"epicID,omitempty"`
	DriverRunID    string     `json:"driverRunID,omitempty"`
	WorkItemID     string     `json:"taskID,omitempty"`
	TaskRunID      string     `json:"taskRunID"`
	Type           string     `json:"type"`
	Status         Status     `json:"status,omitempty"`
	SchedulerState string     `json:"schedulerState,omitempty"`
	Attempt        int        `json:"attempt"`
	ErrorClass     string     `json:"errorClass,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	LogsRef        string     `json:"logsRef,omitempty"`
	ArtifactsRef   string     `json:"artifactsRef,omitempty"`
	NextEligibleAt *time.Time `json:"nextEligibleAt,omitempty"`
	OccurredAt     time.Time  `json:"occurredAt"`
}

type TaskRunSchedulingResult struct {
	Schedulable bool
	ReasonCode  string
}
