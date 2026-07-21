package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	ErrExecutionNotFound          = errors.New("fleetdb: execution not found")
	ErrExecutionInvalid           = errors.New("fleetdb: execution invalid request")
	ErrExecutionConflict          = errors.New("fleetdb: execution conflict")
	ErrExecutionNotOwner          = errors.New("fleetdb: execution not owner")
	ErrExecutionInvalidTransition = errors.New("fleetdb: execution invalid transition")
	ErrExecutionAlreadyResumed    = errors.New("fleetdb: execution driver run already resumed")
	ErrExecutionUnavailable       = errors.New("fleetdb: execution unavailable")
)

// ExecutionClaimAndStartCommand is the low-level request for FleetDB's one
// atomic Issue-claim + TaskRun-start transaction. An empty TaskRunID selects
// the oldest eligible queued run through claim-next-and-start; a non-empty ID
// claims that exact run. LeaseToken is sent only in X-Lease-Token and is never
// copied into the request body or result.
type ExecutionClaimAndStartCommand struct {
	WorkspaceKey       string
	CommandID          string
	TaskRunID          string
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string `json:"-"`
	ClaimTTL           time.Duration
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
}

type ExecutionClaimAndStartResult struct {
	TaskRun    *domain.TaskRun        `json:"task_run"`
	DriverStep *domain.DriverStep     `json:"driver_step"`
	Issue      *ExecutionIssue        `json:"issue"`
	Action     *ExecutionActionLedger `json:"action"`
	Replayed   bool                   `json:"replayed"`
}

// ExecutionTaskRunRequestCommand is FleetDB's atomic queued TaskRun request.
// LeaseToken is write-only and is sent exclusively as X-Lease-Token.
type ExecutionTaskRunRequestCommand struct {
	WorkspaceKey         string
	CommandID            string
	TaskRunID            string
	DriverRunID          string
	DriverStepID         string
	TaskID               string
	ClaimActionID        string
	NodeID               string
	LeaseID              string
	LeaseToken           string `json:"-"`
	FencingToken         int64
	WorkerProfileID      string
	Runner               string
	RunnerRef            string
	RunnerKind           string
	RunnerEntrypoint     string
	RunnerVersionID      string
	ProviderProfile      string
	TargetNodeID         string
	RequiredCapabilities []string
	RunnerPlacement      domain.TaskRunPlacement
	SandboxPlacement     domain.TaskRunPlacement
	RuntimeMetadata      map[string]string
	Input                []byte
	RequestedAt          time.Time
	ReplayOnly           bool
}

type ExecutionTaskRunRequestResult struct {
	DriverStep                *domain.DriverStep      `json:"driver_step"`
	TaskRun                   *domain.TaskRun         `json:"task_run"`
	Action                    *ExecutionActionLedger  `json:"action"`
	ClaimActionID             string                  `json:"claim_action_id"`
	CommittedTaskRunStatus    domain.TaskRunStatus    `json:"committed_task_run_status"`
	CommittedDriverStepStatus domain.DriverStepStatus `json:"committed_driver_step_status"`
	Replayed                  bool                    `json:"replayed"`
}

type ExecutionTaskRunRequeueCommand struct {
	WorkspaceKey    string
	CommandID       string
	TaskRunID       string
	NodeID          string
	LeaseID         string
	LeaseToken      string `json:"-"`
	FencingToken    int64
	RuntimeMetadata map[string]string
	LogsRef         string
	ArtifactsRef    string
	ErrorClass      string
	ErrorMessage    string
	RequeuedAt      time.Time
	NextEligibleAt  time.Time
}

type ExecutionTaskRunRequeueCommit struct {
	WorkspaceKey     string                  `json:"workspace_key"`
	TaskRunID        string                  `json:"task_run_id"`
	DriverRunID      string                  `json:"driver_run_id"`
	DriverStepID     string                  `json:"driver_step_id"`
	TaskID           string                  `json:"task_id"`
	Attempt          int                     `json:"attempt"`
	Status           domain.TaskRunStatus    `json:"status"`
	DriverStepStatus domain.DriverStepStatus `json:"driver_step_status"`
	RuntimeMetadata  map[string]string       `json:"runtime_metadata,omitempty"`
	LogsRef          string                  `json:"logs_ref,omitempty"`
	ArtifactsRef     string                  `json:"artifacts_ref,omitempty"`
	ErrorClass       string                  `json:"error_class,omitempty"`
	ErrorMessage     string                  `json:"error_message,omitempty"`
	RequeuedAt       time.Time               `json:"requeued_at"`
	NextEligibleAt   time.Time               `json:"next_eligible_at,omitempty"`
}

type ExecutionTaskRunRequeueResult struct {
	TaskRun    *domain.TaskRun               `json:"task_run"`
	DriverStep *domain.DriverStep            `json:"driver_step"`
	Action     *ExecutionActionLedger        `json:"action"`
	Committed  ExecutionTaskRunRequeueCommit `json:"committed"`
	Replayed   bool                          `json:"replayed"`
}

