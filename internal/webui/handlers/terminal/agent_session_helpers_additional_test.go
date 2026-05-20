package terminal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

func TestAgentTerminalPureHelperBranches(t *testing.T) {
	if agentTerminalLaunchAllowed(nil) {
		t.Fatal("nil agent should not allow launch")
	}
	if isDaemonOwnedEphemeralWorker(nil) {
		t.Fatal("nil agent should not be daemon-owned")
	}
	if !agentTerminalLaunchAllowed(&domain.Agent{RoleName: " orchestrator ", State: domain.AgentStateStopped, DesiredState: domain.AgentDesiredStopped}) {
		t.Fatal("orchestrator role should always be launchable")
	}
	if agentTerminalLaunchAllowed(&domain.Agent{RoleName: "task", State: domain.AgentStateStopped}) {
		t.Fatal("stopped task should not be launchable")
	}
	if isDaemonOwnedEphemeralWorker(&domain.Agent{RoleName: "lead", Mode: domain.AgentModeEphemeral}) {
		t.Fatal("lead role should not be treated as daemon-owned ephemeral worker")
	}

	old := time.Now().UTC().Add(-time.Hour)
	newer := time.Now().UTC()
	tabs := []tabmeta.TabMetadata{
		{SessionName: "ignore", Kind: "other", AgentID: "nova", UpdatedAt: newer},
		{SessionName: "old", Kind: terminalKindAgent, AgentID: "nova", UpdatedAt: old},
		{SessionName: "new", Kind: terminalKindAgent, AgentID: "nova", UpdatedAt: newer},
	}
	if got := selectAgentTerminalTab(tabs, "nova"); got == nil || got.SessionName != "new" {
		t.Fatalf("select newest tab = %+v", got)
	}
	tabs[0] = tabmeta.TabMetadata{SessionName: "live", Kind: terminalKindAgent, AgentID: "nova", PTYAlive: true, UpdatedAt: old}
	if got := selectAgentTerminalTab(tabs, "nova"); got == nil || got.SessionName != "live" {
		t.Fatalf("select live tab = %+v", got)
	}

	session, label, sort := newAgentTerminalTabPlacement(tabs, &tabmeta.TabMetadata{Label: "kept", SortOrder: 7}, "nova")
	if !strings.HasPrefix(session, "term_") || label != "kept" || sort != 7 {
		t.Fatalf("placement session=%q label=%q sort=%d", session, label, sort)
	}

	base := agentLaunchBaseArgs("WS", "codex")
	if !containsArg(base, "--workspace") || !containsArg(base, "WS") || !containsArg(base, "--backend") || !containsArg(base, "codex") {
		t.Fatalf("agentLaunchBaseArgs = %#v", base)
	}
	if got := builtInAgentLaunchArgs(roleTask, &domain.Agent{Name: "worker", Parent: "EPIC-1"}); !containsArg(got, "--parent") || !containsArg(got, "EPIC-1") {
		t.Fatalf("builtInAgentLaunchArgs = %#v", got)
	}
	custom, err := customAgentLaunchArgs(&domain.Agent{Name: "reviewer", RoleName: "reviewer", Parent: "EPIC-1"}, &domain.Role{PromptFile: "/tmp/prompt.md", TaskFilter: "any"})
	if err != nil {
		t.Fatalf("customAgentLaunchArgs: %v", err)
	}
	for _, want := range []string{"agent", "reviewer", "--prompt", "/tmp/prompt.md", "--task-filter", "any", "--parent", "EPIC-1"} {
		if !containsArg(custom, want) {
			t.Fatalf("custom args %#v missing %q", custom, want)
		}
	}
	env := agentLaunchEnv("WS", "term_1", "codex", "orch-1", &domain.Agent{Name: "nova", RoleName: "lead"})
	if env["LOOM_BACKEND"] != "codex" || env["LOOM_ORCHESTRATOR_SESSION_ID"] != "orch-1" || env["LOOM_AGENT_TERMINAL_ID"] != "term_1" {
		t.Fatalf("agentLaunchEnv = %#v", env)
	}
	if !isLeadRole(" Lead ") || isLeadRole("task") {
		t.Fatal("isLeadRole classification mismatch")
	}
}

func TestAgentLaunchBackendRoleAndExistingOrchestratorBranches(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if got := agentLaunchBackend(ctx, st, "WS", &domain.Agent{Name: "worker", Backend: "agent-backend"}, &domain.Role{Backend: "role-backend"}); got != "agent-backend" {
		t.Fatalf("agent backend = %q", got)
	}
	if got := agentLaunchBackend(ctx, st, "WS", &domain.Agent{Name: "worker"}, &domain.Role{Backend: "role-backend"}); got != "role-backend" {
		t.Fatalf("role backend = %q", got)
	}

	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "orch-existing",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create existing orchestration session: %v", err)
	}
	agent, orchID, err := ensureLeadOrchestratorLink(ctx, st, "WS", "term_1", &domain.Agent{Name: "nova", RoleName: "lead"})
	if err != nil {
		t.Fatalf("ensureLeadOrchestratorLink: %v", err)
	}
	if agent.Name != "nova" || orchID != "orch-existing" {
		t.Fatalf("agent=%+v orchID=%q", agent, orchID)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
