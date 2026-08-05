package agentcomposition

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentBindingStateSource struct {
	bindings store.TriggerBindingStore
}

var _ agents.DesiredStateBindingSource = (*agentBindingStateSource)(nil)

func newAgentBindingStateSource(bindings store.TriggerBindingStore) agents.DesiredStateBindingSource {
	if bindings == nil {
		return nil
	}
	return &agentBindingStateSource{bindings: bindings}
}

func (source *agentBindingStateSource) ListAgentBindingStates(
	ctx context.Context,
	workspace,
	agentID string,
) ([]bool, error) {
	if source == nil || source.bindings == nil {
		return nil, agents.ErrUnavailable
	}
	bindings, err := source.bindings.List(ctx, workspace, store.TriggerBindingFilter{
		TargetAgentServiceID: agentID,
	})
	if err != nil {
		return nil, fmt.Errorf("list Automation bindings for Agent %q: %w", agentID, err)
	}
	states := make([]bool, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.WorkspaceKey != workspace || binding.TargetAgentServiceID != agentID {
			return nil, agents.ErrInvalidPersistedState
		}
		states = append(states, binding.Enabled)
	}
	return states, nil
}
