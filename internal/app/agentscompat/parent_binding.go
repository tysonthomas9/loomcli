package agentscompat

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// ParentBindingCommands is the exact application process manager used after
// Execution has verified the current DriverRun owner generation.
type ParentBindingCommands interface {
	BindVerifiedDriverRunParent(
		context.Context,
		authority.ExecutionAuthority,
		agents.BindSupervisedAssignmentParentCommand,
	) (*agents.SupervisedAssignment, error)
}

type parentBindingCommands struct {
	api    agents.CompatibilityAPI
	issuer *authority.Issuer
}

func NewParentBindingCommands(
	api agents.CompatibilityAPI,
	issuer *authority.Issuer,
) (ParentBindingCommands, error) {
	if api == nil || issuer == nil {
		return nil, fmt.Errorf("compose Agents parent binding workflow: %w", agents.ErrUnavailable)
	}
	return &parentBindingCommands{api: api, issuer: issuer}, nil
}

func (commands *parentBindingCommands) BindVerifiedDriverRunParent(
	ctx context.Context,
	executionAuth authority.ExecutionAuthority,
	command agents.BindSupervisedAssignmentParentCommand,
) (*agents.SupervisedAssignment, error) {
	if commands == nil || commands.api == nil || commands.issuer == nil {
		return nil, agents.ErrUnavailable
	}
	proof := command.Proof
	if executionAuth.Action() != execution.ActionHeartbeatDriverRun ||
		executionAuth.Workspace() != command.WorkspaceKey ||
		executionAuth.Subject() != "driver-run:"+proof.DriverRunID ||
		executionAuth.ResourceKind() != authority.ExecutionResourceDriverRun ||
		executionAuth.ResourceID() != proof.DriverRunID ||
		executionAuth.NodeID() != proof.NodeID ||
		executionAuth.LeaseID() != proof.LeaseID ||
		executionAuth.FencingToken() != proof.FencingToken {
		return nil, fmt.Errorf("DriverRun authority does not match parent binding proof: %w", agents.ErrNotOwner)
	}
	principal, err := commands.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   executionAuth.Subject(),
		Class:     authority.ClassSystem,
		Workspace: command.WorkspaceKey,
		Actions:   []authority.Action{agents.ActionBindSupervisedAssignmentParent},
		ExpiresAt: executionAuth.ExpiresAt(),
	})
	if err != nil {
		return nil, err
	}
	systemAuth, err := commands.issuer.IssueSystem(
		principal,
		command.WorkspaceKey,
		agents.ActionBindSupervisedAssignmentParent,
		"verified DriverRun parent binding "+proof.DriverRunID,
	)
	if err != nil {
		return nil, err
	}
	return commands.api.BindSupervisedAssignmentParent(ctx, systemAuth, command)
}
