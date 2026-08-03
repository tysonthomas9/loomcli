package serve

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ExecutionTaskRunConvergenceDependencies keeps every cross-capability leg
// narrow. The adapter never receives store.Store: terminal TaskRun reads,
// journal append, DriverStep projection, lead lookup, and notification enqueue
// remain explicit consumer ports.
type ExecutionTaskRunConvergenceDependencies struct {
	TaskRuns       store.TaskRunStore
	Checkpoints    store.TaskRunTerminalConvergenceStore
	DriverRuns     store.DriverRunStore
	DriverSteps    store.TerminalDriverStepRepairStore
	Events         store.TaskRunEventStore
	AgentQueries   agentsmodule.IdentityQueries
	WorkerProfiles store.WorkerProfileStore
	Outbox         store.OutboxStore
}

func NewExecutionTaskRunConvergenceDependencies(dependencies ExecutionTaskRunConvergenceDependencies) (execution.TaskRunConvergenceDependencies, error) {
	if dependencies.TaskRuns == nil || dependencies.Checkpoints == nil || dependencies.DriverRuns == nil || dependencies.DriverSteps == nil ||
		dependencies.Events == nil || dependencies.AgentQueries == nil || dependencies.WorkerProfiles == nil || dependencies.Outbox == nil {
		return execution.TaskRunConvergenceDependencies{}, fmt.Errorf("compose TaskRun convergence: all narrow ports are required")
	}
	adapter := &executionTaskRunConvergenceAdapter{dependencies: dependencies}
	return execution.TaskRunConvergenceDependencies{
		Source: adapter, Checkpoints: adapter, Events: adapter, DriverSteps: adapter, LeadResolver: adapter, Notifications: adapter,
	}, nil
}

type executionTaskRunConvergenceAdapter struct {
	dependencies ExecutionTaskRunConvergenceDependencies
}

func (adapter *executionTaskRunConvergenceAdapter) GetTerminalTaskRun(ctx context.Context, workspace, taskRunID string) (*execution.TerminalTaskRunRecord, error) {
	run, err := adapter.dependencies.TaskRuns.Get(ctx, workspace, taskRunID)
	if err != nil {
		return nil, err
	}
	status, err := convergenceExecutionStatus(run.Status)
	if err != nil {
		return nil, err
	}
	if run.FinishedAt == nil {
		return nil, fmt.Errorf("terminal TaskRun %q has no finished time: %w", run.TaskRunID, execution.ErrConflict)
	}
	record := &execution.TerminalTaskRunRecord{
		WorkspaceKey: run.WorkspaceKey, TaskRunID: run.TaskRunID, DriverRunID: run.DriverRunID,
		DriverStepID: run.DriverStepID, WorkItemID: run.TaskID, Status: status,
		Attempt: convergenceTaskRunAttempt(run.RuntimeMetadata), SchedulerState: run.RuntimeMetadata["scheduler_state"],
		ErrorClass: run.ErrorClass, ErrorMessage: run.ErrorMessage, LogsRef: run.LogsRef,
		ArtifactsRef: run.ArtifactsRef, FinishedAt: *run.FinishedAt,
	}
	if run.DriverRunID == "" {
		return record, nil
	}
	parent, err := adapter.dependencies.DriverRuns.Get(ctx, workspace, run.DriverRunID)
	if err != nil {
		return nil, err
	}
	record.EpicID = parent.EpicID
	record.ParentOwner = execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: parent.RunID,
		NodeID: parent.NodeID, LeaseID: parent.LeaseID, FencingToken: parent.FencingToken,
	}
	return record, nil
}

func (adapter *executionTaskRunConvergenceAdapter) ListTaskRunConvergenceCandidates(ctx context.Context, query execution.TaskRunConvergenceCandidateQuery) (execution.TaskRunConvergenceCandidatePage, error) {
	page, err := adapter.dependencies.Checkpoints.ListTaskRunTerminalConvergenceCandidates(ctx, store.TaskRunTerminalConvergenceQuery{
		WorkspaceKey: query.WorkspaceKey, RequiredVersion: query.RequiredVersion,
		After: query.After, Limit: query.Limit,
	})
	if err != nil {
		return execution.TaskRunConvergenceCandidatePage{}, err
	}
	return execution.TaskRunConvergenceCandidatePage{
		TaskRunIDs: append([]string(nil), page.TaskRunIDs...), Next: page.Next,
	}, nil
}

