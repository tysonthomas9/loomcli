// Package agents adapts the Agents public capability to AgentProvisioning's
// role and identity steps. It accepts no caller-supplied authority; production
// composition injects an action-specific issuer for the registered recovery
// component.
package agents

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Capability interface {
	EnsureRole(context.Context, agentprovisioning.EnsureRoleCommand) error
	EnsureAgent(context.Context, agentprovisioning.EnsureAgentCommand) error
}

// AuthorityProvider exposes two fixed-action methods so this adapter cannot
// turn an AgentProvisioning step into another Agents command.
type AuthorityProvider interface {
	AuthorityForRole(context.Context, string, string) (authority.SystemAuthority, error)
	AuthorityForAgent(context.Context, string, string) (authority.SystemAuthority, error)
}

type Adapter struct {
	capability  Capability
	authorities AuthorityProvider
}

var (
	_ agentprovisioning.RoleOperations  = (*Adapter)(nil)
	_ agentprovisioning.AgentOperations = (*Adapter)(nil)
)

func New(capability Capability, authorities AuthorityProvider) (*Adapter, error) {
	if capability == nil || authorities == nil {
		return nil, fmt.Errorf("compose AgentProvisioning Agents adapter: %w", agentprovisioning.ErrUnavailable)
	}
	return &Adapter{capability: capability, authorities: authorities}, nil
}

func (adapter *Adapter) EnsureRole(
	ctx context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	_, err := adapter.authorities.AuthorityForRole(
		ctx,
		command.WorkspaceKey,
		"AgentProvisioning "+command.CommandID,
	)
	if err != nil {
		return authorityError("issue role authority", err)
	}
	err = adapter.capability.EnsureRole(ctx, command)
	return mapError("ensure role", err)
}

func (adapter *Adapter) EnsureAgent(
	ctx context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	_, err := adapter.authorities.AuthorityForAgent(
		ctx,
		command.WorkspaceKey,
		"AgentProvisioning "+command.CommandID,
	)
	if err != nil {
		return authorityError("issue agent authority", err)
	}
	err = adapter.capability.EnsureAgent(ctx, command)
	return mapError("ensure agent", err)
}

func authorityError(operation string, err error) error {
	return fmt.Errorf(
		"%s through Agents guarded owner command: %w",
		operation,
		errors.Join(agentprovisioning.ErrUnavailable, err),
	)
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s through Agents guarded owner command: %w", operation, err)
}
