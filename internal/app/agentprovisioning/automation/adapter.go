// Package automation adapts Automation's exact managed-binding command to the
// AgentProvisioning binding step.
package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Capability interface {
	EnsureBinding(context.Context, agentprovisioning.EnsureBindingCommand) error
}

type AuthorityProvider interface {
	AuthorityForBinding(context.Context, string, string) (authority.SystemAuthority, error)
}

type Adapter struct {
	capability  Capability
	authorities AuthorityProvider
}

var _ agentprovisioning.BindingOperations = (*Adapter)(nil)

func New(capability Capability, authorities AuthorityProvider) (*Adapter, error) {
	if capability == nil || authorities == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Automation adapter: %w", agentprovisioning.ErrUnavailable)
	}
	return &Adapter{capability: capability, authorities: authorities}, nil
}

func (adapter *Adapter) EnsureBinding(
	ctx context.Context,
	command agentprovisioning.EnsureBindingCommand,
) error {
	_, err := adapter.authorities.AuthorityForBinding(
		ctx,
		command.WorkspaceKey,
		"AgentProvisioning "+command.CommandID,
	)
	if err != nil {
		return fmt.Errorf(
			"issue binding authority through Automation guarded owner command: %w",
			errors.Join(agentprovisioning.ErrUnavailable, err),
		)
	}
	err = adapter.capability.EnsureBinding(ctx, command)
	return mapError("ensure binding", err)
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s through Automation guarded owner command: %w", operation, err)
}
