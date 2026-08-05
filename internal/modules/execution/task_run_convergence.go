package execution

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionConvergeTaskRun          authority.Action = "execution.converge-task-run"
	ActionRepairTerminalDriverStep authority.Action = "execution.repair-terminal-driver-step"
	// CurrentTaskRunTerminalConvergenceVersion advances only when the set or
	// meaning of durable convergence legs changes. Older markers are then
	// rediscovered for upgrade backfill.
	CurrentTaskRunTerminalConvergenceVersion = 1
)

type TaskRunTerminalEventType string

const (
	TaskRunTerminalCompleted TaskRunTerminalEventType = "taskRunCompleted"
	TaskRunTerminalFailed    TaskRunTerminalEventType = "taskRunFailed"
	TaskRunTerminalCancelled TaskRunTerminalEventType = "taskRunCancelled"
)

// TerminalTaskRunRecord is the durable source from which lost journal,
// DriverStep, and lead-notification projections can be reconstructed. It is
// token-free by construction.
type TerminalTaskRunRecord struct {
	WorkspaceKey   string
	TaskRunID      string
	DriverRunID    string
	DriverStepID   string
	WorkItemID     string
	EpicID         string
	Status         Status
	Attempt        int
	SchedulerState string
	ErrorClass     string
	ErrorMessage   string
	LogsRef        string
	ArtifactsRef   string
	FinishedAt     time.Time
	ParentOwner    Owner
}

type ConvergeTaskRunCommand struct {
	WorkspaceKey string
	RequestID    string
	TaskRunID    string
	ObservedAt   time.Time
}

type ConvergeTaskRunResult struct {
	TaskRunID           string
	EventEnsured        bool
	DriverStepEnsured   bool
	NotificationEnsured bool
	NotificationSkipped bool
	ConvergenceEnsured  bool
	ConvergenceReplayed bool
	ConvergenceVersion  int
}

type TaskRunTerminalEvent struct {
	WorkspaceKey   string
	EventID        string
	EpicID         string
	DriverRunID    string
	WorkItemID     string
	TaskRunID      string
	Type           TaskRunTerminalEventType
	Status         Status
	SchedulerState string
	Attempt        int
	ErrorClass     string
	ErrorMessage   string
	LogsRef        string
	ArtifactsRef   string
	OccurredAt     time.Time
}

type DriverStepTerminalProjection struct {
	RequestID    string
	WorkspaceKey string
	StepID       string
	DriverRunID  string
	TaskRunID    string
	Status       string
	OutputRef    string
}

type RepairTerminalDriverStepResult struct {
	WorkspaceKey string
	StepID       string
	DriverRunID  string
	TaskRunID    string
	Status       string
	OutputRef    string
	Replay       bool
}

type LeadTaskNotification struct {
	WorkspaceKey string
	EpicID       string
	DriverRunID  string
	TaskRunID    string
	WorkItemID   string
	TargetAgent  string
	Status       Status
	ErrorClass   string
	ErrorMessage string
	LogsRef      string
	ArtifactsRef string
	DedupeKey    string
}

type TaskRunConvergenceCandidateQuery struct {
	WorkspaceKey    string
	RequiredVersion int
	After           string
	Limit           int
}

type TaskRunConvergenceCandidatePage struct {
	TaskRunIDs []string
	Next       string
}

type TaskRunConvergenceSource interface {
	GetTerminalTaskRun(context.Context, string, string) (*TerminalTaskRunRecord, error)
}

type CompleteTaskRunTerminalConvergence struct {
	WorkspaceKey    string
	TaskRunID       string
	RequiredVersion int
	CompletedAt     time.Time
}

type TaskRunTerminalConvergenceCheckpoint struct {
	WorkspaceKey string
	TaskRunID    string
	Version      int
	CompletedAt  time.Time
	Replayed     bool
}

// TaskRunConvergenceCheckpointPort owns both candidate discovery and durable
// completion. It is deliberately separate from the terminal record reader:
// only this typed Execution command can make a TaskRun ineligible next pass.
type TaskRunConvergenceCheckpointPort interface {
	ListTaskRunConvergenceCandidates(context.Context, TaskRunConvergenceCandidateQuery) (TaskRunConvergenceCandidatePage, error)
	CompleteTaskRunTerminalConvergence(context.Context, CompleteTaskRunTerminalConvergence) (TaskRunTerminalConvergenceCheckpoint, error)
}

type TaskRunTerminalEventPort interface {
	EnsureTaskRunTerminalEvent(context.Context, TaskRunTerminalEvent) error
}

type DriverStepTerminalPort interface {
	RepairTerminalDriverStep(context.Context, DriverStepTerminalProjection) (RepairTerminalDriverStepResult, error)
}

