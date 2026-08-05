package driver

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/driver/outboxdelivery"
)

type (
	// AgentMessageDeliveryResult reports how a workflow-originated message
	// reached an agent.
	AgentMessageDeliveryResult = outboxdelivery.AgentMessageDeliveryResult
	// AgentMessageDeliveryOptions carries optional task and dedupe metadata.
	AgentMessageDeliveryOptions = outboxdelivery.AgentMessageDeliveryOptions
	// OutboxDispatcher is the server-side durable agent-message delivery loop.
	OutboxDispatcher = outboxdelivery.OutboxDispatcher
)

const (
	AgentMessageDeliveryStateDelivered   = outboxdelivery.AgentMessageDeliveryStateDelivered
	AgentMessageDeliveryStateUnsupported = outboxdelivery.AgentMessageDeliveryStateUnsupported
)

func DeliverAgentMessageForDriver(
	ctx context.Context,
	messenger outboxdelivery.ChatMessenger,
	workspace,
	driverRunID,
	agentName,
	message string,
) (AgentMessageDeliveryResult, error) {
	return outboxdelivery.DeliverAgentMessageForDriver(
		ctx, messenger, workspace, driverRunID, agentName, message,
	)
}

func DeliverLeadAssignmentForDriver(
	ctx context.Context,
	messenger outboxdelivery.ChatMessenger,
	workspace,
	leadName string,
) (AgentMessageDeliveryResult, error) {
	return outboxdelivery.DeliverLeadAssignmentForDriver(ctx, messenger, workspace, leadName)
}

func DeliverAgentMessageForDriverWithOptions(
	ctx context.Context,
	messenger outboxdelivery.ChatMessenger,
	workspace,
	driverRunID,
	agentName,
	message string,
	options AgentMessageDeliveryOptions,
) (AgentMessageDeliveryResult, error) {
	return outboxdelivery.DeliverAgentMessageForDriverWithOptions(
		ctx, messenger, workspace, driverRunID, agentName, message, options,
	)
}

func NewAgentMessageDeliveryResult(
	agentName string,
	delivery *outboxdelivery.ChatDelivery,
) AgentMessageDeliveryResult {
	return outboxdelivery.NewAgentMessageDeliveryResult(agentName, delivery)
}
