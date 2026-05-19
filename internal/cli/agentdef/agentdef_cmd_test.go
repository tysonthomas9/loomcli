package agentdef

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAgentdefCommandsAgainstLocalStore(t *testing.T) {
	handle := setupAgentdefFleetWorkspace(t)
	defer handle.Close()

	resetAgentFlagGlobals(t)
	agentListJSON = false
	if out := captureAgentdefStdout(t, func() {
		if err := runAgentList(nil, nil); err != nil {
			t.Fatalf("runAgentList empty: %v", err)
		}
	}); !strings.Contains(out, "No agents in workspace WS") {
		t.Fatalf("empty list output = %q", out)
	}

	agentAddRole = "task"
	agentAddAuto = true
	agentAddBackend = "codex"
	agentAddRepos = []string{"api"}
	agentAddRepoGroups = []string{"backend"}
	agentAddCrossRepo = true
	agentAddParent = "EPIC-1"
	agentAddMode = string(domain.AgentModeService)
	agentAddTaskFilter = "kind:task"
	agentAddMaxConc = 2
	agentAddBudget = "strict"
	agentAddTask = "TASK-1"
	agentAddOrchestrator = "orch-1"
	if out := captureAgentdefStdout(t, func() {
		if err := runAgentAdd(nil, []string{"worker"}); err != nil {
			t.Fatalf("runAgentAdd: %v", err)
		}
	}); !strings.Contains(out, "Created agent WS/worker") || !strings.Contains(out, "pinned to task: TASK-1") {
		t.Fatalf("add output = %q", out)
	}

	agentListJSON = false
	if out := captureAgentdefStdout(t, func() {
		if err := runAgentList(nil, nil); err != nil {
			t.Fatalf("runAgentList: %v", err)
		}
	}); !strings.Contains(out, "worker") || !strings.Contains(out, "mode=service") || !strings.Contains(out, "auto") {
		t.Fatalf("list output = %q", out)
	}

	agentShowJSON = false
	if out := captureAgentdefStdout(t, func() {
		if err := runAgentShow(nil, []string{"worker"}); err != nil {
			t.Fatalf("runAgentShow: %v", err)
		}
	}); !strings.Contains(out, "Workspace:    WS") ||
		!strings.Contains(out, "Backend:      codex") ||
		!strings.Contains(out, "Repo groups:  backend") ||
		!strings.Contains(out, "Max conc:     2") {
		t.Fatalf("show output = %q", out)
	}

	agentShowJSON = true
	if out := captureAgentdefStdout(t, func() {
		if err := runAgentShow(nil, []string{"worker"}); err != nil {
			t.Fatalf("runAgentShow json: %v", err)
		}
	}); !strings.Contains(out, `"name": "worker"`) {
		t.Fatalf("show json output = %q", out)
	}

	if out := captureAgentdefStdout(t, func() {
		if err := runAgentStart(nil, []string{"worker"}); err != nil {
			t.Fatalf("runAgentStart: %v", err)
		}
	}); !strings.Contains(out, "Requested agent WS/worker start") {
		t.Fatalf("start output = %q", out)
	}

	agentStopForce = true
	if out := captureAgentdefStdout(t, func() {
		if err := runAgentStop(nil, []string{"worker"}); err != nil {
			t.Fatalf("runAgentStop: %v", err)
		}
	}); !strings.Contains(out, "Requested agent WS/worker stop") {
		t.Fatalf("stop output = %q", out)
	}

	if out := captureAgentdefStdout(t, func() {
		if err := runAgentRemove(nil, []string{"worker"}); err != nil {
			t.Fatalf("runAgentRemove: %v", err)
		}
	}); !strings.Contains(out, "Removed agent WS/worker") {
		t.Fatalf("remove output = %q", out)
	}
}

