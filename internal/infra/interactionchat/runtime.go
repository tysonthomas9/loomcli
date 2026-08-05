// Package interactionchat adapts controlled lead runtimes to Interaction's
// provider-neutral chat port. Store/session metadata and provider transcript
// details remain infrastructure concerns and never cross Interaction's public
// API.
package interactionchat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentinbox"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/sessions/redact"
)

const providerCallTimeout = 10 * time.Second

type codexThreadReader interface {
	ReadThreadWithTurns(
		context.Context,
		string,
	) (*leadcontrol.CodexThread, error)
	Close(string) error
}

// AgentQueries is the narrow Agents read dependency needed by chat delivery.
// It deliberately excludes every identity, lifecycle, and ownership command.
type AgentQueries interface {
	GetAgent(
		context.Context,
		string,
		string,
	) (*agents.Agent, error)
	GetRole(
		context.Context,
		string,
		string,
	) (*agents.Role, error)
}

// LeadRuntimeDependencies adapts the remaining provider-specific lead
// delivery projection at the composition edge. Interaction chat receives
// operations, not the process-wide persistence aggregate.
type LeadRuntimeDependencies = leadcontrol.InteractionChatDependencies

type Runtime struct {
	lead        LeadRuntimeDependencies
	inbox       interaction.InboxEnqueuer
	agents      AgentQueries
	dialCodex   func(context.Context, string) (codexThreadReader, error)
	worktreeFor func(string, string) (string, bool)
	harnesses   map[string]harnessTranscriptReader
	retryDelay  time.Duration
}

var _ interaction.ChatRuntime = (*Runtime)(nil)

func New(
	lead LeadRuntimeDependencies,
	inbox interaction.InboxEnqueuer,
	agentQueries AgentQueries,
) (*Runtime, error) {
	if lead.DeliverMessage == nil || lead.DeliverAssignment == nil ||
		lead.FindSession == nil || inbox == nil || agentQueries == nil {
		return nil, fmt.Errorf(
			"compose Interaction chat runtime: lead delivery, inbox delivery, and Agents queries are required: %w",
			interaction.ErrUnavailable,
		)
	}
	return newRuntime(lead, inbox, agentQueries), nil
}

func newRuntime(
	lead LeadRuntimeDependencies,
	inbox interaction.InboxEnqueuer,
	agentQueries AgentQueries,
) *Runtime {
	return &Runtime{
		lead:   lead,
		inbox:  inbox,
		agents: agentQueries,
		dialCodex: func(
			ctx context.Context,
			endpoint string,
		) (codexThreadReader, error) {
			return leadcontrol.DialCodexAppServer(ctx, endpoint)
		},
		worktreeFor: rememberedAgentWorktree,
		harnesses:   defaultHarnessReaders(),
		retryDelay:  harnessReadRetryDelay,
	}
}

func (runtime *Runtime) DeliverChatMessage(
	ctx context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	if runtime == nil || runtime.lead.DeliverMessage == nil ||
		runtime.inbox == nil || runtime.agents == nil {
		return nil, interaction.ErrUnavailable
	}
	target, err := runtime.targetAgent(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	if !target.controlledLead {
		return runtime.enqueueGenericMessage(ctx, command)
	}
	result, err := runtime.lead.DeliverMessage(
		ctx,
		command.WorkspaceKey,
		command.AgentID,
		command.Body,
		leadcontrol.LeadMessageDeliveryOptions{
			SourceKind:        command.SourceKind,
			SourceRef:         command.SourceRef,
			DriverRunID:       command.DriverRunID,
			TaskRunID:         command.TaskRunID,
			TriggerEventID:    command.TriggerEventID,
			TriggerDeliveryID: command.TriggerDeliveryID,
			DedupeKey:         command.DedupeKey,
		},
		runtime.inbox,
	)
	if err != nil {
		return nil, err
	}
	return chatDelivery(result), nil
}

func (runtime *Runtime) DeliverAssignment(
	ctx context.Context,
	command interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	if runtime == nil || runtime.lead.DeliverAssignment == nil ||
		runtime.inbox == nil || runtime.agents == nil {
		return nil, interaction.ErrUnavailable
	}
	if _, err := runtime.targetAgent(
		ctx,
		command.WorkspaceKey,
		command.AgentID,
	); err != nil {
		return nil, err
	}
	result, err := runtime.lead.DeliverAssignment(
		ctx,
		command.WorkspaceKey,
		command.AgentID,
		runtime.inbox,
	)
	if err != nil {
		return nil, err
	}
	return chatDelivery(result), nil
}

type resolvedTargetAgent struct {
	controlledLead bool
}

func (runtime *Runtime) targetAgent(
	ctx context.Context,
	workspace,
	agentID string,
) (resolvedTargetAgent, error) {
	agent, err := runtime.agents.GetAgent(ctx, workspace, agentID)
	if err != nil {
		return resolvedTargetAgent{}, mapAgentsQueryError(
			"get target agent",
			err,
		)
	}
	if err := validateTargetAgentRecord(agent, workspace, agentID); err != nil {
		return resolvedTargetAgent{}, err
	}
	roleName := strings.TrimSpace(agent.Behavior.RoleName)
	if !strings.EqualFold(roleName, "lead") {
		return resolvedTargetAgent{}, nil
	}
	role, err := runtime.agents.GetRole(ctx, workspace, roleName)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			return resolvedTargetAgent{}, fmt.Errorf(
				"target agent %q references a missing role: %w",
				agentID,
				interaction.ErrInvalidPersistedState,
			)
		}
		return resolvedTargetAgent{}, mapAgentsQueryError(
			"get target agent role",
			err,
		)
	}
	if role == nil ||
		role.WorkspaceKey != workspace ||
		role.Name != roleName {
		return resolvedTargetAgent{}, fmt.Errorf(
			"agents returned a mismatched target role: %w",
			interaction.ErrInvalidPersistedState,
		)
	}
	return resolvedTargetAgent{
		controlledLead: leadcontrol.IsControlledLeadBackend(role.Backend),
	}, nil
}

