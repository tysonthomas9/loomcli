package interactioncomposition

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type interactionInboxEnqueuer struct {
	commands    interaction.RuntimeInboxAPI
	authorities interaction.RuntimeAuthorityProvider
}

var _ interaction.InboxEnqueuer = (*interactionInboxEnqueuer)(nil)

func newInteractionInboxEnqueuer(
	commands interaction.RuntimeInboxAPI,
	authorities interaction.RuntimeAuthorityProvider,
) interaction.InboxEnqueuer {
	if commands == nil || authorities == nil {
		return nil
	}
	return &interactionInboxEnqueuer{commands: commands, authorities: authorities}
}

func (enqueuer *interactionInboxEnqueuer) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	if enqueuer == nil || enqueuer.commands == nil || enqueuer.authorities == nil {
		return nil, interaction.ErrUnavailable
	}
	auth, err := enqueuer.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.InboxDeliveryComponentID,
		command.WorkspaceKey,
		interaction.ActionEnqueueInbox,
	)
	if err != nil {
		return nil, err
	}
	return enqueuer.commands.EnqueueInboxAsSystem(ctx, auth, command)
}
