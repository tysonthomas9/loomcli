package execution

import (
	"encoding/json"
	"time"
)

// DriverRunStatus preserves the shipped DriverRun wire vocabulary. It stays
// distinct from Status, whose task-execution terminal vocabulary uses
// succeeded/blocked.
type DriverRunStatus string

const (
	DriverRunQueued         DriverRunStatus = "queued"
	DriverRunRunning        DriverRunStatus = "running"
	DriverRunCompleted      DriverRunStatus = "completed"
	DriverRunFailed         DriverRunStatus = "failed"
	DriverRunNeedsReview    DriverRunStatus = "needs_review"
	DriverRunCancelled      DriverRunStatus = "cancelled"
	DriverRunSuspendedAwait DriverRunStatus = "suspended_awaiting_event"
)

func (status DriverAwaitStatus) IsTerminal() bool {
	return status == DriverAwaitSatisfied || status == DriverAwaitTimedOut
}

func (status DriverRunStatus) IsTerminal() bool {
	switch status {
	case DriverRunCompleted, DriverRunFailed, DriverRunNeedsReview, DriverRunCancelled:
		return true
	default:
		return false
	}
}

// DriverRun is Execution's public snapshot. It intentionally contains no
// repository methods and no Store/domain type aliases.
type DriverRun struct {
	WorkspaceKey          string            `json:"workspace_key"`
	RunID                 string            `json:"run_id"`
	DriverID              string            `json:"driver_id"`
	DriverVersionID       string            `json:"driver_version_id"`
	Entrypoint            string            `json:"entrypoint,omitempty"`
	SourceKind            string            `json:"source_kind,omitempty"`
	SourceRef             string            `json:"source_ref,omitempty"`
	EpicID                string            `json:"epic_id,omitempty"`
	ParentRunID           string            `json:"parent_run_id,omitempty"`
	TriggerBindingID      string            `json:"trigger_binding_id,omitempty"`
	AgentServiceID        string            `json:"agent_service_id,omitempty"`
	SubjectKey            string            `json:"subject_key,omitempty"`
	Status                DriverRunStatus   `json:"status"`
	Owner                 Owner             `json:"owner,omitempty"`
	IdempotencyKey        string            `json:"idempotency_key,omitempty"`
	Payload               json.RawMessage   `json:"payload,omitempty"`
	Output                map[string]string `json:"output,omitempty"`
	Summary               string            `json:"summary,omitempty"`
	ErrorClass            string            `json:"error_class,omitempty"`
	StartedAt             time.Time         `json:"started_at,omitempty"`
	LastHeartbeat         time.Time         `json:"last_heartbeat,omitempty"`
	FinishedAt            *time.Time        `json:"finished_at,omitempty"`
	AwaitInstanceKey      string            `json:"await_instance_key,omitempty"`
	SuspendedAt           *time.Time        `json:"suspended_at,omitempty"`
	CancelRequestedAt     *time.Time        `json:"cancel_requested_at,omitempty"`
	CancelRequestedReason string            `json:"cancel_requested_reason,omitempty"`
	ResumeSourceEventID   string            `json:"resume_source_event_id,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

// DriverRunQuery is Execution's consumer-owned run-history filter. It keeps
// query callers independent of the horizontal repository filter model.
type DriverRunQuery struct {
	WorkspaceKey   string
	DriverID       string
	EpicID         string
	ParentRunID    string
	AgentServiceID string
	Status         DriverRunStatus
	Limit          int
}

type SubmitDriverRunCommand struct {
	WorkspaceKey     string
	RequestID        string
	RunID            string
	DriverID         string
	DriverVersionID  string
	Entrypoint       string
	SourceKind       string
	SourceRef        string
	EpicID           string
	ParentRunID      string
	TriggerBindingID string
	Payload          json.RawMessage
}

type StartChildDriverRunCommand struct {
	WorkspaceKey string
	RequestID    string
	Owner        Owner
	ChildKey     string
	// ChildRunID is derived by Execution from Owner.ResourceID + ChildKey
	// before the atomic port is invoked. External callers must omit it or send
	// the exact deterministic value.
	ChildRunID      string
	DriverID        string
	DriverVersionID string
	Payload         json.RawMessage
	MaxDepth        int
}

type StartChildDriverRunResult struct {
	Parent      *DriverRun
	Child       *DriverRun
	ParentDepth int
	ChildDepth  int
	ActionID    string
	Replay      bool
}

type CascadeChildDriverRunsCommand struct {
	WorkspaceKey string
	RequestID    string
	Owner        Owner
	ParentRunID  string
	ParentStatus DriverRunStatus
	Reason       string
	ErrorClass   string
	CascadedAt   time.Time
	MaxDepth     int
	// SystemRecovery distinguishes the durable outcome-reconciler lane from
	// the live pre-finalize parent-authority lane. Both address the same
	// deterministic backend action receipt.
	SystemRecovery bool
}

type RecoverChildDriverRunCascadeCommand struct {
	WorkspaceKey string
	RequestID    string
	ParentRunID  string
	ParentStatus DriverRunStatus
	Reason       string
	ErrorClass   string
	CascadedAt   time.Time
	MaxDepth     int
}

// CascadeChildDriverRunsCommit is the immutable action receipt projection.
// Current cancel-requested runs can terminalize before a lost response is
// replayed, so the committed ID sets are kept separate from current runs.
type CascadeChildDriverRunsCommit struct {
	WorkspaceKey          string
	ParentRunID           string
	ParentStatus          DriverRunStatus
	Reason                string
	ErrorClass            string
	CascadedAt            time.Time
	MaxDepth              int
	CancelledRunIDs       []string
	CancelRequestedRunIDs []string
}

type CascadeChildDriverRunsResult struct {
	CancelledRuns       []*DriverRun
	CancelRequestedRuns []*DriverRun
	Committed           *CascadeChildDriverRunsCommit
	ActionID            string
	Replay              bool
}

// RecoverTerminalDriverRunWorkCommand converges TaskRuns and exact Work Item
// claim generations after their parent DriverRun is durably terminal. It is a
// system-only command because the terminal parent no longer has a live owner
// authority with which to fence the mutation.
type RecoverTerminalDriverRunWorkCommand struct {
	WorkspaceKey string
	RequestID    string
	DriverRunID  string
	ParentStatus DriverRunStatus
	Reason       string
	ErrorClass   string
	RecoveredAt  time.Time
}

// RecoverTerminalDriverRunWorkCommit is the immutable semantic receipt used
// to validate both the first response and a replay after response loss.
type RecoverTerminalDriverRunWorkCommit struct {
	WorkspaceKey                  string
	DriverRunID                   string
	ParentStatus                  DriverRunStatus
	Reason                        string
	ErrorClass                    string
	RecoveredAt                   time.Time
	RecoveredTaskRunIDs           []string
	ReleasedWorkItemIDs           []string
	PreservedSuccessorWorkItemIDs []string
}

// RecoverTerminalDriverRunWorkResult contains immutable identities from the
// committed recovery receipt. PreservedSuccessorWorkItemIDs explicitly proves
// that stale parent cleanup did not rewrite a newer Work Item generation.
type RecoverTerminalDriverRunWorkResult struct {
	RecoveredTaskRunIDs           []string
	ReleasedWorkItemIDs           []string
	PreservedSuccessorWorkItemIDs []string
	Committed                     *RecoverTerminalDriverRunWorkCommit
	ActionID                      string
	Replay                        bool
}

type ClaimDriverRunCommand struct {
	WorkspaceKey string
	RequestID    string
	RunID        string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
}

type DriverRunHeartbeatCommand struct {
	WorkspaceKey string
	Owner        Owner
	At           time.Time
}

// ClaimDriverRunWorkItemCommand is one live-parent-authorized Work Item
// claim. RequestID is deterministic for the parent/task pair. ClaimTTL may be
// zero to select the backend's bounded default, or at least one second.
type ClaimDriverRunWorkItemCommand struct {
	WorkspaceKey   string
	RequestID      string
	Owner          Owner
	WorkItemID     string
	ClaimTTL       time.Duration
	RequiredStatus string
	ClaimedAt      time.Time
}

const (
	DriverRunWorkItemRestoreOpen   = "open"
	DriverRunWorkItemRestoreReview = "review"

	DriverRunReviewWorkItemPriorityMin     = 0
	DriverRunReviewWorkItemPriorityMax     = 4
	DriverRunReviewWorkItemMaxLabels       = 8
	DriverRunReviewWorkItemMaxLabelBytes   = 64
	DriverRunReviewWorkItemMaxCommentBytes = 10000
)

// ReleaseDriverRunWorkItemCommand releases exactly the claim action created
// for this parent/task pair. A caller cannot substitute an actor or release a
// different DriverRun's claim.
type ReleaseDriverRunWorkItemCommand struct {
	WorkspaceKey  string
	RequestID     string
	Owner         Owner
	WorkItemID    string
	ClaimActionID string
	RestoreStatus string
	ReleasedAt    time.Time
}

// HandoffDriverRunReviewWorkItemCommand atomically publishes the outcome of a
// completed review child while the parent still owns the exact retained claim.
// The port must prove the child belongs to this parent/Work Item, completed
// successfully, and opted into retained ownership before changing lifecycle.
type HandoffDriverRunReviewWorkItemCommand struct {
	WorkspaceKey  string
	RequestID     string
	Owner         Owner
	WorkItemID    string
	ClaimActionID string
	TaskRunID     string
	TargetStatus  string
	Reason        string
	Priority      *int
	Labels        []string
	CommentBody   string
	ExternalRef   *string
	HandedOffAt   time.Time
}

// DriverRunWorkItem is the Execution-owned projection returned by the
// authoritative owner-fenced Work Item command.
type DriverRunWorkItem struct {
	WorkspaceKey string
	WorkItemID   string
	Title        string
	Status       string
	Priority     int
	IssueType    string
	Assignee     string
	Labels       []string
	SourceRepo   string
	ExternalRef  string
	ParentID     string
	UpdatedAt    time.Time
}

// DriverRunWorkItemAction is the immutable action receipt envelope used to
// certify a Work Item mutation. It deliberately contains no lease token.
type DriverRunWorkItemAction struct {
	WorkspaceKey   string
	ActionID       string
	IdempotencyKey string
	ActionType     string
	TargetRef      string
	RequestedBy    string
	Status         string
	RequestRef     string
	ResponseRef    string
	CreatedAt      time.Time
	AppliedAt      *time.Time
}

type DriverRunWorkItemComment struct {
	CommentID  string
	WorkItemID string
	Author     string
	Body       string
	CreatedAt  time.Time
}

type DriverRunWorkItemMutationResult struct {
	WorkItem *DriverRunWorkItem
	Action   *DriverRunWorkItemAction
	Comment  *DriverRunWorkItemComment
	Replay   bool
}

type FinalizeDriverRunCommand struct {
	WorkspaceKey string
	RequestID    string
	Owner        Owner
	Status       DriverRunStatus
	Summary      string
	ErrorClass   string
	Output       map[string]string
	FinishedAt   time.Time
}

type RecoverDriverRunsCommand struct {
	WorkspaceKey string
	RequestID    string
	ObservedAt   time.Time
	MaxAge       time.Duration
	ErrorClass   string
	Summary      string
	Limit        int
}

type DriverRunRecoveryResult struct {
	WorkspaceKey       string
	StaleBefore        time.Time
	RecoveredAt        time.Time
	Recovered          int
	SkippedFresh       int
	RecoveredRunIDs    []string
	SkippedFreshRunIDs []string
}

type AwaitDriverRunCommand struct {
	WorkspaceKey    string
	RequestID       string
	Owner           Owner
	Pattern         string
	ActorAllow      []string
	Timeout         time.Duration
	AwaitIndex      int
	MaxTimeout      time.Duration
	MaxPerRun       int
	TotalSuspendCap time.Duration
	RegisteredAt    time.Time
}

type ResolveDriverAwaitCommand struct {
	WorkspaceKey string
	RequestID    string
	InstanceKey  string
	EventID      string
	Actor        string
	Payload      json.RawMessage
}

type DriverAwaitStatus string

const (
	DriverAwaitPending   DriverAwaitStatus = "pending"
	DriverAwaitSatisfied DriverAwaitStatus = "satisfied"
	DriverAwaitTimedOut  DriverAwaitStatus = "timed_out"
)

type DriverAwaitInstance struct {
	WorkspaceKey       string            `json:"workspaceKey"`
	InstanceKey        string            `json:"instanceKey"`
	RunID              string            `json:"runId"`
	Pattern            string            `json:"pattern"`
	ActorAllow         []string          `json:"actorAllow,omitempty"`
	Deadline           time.Time         `json:"deadline"`
	RegisteredAt       time.Time         `json:"registeredAt"`
	Status             DriverAwaitStatus `json:"status"`
	SatisfiedByEventID string            `json:"satisfiedByEventId,omitempty"`
	SatisfiedActor     string            `json:"satisfiedActor,omitempty"`
	SatisfiedPayload   json.RawMessage   `json:"satisfiedPayload,omitempty"`
	ResolvedAt         *time.Time        `json:"resolvedAt,omitempty"`
	ResumedAt          *time.Time        `json:"resumedAt,omitempty"`
}

type DriverAwaitResult struct {
	Status   string
	Instance *DriverAwaitInstance
	Run      *DriverRun
	Replay   bool
}

// DriverRunStep is Execution's read-only projection of a step linked to one
// DriverRun. It exposes no repository mutation surface.
type DriverRunStep struct {
	WorkspaceKey string `json:"workspace_key"`
	StepID       string `json:"step_id"`
	DriverRunID  string `json:"driver_run_id"`
	StepKind     string `json:"step_kind"`
	Status       string `json:"status"`
	TaskRunID    string `json:"task_run_id,omitempty"`
}

type DriverRunEvent struct {
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

type DriverRunEventQuery struct {
	WorkspaceKey string
	RunID        string
	After        string
	Limit        int
}

type DriverRunEventPage struct {
	Events []DriverRunEvent `json:"events"`
	Cursor string           `json:"cursor"`
}

const DriverAwaitOutcomeSuspended = "suspended"
