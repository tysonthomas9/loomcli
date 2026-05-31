package defs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type WorkflowRunModule struct {
	RunID           string                   `json:"run_id"`
	WorkflowName    string                   `json:"workflow_name"`
	WorkflowVersion string                   `json:"workflow_version,omitempty"`
	SourcePath      string                   `json:"source_path"`
	SourceHash      string                   `json:"source_hash"`
	Version         string                   `json:"version"`
	BundleHash      string                   `json:"bundle_hash,omitempty"`
	IdempotencyKey  string                   `json:"idempotency_key,omitempty"`
	Input           json.RawMessage          `json:"input,omitempty"`
	Status          domain.WorkflowRunStatus `json:"status,omitempty"`
	Result          json.RawMessage          `json:"result,omitempty"`
	ErrorClass      string                   `json:"error_class,omitempty"`
	ErrorMessage    string                   `json:"error_message,omitempty"`
	WaitCondition   string                   `json:"wait_condition,omitempty"`
	LeaseOwner      string                   `json:"lease_owner,omitempty"`
	LeaseToken      string                   `json:"lease_token,omitempty"`
	FencingToken    int64                    `json:"fencing_token,omitempty"`
	StartedAt       *time.Time               `json:"started_at,omitempty"`
	FinishedAt      *time.Time               `json:"finished_at,omitempty"`
}

func applyWorkflowRuns(ctx context.Context, st store.Store, ws string, runs []WorkflowRunModule) error {
	if len(runs) == 0 {
		return nil
	}
	if st.WorkflowRuns() == nil {
		return fmt.Errorf("workflow run store not configured")
	}
	for _, run := range runs {
		if err := applyWorkflowRun(ctx, st, ws, run); err != nil {
			return err
		}
	}
	return nil
}

func applyWorkflowRun(ctx context.Context, st store.Store, ws string, run WorkflowRunModule) error {
	if run.RunID != "" {
		existing, err := st.WorkflowRuns().Get(ctx, ws, run.RunID)
		if err == nil {
			return syncWorkflowRunState(ctx, st, ws, existing.RunID, run)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("get workflow run %s: %w", run.RunID, err)
		}
	}
	created, err := st.WorkflowRuns().CreateOrResume(ctx, store.WorkflowRunCreate{
		WorkspaceKey:    ws,
		RunID:           run.RunID,
		WorkflowName:    run.WorkflowName,
		WorkflowVersion: run.WorkflowVersion,
		BundleHash:      run.BundleHash,
		IdempotencyKey:  run.IdempotencyKey,
		Input:           cloneWorkflowRunRaw(run.Input),
		Status:          workflowRunStatusOrQueued(run.Status),
		LeaseOwner:      run.LeaseOwner,
		LeaseToken:      run.LeaseToken,
		StartedAt:       workflowRunStartedAt(run),
	})
	if err == nil {
		if created.RunID != run.RunID {
			return fmt.Errorf("workflow run %s import resumed existing run %s", run.RunID, created.RunID)
		}
		return syncWorkflowRunState(ctx, st, ws, created.RunID, run)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("create workflow run %s: %w", run.RunID, err)
	}
	existing, getErr := st.WorkflowRuns().Get(ctx, ws, run.RunID)
	if getErr != nil {
		return fmt.Errorf("get existing workflow run %s after create conflict: %w", run.RunID, getErr)
	}
	return syncWorkflowRunState(ctx, st, ws, existing.RunID, run)
}

func syncWorkflowRunState(ctx context.Context, st store.Store, ws, runID string, run WorkflowRunModule) error {
	status := workflowRunStatusOrQueued(run.Status)
	result := cloneWorkflowRunRaw(run.Result)
	errorClass := run.ErrorClass
	errorMessage := run.ErrorMessage
	waitCondition := run.WaitCondition
	leaseOwner := run.LeaseOwner
	leaseToken := run.LeaseToken
	fencingToken := run.FencingToken
	startedAt := workflowRunStartedAt(run)
	finishedAt := cloneWorkflowRunTime(run.FinishedAt)
	patch := store.WorkflowRunUpdate{
		Status:        &status,
		Result:        &result,
		ErrorClass:    &errorClass,
		ErrorMessage:  &errorMessage,
		WaitCondition: &waitCondition,
		LeaseOwner:    &leaseOwner,
		LeaseToken:    &leaseToken,
		FencingToken:  &fencingToken,
		StartedAt:     &startedAt,
		FinishedAt:    &finishedAt,
	}
	if _, err := st.WorkflowRuns().Update(ctx, ws, runID, patch); err != nil {
		return fmt.Errorf("update workflow run %s: %w", runID, err)
	}
	return nil
}

func workflowRunStatusOrQueued(status domain.WorkflowRunStatus) domain.WorkflowRunStatus {
	if status == "" {
		return domain.WorkflowRunQueued
	}
	return status
}

func workflowRunStartedAt(run WorkflowRunModule) time.Time {
	if run.StartedAt == nil {
		return time.Time{}
	}
	return *run.StartedAt
}

func cloneWorkflowRunRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneWorkflowRunTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
