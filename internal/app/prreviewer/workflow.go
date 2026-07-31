// Package prreviewer owns the narrow application workflow that provisions the
// durable identity used by the pull-request review UI. It composes the Agents
// capability without exposing its system issuer to the HTTP adapter.
package prreviewer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	RoleName        = "pr-reviewer"
	RolePromptFile  = "builtin:pr-review-checkout"
	RoleDescription = "PR review checkout terminal agent"

	// AuthoritySubject is the fixed composition-owned system principal used
	// only for this application workflow's two Agents provisioning commands.
	AuthoritySubject = "serve-pr-reviewer-provisioning"
)

var ErrUnavailable = errors.New("pr reviewer provisioning unavailable")

// Commands is the only PR-reviewer provisioning surface exposed to web
// composition.
type Commands interface {
	EnsureReviewer(context.Context, EnsureCommand) (*EnsureResult, error)
}

type EnsureCommand struct {
	WorkspaceKey string
	AgentID      string
}

// EnsureResult records the independently durable Role step. If the Agent step
// fails, RoleCommitted remains true: the exact shared reviewer Role is safe to
// retain and a later replay converges the same definition.
type EnsureResult struct {
	RoleCommitted bool
	Agent         *agents.Agent
}

type Capability interface {
	EnsureManagedRole(
		context.Context,
		authority.SystemAuthority,
		agents.EnsureRoleCommand,
	) (*agents.Role, error)
	EnsureManagedAgent(
		context.Context,
		authority.SystemAuthority,
		agents.EnsureAgentCommand,
	) (*agents.Agent, error)
}

// AuthorityProvider exposes fixed-action methods. The workflow cannot select
// a different Agents action or recover the composition-owned issuer.
type AuthorityProvider interface {
	AuthorityForReviewerRole(context.Context, string, string) (authority.SystemAuthority, error)
	AuthorityForReviewerAgent(context.Context, string, string) (authority.SystemAuthority, error)
}

type Workflow struct {
	agents      Capability
	authorities AuthorityProvider
}

var _ Commands = (*Workflow)(nil)

func New(capability Capability, authorities AuthorityProvider) (*Workflow, error) {
	if capability == nil || authorities == nil {
		return nil, fmt.Errorf("compose PR reviewer provisioning: %w", ErrUnavailable)
	}
	return &Workflow{agents: capability, authorities: authorities}, nil
}

//nolint:funlen // Reviewer convergence keeps authority, idempotency, and exact-result validation in one transaction flow.
func (workflow *Workflow) EnsureReviewer(
	ctx context.Context,
	command EnsureCommand,
) (*EnsureResult, error) {
	if workflow == nil || workflow.agents == nil || workflow.authorities == nil {
		return nil, ErrUnavailable
	}
	if ctx == nil {
		return nil, fmt.Errorf("pr reviewer context is required: %w", agents.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspace, agentID, err := normalize(command)
	if err != nil {
		return nil, err
	}

	roleReason := "ensure shared PR reviewer role for " + agentID
	roleAuth, err := workflow.authorities.AuthorityForReviewerRole(ctx, workspace, roleReason)
	if err != nil {
		return nil, fmt.Errorf("issue pr reviewer Role authority: %w", err)
	}
	if _, err := workflow.agents.EnsureManagedRole(ctx, roleAuth, agents.EnsureRoleCommand{
		RequestID:    "pr-reviewer-role/" + workspace,
		WorkspaceKey: workspace,
		Role: agents.RoleDefinition{
			Name:        RoleName,
			Kind:        "interactive",
			Description: RoleDescription,
			PromptFile:  RolePromptFile,
		},
	}); err != nil {
		return nil, fmt.Errorf("ensure pr reviewer Role through Agents: %w", err)
	}

	result := &EnsureResult{RoleCommitted: true}
	agentReason := "ensure PR reviewer Agent " + agentID
	agentAuth, err := workflow.authorities.AuthorityForReviewerAgent(ctx, workspace, agentReason)
	if err != nil {
		return result, fmt.Errorf("issue pr reviewer Agent authority: %w", err)
	}
	result.Agent, err = workflow.agents.EnsureManagedAgent(ctx, agentAuth, agents.EnsureAgentCommand{
		RequestID: "pr-reviewer-agent/" + agentID,
		CreateAgentCommand: agents.CreateAgentCommand{
			WorkspaceKey: workspace,
			AgentID:      agentID,
			Name:         agentID,
			Kind:         agents.AgentKindSupport,
			Behavior:     agents.BehaviorReference{RoleName: RoleName},
			DesiredState: agents.DesiredRunning,
			MaxInstances: 1,
		},
	})
	if err != nil {
		return result, fmt.Errorf("ensure pr reviewer Agent through Agents: %w", err)
	}
	if result.Agent == nil {
		return result, fmt.Errorf("agents returned no pr reviewer identity: %w", agents.ErrInvalidPersistedState)
	}
	return result, nil
}

func normalize(command EnsureCommand) (string, string, error) {
	workspace := strings.TrimSpace(command.WorkspaceKey)
	agentID := strings.TrimSpace(command.AgentID)
	if workspace == "" || workspace != command.WorkspaceKey {
		return "", "", fmt.Errorf("canonical workspace is required: %w", agents.ErrInvalid)
	}
	if agentID == "" || agentID != command.AgentID || strings.Contains(agentID, ":") {
		return "", "", fmt.Errorf("canonical PR reviewer Agent ID is required: %w", agents.ErrInvalid)
	}
	return workspace, agentID, nil
}
