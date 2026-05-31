package defs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type TaskRunModule struct {
	TaskRunID       string               `json:"task_run_id"`
	IdempotencyKey  string               `json:"idempotency_key,omitempty"`
	WorkflowRunID   string               `json:"workflow_run_id"`
	WorkItemID      string               `json:"work_item_id"`
	RoleName        string               `json:"role_name"`
	SourcePath      string               `json:"source_path"`
	SourceHash      string               `json:"source_hash"`
	Version         string               `json:"version"`
	ClaimActor      string               `json:"claim_actor,omitempty"`
	ClaimEventID    string               `json:"claim_event_id,omitempty"`
	Status          domain.TaskRunStatus `json:"status,omitempty"`
	AgentID         string               `json:"agent_id,omitempty"`
	NodeID          string               `json:"node_id,omitempty"`
	CommandID       string               `json:"command_id,omitempty"`
	SessionID       string               `json:"session_id,omitempty"`
	LeaseID         string               `json:"lease_id,omitempty"`
	ParentSessionID string               `json:"parent_session_id,omitempty"`
	Reason          string               `json:"reason,omitempty"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	FinishedAt      *time.Time           `json:"finished_at,omitempty"`
	ErrorClass      string               `json:"error_class,omitempty"`
	ErrorMessage    string               `json:"error_message,omitempty"`
	Metadata        map[string]string    `json:"metadata,omitempty"`
}

func applyTaskRuns(ctx context.Context, st store.Store, ws string, runs []TaskRunModule) error {
	if len(runs) == 0 {
		return nil
	}
	if st.TaskRuns() == nil {
		return fmt.Errorf("task run store not configured")
	}
	for _, run := range runs {
		if err := applyTaskRun(ctx, st, ws, run); err != nil {
			return err
		}
	}
	return nil
}

func applyTaskRun(ctx context.Context, st store.Store, ws string, run TaskRunModule) error {
	if run.TaskRunID != "" {
		existing, err := st.TaskRuns().Get(ctx, ws, run.TaskRunID)
		if err == nil {
			return syncTaskRunState(ctx, st, ws, existing.TaskRunID, run)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("get task run %s: %w", run.TaskRunID, err)
		}
	}
	created, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:    ws,
		TaskRunID:       run.TaskRunID,
		IdempotencyKey:  run.IdempotencyKey,
		WorkflowRunID:   run.WorkflowRunID,
		WorkItemID:      run.WorkItemID,
		RoleName:        run.RoleName,
		ClaimActor:      run.ClaimActor,
		ClaimEventID:    run.ClaimEventID,
		Status:          taskRunStatusOrQueued(run.Status),
		AgentID:         run.AgentID,
		NodeID:          run.NodeID,
		CommandID:       run.CommandID,
		SessionID:       run.SessionID,
		LeaseID:         run.LeaseID,
		ParentSessionID: run.ParentSessionID,
		Reason:          run.Reason,
		Metadata:        cloneStringMap(run.Metadata),
	})
	if err == nil {
		if created.TaskRunID != run.TaskRunID {
			return fmt.Errorf("task run %s import resumed existing run %s", run.TaskRunID, created.TaskRunID)
		}
		return syncTaskRunState(ctx, st, ws, created.TaskRunID, run)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("ensure task run %s: %w", run.TaskRunID, err)
	}
	existing, getErr := st.TaskRuns().Get(ctx, ws, run.TaskRunID)
	if getErr != nil {
		return fmt.Errorf("get existing task run %s after ensure conflict: %w", run.TaskRunID, getErr)
	}
	return syncTaskRunState(ctx, st, ws, existing.TaskRunID, run)
}

func syncTaskRunState(ctx context.Context, st store.Store, ws, taskRunID string, run TaskRunModule) error {
	claimActor := run.ClaimActor
	claimEventID := run.ClaimEventID
	status := taskRunStatusOrQueued(run.Status)
	agentID := run.AgentID
	nodeID := run.NodeID
	commandID := run.CommandID
	sessionID := run.SessionID
	leaseID := run.LeaseID
	parentSessionID := run.ParentSessionID
	reason := run.Reason
	startedAt := taskRunStartedAt(run)
	finishedAt := cloneWorkflowRunTime(run.FinishedAt)
	errorClass := run.ErrorClass
	errorMessage := run.ErrorMessage
	metadata := cloneStringMap(run.Metadata)
	patch := store.TaskRunUpdate{
		ClaimActor:      &claimActor,
		ClaimEventID:    &claimEventID,
		Status:          &status,
		AgentID:         &agentID,
		NodeID:          &nodeID,
		CommandID:       &commandID,
		SessionID:       &sessionID,
		LeaseID:         &leaseID,
		ParentSessionID: &parentSessionID,
		Reason:          &reason,
		StartedAt:       &startedAt,
		FinishedAt:      &finishedAt,
		ErrorClass:      &errorClass,
		ErrorMessage:    &errorMessage,
		Metadata:        &metadata,
	}
	if _, err := st.TaskRuns().Update(ctx, ws, taskRunID, patch); err != nil {
		return fmt.Errorf("update task run %s: %w", taskRunID, err)
	}
	return nil
}

func taskRunStatusOrQueued(status domain.TaskRunStatus) domain.TaskRunStatus {
	if status == "" {
		return domain.TaskRunQueued
	}
	return status
}

func taskRunStartedAt(run TaskRunModule) time.Time {
	if run.StartedAt == nil {
		return time.Time{}
	}
	return *run.StartedAt
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}
