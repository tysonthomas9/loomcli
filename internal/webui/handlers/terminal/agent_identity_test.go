package terminal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type terminalAgentIdentityStub struct {
	record *agents.Agent
	err    error
	calls  int
}

func (stub *terminalAgentIdentityStub) GetAgent(
	context.Context,
	string,
	string,
) (*agents.Agent, error) {
	stub.calls++
	return stub.record, stub.err
}

func TestLoadTerminalAgentPrefersCanonicalAgentsProjection(t *testing.T) {
	st := memstore.New()
	createTerminalIdentityWorkspace(t, st)
	if _, err := st.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "reviewer",
		RoleName:     "legacy-role",
	}); err != nil {
		t.Fatalf("create legacy collision: %v", err)
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	identity := &terminalAgentIdentityStub{record: &agents.Agent{
		WorkspaceKey: "WS",
		AgentID:      "reviewer",
		Name:         "Reviewer",
		Kind:         agents.AgentKindSupport,
		Behavior:     agents.BehaviorReference{RoleName: "pr-reviewer"},
		DesiredState: agents.DesiredRunning,
		MaxInstances: 1,
		BudgetPolicy: "bounded",
		CreatedAt:    now,
		UpdatedAt:    now,
	}}

	got, err := loadTerminalAgent(t.Context(), st.Agents(), "WS", "reviewer", identity)
	if err != nil {
		t.Fatalf("loadTerminalAgent: %v", err)
	}
	if got.Name != "reviewer" ||
		got.RoleName != "pr-reviewer" ||
		got.DesiredState != domain.AgentDesiredRunning ||
		got.MaxConcurrency != 1 ||
		got.BudgetPolicy != "bounded" {
		t.Fatalf("canonical projection = %+v", got)
	}
}

func TestLoadTerminalAgentUsesBoundedLegacyReadOnlyCompatibility(t *testing.T) {
	st := memstore.New()
	createTerminalIdentityWorkspace(t, st)
	if _, err := st.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "legacy",
		RoleName:     "lead",
	}); err != nil {
		t.Fatalf("create legacy agent: %v", err)
	}
	identity := &terminalAgentIdentityStub{err: agents.ErrNotFound}

	got, err := loadTerminalAgent(t.Context(), st.Agents(), "WS", "legacy", identity)
	if err != nil {
		t.Fatalf("loadTerminalAgent: %v", err)
	}
	if got.Name != "legacy" || got.RoleName != "lead" || identity.calls != 1 {
		t.Fatalf("legacy projection = %+v, calls = %d", got, identity.calls)
	}
}

func TestLoadTerminalAgentDoesNotBypassCanonicalFailure(t *testing.T) {
	st := memstore.New()
	createTerminalIdentityWorkspace(t, st)
	if _, err := st.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "legacy",
		RoleName:     "lead",
	}); err != nil {
		t.Fatalf("create legacy agent: %v", err)
	}
	identity := &terminalAgentIdentityStub{err: agents.ErrUnavailable}

	_, err := loadTerminalAgent(t.Context(), st.Agents(), "WS", "legacy", identity)
	if err == nil || !errors.Is(err, agents.ErrUnavailable) {
		t.Fatalf("loadTerminalAgent error = %v, want canonical unavailable", err)
	}
}

func createTerminalIdentityWorkspace(t *testing.T, st *memstore.Store) {
	t.Helper()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key:  "WS",
		Name: "Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
}
