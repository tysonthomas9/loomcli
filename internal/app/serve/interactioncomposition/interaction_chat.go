package interactioncomposition

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// ComposeInteractionChat attaches Interaction's provider-neutral chat surface
// to an already composed capability. The runtime may depend on the
// capability's authority-free InboxEnqueuer, so this intentionally runs after
// the atomic session/inbox owner has been constructed.
func ComposeInteractionChat(
	capability *InteractionCapability,
	runtime interaction.ChatRuntime,
) error {
	if capability == nil || capability.issuer == nil || runtime == nil {
		return fmt.Errorf(
			"compose Interaction chat: capability issuer and runtime are required: %w",
			interaction.ErrUnavailable,
		)
	}
	if capability.chatAPI != nil || capability.chatMessenger != nil {
		return fmt.Errorf(
			"compose Interaction chat: chat surface is already composed: %w",
			interaction.ErrConflict,
		)
	}
	admission, err := capability.issuer.NewAdmission(
		interaction.ChatOperationRules()...,
	)
	if err != nil {
		return fmt.Errorf("compose Interaction chat admission: %w", err)
	}
	service, err := interaction.NewChat(runtime, admission)
	if err != nil {
		return err
	}
	authorities := newInteractionRuntimeAuthorityProvider(
		capability.issuer,
		time.Now,
	)
	messenger := newInteractionChatMessenger(service, authorities)
	if messenger == nil {
		return fmt.Errorf(
			"compose Interaction chat messenger: %w",
			interaction.ErrUnavailable,
		)
	}
	capability.chatAPI = service
	capability.chatMessenger = messenger
	return nil
}

type interactionChatMessenger struct {
	commands    interaction.RuntimeChatAPI
	authorities interaction.RuntimeAuthorityProvider
}

var _ interaction.ChatMessenger = (*interactionChatMessenger)(nil)

func newInteractionChatMessenger(
	commands interaction.RuntimeChatAPI,
	authorities interaction.RuntimeAuthorityProvider,
) interaction.ChatMessenger {
	if commands == nil || authorities == nil {
		return nil
	}
	return &interactionChatMessenger{
		commands:    commands,
		authorities: authorities,
	}
}

func (messenger *interactionChatMessenger) DeliverChatMessage(
	ctx context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	if messenger == nil || messenger.commands == nil ||
		messenger.authorities == nil {
		return nil, interaction.ErrUnavailable
	}
	auth, err := messenger.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.ChatDeliveryComponentID,
		command.WorkspaceKey,
		interaction.ActionDeliverChatMessage,
	)
	if err != nil {
		return nil, err
	}
	return messenger.commands.DeliverChatMessageAsSystem(ctx, auth, command)
}

func (messenger *interactionChatMessenger) DeliverAssignment(
	ctx context.Context,
	command interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	if messenger == nil || messenger.commands == nil ||
		messenger.authorities == nil {
		return nil, interaction.ErrUnavailable
	}
	auth, err := messenger.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.ChatDeliveryComponentID,
		command.WorkspaceKey,
		interaction.ActionDeliverAssignment,
	)
	if err != nil {
		return nil, err
	}
	return messenger.commands.DeliverAssignmentAsSystem(ctx, auth, command)
}
