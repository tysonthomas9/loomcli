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

type triggerDeliveryListPort interface {
	List(context.Context, string, store.TriggerDeliveryFilter) ([]*automation.Delivery, error)
}

type driverRunGetPort interface {
	Get(context.Context, string, string) (*domain.DriverRun, error)
}

type taskWorkflowRunReader struct {
	taskRuns   taskRunListPort
	events     triggerEventListPort
	deliveries triggerDeliveryListPort
	driverRuns driverRunGetPort
}

// NewTaskWorkflowRunReader constructs the projection from exact-purpose,
// read-only persistence ports. Composition passes individual stores rather
// than the process-wide composite Store.
func NewTaskWorkflowRunReader(
	taskRuns taskRunListPort,
	events triggerEventListPort,
	deliveries triggerDeliveryListPort,
	driverRuns driverRunGetPort,
) TaskWorkflowRunReader {
	if taskRuns == nil || events == nil || deliveries == nil || driverRuns == nil {
		return nil
	}
	return &taskWorkflowRunReader{
		taskRuns:   taskRuns,
		events:     events,
		deliveries: deliveries,
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
//	issue id -> TriggerEvent.subject_ref -> TriggerDelivery.driver_run_id
//
// Payload fields are deliberately not consulted.
func (reader *taskWorkflowRunReader) ListTaskWorkflowRuns(
	ctx context.Context,
	query TaskWorkflowRunQuery,
) (TaskWorkflowRunResult, error) {
	workspace := strings.TrimSpace(query.WorkspaceKey)
	taskID := strings.TrimSpace(query.TaskID)
	if reader == nil || reader.taskRuns == nil ||
		reader.events == nil || reader.deliveries == nil || reader.driverRuns == nil {
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

	runsByID := make(map[string]*domain.DriverRun)
	for _, event := range events {
		if err := reader.collectTaskWorkflowEventRuns(
			ctx, workspace, subjectRef, event, representedDriverRuns, runsByID,
		); err != nil {
			return TaskWorkflowRunResult{}, err
		}
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

func (reader *taskWorkflowRunReader) collectTaskWorkflowEventRuns(
	ctx context.Context,
	workspace, subjectRef string,
	event *automation.Event,
	representedDriverRuns map[string]struct{},
	runsByID map[string]*domain.DriverRun,
) error {
	// Keep the exact check even when the backing store pushes SubjectRef down. It
	// is the final no-leakage guard for compatibility stores.
	if event == nil || event.WorkspaceKey != workspace || event.SubjectRef != subjectRef {
		return nil
	}
	deliveries, err := reader.deliveries.List(ctx, workspace, store.TriggerDeliveryFilter{
		TriggerEventID: event.EventID,
	})
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		if delivery == nil || delivery.WorkspaceKey != workspace ||
			delivery.TriggerEventID != event.EventID || strings.TrimSpace(delivery.DriverRunID) == "" {
			continue
		}
		if _, represented := representedDriverRuns[delivery.DriverRunID]; represented {
			continue
		}
		if _, duplicate := runsByID[delivery.DriverRunID]; duplicate {
			continue
		}
		run, err := reader.driverRuns.Get(ctx, workspace, delivery.DriverRunID)
		if err != nil {
			// A partially written or concurrently repaired delivery must not make
			// every other issue run disappear.
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return err
		}
		if run == nil || run.WorkspaceKey != workspace || run.RunID != delivery.DriverRunID ||
			run.SourceRef != event.EventID || run.TriggerBindingID != delivery.TriggerBindingID {
			continue
		}
		runsByID[run.RunID] = run
	}
	return nil
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