// EpicLeadQueryPort is the narrow Phase-4 consumer port into Agents. It can
// resolve a target but cannot mutate Agent or Interaction state.
type EpicLeadQueryPort interface {
	ResolveEpicLead(context.Context, string, string) (string, error)
}

// LeadTaskNotificationPort is the narrow consumer port into the durable
// Interaction notification queue. DedupeKey makes replay idempotent.
type LeadTaskNotificationPort interface {
	EnsureLeadTaskNotification(context.Context, LeadTaskNotification) error
}

type TaskRunConvergenceDependencies struct {
	Source        TaskRunConvergenceSource
	Checkpoints   TaskRunConvergenceCheckpointPort
	Events        TaskRunTerminalEventPort
	DriverSteps   DriverStepTerminalPort
	LeadResolver  EpicLeadQueryPort
	Notifications LeadTaskNotificationPort
}

type TaskRunConvergenceAPI interface {
	ConvergeTaskRun(context.Context, authority.SystemAuthority, ConvergeTaskRunCommand) (ConvergeTaskRunResult, error)
	RepairTerminalDriverStep(context.Context, authority.SystemAuthority, DriverStepTerminalProjection) (RepairTerminalDriverStepResult, error)
}

func (service *Service) ConvergeTaskRun(ctx context.Context, auth authority.SystemAuthority, command ConvergeTaskRunCommand) (ConvergeTaskRunResult, error) {
	if err := service.requireSystem(ActionConvergeTaskRun, command.WorkspaceKey, auth); err != nil {
		return ConvergeTaskRunResult{}, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.TaskRunID) == "" || command.ObservedAt.IsZero() {
		return ConvergeTaskRunResult{}, ErrInvalid
	}
	dependencies := service.dependencies.Convergence
	if dependencies.Source == nil || dependencies.Checkpoints == nil || dependencies.Events == nil {
		return ConvergeTaskRunResult{}, ErrUnavailable
	}
	record, err := dependencies.Source.GetTerminalTaskRun(ctx, command.WorkspaceKey, command.TaskRunID)
	if err != nil {
		return ConvergeTaskRunResult{}, err
	}
	if err := validateTerminalTaskRunRecord(command, record); err != nil {
		return ConvergeTaskRunResult{}, err
	}
	result := ConvergeTaskRunResult{TaskRunID: record.TaskRunID}
	if err := dependencies.Events.EnsureTaskRunTerminalEvent(ctx, terminalTaskRunEvent(record)); err != nil {
		return result, fmt.Errorf("ensure terminal TaskRun event: %w", err)
	}
	result.EventEnsured = true
	result.DriverStepEnsured, err = ensureTerminalDriverStep(ctx, dependencies.DriverSteps, command.RequestID, record)
	if err != nil {
		return result, err
	}
	result.NotificationEnsured, result.NotificationSkipped, err = ensureTerminalLeadNotification(ctx, dependencies, record)
	if err != nil {
		return result, err
	}
	checkpoint, err := dependencies.Checkpoints.CompleteTaskRunTerminalConvergence(ctx, CompleteTaskRunTerminalConvergence{
		WorkspaceKey: record.WorkspaceKey, TaskRunID: record.TaskRunID,
		RequiredVersion: CurrentTaskRunTerminalConvergenceVersion, CompletedAt: command.ObservedAt,
	})
	if err != nil {
		return result, fmt.Errorf("complete terminal TaskRun convergence: %w", err)
	}
	if checkpoint.WorkspaceKey != record.WorkspaceKey || checkpoint.TaskRunID != record.TaskRunID ||
		checkpoint.Version < CurrentTaskRunTerminalConvergenceVersion || checkpoint.CompletedAt.IsZero() {
		return result, fmt.Errorf("%w: terminal TaskRun convergence checkpoint escaped requested envelope", ErrConflict)
	}
	result.ConvergenceEnsured = true
	result.ConvergenceReplayed = checkpoint.Replayed
	result.ConvergenceVersion = checkpoint.Version
	return result, nil
}

func ensureTerminalDriverStep(ctx context.Context, port DriverStepTerminalPort, requestID string, record *TerminalTaskRunRecord) (bool, error) {
	if record.DriverStepID == "" {
		return false, nil
	}
	if port == nil {
		return false, ErrUnavailable
	}
	if _, err := repairTerminalDriverStep(ctx, port, terminalDriverStepProjection(requestID, record)); err != nil {
		return false, fmt.Errorf("ensure terminal DriverStep projection: %w", err)
	}
	return true, nil
}