type ExecutionTaskRunRetryExhaustionCommand struct {
	WorkspaceKey        string
	CommandID           string
	TaskRunID           string
	NodeID              string
	LeaseID             string
	LeaseToken          string `json:"-"`
	FencingToken        int64
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

type ExecutionTaskRunRetryExhaustionCommit struct {
	WorkspaceKey        string               `json:"workspace_key"`
	TaskRunID           string               `json:"task_run_id"`
	TaskID              string               `json:"task_id"`
	Status              domain.TaskRunStatus `json:"status"`
	IssueBlocked        bool                 `json:"issue_blocked"`
	Attempt             int                  `json:"attempt"`
	MaxAttempts         int                  `json:"max_attempts"`
	ExitCode            *int                 `json:"exit_code,omitempty"`
	LogsRef             string               `json:"logs_ref,omitempty"`
	ArtifactsRef        string               `json:"artifacts_ref,omitempty"`
	RequiredArtifactIDs []string             `json:"required_artifact_ids,omitempty"`
	RequireArtifacts    bool                 `json:"require_artifacts"`
	InputTokens         int64                `json:"input_tokens,omitempty"`
	OutputTokens        int64                `json:"output_tokens,omitempty"`
	CacheReadTokens     int64                `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens    int64                `json:"cache_write_tokens,omitempty"`
	EstimatedCostUSD    float64              `json:"estimated_cost_usd,omitempty"`
	RuntimeMetadata     map[string]string    `json:"runtime_metadata,omitempty"`
	ErrorClass          string               `json:"error_class"`
	ErrorMessage        string               `json:"error_message"`
	FinishedAt          time.Time            `json:"finished_at"`
}

type ExecutionTaskRunRetryExhaustionResult struct {
	TaskRun *domain.TaskRun `json:"task_run"`
	// Issue is the current Work Item projection. FleetDB returns null only for
	// a defensively absent Issue; a stale command may instead return the
	// successor projection that it deliberately preserved.
	Issue     *ExecutionIssue                       `json:"issue"`
	Action    *ExecutionActionLedger                `json:"action"`
	Committed ExecutionTaskRunRetryExhaustionCommit `json:"committed"`
	Replayed  bool                                  `json:"replayed"`
}

type ExecutionDriverRunClaimCommand struct {
	WorkspaceKey string
	RequestID    string
	RunID        string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
}

type ExecutionDriverRunHeartbeatCommand struct {
	WorkspaceKey string
	RunID        string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	FencingToken int64
}

type ExecutionDriverRunWorkItemClaimCommand struct {
	WorkspaceKey string
	CommandID    string
	RunID        string
	TaskID       string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	FencingToken int64
	ClaimTTL     time.Duration
	ClaimedAt    time.Time
}

type ExecutionDriverRunWorkItemReleaseCommand struct {
	WorkspaceKey  string
	CommandID     string
	RunID         string
	TaskID        string
	NodeID        string
	LeaseID       string
	LeaseToken    string `json:"-"`
	FencingToken  int64
	ClaimActionID string
	ReleasedAt    time.Time
}

type ExecutionDriverRunWorkItemResult struct {
	Issue    *ExecutionIssue        `json:"issue"`
	Action   *ExecutionActionLedger `json:"action"`
	Replayed bool                   `json:"replayed"`
}

type ExecutionDriverRunSuspendCommand struct {
	WorkspaceKey     string
	RunID            string
	NodeID           string
	LeaseID          string
	LeaseToken       string `json:"-"`
	FencingToken     int64
	AwaitInstanceKey string
}

// ExecutionDriverRunStaleTaskRecoveryCommand is the parent-owner command for
// recovering stale child TaskRuns. It is deliberately not a system sweep:
// the raw parent token is required and crosses the wire only in
// X-Lease-Token.
type ExecutionDriverRunStaleTaskRecoveryCommand struct {
	WorkspaceKey string
	RequestID    string
	RunID        string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	FencingToken int64
	StaleBefore  time.Time
	ErrorClass   string
	ErrorMessage string
}

type ExecutionDriverRunStaleTaskRecoveryResult struct {
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

type ExecutionDriverRunFinalizeCommand struct {
	WorkspaceKey string
	RequestID    string
	RunID        string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	FencingToken int64
	Status       domain.DriverRunStatus
	Summary      string
	ErrorClass   string
	Output       map[string]string
}

type ExecutionDriverRunChildStartCommand struct {
	WorkspaceKey    string
	RequestID       string
	ParentRunID     string
	NodeID          string
	LeaseID         string
	LeaseToken      string `json:"-"`
	FencingToken    int64
	ChildKey        string
	ChildRunID      string
	DriverID        string
	DriverVersionID string
	Payload         []byte
	MaxDepth        int
	RequestedAt     time.Time
}

type ExecutionDriverRunChildStartResult struct {
	Parent      *domain.DriverRun      `json:"parent"`
	Child       *domain.DriverRun      `json:"child"`
	ParentDepth int                    `json:"parent_depth"`
	ChildDepth  int                    `json:"child_depth"`
	Action      *ExecutionActionLedger `json:"action"`
	ActionID    string                 `json:"action_id"`
	Replay      bool                   `json:"replay"`
}

type ExecutionDriverRunCascadeCommand struct {
	WorkspaceKey   string
	RequestID      string
	ParentRunID    string
	NodeID         string
	LeaseID        string
	LeaseToken     string `json:"-"`
	FencingToken   int64
	ParentStatus   domain.DriverRunStatus
	Reason         string
	ErrorClass     string
	CascadedAt     time.Time
	MaxDepth       int
	SystemRecovery bool
}

type ExecutionDriverRunCascadeCommit struct {
	WorkspaceKey          string                 `json:"workspace_key"`
	ParentRunID           string                 `json:"parent_run_id"`
	ParentStatus          domain.DriverRunStatus `json:"parent_status"`
	Reason                string                 `json:"reason"`
	ErrorClass            string                 `json:"error_class"`
	CascadedAt            time.Time              `json:"cascaded_at"`
	MaxDepth              int                    `json:"max_depth"`
	CancelledRunIDs       []string               `json:"cancelled_run_ids"`
	CancelRequestedRunIDs []string               `json:"cancel_requested_run_ids"`
	ActionID              string                 `json:"action_id"`
}

type ExecutionDriverRunCascadeResult struct {
	CancelledRuns       []*domain.DriverRun              `json:"cancelled_runs"`
	CancelRequestedRuns []*domain.DriverRun              `json:"cancel_requested_runs"`
	Committed           *ExecutionDriverRunCascadeCommit `json:"committed"`
	Action              *ExecutionActionLedger           `json:"action"`
	ActionID            string                           `json:"action_id"`
	Replay              bool                             `json:"replay"`
}

// ExecutionTerminalDriverRunWorkRecoveryCommand asks FleetDB to atomically
// converge all TaskRuns and exact Work Item claim generations still owned by
// a durably terminal DriverRun. It carries no lease token because the parent
// owner is necessarily gone; the route is system-authorized instead.
type ExecutionTerminalDriverRunWorkRecoveryCommand struct {
	WorkspaceKey string
	RequestID    string
	DriverRunID  string
	ParentStatus domain.DriverRunStatus
	Reason       string
	ErrorClass   string
	RecoveredAt  time.Time
}

type ExecutionTerminalDriverRunWorkRecoveryResult struct {
	WorkspaceKey                  string                 `json:"workspace_key"`
	DriverRunID                   string                 `json:"driver_run_id"`
	ParentStatus                  domain.DriverRunStatus `json:"parent_status"`
	Reason                        string                 `json:"reason"`
	ErrorClass                    string                 `json:"error_class"`
	RecoveredAt                   time.Time              `json:"recovered_at"`
	RecoveredTaskRunIDs           []string               `json:"recovered_task_run_ids"`
	ReleasedWorkItemIDs           []string               `json:"released_work_item_ids"`
	PreservedSuccessorWorkItemIDs []string               `json:"preserved_successor_work_item_ids"`
	Action                        *ExecutionActionLedger `json:"action"`
	ActionID                      string                 `json:"action_id"`
	Replayed                      bool                   `json:"replayed"`
}

// ExecutionIssue is the claim result subset needed to verify that FleetDB
// claimed the exact Work Item backing the started TaskRun.
type ExecutionIssue struct {
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Priority  int       `json:"priority"`
	Type      string    `json:"type"`
	Assignee  string    `json:"assignee,omitempty"`
	Labels    []string  `json:"labels"`
	Repo      string    `json:"repo,omitempty"`
	ParentID  string    `json:"parent_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ExecutionActionLedger is the minimal committed action receipt returned by
// claim-and-start. It is read-only here; Execution remains the sole writer.
type ExecutionActionLedger struct {
	WorkspaceKey   string     `json:"workspace_key"`
	ActionID       string     `json:"action_id"`
	IdempotencyKey string     `json:"idempotency_key"`
	ActionType     string     `json:"action_type"`
	TargetRef      string     `json:"target_ref"`
	RequestedBy    string     `json:"requested_by"`
	Status         string     `json:"status"`
	RequestRef     string     `json:"request_ref,omitempty"`
	ResponseRef    string     `json:"response_ref,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	AppliedAt      *time.Time `json:"applied_at,omitempty"`
}

// ExecutionTransport is the shared Client's narrow TaskRun execution surface.
// The existing lifecycle operations delegate to the same taskRunStore so the
// compatibility Store and the capability adapter cannot drift on wire shape.
type ExecutionTransport interface {
	RequestTaskRun(context.Context, ExecutionTaskRunRequestCommand) (*ExecutionTaskRunRequestResult, error)
	ClaimAndStartTaskRun(context.Context, ExecutionClaimAndStartCommand) (*ExecutionClaimAndStartResult, error)
	RequeueTaskRunAndResetStep(context.Context, ExecutionTaskRunRequeueCommand) (*ExecutionTaskRunRequeueResult, error)
	ExhaustTaskRunRetries(context.Context, ExecutionTaskRunRetryExhaustionCommand) (*ExecutionTaskRunRetryExhaustionResult, error)
	UpdateTaskRunWorkItemDesign(context.Context, ExecutionTaskRunWorkItemDesignCommand) (*ExecutionTaskRunWorkItemDesignResult, error)
	HeartbeatTaskRun(context.Context, string, string, store.TaskRunHeartbeat) (*domain.TaskRun, error)
	RequeueTaskRun(context.Context, string, string, store.TaskRunRequeue) (*domain.TaskRun, error)
	CompleteTaskRun(context.Context, string, string, store.TaskRunComplete) (*domain.TaskRun, error)
	AppendTaskRunLog(context.Context, string, string, store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error)
	ClaimDriverRun(context.Context, ExecutionDriverRunClaimCommand) (*domain.DriverRun, error)
	HeartbeatDriverRun(context.Context, ExecutionDriverRunHeartbeatCommand) (*domain.DriverRun, error)
	ClaimDriverRunWorkItem(context.Context, ExecutionDriverRunWorkItemClaimCommand) (*ExecutionDriverRunWorkItemResult, error)
	ReleaseDriverRunWorkItem(context.Context, ExecutionDriverRunWorkItemReleaseCommand) (*ExecutionDriverRunWorkItemResult, error)
	SuspendDriverRun(context.Context, ExecutionDriverRunSuspendCommand) (*domain.DriverRun, error)
	FinalizeDriverRun(context.Context, ExecutionDriverRunFinalizeCommand) (*domain.DriverRun, error)
	RecoverStaleChildTaskRuns(context.Context, ExecutionDriverRunStaleTaskRecoveryCommand) (*ExecutionDriverRunStaleTaskRecoveryResult, error)
	StartChildDriverRun(context.Context, ExecutionDriverRunChildStartCommand) (*ExecutionDriverRunChildStartResult, error)
	CascadeChildDriverRuns(context.Context, ExecutionDriverRunCascadeCommand) (*ExecutionDriverRunCascadeResult, error)
	RecoverTerminalDriverRunWork(context.Context, ExecutionTerminalDriverRunWorkRecoveryCommand) (*ExecutionTerminalDriverRunWorkRecoveryResult, error)
}

// TaskRunTerminalConvergenceTransport is the typed, service-only checkpoint
// surface. Keeping it separate avoids widening focused ExecutionTransport
// test doubles while making the production foundation contract explicit.
type TaskRunTerminalConvergenceTransport interface {
	store.TaskRunTerminalConvergenceStore
}

// ExecutionFoundationTransport is the production composition surface. Keep
// ExecutionTransport narrow enough for focused capability test doubles while
// making terminal DriverStep convergence a static requirement of the shared
// FleetDB client returned to serve composition.
type ExecutionFoundationTransport interface {
	ExecutionTransport
	store.TerminalDriverStepRepairStore
	TaskRunTerminalConvergenceTransport
}

type executionStore struct{ client *Client }

var _ ExecutionTransport = (*executionStore)(nil)
var _ ExecutionFoundationTransport = (*executionStore)(nil)

func executionStringsPresent(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func executionChecksPass(checks ...bool) bool {
	for _, check := range checks {
		if !check {
			return false
		}
	}
	return true
}

func (s *executionStore) ListTaskRunTerminalConvergenceCandidates(
	ctx context.Context,
	query store.TaskRunTerminalConvergenceQuery,
) (store.TaskRunTerminalConvergencePage, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.After = strings.TrimSpace(query.After)
	if query.WorkspaceKey == "" || query.RequiredVersion <= 0 {
		return store.TaskRunTerminalConvergencePage{}, fmt.Errorf("terminal convergence workspace and positive version are required: %w", ErrExecutionInvalid)
	}
	limit := query.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	values := url.Values{}
	values.Set("required_version", strconv.Itoa(query.RequiredVersion))
	values.Set("limit", strconv.Itoa(limit))
	if query.After != "" {
		values.Set("after", query.After)
	}
	path := "/api/v1/" + pathEscape(query.WorkspaceKey) + "/task-runs/terminal-convergence-candidates?" + values.Encode()
	var page store.TaskRunTerminalConvergencePage
	if err := s.client.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return store.TaskRunTerminalConvergencePage{}, mapExecutionTransportError("list terminal TaskRun convergence candidates", err)
	}
	if len(page.TaskRunIDs) > limit {
		return store.TaskRunTerminalConvergencePage{}, fmt.Errorf("terminal convergence page exceeds requested limit: %w", ErrExecutionUnavailable)
	}
	previous := query.After
	for _, taskRunID := range page.TaskRunIDs {
		if strings.TrimSpace(taskRunID) == "" || taskRunID <= previous {
			return store.TaskRunTerminalConvergencePage{}, fmt.Errorf("terminal convergence page is not strictly ordered after cursor: %w", ErrExecutionUnavailable)
		}
		previous = taskRunID
	}
	if page.Next != "" && (len(page.TaskRunIDs) == 0 || page.Next != page.TaskRunIDs[len(page.TaskRunIDs)-1]) {
		return store.TaskRunTerminalConvergencePage{}, fmt.Errorf("terminal convergence page returned divergent cursor: %w", ErrExecutionUnavailable)
	}
	page.TaskRunIDs = append([]string(nil), page.TaskRunIDs...)
	return page, nil
}

func (s *executionStore) CompleteTaskRunTerminalConvergence(
	ctx context.Context,
	command store.TaskRunTerminalConvergenceComplete,
) (*store.TaskRunTerminalConvergenceResult, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.TaskRunID = strings.TrimSpace(command.TaskRunID)
	command.CompletedAt = command.CompletedAt.UTC()
	if !executionStringsPresent(command.WorkspaceKey, command.TaskRunID) || command.RequiredVersion <= 0 || command.CompletedAt.IsZero() {
		return nil, fmt.Errorf("terminal convergence identity, positive version, and completion time are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		RequiredVersion int       `json:"required_version"`
		CompletedAt     time.Time `json:"completed_at"`
	}{RequiredVersion: command.RequiredVersion, CompletedAt: command.CompletedAt}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/task-runs/" + pathEscape(command.TaskRunID) + "/complete-terminal-convergence"
	var result store.TaskRunTerminalConvergenceResult
	if err := s.client.do(ctx, http.MethodPost, path, body, &result); err != nil {
		return nil, mapExecutionTransportError("complete terminal TaskRun convergence", err)
	}
	if result.TaskRun == nil || result.TaskRun.WorkspaceKey != command.WorkspaceKey ||
		result.TaskRun.TaskRunID != command.TaskRunID || !result.TaskRun.Status.IsTerminal() ||
		result.TaskRun.TerminalConvergenceVersion < command.RequiredVersion || result.TaskRun.TerminalConvergedAt == nil {
		return nil, fmt.Errorf("terminal convergence completion returned divergent marker: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func (s *executionStore) RequeueTaskRunAndResetStep(ctx context.Context, command ExecutionTaskRunRequeueCommand) (*ExecutionTaskRunRequeueResult, error) {
	if !executionStringsPresent(command.WorkspaceKey, command.CommandID, command.TaskRunID, command.NodeID, command.LeaseID, command.LeaseToken) ||
		command.FencingToken <= 0 || command.RequeuedAt.IsZero() {
		return nil, fmt.Errorf("execution task-run requeue identity, owner, and time are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		CommandID       string            `json:"command_id"`
		NodeID          string            `json:"node_id"`
		LeaseID         string            `json:"lease_id"`
		FencingToken    int64             `json:"fencing_token"`
		RuntimeMetadata map[string]string `json:"runtime_metadata,omitempty"`
		LogsRef         string            `json:"logs_ref,omitempty"`
		ArtifactsRef    string            `json:"artifacts_ref,omitempty"`
		ErrorClass      string            `json:"error_class,omitempty"`
		ErrorMessage    string            `json:"error_message,omitempty"`
		RequeuedAt      time.Time         `json:"requeued_at"`
		NextEligibleAt  time.Time         `json:"next_eligible_at,omitempty"`
	}{
		CommandID: command.CommandID, NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: command.FencingToken,
		RuntimeMetadata: cloneExecutionTransportStringMap(command.RuntimeMetadata), LogsRef: command.LogsRef, ArtifactsRef: command.ArtifactsRef,
		ErrorClass: command.ErrorClass, ErrorMessage: command.ErrorMessage, RequeuedAt: command.RequeuedAt, NextEligibleAt: command.NextEligibleAt,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/task-runs/" + pathEscape(command.TaskRunID) + "/requeue-and-reset-step"
	var result ExecutionTaskRunRequeueResult
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("requeue task run and reset step", err)
	}
	wantActionID := "task-run-requeue:" + command.CommandID
	if result.TaskRun == nil || result.DriverStep == nil || result.Action == nil {
		return nil, fmt.Errorf("task-run requeue returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	if !executionChecksPass(
		result.TaskRun.WorkspaceKey == command.WorkspaceKey, result.TaskRun.TaskRunID == command.TaskRunID,
		result.DriverStep.WorkspaceKey == command.WorkspaceKey, result.DriverStep.StepID == result.TaskRun.DriverStepID,
		result.Committed.WorkspaceKey == command.WorkspaceKey, result.Committed.TaskRunID == command.TaskRunID,
		result.Committed.Status == domain.TaskRunQueued, result.Committed.DriverStepStatus == domain.DriverStepQueued,
		result.Action.ActionID == wantActionID, result.Action.IdempotencyKey == wantActionID,
		result.Action.ActionType == "requeue_task_run",
	) {
		return nil, fmt.Errorf("task-run requeue returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

type executionTaskRunRetryExhaustionBody struct {
	CommandID           string            `json:"command_id"`
	NodeID              string            `json:"node_id"`
	LeaseID             string            `json:"lease_id"`
	FencingToken        int64             `json:"fencing_token"`
	Attempt             int               `json:"attempt"`
	MaxAttempts         int               `json:"max_attempts"`
	ExitCode            *int              `json:"exit_code,omitempty"`
	LogsRef             string            `json:"logs_ref,omitempty"`
	ArtifactsRef        string            `json:"artifacts_ref,omitempty"`
	RequiredArtifactIDs []string          `json:"required_artifact_ids,omitempty"`
	RequireArtifacts    bool              `json:"require_artifacts,omitempty"`
	InputTokens         int64             `json:"input_tokens,omitempty"`
	OutputTokens        int64             `json:"output_tokens,omitempty"`
	CacheReadTokens     int64             `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens    int64             `json:"cache_write_tokens,omitempty"`
	EstimatedCostUSD    float64           `json:"estimated_cost_usd,omitempty"`
	RuntimeMetadata     map[string]string `json:"runtime_metadata,omitempty"`
	ErrorClass          string            `json:"error_class"`
	ErrorMessage        string            `json:"error_message"`
	FinishedAt          time.Time         `json:"finished_at"`
}

func (s *executionStore) ExhaustTaskRunRetries(ctx context.Context, command ExecutionTaskRunRetryExhaustionCommand) (*ExecutionTaskRunRetryExhaustionResult, error) {
	if !executionStringsPresent(command.WorkspaceKey, command.CommandID, command.TaskRunID, command.NodeID, command.LeaseID, command.LeaseToken) ||
		command.FencingToken <= 0 || command.Attempt < 1 || command.MaxAttempts < 1 || command.FinishedAt.IsZero() {
		return nil, fmt.Errorf("execution task-run exhaustion identity, owner, attempts, and time are required: %w", ErrExecutionInvalid)
	}
	body := executionTaskRunRetryExhaustionBody{
		CommandID: command.CommandID, NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: command.FencingToken,
		Attempt: command.Attempt, MaxAttempts: command.MaxAttempts, ExitCode: command.ExitCode, LogsRef: command.LogsRef,
		ArtifactsRef: command.ArtifactsRef, RequiredArtifactIDs: append([]string(nil), command.RequiredArtifactIDs...),
		RequireArtifacts: command.RequireArtifacts, InputTokens: command.InputTokens, OutputTokens: command.OutputTokens,
		CacheReadTokens: command.CacheReadTokens, CacheWriteTokens: command.CacheWriteTokens, EstimatedCostUSD: command.EstimatedCostUSD,
		RuntimeMetadata: cloneExecutionTransportStringMap(command.RuntimeMetadata), ErrorClass: command.ErrorClass, ErrorMessage: command.ErrorMessage,
		FinishedAt: command.FinishedAt,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/task-runs/" + pathEscape(command.TaskRunID) + "/exhaust-retries"
	var result ExecutionTaskRunRetryExhaustionResult
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("exhaust task-run retries", err)
	}
	wantActionID := "task-run-exhaust:" + command.CommandID
	if result.TaskRun == nil || result.Action == nil {
		return nil, fmt.Errorf("task-run exhaustion returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	wantResponseRef := executionTaskRunRetryExhaustionResponseRef(command.TaskRunID, result.Committed.IssueBlocked)
	if !executionChecksPass(
		result.TaskRun.WorkspaceKey == command.WorkspaceKey, result.TaskRun.TaskRunID == command.TaskRunID,
		result.TaskRun.Status == domain.TaskRunFailed, strings.TrimSpace(result.TaskRun.TaskID) != "",
		result.Committed.WorkspaceKey == command.WorkspaceKey, result.Committed.TaskRunID == command.TaskRunID,
		result.Committed.TaskID == result.TaskRun.TaskID, result.Committed.Status == domain.TaskRunFailed,
		result.Action.ActionID == wantActionID, result.Action.IdempotencyKey == wantActionID,
		result.Action.WorkspaceKey == command.WorkspaceKey, result.Action.ActionType == "exhaust_task_run_retries",
		result.Action.TargetRef == command.TaskRunID, result.Action.RequestedBy == "node:"+command.NodeID,
		result.Action.Status == "applied", strings.HasPrefix(result.Action.RequestRef, "sha256:"),
		result.Action.ResponseRef == wantResponseRef, !result.Action.CreatedAt.IsZero(),
		result.Action.AppliedAt != nil && result.Action.AppliedAt.Equal(result.Action.CreatedAt),
		executionTaskRunRetryExhaustionIssueMatches(&result, command.WorkspaceKey),
	) {
		return nil, fmt.Errorf("task-run exhaustion returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	if !result.Replayed && result.Committed.IssueBlocked && (result.Issue == nil || !strings.EqualFold(result.Issue.Status, "blocked")) {
		return nil, fmt.Errorf("task-run exhaustion returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func executionTaskRunRetryExhaustionResponseRef(taskRunID string, issueBlocked bool) string {
	outcome := "preserved"
	if issueBlocked {
		outcome = "blocked"
	}
	return "task-run://" + strings.TrimSpace(taskRunID) + "#failed;linked-issue#" + outcome
}

func executionTaskRunRetryExhaustionIssueMatches(result *ExecutionTaskRunRetryExhaustionResult, workspace string) bool {
	if result == nil || result.TaskRun == nil {
		return false
	}
	if result.Issue == nil {
		return true
	}
	return result.Issue.Workspace == workspace && result.Issue.ID == result.TaskRun.TaskID && !result.Issue.UpdatedAt.IsZero()
}

func (s *executionStore) HeartbeatTaskRun(ctx context.Context, workspaceKey, taskRunID string, heartbeat store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	run, err := s.client.taskRuns.Heartbeat(ctx, workspaceKey, taskRunID, heartbeat)
	if err != nil {
		return nil, mapExecutionTransportError("heartbeat task run", err)
	}
	return run, nil
}

func (s *executionStore) RequeueTaskRun(ctx context.Context, workspaceKey, taskRunID string, requeue store.TaskRunRequeue) (*domain.TaskRun, error) {
	run, err := s.client.taskRuns.Requeue(ctx, workspaceKey, taskRunID, requeue)
	if err != nil {
		return nil, mapExecutionTransportError("requeue task run", err)
	}
	return run, nil
}

func (s *executionStore) CompleteTaskRun(ctx context.Context, workspaceKey, taskRunID string, complete store.TaskRunComplete) (*domain.TaskRun, error) {
	run, err := s.client.taskRuns.Complete(ctx, workspaceKey, taskRunID, complete)
	if err != nil {
		return nil, mapExecutionTransportError("complete task run", err)
	}
	return run, nil
}

func (s *executionStore) AppendTaskRunLog(ctx context.Context, workspaceKey, taskRunID string, appendLog store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	entry, err := s.client.taskRuns.AppendLog(ctx, workspaceKey, taskRunID, appendLog)
	if err != nil {
		return nil, mapExecutionTransportError("append task run log", err)
	}
	return entry, nil
}

func (s *executionStore) ClaimDriverRun(ctx context.Context, command ExecutionDriverRunClaimCommand) (*domain.DriverRun, error) {
	if strings.TrimSpace(command.WorkspaceKey) == "" || strings.TrimSpace(command.RequestID) == "" ||
		strings.TrimSpace(command.RunID) == "" || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.LeaseID) == "" || strings.TrimSpace(command.LeaseToken) == "" {
		return nil, fmt.Errorf("execution DriverRun claim identity and token are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		RequestID string `json:"request_id"`
		NodeID    string `json:"node_id"`
		LeaseID   string `json:"lease_id"`
	}{RequestID: command.RequestID, NodeID: command.NodeID, LeaseID: command.LeaseID}
	var run domain.DriverRun
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) + "/claim"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &run, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("claim DriverRun", err)
	}
	if run.WorkspaceKey != command.WorkspaceKey || run.RunID != command.RunID || run.Status != domain.DriverRunRunning ||
		run.NodeID != command.NodeID || run.LeaseID != command.LeaseID || run.FencingToken <= 0 {
		return nil, fmt.Errorf("DriverRun claim returned divergent owner: %w", ErrExecutionUnavailable)
	}
	return &run, nil
}

func (s *executionStore) HeartbeatDriverRun(ctx context.Context, command ExecutionDriverRunHeartbeatCommand) (*domain.DriverRun, error) {
	if strings.TrimSpace(command.WorkspaceKey) == "" || strings.TrimSpace(command.RunID) == "" ||
		strings.TrimSpace(command.NodeID) == "" || strings.TrimSpace(command.LeaseID) == "" ||
		strings.TrimSpace(command.LeaseToken) == "" || command.FencingToken <= 0 {
		return nil, fmt.Errorf("execution DriverRun heartbeat owner and token are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		NodeID       string `json:"node_id"`
		LeaseID      string `json:"lease_id"`
		FencingToken int64  `json:"fencing_token"`
	}{NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: command.FencingToken}
	var run domain.DriverRun
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) + "/heartbeat"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &run, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("heartbeat DriverRun", err)
	}
	return &run, nil
}

func (s *executionStore) SuspendDriverRun(ctx context.Context, command ExecutionDriverRunSuspendCommand) (*domain.DriverRun, error) {
	if strings.TrimSpace(command.WorkspaceKey) == "" || strings.TrimSpace(command.RunID) == "" ||
		strings.TrimSpace(command.NodeID) == "" || strings.TrimSpace(command.LeaseID) == "" ||
		strings.TrimSpace(command.LeaseToken) == "" || command.FencingToken <= 0 ||
		strings.TrimSpace(command.AwaitInstanceKey) == "" {
		return nil, fmt.Errorf("execution DriverRun suspend owner, token, and await identity are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		NodeID           string `json:"node_id"`
		LeaseID          string `json:"lease_id"`
		FencingToken     int64  `json:"fencing_token"`
		AwaitInstanceKey string `json:"await_instance_key"`
	}{
		NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: command.FencingToken,
		AwaitInstanceKey: command.AwaitInstanceKey,
	}
	var run domain.DriverRun
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) + "/suspend"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &run, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("suspend DriverRun", err)
	}
	if run.WorkspaceKey != command.WorkspaceKey || run.RunID != command.RunID ||
		run.Status != domain.DriverRunSuspendedAwaitingEvent || run.AwaitInstanceKey != command.AwaitInstanceKey {
		return nil, fmt.Errorf("DriverRun suspend returned divergent state: %w", ErrExecutionUnavailable)
	}
	return &run, nil
}

func (s *executionStore) FinalizeDriverRun(ctx context.Context, command ExecutionDriverRunFinalizeCommand) (*domain.DriverRun, error) {
	if strings.TrimSpace(command.WorkspaceKey) == "" || strings.TrimSpace(command.RequestID) == "" ||
		strings.TrimSpace(command.RunID) == "" || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.LeaseID) == "" || strings.TrimSpace(command.LeaseToken) == "" || command.FencingToken <= 0 {
		return nil, fmt.Errorf("execution DriverRun finalize identity, owner, and token are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		RequestID    string                 `json:"request_id"`
		NodeID       string                 `json:"node_id"`
		LeaseID      string                 `json:"lease_id"`
		FencingToken int64                  `json:"fencing_token"`
		Status       domain.DriverRunStatus `json:"status"`
		Summary      string                 `json:"summary,omitempty"`
		ErrorClass   string                 `json:"error_class,omitempty"`
		Output       map[string]string      `json:"output,omitempty"`
	}{
		RequestID: command.RequestID, NodeID: command.NodeID, LeaseID: command.LeaseID,
		FencingToken: command.FencingToken, Status: command.Status, Summary: command.Summary,
		ErrorClass: command.ErrorClass, Output: cloneExecutionTransportStringMap(command.Output),
	}
	var run domain.DriverRun
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) + "/finish"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &run, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("finalize DriverRun", err)
	}
	return &run, nil
}

func (s *executionStore) RecoverStaleChildTaskRuns(ctx context.Context, command ExecutionDriverRunStaleTaskRecoveryCommand) (*ExecutionDriverRunStaleTaskRecoveryResult, error) {
	if strings.TrimSpace(command.WorkspaceKey) == "" || strings.TrimSpace(command.RequestID) == "" ||
		strings.TrimSpace(command.RunID) == "" || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.LeaseID) == "" || strings.TrimSpace(command.LeaseToken) == "" ||
		command.FencingToken <= 0 || command.StaleBefore.IsZero() ||
		strings.TrimSpace(command.ErrorClass) == "" || strings.TrimSpace(command.ErrorMessage) == "" {
		return nil, fmt.Errorf("execution stale child TaskRun recovery identity, owner, token, cutoff, and error are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		RequestID    string    `json:"request_id"`
		NodeID       string    `json:"node_id"`
		LeaseID      string    `json:"lease_id"`
		FencingToken int64     `json:"fencing_token"`
		StaleBefore  time.Time `json:"stale_before"`
		ErrorClass   string    `json:"error_class"`
		ErrorMessage string    `json:"error_message"`
	}{
		RequestID: command.RequestID, NodeID: command.NodeID, LeaseID: command.LeaseID,
		FencingToken: command.FencingToken, StaleBefore: command.StaleBefore,
		ErrorClass: command.ErrorClass, ErrorMessage: command.ErrorMessage,
	}
	var result ExecutionDriverRunStaleTaskRecoveryResult
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) + "/recover-stale-tasks"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("recover stale child TaskRuns", err)
	}
	if result.WorkspaceKey != command.WorkspaceKey || result.DriverRunID != command.RunID ||
		!result.StaleBefore.Equal(command.StaleBefore) || result.RecoveredAt.IsZero() ||
		result.Recovered < 0 || result.Released < 0 || result.SkippedFresh < 0 ||
		result.Recovered != len(result.RecoveredTaskRunIDs) {
		return nil, fmt.Errorf("stale child TaskRun recovery returned divergent state: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func (s *executionStore) StartChildDriverRun(ctx context.Context, command ExecutionDriverRunChildStartCommand) (*ExecutionDriverRunChildStartResult, error) {
	if !executionStringsPresent(
		command.WorkspaceKey, command.RequestID, command.ParentRunID, command.NodeID, command.LeaseID,
		command.LeaseToken, command.ChildKey, command.ChildRunID, command.DriverID, command.DriverVersionID,
	) || command.FencingToken <= 0 ||
		command.MaxDepth < 1 || command.RequestedAt.IsZero() || len(command.Payload) == 0 || !json.Valid(command.Payload) {
		return nil, fmt.Errorf("execution child DriverRun identity, owner, payload, and time are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		RequestID       string          `json:"request_id"`
		NodeID          string          `json:"node_id"`
		LeaseID         string          `json:"lease_id"`
		FencingToken    int64           `json:"fencing_token"`
		ChildKey        string          `json:"child_key"`
		ChildRunID      string          `json:"child_run_id"`
		DriverID        string          `json:"driver_id"`
		DriverVersionID string          `json:"driver_version_id"`
		Payload         json.RawMessage `json:"payload"`
		MaxDepth        int             `json:"max_depth"`
		RequestedAt     time.Time       `json:"requested_at"`
	}{
		RequestID: command.RequestID, NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: command.FencingToken,
		ChildKey: command.ChildKey, ChildRunID: command.ChildRunID, DriverID: command.DriverID,
		DriverVersionID: command.DriverVersionID, Payload: append(json.RawMessage(nil), command.Payload...),
		MaxDepth: command.MaxDepth, RequestedAt: command.RequestedAt,
	}
	var result ExecutionDriverRunChildStartResult
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.ParentRunID) + "/children/start"
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("start child DriverRun", err)
	}
	if result.Parent == nil || result.Child == nil || result.Action == nil || result.ActionID == "" {
		return nil, fmt.Errorf("child DriverRun start returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	if !executionChecksPass(
		result.Parent.WorkspaceKey == command.WorkspaceKey, result.Parent.RunID == command.ParentRunID,
		result.Child.WorkspaceKey == command.WorkspaceKey, result.Child.RunID == command.ChildRunID,
		result.Action.ActionID == result.ActionID, result.Action.ActionType == "start_child_driver_run",
	) {
		return nil, fmt.Errorf("child DriverRun start returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func (s *executionStore) CascadeChildDriverRuns(ctx context.Context, command ExecutionDriverRunCascadeCommand) (*ExecutionDriverRunCascadeResult, error) {
	if !executionStringsPresent(command.WorkspaceKey, command.RequestID, command.ParentRunID, command.Reason, command.ErrorClass) ||
		command.CascadedAt.IsZero() || command.MaxDepth < 1 {
		return nil, fmt.Errorf("execution DriverRun cascade identity and terminal intent are required: %w", ErrExecutionInvalid)
	}
	var path string
	var body any
	headers := map[string]string(nil)
	if command.SystemRecovery {
		body = struct {
			RequestID    string                 `json:"request_id"`
			ParentStatus domain.DriverRunStatus `json:"parent_status"`
			Reason       string                 `json:"reason"`
			ErrorClass   string                 `json:"error_class"`
			CascadedAt   time.Time              `json:"cascaded_at"`
			MaxDepth     int                    `json:"max_depth"`
		}{command.RequestID, command.ParentStatus, command.Reason, command.ErrorClass, command.CascadedAt, command.MaxDepth}
		path = "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.ParentRunID) + "/commands/recover-child-cascade"
	} else {
		if !executionStringsPresent(command.NodeID, command.LeaseID, command.LeaseToken) || command.FencingToken <= 0 {
			return nil, fmt.Errorf("live DriverRun cascade owner and token are required: %w", ErrExecutionInvalid)
		}
		body = struct {
			RequestID    string                 `json:"request_id"`
			NodeID       string                 `json:"node_id"`
			LeaseID      string                 `json:"lease_id"`
			FencingToken int64                  `json:"fencing_token"`
			ParentStatus domain.DriverRunStatus `json:"parent_status"`
			Reason       string                 `json:"reason"`
			ErrorClass   string                 `json:"error_class"`
			CascadedAt   time.Time              `json:"cascaded_at"`
			MaxDepth     int                    `json:"max_depth"`
		}{command.RequestID, command.NodeID, command.LeaseID, command.FencingToken, command.ParentStatus, command.Reason, command.ErrorClass, command.CascadedAt, command.MaxDepth}
		headers = map[string]string{"X-Lease-Token": command.LeaseToken}
		path = "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.ParentRunID) + "/commands/cascade-children"
	}
	var result ExecutionDriverRunCascadeResult
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &result, headers); err != nil {
		return nil, mapExecutionTransportError("cascade child DriverRuns", err)
	}
	if result.Committed == nil || result.Action == nil || result.ActionID == "" {
		return nil, fmt.Errorf("DriverRun cascade returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	if !executionChecksPass(
		result.Committed.WorkspaceKey == command.WorkspaceKey, result.Committed.ParentRunID == command.ParentRunID,
		result.Action.ActionID == result.ActionID, result.Action.ActionType == "cascade_child_driver_runs",
	) {
		return nil, fmt.Errorf("DriverRun cascade returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}
