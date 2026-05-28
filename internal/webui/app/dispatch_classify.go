package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func classifyFromStore(st store.Store) func(terminal.SessionKey) terminal.AgentKind {
	if st == nil {
		return func(terminal.SessionKey) terminal.AgentKind { return terminal.AgentEphemeral }
	}
	return func(key terminal.SessionKey) terminal.AgentKind {
		if key.Workspace == "" {
			return terminal.AgentEphemeral
		}
		if key.Name == "" {
			if anyServiceAgent(context.Background(), st, key.Workspace) {
				return terminal.AgentPersistent
			}
			return terminal.AgentEphemeral
		}
		agent, err := st.Agents().Get(context.Background(), key.Workspace, key.Name)
		if err != nil || agent == nil || agent.Mode != domain.AgentModeService {
			return terminal.AgentEphemeral
		}
		return terminal.AgentPersistent
	}
}

func anyServiceAgent(ctx context.Context, st store.Store, workspace string) bool {
	agents, err := st.Agents().List(ctx, workspace)
	if err != nil {
		return false
	}
	for _, agent := range agents {
		if agent != nil && agent.Mode == domain.AgentModeService {
			return true
		}
	}
	return false
}
