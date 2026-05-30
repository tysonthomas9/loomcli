package tsfirst

import (
	"time"

	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
)

func completeLocalConnectResult(result connectResult, agent defspkg.AgentModule, message, operationID, providerSessionID, prompt string, duration time.Duration, invocation localInvocationResult) (connectResult, localTurn) {
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
	turn := localTurn{
		Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
		OperationID:       operationID,
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
		PromptHash:        hashText(prompt),
		ToolRuntime:       result.ToolRuntime,
		ToolCalls:         result.ToolCalls,
		Resume:            result.Resume,
	}
	return result, turn
}