func ensureTerminalLeadNotification(
	ctx context.Context,
	dependencies TaskRunConvergenceDependencies,
	record *TerminalTaskRunRecord,
) (bool, bool, error) {
	if !terminalTaskRunNeedsLeadNotification(record) {
		return false, true, nil
	}
	if dependencies.LeadResolver == nil || dependencies.Notifications == nil {
		return false, false, ErrUnavailable
	}
	lead, err := dependencies.LeadResolver.ResolveEpicLead(ctx, record.WorkspaceKey, record.EpicID)
	if err != nil {
		return false, false, fmt.Errorf("resolve epic lead: %w", err)
	}
	lead = strings.TrimSpace(lead)
	if lead == "" {
		return false, true, nil
	}
	if err := dependencies.Notifications.EnsureLeadTaskNotification(ctx, terminalLeadTaskNotification(record, lead)); err != nil {
		return false, false, fmt.Errorf("ensure lead TaskRun notification: %w", err)
	}
	return true, false, nil
}

func (service *Service) RepairTerminalDriverStep(ctx context.Context, auth authority.SystemAuthority, command DriverStepTerminalProjection) (RepairTerminalDriverStepResult, error) {
	if err := service.requireSystem(ActionRepairTerminalDriverStep, command.WorkspaceKey, auth); err != nil {
		return RepairTerminalDriverStepResult{}, err
	}
	port := service.dependencies.Convergence.DriverSteps
	if port == nil {
		return RepairTerminalDriverStepResult{}, ErrUnavailable
	}
	return repairTerminalDriverStep(ctx, port, command)
}

func repairTerminalDriverStep(ctx context.Context, port DriverStepTerminalPort, command DriverStepTerminalProjection) (RepairTerminalDriverStepResult, error) {
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.WorkspaceKey) == "" ||
		strings.TrimSpace(command.StepID) == "" || strings.TrimSpace(command.DriverRunID) == "" ||
		strings.TrimSpace(command.TaskRunID) == "" || !validTerminalDriverStepStatus(command.Status) {
		return RepairTerminalDriverStepResult{}, ErrInvalid
	}
	result, err := port.RepairTerminalDriverStep(ctx, command)
	if err != nil {
		return RepairTerminalDriverStepResult{}, err
	}
	if result.WorkspaceKey != command.WorkspaceKey || result.StepID != command.StepID ||
		result.DriverRunID != command.DriverRunID || result.TaskRunID != command.TaskRunID ||
		result.Status != command.Status || result.OutputRef != command.OutputRef {
		return RepairTerminalDriverStepResult{}, fmt.Errorf("%w: terminal DriverStep repair escaped requested envelope", ErrConflict)
	}
	return result, nil
}

func validTerminalDriverStepStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "skipped"
}

func validateTerminalTaskRunRecord(command ConvergeTaskRunCommand, record *TerminalTaskRunRecord) error {
	if record == nil || record.WorkspaceKey != command.WorkspaceKey || record.TaskRunID != command.TaskRunID ||
		!terminal(record.Status) || record.Status == StatusBlocked || record.Attempt < 0 || record.FinishedAt.IsZero() {
		return fmt.Errorf("%w: invalid terminal TaskRun convergence source", ErrConflict)
	}
	return nil
}

func terminalTaskRunEvent(record *TerminalTaskRunRecord) TaskRunTerminalEvent {
	eventType := TaskRunTerminalFailed
	switch record.Status {
	case StatusSucceeded:
		eventType = TaskRunTerminalCompleted
	case StatusCancelled:
		eventType = TaskRunTerminalCancelled
	}
	return TaskRunTerminalEvent{
		WorkspaceKey: record.WorkspaceKey, EventID: terminalTaskRunEventID(record.TaskRunID, record.Attempt, eventType),
		EpicID: record.EpicID, DriverRunID: record.DriverRunID, WorkItemID: record.WorkItemID,
		TaskRunID: record.TaskRunID, Type: eventType, Status: record.Status,
		SchedulerState: record.SchedulerState, Attempt: record.Attempt,
		ErrorClass: record.ErrorClass, ErrorMessage: record.ErrorMessage,
		LogsRef: record.LogsRef, ArtifactsRef: record.ArtifactsRef, OccurredAt: record.FinishedAt,
	}
}

func terminalTaskRunEventID(taskRunID string, attempt int, eventType TaskRunTerminalEventType) string {
	return taskRunID + "#" + strconv.Itoa(attempt) + "#" + string(eventType)
}

func terminalDriverStepProjection(requestID string, record *TerminalTaskRunRecord) DriverStepTerminalProjection {
	status := "failed"
	switch record.Status {
	case StatusSucceeded:
		status = "completed"
	case StatusCancelled:
		status = "skipped"
	}
	outputRef := record.ArtifactsRef
	if outputRef == "" {
		outputRef = record.LogsRef
	}
	return DriverStepTerminalProjection{
		RequestID:    requestID + ":driver-step",
		WorkspaceKey: record.WorkspaceKey, StepID: record.DriverStepID, DriverRunID: record.DriverRunID,
		TaskRunID: record.TaskRunID, Status: status, OutputRef: outputRef,
	}
}

