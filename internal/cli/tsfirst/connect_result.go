package tsfirst

import (
	"context"
	"errors"
	"time"

	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
)

func completeLocalConnectResult(result connectResult, agent defspkg.AgentModule, message, operationID, providerSessionID, prompt string, started, completed time.Time, duration time.Duration, invocation localInvocationResult) (connectResult, localTurn) {
	promptHash := hashText(prompt)
	result.Message = message
	result.Response = invocation.Response
	result.ProviderSessionID = providerSessionID
	result.ProviderModel = invocation.ProviderModel
	result.ProviderMetadata = invocation.ProviderMetadata
	result.OperationID = operationID
	result.DurationMS = duration.Milliseconds()
	result.Usage = invocation.Usage
	if invocation.ToolRuntime != nil {
		result.ToolRuntime = invocation.ToolRuntime
	}
	if len(invocation.ToolCalls) > 0 {
		result.ToolCalls = append([]connectToolCall(nil), invocation.ToolCalls...)
	}
	if invocation.Resume != nil {
		result.Resume = invocation.Resume
	}
	result.Operation = connectOperationEnvelope(result, operationID, promptHash, started, completed, invocation)
	turn := localTurn{
		Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
		OperationID:       operationID,
		Operation:         result.Operation,
		Agent:             agent.Name,
		Instance:          result.Instance,
		Session:           result.Session,
		Backend:           result.Backend,
		Model:             result.Model,
		ProviderModel:     invocation.ProviderModel,
		ProviderSessionID: providerSessionID,
		ProviderMetadata:  invocation.ProviderMetadata,
		DefinitionVersion: agent.Version,
		Message:           message,
		Response:          invocation.Response,
		DurationMS:        result.DurationMS,
		Usage:             invocation.Usage,
		PromptHash:        promptHash,
		ToolRuntime:       result.ToolRuntime,
		ToolCalls:         result.ToolCalls,
		Resume:            result.Resume,
	}
	return result, turn
}

func failLocalConnectResult(result connectResult, agent defspkg.AgentModule, message, operationID, providerSessionID, prompt string, started, completed time.Time, cause error) (connectResult, localTurn) {
	promptHash := hashText(prompt)
	result.Message = message
	result.ProviderSessionID = providerSessionID
	result.OperationID = operationID
	result.DurationMS = completed.Sub(started).Milliseconds()
	result.ErrorClass = connectErrorClass(cause)
	result.ErrorMessage = cause.Error()
	result.Operation = failedConnectOperationEnvelope(result, operationID, promptHash, started, completed)
	turn := localTurn{
		Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
		OperationID:       operationID,
		Operation:         result.Operation,
		Agent:             agent.Name,
		Instance:          result.Instance,
		Session:           result.Session,
		Backend:           result.Backend,
		Model:             result.Model,
		ProviderSessionID: providerSessionID,
		DefinitionVersion: agent.Version,
		Message:           message,
		DurationMS:        result.DurationMS,
		PromptHash:        promptHash,
		ToolRuntime:       result.ToolRuntime,
		ErrorClass:        result.ErrorClass,
		ErrorMessage:      result.ErrorMessage,
	}
	return result, turn
}

func connectOperationEnvelope(result connectResult, operationID, promptHash string, started, completed time.Time, invocation localInvocationResult) *connectOperation {
	operation := &connectOperation{
		ID:                operationID,
		Kind:              "local_connect_prompt",
		Status:            "completed",
		Text:              invocation.Response,
		Model:             result.Model,
		ProviderModel:     invocation.ProviderModel,
		ProviderSessionID: result.ProviderSessionID,
		Usage:             invocation.Usage,
		StartedAt:         started.Format(time.RFC3339Nano),
		CompletedAt:       completed.Format(time.RFC3339Nano),
		DurationMS:        result.DurationMS,
		PromptHash:        promptHash,
		TranscriptPath:    result.TranscriptPath,
		Resume:            result.Resume,
		ToolCalls:         append([]connectToolCall(nil), result.ToolCalls...),
		EventCorrelation: map[string]string{
			"agent":    result.Agent,
			"instance": result.Instance,
			"session":  result.Session,
		},
	}
	if len(operation.ToolCalls) == 0 {
		operation.ToolCalls = nil
	}
	if result.ProviderMetadata != nil {
		operation.Metadata = map[string]any{"provider": result.ProviderMetadata}
	}
	return operation
}

func failedConnectOperationEnvelope(result connectResult, operationID, promptHash string, started, completed time.Time) *connectOperation {
	return &connectOperation{
		ID:                operationID,
		Kind:              "local_connect_prompt",
		Status:            "failed",
		Model:             result.Model,
		ProviderSessionID: result.ProviderSessionID,
		StartedAt:         started.Format(time.RFC3339Nano),
		CompletedAt:       completed.Format(time.RFC3339Nano),
		DurationMS:        result.DurationMS,
		PromptHash:        promptHash,
		TranscriptPath:    result.TranscriptPath,
		ErrorClass:        result.ErrorClass,
		ErrorMessage:      result.ErrorMessage,
		EventCorrelation: map[string]string{
			"agent":    result.Agent,
			"instance": result.Instance,
			"session":  result.Session,
		},
	}
}

func connectErrorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "backend_error"
	}
}
