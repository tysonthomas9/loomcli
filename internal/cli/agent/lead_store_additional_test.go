package agent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestCurrentLeadAssignmentPromptUsesOpenStore(t *testing.T) {
	requireAgentFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvFleetDBActor, "agent-lead-test")
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(envAgentName, "nova")

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := handle.Store.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "nova",
		RoleName:     "lead",
		Parent:       "EPIC-1",
	}); err != nil {
		t.Fatalf("create lead agent: %v", err)
	}
	if _, err := handle.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "orch-1",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create orchestration session: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	prompt := currentLeadAssignmentPrompt(ctx)
	if !strings.Contains(prompt, "assigned_epic: EPIC-1") || !strings.Contains(prompt, "orchestration_session: orch-1") {
		t.Fatalf("assignment prompt = %q", prompt)
	}
	handle, err = bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = handle.Close() }()
	session, err := handle.Store.AgentSessions().Get(ctx, "WS", "orch-1")
	if err != nil {
		t.Fatalf("get orchestration session: %v", err)
	}
	if session.Metadata["lead_assignment_delivered_epic"] != "EPIC-1" {
		t.Fatalf("session metadata = %#v", session.Metadata)
	}
}

func TestRegisterLeadOrchestratorSessionAgainstOpenStore(t *testing.T) {
	requireAgentFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvFleetDBActor, "agent-lead-register-test")
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(envOrchestratorSessionID, "lead-session-1")
	t.Setenv(envAgentName, "nova")

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	reg := registerLeadOrchestratorSession(ctx, t.TempDir())
	if reg.SessionID != "lead-session-1" || reg.AgentID != "nova" || reg.Workspace != "WS" || reg.Store() == nil {
		t.Fatalf("registration = %+v store=%v", reg, reg.Store())
	}
	reg.Finalize()

	handle, err = bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = handle.Close() }()
	session, err := handle.Store.AgentSessions().Get(ctx, "WS", "lead-session-1")
	if err != nil {
		t.Fatalf("get lead session: %v", err)
	}
	if session.Status != domain.AgentSessionCompleted || session.FinishedAt == nil {
		t.Fatalf("finalized session = %+v", session)
	}
}

func requireAgentFleetDB(t *testing.T) {
	t.Helper()
	if os.Getenv("FLEET_DB_BIN") != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}
