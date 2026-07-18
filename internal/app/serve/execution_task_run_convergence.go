package serve

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ExecutionTaskRunConvergenceDependencies keeps every cross-capability leg
// narrow. The adapter never receives store.Store: terminal TaskRun reads,
// journal append, DriverStep projection, lead lookup, and notification enqueue
// remain explicit consumer ports.
type ExecutionTaskRunConvergenceDependencies struct {
	TaskRuns    store.TaskRunStore
	DriverRuns  store.DriverRunStore
	DriverSteps store.TerminalDriverStepRepairStore
	Events      store.TaskRunEventStore
	Agents      store.AgentStore
	Outbox      store.OutboxStore
}

func NewExecutionTaskRunConvergenceDependencies(dependencies ExecutionTaskRunConvergenceDependencies) (execution.TaskRunConvergenceDependencies, error) {
	if dependencies.TaskRuns == nil || dependencies.DriverRuns == nil || dependencies.DriverSteps == nil ||
		dependencies.Events == nil || dependencies.Agents == nil || dependencies.Outbox == nil {
		return execution.TaskRunConvergenceDependencies{}, fmt.Errorf("compose TaskRun convergence: all narrow ports are required")
	}
	adapter := &executionTaskRunConvergenceAdapter{dependencies: dependencies}
	return execution.TaskRunConvergenceDependencies{
		Source: adapter, Events: adapter, DriverSteps: adapter, LeadResolver: adapter, Notifications: adapter,
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
	ids := make([]string, 0)
	for _, status := range []domain.TaskRunStatus{domain.TaskRunCompleted, domain.TaskRunFailed, domain.TaskRunCancelled} {
		runs, err := adapter.dependencies.TaskRuns.List(ctx, query.WorkspaceKey, store.TaskRunFilter{Status: status})
		if err != nil {
			return execution.TaskRunConvergenceCandidatePage{}, err
		}
		for _, run := range runs {
			if run != nil && strings.TrimSpace(run.TaskRunID) != "" {
				ids = append(ids, run.TaskRunID)
			}
		}
	}
	sort.Strings(ids)
	start := sort.SearchStrings(ids, query.After)
	for start < len(ids) && ids[start] <= query.After {
		start++
	}
	limit := query.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	end := start + limit
	if end > len(ids) {
		end = len(ids)
	}
	page := execution.TaskRunConvergenceCandidatePage{TaskRunIDs: append([]string(nil), ids[start:end]...)}
	if end < len(ids) && end > start {
		page.Next = ids[end-1]
	}
	return page, nil
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
	agents, err := adapter.dependencies.Agents.List(ctx, workspace)
	if err != nil {
		return "", err
	}
	for _, agent := range agents {
		if agent == nil || strings.TrimSpace(agent.Parent) != epicID {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(agent.RoleName)) {
		case "lead", "orchestrator":
			return agent.Name, nil
		}
	}
	return "", nil
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
