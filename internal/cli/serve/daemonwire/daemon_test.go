package daemonwire

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
)

func TestBuildStoreBackedDaemonConfigFnUsesFleetDBStore(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Setenv(bootstrap.EnvWorkspace, "WS1")

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxPriority := 2
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS1",
		Name:         "task",
		TaskFilter:   "ready",
		Backend:      "codex",
		MaxPriority:  &maxPriority,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	maxAgents := 7
	profile, err := st.Daemon().Get(ctx, "WS1")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	profile.PIDFile = ".loom/custom.pid"
	profile.MaxAgents = &maxAgents
	if _, err := st.Daemon().Upsert(ctx, profile); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:     "WS1",
		Name:             "nova",
		RoleName:         "task",
		Backend:          "codex",
		FallbackBackends: []string{"claude"},
		Repos:            []string{"api"},
		RepoGroups:       []string{"backend"},
		CrossRepo:        true,
		Parent:           "epic-1",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	fn := BuildStoreBackedDaemonConfigFn(st)
	if fn == nil {
		t.Fatal("BuildStoreBackedDaemonConfigFn returned nil")
	}
	raw, err := fn()
	if err != nil {
		t.Fatalf("daemon config fn: %v", err)
	}
	var got config.DaemonConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if got.Backend != "fleetdb" || got.Daemon.IssueBackend != "fleetdb" {
		t.Fatalf("backend = %q daemon.issue_backend = %q, want fleetdb", got.Backend, got.Daemon.IssueBackend)
	}
	if got.Daemon.PIDFile != ".loom/custom.pid" {
		t.Fatalf("pid_file = %q", got.Daemon.PIDFile)
	}
	if got.Daemon.MaxAgents == nil || *got.Daemon.MaxAgents != 7 {
		t.Fatalf("max_agents = %v, want 7", got.Daemon.MaxAgents)
	}
	if role, ok := got.Roles["task"]; !ok || role.TaskFilter != "ready" || role.Backend != "codex" {
		t.Fatalf("role task = %+v, ok=%v", role, ok)
	}
	if len(got.Agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(got.Agents))
	}
	agent := got.Agents[0]
	if agent.Worktree != "nova" || agent.Role != "task" || !agent.CrossRepo || agent.Parent != "epic-1" {
		t.Fatalf("agent = %+v", agent)
	}
	if len(agent.Repos) != 1 || agent.Repos[0] != "api" {
		t.Fatalf("agent repos = %v, want [api]", agent.Repos)
	}
	if len(agent.RepoGroups) != 1 || agent.RepoGroups[0] != "backend" {
		t.Fatalf("agent repo_groups = %v, want [backend]", agent.RepoGroups)
	}
}

// TestSendControlRequest_MissingSocketIsSupervisorUnavailable pins the wiring
// the webui's claim-hold GET depends on: a dial failure must be recognizable
// with errors.Is, not by prose. Without this, the handler's 200 answer for a
// supervisor-less host silently reverts to a 503.
func TestSendControlRequest_MissingSocketIsSupervisorUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "daemon.sock")

	if _, err := sendControlRequest(missing, "agent_list", "", false, time.Second); !errors.Is(err, agentcontrol.ErrSupervisorUnavailable) {
		t.Errorf("sendControlRequest err = %v, want ErrSupervisorUnavailable", err)
	}
	if _, err := sendControlRequestArgs(missing, "claims_hold_get", "", nil, time.Second); !errors.Is(err, agentcontrol.ErrSupervisorUnavailable) {
		t.Errorf("sendControlRequestArgs err = %v, want ErrSupervisorUnavailable", err)
	}
}
