// Package readprojection assembles read-only UI projections that span durable
// capability records without exposing their persistence ports to HTTP handlers.
package readprojection

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const issueTriggerSubjectPrefix = "issue:"

var (
	ErrInvalidTaskWorkflowRunQuery = errors.New("invalid task workflow run query")
	ErrTaskWorkflowRunsUnavailable = errors.New("task workflow run projection is unavailable")
)

// TaskWorkflowRunQuery identifies one task-scoped automation projection.
type TaskWorkflowRunQuery struct {
	WorkspaceKey string
	TaskID       string
	Limit        int
}

// TaskWorkflowRunResult carries the immutable task association and its
// sessionless DriverRuns.
type TaskWorkflowRunResult struct {
	SubjectRef string
	Runs       []*domain.DriverRun
}

// TaskWorkflowRunReader is the narrow read-model port exposed to HTTP
// composition. It owns no mutation capability.
type TaskWorkflowRunReader interface {
	ListTaskWorkflowRuns(context.Context, TaskWorkflowRunQuery) (TaskWorkflowRunResult, error)
}

type taskRunListPort interface {
	List(context.Context, string, store.TaskRunFilter) ([]*domain.TaskRun, error)
}

type triggerEventListPort interface {
	List(context.Context, string, store.TriggerEventFilter) ([]*automation.Event, error)
}

type driverRunListPort interface {
	List(context.Context, string, store.DriverRunFilter) ([]*domain.DriverRun, error)
}

type taskWorkflowRunReader struct {
	taskRuns   taskRunListPort
	events     triggerEventListPort
	driverRuns driverRunListPort
}

// NewTaskWorkflowRunReader constructs the projection from exact-purpose,
// read-only persistence ports. Composition passes individual stores rather
// than the process-wide composite Store.
func NewTaskWorkflowRunReader(
	taskRuns taskRunListPort,
	events triggerEventListPort,
	driverRuns driverRunListPort,
) TaskWorkflowRunReader {
	if taskRuns == nil || events == nil || driverRuns == nil {
		return nil
	}
	return &taskWorkflowRunReader{
		taskRuns:   taskRuns,
		events:     events,
		driverRuns: driverRuns,
	}
}

// ListTaskWorkflowRuns returns trigger-admitted DriverRuns for exactly one
// issue that are not already represented by an Execution-owned TaskRun for
// that issue. TaskRun is the durable batch-attempt projection exposed by the
// task session audit API; Interaction AgentSession rows are deliberately not
// consulted here.
//
// The association is entirely structural and immutable:
//
//	issue id -> TriggerEvent.subject_ref -> DriverRun.source_ref
//
// Payload fields are deliberately not consulted.
func (reader *taskWorkflowRunReader) ListTaskWorkflowRuns(
	ctx context.Context,
	query TaskWorkflowRunQuery,
) (TaskWorkflowRunResult, error) {
	workspace := strings.TrimSpace(query.WorkspaceKey)
	taskID := strings.TrimSpace(query.TaskID)
	if reader == nil || reader.taskRuns == nil ||
		reader.events == nil || reader.driverRuns == nil {
		return TaskWorkflowRunResult{}, ErrTaskWorkflowRunsUnavailable
	}
	if workspace == "" || workspace != query.WorkspaceKey || taskID == "" || taskID != query.TaskID {
		return TaskWorkflowRunResult{}, ErrInvalidTaskWorkflowRunQuery
	}

	representedDriverRuns, err := reader.representedDriverRunIDs(ctx, workspace, taskID)
	if err != nil {
		return TaskWorkflowRunResult{}, err
	}

	subjectRef := issueTriggerSubjectPrefix + taskID
	events, err := reader.events.List(ctx, workspace, store.TriggerEventFilter{SubjectRef: subjectRef})
	if err != nil {
		return TaskWorkflowRunResult{}, err
	}

	eventIDs := make(map[string]struct{}, len(events))
	for _, event := range events {
		// Keep the exact check even when the backing store pushes SubjectRef
		// down. It is the final no-leakage guard for projection stores.
		if event == nil || event.WorkspaceKey != workspace || event.SubjectRef != subjectRef {
			continue
		}
		eventIDs[event.EventID] = struct{}{}
	}

	allRuns, err := reader.driverRuns.List(ctx, workspace, store.DriverRunFilter{})
	if err != nil {
		return TaskWorkflowRunResult{}, err
	}
	runsByID := make(map[string]*domain.DriverRun)
	for _, run := range allRuns {
		if run == nil || run.WorkspaceKey != workspace || strings.TrimSpace(run.RunID) == "" {
			continue
		}
		if _, taskEvent := eventIDs[run.SourceRef]; !taskEvent {
			continue
		}
		if _, represented := representedDriverRuns[run.RunID]; represented {
			continue
		}
		runsByID[run.RunID] = run
	}

	runs := make([]*domain.DriverRun, 0, len(runsByID))
	for _, run := range runsByID {
		runs = append(runs, run)
	}
	store.SortDriverRunsNewestFirst(runs)
	if query.Limit > 0 && len(runs) > query.Limit {
		runs = runs[:query.Limit]
	}
	if runs == nil {
		runs = []*domain.DriverRun{}
	}
	return TaskWorkflowRunResult{SubjectRef: subjectRef, Runs: runs}, nil
}

func (reader *taskWorkflowRunReader) representedDriverRunIDs(
	ctx context.Context,
	workspace, taskID string,
) (map[string]struct{}, error) {
	taskRuns, err := reader.taskRuns.List(ctx, workspace, store.TaskRunFilter{TaskID: taskID})
	if err != nil {
		return nil, err
	}
	represented := make(map[string]struct{}, len(taskRuns))
	for _, taskRun := range taskRuns {
		if taskRun == nil || taskRun.WorkspaceKey != workspace || taskRun.TaskID != taskID {
			continue
		}
		if strings.TrimSpace(taskRun.DriverRunID) == "" {
			continue
		}
		represented[taskRun.DriverRunID] = struct{}{}
	}
	return represented, nil
}
