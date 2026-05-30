package defs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/store"
)

type RunEventModule struct {
	EventID       string          `json:"event_id"`
	WorkflowRunID string          `json:"workflow_run_id"`
	TaskRunID     string          `json:"task_run_id,omitempty"`
	EventIndex    int64           `json:"event_index,omitempty"`
	SourcePath    string          `json:"source_path"`
	SourceHash    string          `json:"source_hash"`
	Version       string          `json:"version"`
	Type          string          `json:"type"`
	Message       string          `json:"message,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

func applyRunEvents(ctx context.Context, st store.Store, ws string, events []RunEventModule) error {
	if len(events) == 0 {
		return nil
	}
	if st.RunEvents() == nil {
		return fmt.Errorf("run event store not configured")
	}
	for _, event := range events {
		if err := applyRunEvent(ctx, st, ws, event); err != nil {
			return err
		}
	}
	return nil
}

func applyRunEvent(ctx context.Context, st store.Store, ws string, event RunEventModule) error {
	exists, err := runEventExists(ctx, st, ws, event)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  ws,
		EventID:       event.EventID,
		WorkflowRunID: event.WorkflowRunID,
		TaskRunID:     event.TaskRunID,
		Type:          event.Type,
		Message:       event.Message,
		Data:          cloneWorkflowRunRaw(event.Data),
	}); err != nil {
		return fmt.Errorf("append run event %s: %w", event.EventID, err)
	}
	return nil
}

func runEventExists(ctx context.Context, st store.Store, ws string, event RunEventModule) (bool, error) {
	events, err := st.RunEvents().List(ctx, ws, store.RunEventFilter{WorkflowRunID: event.WorkflowRunID, Limit: 10000})
	if err != nil {
		return false, fmt.Errorf("list run events for %s: %w", event.WorkflowRunID, err)
	}
	for _, existing := range events {
		if existing != nil && existing.EventID == event.EventID {
			return true, nil
		}
	}
	return false, nil
}
