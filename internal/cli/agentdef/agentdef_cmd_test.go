package agentdef

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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
	origTaskFilter, origBudget, origTask := agentAddTaskFilter, agentAddBudget, agentAddTask
	origAuto, origCrossRepo := agentAddAuto, agentAddCrossRepo
	origRepos, origGroups := agentAddRepos, agentAddRepoGroups
	origMaxConc := agentAddMaxConc
	t.Cleanup(func() {
		agentAddRole, agentAddBackend, agentAddParent, agentAddMode = origRole, origBackend, origParent, origMode
		agentAddTaskFilter, agentAddBudget, agentAddTask = origTaskFilter, origBudget, origTask
		agentAddAuto, agentAddCrossRepo = origAuto, origCrossRepo
		agentAddRepos, agentAddRepoGroups = origRepos, origGroups
		agentAddMaxConc = origMaxConc
	})
}
