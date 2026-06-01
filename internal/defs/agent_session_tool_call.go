package defs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

type AgentSessionToolCallModule struct {
	CallID              string            `json:"call_id"`
	ProviderCallID      string            `json:"provider_call_id,omitempty"`
	OperationID         string            `json:"operation_id"`
	SessionID           string            `json:"session_id"`
	AgentID             string            `json:"agent_id"`
	SourcePath          string            `json:"source_path"`
	SourceHash          string            `json:"source_hash"`
	Version             string            `json:"version"`
	WorkflowRunID       string            `json:"workflow_run_id,omitempty"`
	TaskRunID           string            `json:"task_run_id,omitempty"`
	TaskID              string            `json:"task_id,omitempty"`
	Name                string            `json:"name"`
	Status              string            `json:"status,omitempty"`
	AuthorizationStatus string            `json:"authorization_status,omitempty"`
	IdempotencyKey      string            `json:"idempotency_key,omitempty"`
	ToolVersion         string            `json:"tool_version,omitempty"`
	ToolSourceHash      string            `json:"tool_source_hash,omitempty"`
	Handler             string            `json:"handler,omitempty"`
	Runtime             string            `json:"runtime,omitempty"`
	Timeout             string            `json:"timeout,omitempty"`
	Cancellable         bool              `json:"cancellable,omitempty"`
	ReadOnly            bool              `json:"read_only,omitempty"`
	Redacted            bool              `json:"redacted,omitempty"`
	Args                json.RawMessage   `json:"args,omitempty"`
	Result              json.RawMessage   `json:"result,omitempty"`
	ErrorClass          string            `json:"error_class,omitempty"`
	ErrorMessage        string            `json:"error_message,omitempty"`
	StartedAt           *time.Time        `json:"started_at,omitempty"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty"`
	DurationMS          int64             `json:"duration_ms,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
}

func validateAgentSessionToolCallModules(plan *Plan, seen map[string]string) error {
	for _, call := range plan.AgentSessionToolCalls {
		sourcePath := firstNonEmpty(call.SourcePath, "agent_session_tool_call:"+call.CallID)
		if strings.TrimSpace(call.CallID) == "" {
			return fmt.Errorf("%s: agent session tool call id is required", sourcePath)
		}
		if strings.TrimSpace(call.OperationID) == "" {
			return fmt.Errorf("%s: agent session tool call %q must declare an operation_id", sourcePath, call.CallID)
		}
		if strings.TrimSpace(call.SessionID) == "" {
			return fmt.Errorf("%s: agent session tool call %q must declare a session_id", sourcePath, call.CallID)
		}
		if strings.TrimSpace(call.AgentID) == "" {
			return fmt.Errorf("%s: agent session tool call %q must declare an agent_id", sourcePath, call.CallID)
		}
		if strings.TrimSpace(call.Name) == "" {
			return fmt.Errorf("%s: agent session tool call %q must declare a name", sourcePath, call.CallID)
		}
		if prior := seen["agent-session-tool-call:"+call.CallID]; prior != "" {
			return fmt.Errorf("duplicate agent session tool call %q in %s and %s", call.CallID, prior, sourcePath)
		}
		seen["agent-session-tool-call:"+call.CallID] = sourcePath
	}
	return nil
}

func applyAgentSessionToolCalls(ctx context.Context, st store.Store, ws string, calls []AgentSessionToolCallModule) error {
	if len(calls) == 0 {
		return nil
	}
	if st.AgentSessionToolCalls() == nil {
		return fmt.Errorf("agent session tool call store not configured")
	}
	for _, call := range calls {
		if err := applyAgentSessionToolCall(ctx, st, ws, call); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentSessionToolCall(ctx context.Context, st store.Store, ws string, call AgentSessionToolCallModule) error {
	if _, err := st.AgentSessionToolCalls().Upsert(ctx, store.AgentSessionToolCallUpsert{
		WorkspaceKey:        ws,
		CallID:              call.CallID,
		ProviderCallID:      call.ProviderCallID,
		OperationID:         call.OperationID,
		SessionID:           call.SessionID,
		AgentID:             call.AgentID,
		WorkflowRunID:       call.WorkflowRunID,
		TaskRunID:           call.TaskRunID,
		TaskID:              call.TaskID,
		Name:                call.Name,
		Status:              firstNonEmpty(call.Status, "completed"),
		AuthorizationStatus: call.AuthorizationStatus,
		IdempotencyKey:      call.IdempotencyKey,
		ToolVersion:         call.ToolVersion,
		SourceHash:          call.ToolSourceHash,
		Handler:             call.Handler,
		Runtime:             call.Runtime,
		Timeout:             call.Timeout,
		Cancellable:         call.Cancellable,
		ReadOnly:            call.ReadOnly,
		Redacted:            call.Redacted,
		Args:                cloneWorkflowRunRaw(call.Args),
		Result:              cloneWorkflowRunRaw(call.Result),
		ErrorClass:          call.ErrorClass,
		ErrorMessage:        call.ErrorMessage,
		StartedAt:           timeValue(call.StartedAt),
		CompletedAt:         cloneWorkflowRunTime(call.CompletedAt),
		DurationMS:          call.DurationMS,
		Metadata:            cloneStringMap(call.Metadata),
	}); err != nil {
		return fmt.Errorf("upsert agent session tool call %s: %w", call.CallID, err)
	}
	return nil
}
