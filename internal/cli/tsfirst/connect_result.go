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
	result.OperationID = operationID
	result.DurationMS = duration.Milliseconds()
	result.Usage = invocation.Usage
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
		DefinitionVersion: agent.Version,
		Message:           message,
		Response:          invocation.Response,
		DurationMS:        result.DurationMS,
		Usage:             invocation.Usage,
		PromptHash:        hashText(prompt),
	}
	return result, turn
}