func (adapter *executionTaskRunConvergenceAdapter) CompleteTaskRunTerminalConvergence(
	ctx context.Context,
	command execution.CompleteTaskRunTerminalConvergence,
) (execution.TaskRunTerminalConvergenceCheckpoint, error) {
	result, err := adapter.dependencies.Checkpoints.CompleteTaskRunTerminalConvergence(ctx, store.TaskRunTerminalConvergenceComplete{
		WorkspaceKey: command.WorkspaceKey, TaskRunID: command.TaskRunID,
		RequiredVersion: command.RequiredVersion, CompletedAt: command.CompletedAt,
	})
	if err != nil {
		return execution.TaskRunTerminalConvergenceCheckpoint{}, err
	}
	if result == nil || result.TaskRun == nil || result.TaskRun.TerminalConvergedAt == nil {
		return execution.TaskRunTerminalConvergenceCheckpoint{}, execution.ErrConflict
	}
	return execution.TaskRunTerminalConvergenceCheckpoint{
		WorkspaceKey: result.TaskRun.WorkspaceKey, TaskRunID: result.TaskRun.TaskRunID,
		Version: result.TaskRun.TerminalConvergenceVersion, CompletedAt: *result.TaskRun.TerminalConvergedAt,
		Replayed: result.Replayed,
	}, nil
}

func (adapter *executionTaskRunConvergenceAdapter) EnsureTaskRunTerminalEvent(ctx context.Context, event execution.TaskRunTerminalEvent) error {
	eventType, status, err := convergenceTerminalEventWire(event)
	if err != nil {
		return err
	}
	_, err = adapter.dependencies.Events.Append(ctx, store.TaskRunEventAppend{
		WorkspaceKey: event.WorkspaceKey, EventID: event.EventID, EpicID: event.EpicID,
		DriverRunID: event.DriverRunID, TaskID: event.WorkItemID, TaskRunID: event.TaskRunID,
		Type: eventType, Status: status, SchedulerState: event.SchedulerState, Attempt: event.Attempt,
		ErrorClass: event.ErrorClass, ErrorMessage: event.ErrorMessage, LogsRef: event.LogsRef,
		ArtifactsRef: event.ArtifactsRef, OccurredAt: event.OccurredAt,
	})
	return err
}

func (adapter *executionTaskRunConvergenceAdapter) RepairTerminalDriverStep(ctx context.Context, projection execution.DriverStepTerminalProjection) (execution.RepairTerminalDriverStepResult, error) {
	desired := domain.DriverStepStatus(projection.Status)
	if !desired.IsTerminal() {
		return execution.RepairTerminalDriverStepResult{}, execution.ErrInvalid
	}
	step, replay, err := adapter.dependencies.DriverSteps.RepairTerminalDriverStep(ctx, store.TerminalDriverStepRepair{
		RequestID: projection.RequestID, WorkspaceKey: projection.WorkspaceKey,
		DriverRunID: projection.DriverRunID, DriverStepID: projection.StepID, TaskRunID: projection.TaskRunID,
		Status: desired, OutputRef: projection.OutputRef,
	})
	if err != nil {
		return execution.RepairTerminalDriverStepResult{}, err
	}
	if step == nil {
		return execution.RepairTerminalDriverStepResult{}, execution.ErrConflict
	}
	return execution.RepairTerminalDriverStepResult{
		WorkspaceKey: step.WorkspaceKey, StepID: step.StepID, DriverRunID: step.DriverRunID,
		TaskRunID: step.TaskRunID, Status: string(step.Status), OutputRef: step.OutputRef, Replay: replay,
	}, nil
}

