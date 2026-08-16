package epicrunner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestLoadLeadAssignmentContextReturnsAssignedLead(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	createAssignmentRole(t, ctx, st, "lead")

	profile, err := st.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{
		WorkspaceKey: "WS",
		ProfileID:    "nova-profile",
		Role:         "lead",
		ParentEpic:   "EPIC-1",
	})
	if err != nil {
		t.Fatalf("create lead profile: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "nova",
		Kind:         agents.AgentKindLead,
		RoleName:     "lead",
		ProfileName:  "nova-profile",
	}); err != nil {
		t.Fatalf("create lead identity: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, err := LoadLeadAssignmentContext(ctx, NewStoreLeadAssignmentSource(st), "WS", "nova")
	if err != nil {
		t.Fatalf("LoadLeadAssignmentContext: %v", err)
	}
	if got == nil || got.EpicID != "EPIC-1" || got.OrchestratorSessionID != "lead-session" {
		t.Fatalf("context = %+v, want EPIC-1 with lead session", got)
	}
	if got.AssignmentVersion != profile.UpdatedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("assignment version = %q, want profile revision %q", got.AssignmentVersion, profile.UpdatedAt.UTC().Format(time.RFC3339Nano))
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
	createAssignmentRole(t, ctx, st, "lead")
	createAssignmentRole(t, ctx, st, "task")

	for _, in := range []execution.WorkerProfileCreate{
		{WorkspaceKey: "WS", ProfileID: "atlas-profile", Role: "lead"},
		{WorkspaceKey: "WS", ProfileID: "worker-profile", Role: "task", ParentEpic: "EPIC-1"},
	} {
		if _, err := st.WorkerProfiles().Create(ctx, in); err != nil {
			t.Fatalf("create profile %s: %v", in.ProfileID, err)
		}
	}
	for _, in := range []agents.AgentServiceCreate{
		{WorkspaceKey: "WS", ServiceID: "atlas", Kind: agents.AgentKindLead, RoleName: "lead", ProfileName: "atlas-profile"},
		{WorkspaceKey: "WS", ServiceID: "worker", Kind: agents.AgentKindSupport, RoleName: "task", ProfileName: "worker-profile"},
	} {
		if _, err := st.AgentServices().Create(ctx, in); err != nil {
			t.Fatalf("create identity %s: %v", in.ServiceID, err)
		}
	}

	for _, name := range []string{"atlas", "worker", "ghost"} {
		got, err := LoadLeadAssignmentContext(ctx, NewStoreLeadAssignmentSource(st), "WS", name)
		if err != nil {
			t.Fatalf("LoadLeadAssignmentContext(%s): %v", name, err)
		}
		if got != nil {
			t.Fatalf("LoadLeadAssignmentContext(%s) = %+v, want nil", name, got)
		}
	}
}

func TestLoadLeadAssignmentContextRejectsRoleMismatch(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignmentRole(t, ctx, st, "lead")
	createAssignmentRole(t, ctx, st, "task")
	if _, err := st.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{
		WorkspaceKey: "WS",
		ProfileID:    "nova-profile",
		Role:         "task",
		ParentEpic:   "EPIC-1",
	}); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, agents.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "nova",
		Kind:         agents.AgentKindLead,
		RoleName:     "lead",
		ProfileName:  "nova-profile",
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	got, err := LoadLeadAssignmentContext(ctx, NewStoreLeadAssignmentSource(st), "WS", "nova")
	if err != nil {
		t.Fatalf("LoadLeadAssignmentContext: %v", err)
	}
	if got != nil {
		t.Fatalf("context = %+v, want nil for role mismatch", got)
	}
}

func createAssignmentRole(t *testing.T, ctx context.Context, st *memstore.Store, name string) {
	t.Helper()
	if _, err := st.Roles().Create(ctx, agents.RoleRecordCreate{WorkspaceKey: "WS", Name: name}); err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
}