func TestAgentCreateFromFlagsAndPinnedCommand(t *testing.T) {
	resetAgentFlagGlobals(t)
	agentAddRole = "task"
	agentAddAuto = true
	agentAddBackend = "codex"
	agentAddRepos = []string{"api"}
	agentAddRepoGroups = []string{"backend"}
	agentAddCrossRepo = true
	agentAddParent = "EPIC-1"
	agentAddMode = string(domain.AgentModeService)
	agentAddTaskFilter = "kind:task"
	agentAddMaxConc = 3
	agentAddBudget = "strict"
	agentAddTask = "TASK-1"

	in := agentCreateFromFlags("WS", "worker", domain.AgentMode(agentAddMode))
	if in.WorkspaceKey != "WS" || in.Name != "worker" || in.RoleName != "task" ||
		!in.Auto || in.Backend != "codex" || !in.CrossRepo ||
		in.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("agent create = %+v", in)
	}

	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := enqueueAgentAddTaskStart(ctx, st, "WS", "worker", "orch-1"); err != nil {
		t.Fatalf("enqueue pinned task: %v", err)
	}
	cmds, err := st.AgentCommands().List(ctx, "WS", store.AgentCommandFilter{TargetAgentID: "worker"})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Payload["task_id"] != "TASK-1" || cmds[0].Payload["parent_session_id"] != "orch-1" {
		t.Fatalf("commands = %+v", cmds)
	}
}

func TestAgentDesiredStateHelpers(t *testing.T) {
	if err := enqueueAgentAddTaskStart(context.Background(), memstore.New(), "WS", "worker", ""); err != nil {
		t.Fatalf("empty task should not enqueue: %v", err)
	}
	resetAgentFlagGlobals(t)
	agentAddRole = "task"
	in := agentCreateFromFlags("WS", "worker", "")
	if in.DesiredState != "" {
		t.Fatalf("unpinned desired state = %q, want empty", in.DesiredState)
	}
}

func TestEnsureAgentDefinitionLocalWorktreesNoopsWithoutLocalPath(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	err := ensureAgentDefinitionLocalWorktrees(ctx, st, domain.Agent{
		WorkspaceKey: "WS",
		Name:         "worker",
		RoleName:     "task",
	})
	if err != nil {
		t.Fatalf("ensure without local path: %v", err)
	}
}

func TestEnsureAgentDefinitionLocalWorktreesRequiresSelectedRepos(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.MutateWorkspaceLocalState("WS", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = t.TempDir()
		return nil
	}); err != nil {
		t.Fatalf("MutateWorkspaceLocalState: %v", err)
	}

	err := ensureAgentDefinitionLocalWorktrees(ctx, st, domain.Agent{
		WorkspaceKey: "WS",
		Name:         "worker",
		RoleName:     "task",
	})
	if err == nil || !strings.Contains(err.Error(), "has no repos") {
		t.Fatalf("ensure with no repos = %v, want no repos error", err)
	}
}

func resetAgentFlagGlobals(t *testing.T) {
	t.Helper()
	origRole, origBackend, origParent, origMode := agentAddRole, agentAddBackend, agentAddParent, agentAddMode
	origTaskFilter, origBudget, origTask, origOrch := agentAddTaskFilter, agentAddBudget, agentAddTask, agentAddOrchestrator
	origAuto, origCrossRepo, origStopForce := agentAddAuto, agentAddCrossRepo, agentStopForce
	origRepos, origGroups := agentAddRepos, agentAddRepoGroups
	origMaxConc := agentAddMaxConc
	origListJSON, origShowJSON := agentListJSON, agentShowJSON
	t.Cleanup(func() {
		agentAddRole, agentAddBackend, agentAddParent, agentAddMode = origRole, origBackend, origParent, origMode
		agentAddTaskFilter, agentAddBudget, agentAddTask, agentAddOrchestrator = origTaskFilter, origBudget, origTask, origOrch
		agentAddAuto, agentAddCrossRepo, agentStopForce = origAuto, origCrossRepo, origStopForce
		agentAddRepos, agentAddRepoGroups = origRepos, origGroups
		agentAddMaxConc = origMaxConc
		agentListJSON, agentShowJSON = origListJSON, origShowJSON
	})
}

func setupAgentdefFleetWorkspace(t *testing.T) *bootstrap.StoreHandle {
	t.Helper()
	requireAgentdefFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(bootstrap.EnvFleetDBActor, "agentdef-test")
	t.Setenv(bootstrap.EnvFleetDBURL, "")

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		_ = handle.Close()
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := handle.Store.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "task"}); err != nil {
		_ = handle.Close()
		t.Fatalf("create role: %v", err)
	}
	if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: "WS",
		Name:         "api",
		RemoteURL:    "/tmp/api",
		Groups:       []string{"backend"},
		SourceRepoID: "api",
	}); err != nil {
		_ = handle.Close()
		t.Fatalf("create repo: %v", err)
	}
	return handle
}

func requireAgentdefFleetDB(t *testing.T) {
	t.Helper()
	if os.Getenv("FLEET_DB_BIN") != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}

func captureAgentdefStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	return b.String()
}