func terminalTaskRunNeedsLeadNotification(record *TerminalTaskRunRecord) bool {
	return record.EpicID != "" && (record.Status == StatusSucceeded || record.SchedulerState == "blocked")
}

func terminalLeadTaskNotification(record *TerminalTaskRunRecord, lead string) LeadTaskNotification {
	return LeadTaskNotification{
		WorkspaceKey: record.WorkspaceKey, EpicID: record.EpicID, DriverRunID: record.DriverRunID,
		TaskRunID: record.TaskRunID, WorkItemID: record.WorkItemID, TargetAgent: lead, Status: record.Status,
		ErrorClass: record.ErrorClass, ErrorMessage: record.ErrorMessage, LogsRef: record.LogsRef,
		ArtifactsRef: record.ArtifactsRef,
		DedupeKey:    "lead-task-message:" + record.EpicID + ":" + record.TaskRunID + ":" + string(record.Status),
	}
}

// TaskRunConvergencePass is the runtime-host-managed recovery loop. The
// durable terminal TaskRun is the source of truth; every projection write is
// idempotent, so a crash after any leg is repaired on the next pass.
type TaskRunConvergencePass struct {
	WorkspaceKey string
	Scopes       TaskRunRecoveryScopePort
	Checkpoints  TaskRunConvergenceCheckpointPort
	API          TaskRunConvergenceAPI
	Authorities  SystemAuthorityResolver
	Limit        int
}

func (pass *TaskRunConvergencePass) RunOnce(ctx context.Context) error {
	if pass == nil || pass.Checkpoints == nil || pass.API == nil || pass.Authorities == nil {
		return ErrUnavailable
	}
	workspaces, err := taskRunConvergenceWorkspaces(ctx, pass.WorkspaceKey, pass.Scopes)
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if err := pass.runWorkspace(ctx, workspace); err != nil {
			return err
		}
	}
	return nil
}

func (pass *TaskRunConvergencePass) runWorkspace(ctx context.Context, workspace string) error {
	limit := pass.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	auth, err := pass.Authorities.ResolveExecutionSystemAuthority(ctx, workspace, ActionConvergeTaskRun, string(TaskRunConvergenceComponentID))
	if err != nil {
		return err
	}
	after := ""
	seen := map[string]struct{}{}
	for {
		page, err := pass.Checkpoints.ListTaskRunConvergenceCandidates(ctx, TaskRunConvergenceCandidateQuery{
			WorkspaceKey: workspace, RequiredVersion: CurrentTaskRunTerminalConvergenceVersion,
			After: after, Limit: limit,
		})
		if err != nil {
			return err
		}
		for _, taskRunID := range page.TaskRunIDs {
			taskRunID = strings.TrimSpace(taskRunID)
			if taskRunID == "" {
				return ErrConflict
			}
			if _, duplicate := seen[taskRunID]; duplicate {
				return fmt.Errorf("%w: convergence candidate source repeated %q", ErrConflict, taskRunID)
			}
			seen[taskRunID] = struct{}{}
			_, err := pass.API.ConvergeTaskRun(ctx, auth, ConvergeTaskRunCommand{
				WorkspaceKey: workspace,
				RequestID:    "reconcile-task-run:" + taskRunID,
				TaskRunID:    taskRunID,
				ObservedAt:   time.Now().UTC(),
			})
			if err != nil {
				return err
			}
		}
		next := strings.TrimSpace(page.Next)
		if next == "" {
			return nil
		}
		if next == after {
			return fmt.Errorf("%w: convergence candidate cursor did not advance", ErrConflict)
		}
		after = next
	}
}

func taskRunConvergenceWorkspaces(ctx context.Context, configured string, scopes TaskRunRecoveryScopePort) ([]string, error) {
	if workspace := strings.TrimSpace(configured); workspace != "" {
		return []string{workspace}, nil
	}
	if scopes == nil {
		return nil, ErrUnavailable
	}
	workspaces, err := scopes.ListTaskRunRecoveryWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		workspace = strings.TrimSpace(workspace)
		if workspace == "" {
			return nil, fmt.Errorf("%w: empty TaskRun convergence workspace", ErrConflict)
		}
		if _, duplicate := seen[workspace]; duplicate {
			continue
		}
		seen[workspace] = struct{}{}
		clean = append(clean, workspace)
	}
	sort.Strings(clean)
	return clean, nil
}
