package interactioncomposition

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type interactionForceInterrupter struct {
	commands    interaction.RuntimeForceInterruptAPI
	authorities interaction.RuntimeAuthorityProvider
}

var _ interaction.ForceInterrupter = (*interactionForceInterrupter)(nil)

func newInteractionForceInterrupter(
	commands interaction.RuntimeForceInterruptAPI,
	authorities interaction.RuntimeAuthorityProvider,
) interaction.ForceInterrupter {
	if commands == nil || authorities == nil {
		return nil
	}
	return &interactionForceInterrupter{commands: commands, authorities: authorities}
}

func (interrupter *interactionForceInterrupter) ForceInterrupt(
	ctx context.Context,
	command interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	if interrupter == nil || interrupter.commands == nil || interrupter.authorities == nil {
		return interaction.ForceInterruptResult{}, interaction.ErrUnavailable
	}
	auth, err := interrupter.authorities.AuthorityForInteractionRuntime(
		ctx,
		interaction.TerminalLifecycleComponentID,
		command.WorkspaceKey,
		interaction.ActionForceInterrupt,
	)
	if err != nil {
		return interaction.ForceInterruptResult{}, err
	}
	return interrupter.commands.ForceInterrupt(ctx, auth, command)
}
