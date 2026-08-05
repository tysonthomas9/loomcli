package terminal

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

// terminalAgentIdentity is the narrow Agents-owned read surface needed to
// launch an interactive child. Terminal code receives no Agent mutation
// command and never reaches the FleetDB transport.
type terminalAgentIdentity interface {
	GetAgent(context.Context, string, string) (*agents.Agent, error)
}

// terminalLegacyAgentIdentity is the bounded read-only compatibility seam for
// workspaces whose legacy Agent projection has not yet been retired.
type terminalLegacyAgentIdentity interface {
	Get(context.Context, string, string) (*domain.Agent, error)
}

func firstTerminalAgentIdentity(values []terminalAgentIdentity) terminalAgentIdentity {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// loadTerminalAgent resolves the canonical Phase 5 Agent identity first. The
// legacy Agent row is a bounded read-only compatibility projection for
// workspaces that have not yet been backfilled; all new identity writes go
// through Agents.
func loadTerminalAgent(
	ctx context.Context,
	legacy terminalLegacyAgentIdentity,
	workspace,
	agentName string,
	identities ...terminalAgentIdentity,
) (*domain.Agent, error) {
	if identity := firstTerminalAgentIdentity(identities); identity != nil {
		record, err := identity.GetAgent(ctx, workspace, agentName)
		switch {
		case err == nil && record != nil:
			return terminalAgentFromCanonical(record), nil
		case err == nil:
			return nil, fmt.Errorf(
				"canonical Agents returned an empty identity: %w",
				agents.ErrInvalidPersistedState,
			)
		case !errors.Is(err, agents.ErrNotFound):
			return nil, fmt.Errorf("failed to load canonical agent: %w", err)
		}
	}
	if legacy == nil {
		return nil, fmt.Errorf(
			"legacy agent identity compatibility is unavailable: %w",
			agents.ErrUnavailable,
		)
	}
	agent, err := legacy.Get(ctx, workspace, agentName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("agent not found: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to load agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent not found: %w", domain.ErrNotFound)
	}
	return agent, nil
}

func terminalAgentFromCanonical(record *agents.Agent) *domain.Agent {
	if record == nil {
		return nil
	}
	return &domain.Agent{
		WorkspaceKey:   record.WorkspaceKey,
		Name:           record.AgentID,
		RoleName:       record.Behavior.RoleName,
		MaxConcurrency: record.MaxInstances,
		BudgetPolicy:   record.BudgetPolicy,
		DesiredState:   domain.AgentDesiredState(record.DesiredState),
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}
}
