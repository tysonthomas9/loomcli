package defs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type AgentSessionOperationModule struct {
	OperationID       string                             `json:"operation_id"`
	SessionID         string                             `json:"session_id"`
	AgentID           string                             `json:"agent_id"`
	SourcePath        string                             `json:"source_path"`
	SourceHash        string                             `json:"source_hash"`
	Version           string                             `json:"version"`
	WorkflowRunID     string                             `json:"workflow_run_id,omitempty"`
	TaskRunID         string                             `json:"task_run_id,omitempty"`
	TaskID            string                             `json:"task_id,omitempty"`
	Kind              string                             `json:"kind,omitempty"`
	Status            domain.AgentSessionOperationStatus `json:"status,omitempty"`
	Model             string                             `json:"model,omitempty"`
	Provider          string                             `json:"provider,omitempty"`
	ProviderModel     string                             `json:"provider_model,omitempty"`
	ProviderSessionID string                             `json:"provider_session_id,omitempty"`
	PromptHash        string                             `json:"prompt_hash,omitempty"`
	Text              string                             `json:"text,omitempty"`
	Input             json.RawMessage                    `json:"input,omitempty"`
	Result            json.RawMessage                    `json:"result,omitempty"`
	Usage             json.RawMessage                    `json:"usage,omitempty"`
	ToolCalls         json.RawMessage                    `json:"tool_calls,omitempty"`
	ErrorClass        string                             `json:"error_class,omitempty"`
	ErrorMessage      string                             `json:"error_message,omitempty"`
	StartedAt         *time.Time                         `json:"started_at,omitempty"`
	CompletedAt       *time.Time                         `json:"completed_at,omitempty"`
	DurationMS        int64                              `json:"duration_ms,omitempty"`
	Metadata          map[string]string                  `json:"metadata,omitempty"`
}

func validateAgentSessionOperationModules(plan *Plan, seen map[string]string) error {
	for _, op := range plan.AgentSessionOperations {
		sourcePath := firstNonEmpty(op.SourcePath, "agent_session_operation:"+op.OperationID)
		if strings.TrimSpace(op.OperationID) == "" {
			return fmt.Errorf("%s: agent session operation id is required", sourcePath)
		}
		if strings.TrimSpace(op.SessionID) == "" {
			return fmt.Errorf("%s: agent session operation %q must declare a session_id", sourcePath, op.OperationID)
		}
		if strings.TrimSpace(op.AgentID) == "" {
			return fmt.Errorf("%s: agent session operation %q must declare an agent_id", sourcePath, op.OperationID)
		}
		if strings.TrimSpace(op.Kind) == "" {
			return fmt.Errorf("%s: agent session operation %q must declare a kind", sourcePath, op.OperationID)
		}
		if prior := seen["agent-session-operation:"+op.OperationID]; prior != "" {
			return fmt.Errorf("duplicate agent session operation %q in %s and %s", op.OperationID, prior, sourcePath)
		}
		seen["agent-session-operation:"+op.OperationID] = sourcePath
	}
	return nil
}

func applyAgentSessionOperations(ctx context.Context, st store.Store, ws string, operations []AgentSessionOperationModule) error {
	if len(operations) == 0 {
		return nil
	}
	if st.AgentSessionOperations() == nil {
		return fmt.Errorf("agent session operation store not configured")
	}
	for _, operation := range operations {
		if err := applyAgentSessionOperation(ctx, st, ws, operation); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentSessionOperation(ctx context.Context, st store.Store, ws string, operation AgentSessionOperationModule) error {
	if _, err := st.AgentSessionOperations().Upsert(ctx, store.AgentSessionOperationUpsert{
		WorkspaceKey:      ws,
		OperationID:       operation.OperationID,
		SessionID:         operation.SessionID,
		AgentID:           operation.AgentID,
		WorkflowRunID:     operation.WorkflowRunID,
		TaskRunID:         operation.TaskRunID,
		TaskID:            operation.TaskID,
		Kind:              operation.Kind,
		Status:            agentSessionOperationStatusOrAdmitted(operation.Status),
		Model:             operation.Model,
		Provider:          operation.Provider,
		ProviderModel:     operation.ProviderModel,
		ProviderSessionID: operation.ProviderSessionID,
		PromptHash:        operation.PromptHash,
		Text:              operation.Text,
		Input:             cloneWorkflowRunRaw(operation.Input),
		Result:            cloneWorkflowRunRaw(operation.Result),
		Usage:             cloneWorkflowRunRaw(operation.Usage),
		ToolCalls:         cloneWorkflowRunRaw(operation.ToolCalls),
		ErrorClass:        operation.ErrorClass,
		ErrorMessage:      operation.ErrorMessage,
		StartedAt:         timeValue(operation.StartedAt),
		CompletedAt:       cloneWorkflowRunTime(operation.CompletedAt),
		DurationMS:        operation.DurationMS,
		Metadata:          cloneStringMap(operation.Metadata),
	}); err != nil {
		return fmt.Errorf("upsert agent session operation %s: %w", operation.OperationID, err)
	}
	return nil
}

func agentSessionOperationStatusOrAdmitted(status domain.AgentSessionOperationStatus) domain.AgentSessionOperationStatus {
	if status == "" {
		return domain.AgentSessionOperationAdmitted
	}
	return status
}
