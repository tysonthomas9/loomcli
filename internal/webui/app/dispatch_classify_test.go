package app

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestClassifyFromStoreDefaultsEphemeral(t *testing.T) {
	classify := classifyFromStore(nil)

	if got := classify(terminal.SessionKey{Workspace: "WS", Name: "agent"}); got != terminal.AgentEphemeral {
		t.Fatalf("classify nil store = %v, want ephemeral", got)
	}
}

func TestClassifyFromStoreRoutesServiceAgentPersistent(t *testing.T) {
	st := memstore.New()
	createAgent(t, st, "WS", "coder", domain.AgentModeService)
	createAgent(t, st, "WS", "planner", domain.AgentModeEphemeral)

	classify := classifyFromStore(st)

	if got := classify(terminal.SessionKey{Workspace: "WS", Name: "coder"}); got != terminal.AgentPersistent {
		t.Fatalf("service agent classify = %v, want persistent", got)
	}
	if got := classify(terminal.SessionKey{Workspace: "WS", Name: "planner"}); got != terminal.AgentEphemeral {
		t.Fatalf("ephemeral agent classify = %v, want ephemeral", got)
	}
	if got := classify(terminal.SessionKey{Workspace: "WS", Name: "missing"}); got != terminal.AgentEphemeral {
		t.Fatalf("missing agent classify = %v, want ephemeral", got)
	}
}

func TestClassifyFromStoreWorkspaceSentinelUsesAnyServiceAgent(t *testing.T) {
	st := memstore.New()
	createAgent(t, st, "WS", "coder", domain.AgentModeService)
	createAgent(t, st, "OTHER", "planner", domain.AgentModeEphemeral)

	classify := classifyFromStore(st)

	if got := classify(terminal.SessionKey{Workspace: "WS"}); got != terminal.AgentPersistent {
		t.Fatalf("workspace sentinel with service agent = %v, want persistent", got)
	}
	if got := classify(terminal.SessionKey{Workspace: "OTHER"}); got != terminal.AgentEphemeral {
		t.Fatalf("workspace sentinel without service agent = %v, want ephemeral", got)
	}
}

func createAgent(t *testing.T, st store.Store, workspace, name string, mode domain.AgentMode) {
	t.Helper()
	if _, err := st.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         name,
		RoleName:     "task",
		Mode:         mode,
	}); err != nil {
		t.Fatalf("create agent %s/%s: %v", workspace, name, err)
	}
}
