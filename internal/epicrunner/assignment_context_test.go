package epicrunner

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestLoadLeadAssignmentContextReturnsAssignedLead(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "nova",
		RoleName:     "lead",
		Parent:       "EPIC-1",
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, err := LoadLeadAssignmentContext(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("LoadLeadAssignmentContext: %v", err)
	}
	if got == nil || got.EpicID != "EPIC-1" || got.OrchestratorSessionID != "lead-session" {
		t.Fatalf("context = %+v, want EPIC-1 with lead session", got)
	}
	text := FormatLeadAssignmentContext(got)
	if !strings.Contains(text, "assigned_epic: EPIC-1") || !strings.Contains(text, "authoritative backend state") {
		t.Fatalf("formatted context = %q", text)
	}
}

func TestLoadLeadAssignmentContextSkipsUnassignedAndNonLeadAgents(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	for _, in := range []store.AgentCreate{
		{WorkspaceKey: "WS", Name: "atlas", RoleName: "lead"},
		{WorkspaceKey: "WS", Name: "worker", RoleName: "task", Parent: "EPIC-1"},
	} {
		if _, err := st.Agents().Create(ctx, in); err != nil {
			t.Fatalf("create agent %s: %v", in.Name, err)
		}
	}

	for _, name := range []string{"atlas", "worker", "ghost"} {
		got, err := LoadLeadAssignmentContext(ctx, st, "WS", name)
		if err != nil {
			t.Fatalf("LoadLeadAssignmentContext(%s): %v", name, err)
		}
		if got != nil {
			t.Fatalf("LoadLeadAssignmentContext(%s) = %+v, want nil", name, got)
		}
	}
}