func (adapter *executionTaskRunConvergenceAdapter) ResolveEpicLead(ctx context.Context, workspace, epicID string) (string, error) {
	agents, err := adapter.dependencies.AgentQueries.ListAgents(ctx, workspace, agentsmodule.AgentFilter{})
	if err != nil {
		return "", err
	}
	for _, agent := range agents {
		if agent == nil || strings.TrimSpace(agent.ProfileName) == "" || !canonicalEpicLead(agent) {
			continue
		}
		profile, err := adapter.dependencies.WorkerProfiles.Get(ctx, workspace, agent.ProfileName)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return "", err
		}
		if profile != nil && strings.TrimSpace(profile.ParentEpic) == epicID {
			return agent.AgentID, nil
		}
	}
	return "", nil
}

func canonicalEpicLead(agent *agentsmodule.Agent) bool {
	if agent == nil {
		return false
	}
	switch agent.Kind {
	case agentsmodule.AgentKindLead, agentsmodule.AgentKindOrchestrator, agentsmodule.AgentKindCampaignOrchestrator:
		return true
	}
	roleName := strings.ToLower(strings.TrimSpace(agent.Behavior.RoleName))
	return roleName == "lead" || roleName == "orchestrator"
}

func (adapter *executionTaskRunConvergenceAdapter) EnsureLeadTaskNotification(ctx context.Context, notification execution.LeadTaskNotification) error {
	_, err := adapter.dependencies.Outbox.Create(ctx, store.OutboxCreate{
		WorkspaceKey: notification.WorkspaceKey, Kind: domain.OutboxKindLeadTaskMessage,
		EpicID: notification.EpicID, DriverRunID: notification.DriverRunID, TaskRunID: notification.TaskRunID,
		TargetAgent: notification.TargetAgent, Body: convergenceLeadTaskMessage(notification), DedupeKey: notification.DedupeKey,
	})
	return err
}

func convergenceExecutionStatus(status domain.TaskRunStatus) (execution.Status, error) {
	switch status {
	case domain.TaskRunCompleted:
		return execution.StatusSucceeded, nil
	case domain.TaskRunFailed:
		return execution.StatusFailed, nil
	case domain.TaskRunCancelled:
		return execution.StatusCancelled, nil
	default:
		return "", fmt.Errorf("TaskRun status %q is not terminal: %w", status, execution.ErrInvalid)
	}
}

func convergenceTerminalEventWire(event execution.TaskRunTerminalEvent) (domain.TaskRunEventType, domain.TaskRunStatus, error) {
	switch event.Type {
	case execution.TaskRunTerminalCompleted:
		return domain.TaskRunEventCompleted, domain.TaskRunCompleted, nil
	case execution.TaskRunTerminalFailed:
		return domain.TaskRunEventFailed, domain.TaskRunFailed, nil
	case execution.TaskRunTerminalCancelled:
		return domain.TaskRunEventCancelled, domain.TaskRunCancelled, nil
	default:
		return "", "", execution.ErrInvalid
	}
}

func convergenceTaskRunAttempt(metadata map[string]string) int {
	attempt, err := strconv.Atoi(strings.TrimSpace(metadata["scheduler_attempt"]))
	if err != nil || attempt < 0 {
		return 0
	}
	return attempt
}

func convergenceLeadTaskMessage(notification execution.LeadTaskNotification) string {
	headline := "Loom completed a child task under the active epic-runner workflow."
	subject := "completion"
	if notification.Status != execution.StatusSucceeded {
		headline = "Loom blocked a child task under the active epic-runner workflow; retries are exhausted and the run needs review."
		subject = "blocked task"
	}
	lines := []string{
		headline, "", "epic: " + notification.EpicID, "task: " + notification.WorkItemID,
		"task_run: " + notification.TaskRunID,
	}
	if notification.LogsRef != "" {
		lines = append(lines, "logs: "+notification.LogsRef)
	}
	if notification.ArtifactsRef != "" {
		lines = append(lines, "artifacts: "+notification.ArtifactsRef)
	}
	lines = append(lines, "", "Acknowledge this "+subject+" in the visible conversation, update your epic status summary, and continue monitoring the remaining child tasks. Do not start another epic runner.")
	return strings.Join(lines, "\n")
}
