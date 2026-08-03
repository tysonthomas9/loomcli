package terminal

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

// terminalAgentIdentity is the narrow Agents-owned read surface needed to
// launch an interactive child. Terminal code receives no Agent mutation
// command and never reaches the FleetDB transport.
type terminalAgentIdentity interface {
	GetAgent(context.Context, string, string) (*agents.Agent, error)
}

func firstTerminalAgentIdentity(values []terminalAgentIdentity) terminalAgentIdentity {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// loadTerminalAgent resolves the canonical Agent identity. Phase 6 deliberately
// fails closed instead of falling back to the retired supervised-assignment
// projection.
func loadTerminalAgent(
	ctx context.Context,
	workspace,
	agentName string,
	identities ...terminalAgentIdentity,
) (*agents.RuntimeIdentity, error) {
	identity := firstTerminalAgentIdentity(identities)
	if identity == nil {
		return nil, fmt.Errorf("canonical agent identity is unavailable: %w", agents.ErrUnavailable)
	}
	record, err := identity.GetAgent(ctx, workspace, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to load canonical agent: %w", err)
	}
	if record == nil {
		return nil, fmt.Errorf(
			"canonical Agents returned an empty identity: %w",
			agents.ErrInvalidPersistedState,
		)
	}
	runtime, err := agents.ResolveRuntimeIdentity(record)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical agent runtime identity: %w", err)
	}
	return runtime, nil
}