func validateTargetAgentRecord(
	agent *agents.Agent,
	workspace,
	agentID string,
) error {
	if agent == nil ||
		agent.WorkspaceKey != workspace ||
		agent.AgentID != agentID {
		return fmt.Errorf(
			"agents returned a mismatched target identity: %w",
			interaction.ErrInvalidPersistedState,
		)
	}
	if agent.DeletedAt != nil {
		return fmt.Errorf(
			"target agent %q is archived: %w",
			agentID,
			interaction.ErrNotFound,
		)
	}
	return nil
}

func mapAgentsQueryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, agents.ErrInvalid):
		sentinel = interaction.ErrInvalid
	case errors.Is(err, agents.ErrNotFound):
		sentinel = interaction.ErrNotFound
	case errors.Is(err, agents.ErrAlreadyExists),
		errors.Is(err, agents.ErrConflict):
		sentinel = interaction.ErrConflict
	case errors.Is(err, agents.ErrNotOwner):
		sentinel = interaction.ErrNotOwner
	case errors.Is(err, agents.ErrInvalidTransition):
		sentinel = interaction.ErrInvalidTransition
	case errors.Is(err, agents.ErrInvalidPersistedState):
		sentinel = interaction.ErrInvalidPersistedState
	default:
		sentinel = interaction.ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(sentinel, err))
}

func (runtime *Runtime) enqueueGenericMessage(
	ctx context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	message, err := agentinbox.Enqueue(
		ctx,
		runtime.inbox,
		command.WorkspaceKey,
		command.AgentID,
		command.Body,
		agentinbox.MessageOptions{
			SourceKind:        command.SourceKind,
			SourceRef:         command.SourceRef,
			DriverRunID:       command.DriverRunID,
			TaskRunID:         command.TaskRunID,
			TriggerEventID:    command.TriggerEventID,
			TriggerDeliveryID: command.TriggerDeliveryID,
			DedupeKey:         command.DedupeKey,
		},
	)
	if err != nil {
		return nil, err
	}
	if message == nil ||
		message.WorkspaceKey != command.WorkspaceKey ||
		message.TargetAgentID != command.AgentID ||
		message.InboxMessageID == "" {
		return nil, fmt.Errorf(
			"interaction inbox returned a mismatched delivery: %w",
			interaction.ErrInvalidPersistedState,
		)
	}
	return &interaction.ChatDelivery{
		State: interaction.ChatDeliveryQueued,
		Reason: "agent message queued; no runtime delivery adapter is " +
			"configured",
		SessionID:      message.SessionID,
		InboxMessageID: message.InboxMessageID,
	}, nil
}

func chatDelivery(
	result *leadcontrol.DeliveryResult,
) *interaction.ChatDelivery {
	if result == nil {
		return nil
	}
	delivery := &interaction.ChatDelivery{
		State:          interaction.ChatDeliveryState(result.State),
		Reason:         redact.String(result.Reason),
		Provider:       result.Provider,
		SessionID:      result.SessionID,
		InboxMessageID: result.InboxMessageID,
	}
	if result.Provider != "" &&
		result.Provider != leadcontrol.RuntimeProviderCodex {
		delivery.RuntimeStatus = result.HarnessRuntime.Status
		delivery.Controlled = result.HarnessRuntime.Controlled
	} else {
		delivery.RuntimeStatus = result.Runtime.Status
		delivery.Controlled = result.Runtime.Controlled
	}
	return delivery
}

func (runtime *Runtime) ReadConversation(
	ctx context.Context,
	query interaction.ConversationQuery,
) (*interaction.Conversation, error) {
	if runtime == nil || runtime.lead.FindSession == nil {
		return nil, interaction.ErrUnavailable
	}
	session, err := runtime.lead.FindSession(
		ctx,
		query.WorkspaceKey,
		query.AgentID,
	)
	if err != nil {
		return nil, err
	}
	provider := ""
	if session != nil {
		provider = strings.ToLower(strings.TrimSpace(
			session.Metadata[leadcontrol.MetadataRuntimeProvider],
		))
	}
	var conversation *interaction.Conversation
	switch provider {
	case "", leadcontrol.RuntimeProviderCodex:
		conversation = runtime.readCodexConversation(ctx, session)
	default:
		conversation, err = runtime.readHarnessConversation(
			ctx,
			query,
			session,
			provider,
		)
		if err != nil {
			return nil, err
		}
	}
	if conversation == nil {
		return nil, interaction.ErrInvalidPersistedState
	}
	for index := range conversation.Messages {
		conversation.Messages[index].Text = redact.String(
			conversation.Messages[index].Text,
		)
	}
	if conversation.Messages == nil {
		conversation.Messages = []interaction.ConversationMessage{}
	}
	return conversation, nil
}
