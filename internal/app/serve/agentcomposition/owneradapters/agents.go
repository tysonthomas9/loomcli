package owneradapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type AgentsOperations interface {
	EnsureRole(context.Context, agentprovisioning.EnsureRoleCommand) error
	EnsureAgent(context.Context, agentprovisioning.EnsureAgentCommand) error
}

// AgentsAuthority exposes fixed actions so the guarded
// durable adapter cannot turn a provisioning step into another Agents action.
type AgentsAuthority interface {
	AuthorityForRole(context.Context, string, string) (authority.SystemAuthority, error)
	AuthorityForAgent(context.Context, string, string) (authority.SystemAuthority, error)
}

type AgentsAdapter struct {
	operations  AgentsOperations
	authorities AgentsAuthority
}

var (
	_ agentprovisioning.RoleOperations  = (*AgentsAdapter)(nil)
	_ agentprovisioning.AgentOperations = (*AgentsAdapter)(nil)
)

func NewAgentsAdapter(
	operations AgentsOperations,
	authorities AgentsAuthority,
) (*AgentsAdapter, error) {
	if operations == nil || authorities == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Agents adapter: %w", agentprovisioning.ErrUnavailable)
	}
	return &AgentsAdapter{operations: operations, authorities: authorities}, nil
}

func (adapter *AgentsAdapter) EnsureRole(
	ctx context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	_, err := adapter.authorities.AuthorityForRole(
		ctx,
		command.WorkspaceKey,
		"AgentProvisioning "+command.CommandID,
	)
	if err != nil {
		return agentsAuthorityError("issue role authority", err)
	}
	err = adapter.operations.EnsureRole(ctx, command)
	return mapAgentsError("ensure role", err)
}

func (adapter *AgentsAdapter) EnsureAgent(
	ctx context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	_, err := adapter.authorities.AuthorityForAgent(
		ctx,
		command.WorkspaceKey,
		"AgentProvisioning "+command.CommandID,
	)
	if err != nil {
		return agentsAuthorityError("issue agent authority", err)
	}
	err = adapter.operations.EnsureAgent(ctx, command)
	return mapAgentsError("ensure agent", err)
}

func agentsAuthorityError(operation string, err error) error {
	return fmt.Errorf(
		"%s through Agents guarded owner command: %w",
		operation,
		errors.Join(agentprovisioning.ErrUnavailable, err),
	)
}

func mapAgentsError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s through Agents guarded owner command: %w", operation, err)
}
