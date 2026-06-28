package driver

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentinbox"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// AgentMessageDeliveryResult reports how a workflow-originated message reached
// an agent: live delivery into a controlled lead runtime, or queued into the
// agent inbox.
type AgentMessageDeliveryResult struct {
	AgentName       string `json:"agentName"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	InboxMessageID  string `json:"inboxMessageId,omitempty"`
	RuntimeProvider string `json:"runtimeProvider,omitempty"`
	RuntimeStatus   string `json:"runtimeStatus,omitempty"`
	Controlled      bool   `json:"controlled,omitempty"`
}

// AgentMessageDeliveryOptions carries optional delivery metadata for
// DeliverAgentMessageForDriverWithOptions. DedupeKey, when set, overrides the
// inbox-side dedupe key so redelivery (e.g. the outbox dispatcher retrying)
// never enqueues the same message twice.
type AgentMessageDeliveryOptions struct {
	TaskRunID string
	DedupeKey string
}

// DeliverAgentMessageForDriver routes a driver-run message to an agent:
// controlled lead agents get live lead-control delivery, everything else is
// queued into the agent inbox. Shared by the driver CLI deliver-agent-message
// subcommand and the driver-op HTTP API.
func DeliverAgentMessageForDriver(ctx context.Context, st store.Store, workspace, driverRunID, agentName, message string) (AgentMessageDeliveryResult, error) {
	return DeliverAgentMessageForDriverWithOptions(ctx, st, workspace, driverRunID, agentName, message, AgentMessageDeliveryOptions{})
}

// DeliverAgentMessageForDriverWithOptions is DeliverAgentMessageForDriver
// with explicit delivery options; the outbox dispatcher uses it to forward
// the row's DedupeKey into the agent inbox.
func DeliverAgentMessageForDriverWithOptions(ctx context.Context, st store.Store, workspace, driverRunID, agentName, message string, opts AgentMessageDeliveryOptions) (AgentMessageDeliveryResult, error) {
	agent, err := st.Agents().Get(ctx, workspace, agentName)
	if err != nil {
		return AgentMessageDeliveryResult{}, fmt.Errorf("get target agent: %w", err)
	}
	if isControlledLeadAgent(agent) {
		delivery, err := leadcontrol.DeliverLeadMessageWithOptions(ctx, st, workspace, agentName, message, leadcontrol.LeadMessageDeliveryOptions{
			SourceKind:  "workflow",
			DriverRunID: driverRunID,
			TaskRunID:   opts.TaskRunID,
			DedupeKey:   opts.DedupeKey,
		})
		if err != nil {
			return AgentMessageDeliveryResult{}, err
		}
		return NewAgentMessageDeliveryResult(agentName, delivery), nil
	}
	msg, err := agentinbox.Enqueue(ctx, st, workspace, agentName, message, agentinbox.MessageOptions{
		SourceKind:  "workflow",
		SourceRef:   "driver-run://" + strings.TrimSpace(driverRunID),
		DriverRunID: driverRunID,
		TaskRunID:   opts.TaskRunID,
		DedupeKey:   opts.DedupeKey,
	})
	if err != nil {
		return AgentMessageDeliveryResult{}, err
	}
	return AgentMessageDeliveryResult{
		AgentName:      agentName,
		State:          "queued",
		Reason:         "agent message queued; no runtime delivery adapter is configured",
		SessionID:      msg.SessionID,
		InboxMessageID: msg.InboxMessageID,
	}, nil
}

func isControlledLeadAgent(agent *domain.Agent) bool {
	if agent == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(agent.RoleName), "lead") && leadcontrol.IsControlledLeadBackend(agent.Backend)
}

// NewAgentMessageDeliveryResult converts a lead-control delivery into the
// driver-facing result shape.
func NewAgentMessageDeliveryResult(agentName string, delivery *leadcontrol.DeliveryResult) AgentMessageDeliveryResult {
	result := AgentMessageDeliveryResult{
		AgentName: agentName,
		State:     string(leadcontrol.DeliveryStateNone),
	}
	if delivery != nil {
		result.State = string(delivery.State)
		result.Reason = delivery.Reason
		result.SessionID = delivery.SessionID
		result.InboxMessageID = delivery.InboxMessageID
		result.RuntimeProvider = delivery.Provider
		if delivery.Provider != "" && delivery.Provider != leadcontrol.RuntimeProviderCodex {
			result.RuntimeStatus = delivery.HarnessRuntime.Status
			result.Controlled = delivery.HarnessRuntime.Controlled
		} else {
			result.RuntimeStatus = delivery.Runtime.Status
			result.Controlled = delivery.Runtime.Controlled
		}
	}
	return result
}
