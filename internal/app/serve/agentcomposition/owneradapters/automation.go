package owneradapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type AutomationOperations interface {
	EnsureBinding(context.Context, agentprovisioning.EnsureBindingCommand) error
}

type AutomationAuthority interface {
	AuthorityForBinding(context.Context, string, string) (authority.SystemAuthority, error)
}

type AutomationAdapter struct {
	operations  AutomationOperations
	authorities AutomationAuthority
}

var _ agentprovisioning.BindingOperations = (*AutomationAdapter)(nil)

func NewAutomationAdapter(
	operations AutomationOperations,
	authorities AutomationAuthority,
) (*AutomationAdapter, error) {
	if operations == nil || authorities == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Automation adapter: %w", agentprovisioning.ErrUnavailable)
	}
	return &AutomationAdapter{operations: operations, authorities: authorities}, nil
}

func (adapter *AutomationAdapter) EnsureBinding(
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
	err = adapter.operations.EnsureBinding(ctx, command)
	return mapAutomationError("ensure binding", err)
}

func mapAutomationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s through Automation guarded owner command: %w", operation, err)
}
