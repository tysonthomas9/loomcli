package agentdef

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRunAgentAddJSONReturnsCreatedAgentAndKeepsTaskCommandStructured(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	const workspace = "AGENTADD"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "Agent Add"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "loom-config"))
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: workspace,
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			workspace: {},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	withAgentdefStore(t, st, workspace)
	withAgentdefGlobals(t, func() {
		agentAddRole = "coder"
		agentAddBackend = "echo"
		agentAddTask = "TASK-1"
		agentAddJSON = true
		var written any
		agentdefWriteJSON = func(v any) error {
			written = v
			return nil
		}

		if err := runAgentAdd(&cobra.Command{}, []string{"nova"}); err != nil {
			t.Fatalf("runAgentAdd() error = %v", err)
		}
		created, ok := written.(*domain.Agent)
		if !ok {
			t.Fatalf("agentdefWriteJSON() value = %T, want *domain.Agent", written)
		}
		if created.Name != "nova" || created.RoleName != "coder" || created.Backend != "echo" {
			t.Fatalf("created agent = %+v, want nova/coder/echo", created)
		}
		commands, err := st.AgentCommands().List(ctx, workspace, store.AgentCommandFilter{TargetAgentID: "nova"})
		if err != nil {
			t.Fatalf("list agent commands: %v", err)
		}
		if len(commands) != 1 || commands[0].Type != "start" || commands[0].Payload["task_id"] != "TASK-1" {
			t.Fatalf("commands = %+v, want one structured start command for TASK-1", commands)
		}
	})
}

func TestRunAgentStartJSONReturnsUpdatedAgentAndCommand(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	const workspace = "AGENTDEF"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "Agent Definitions"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         "nova",
		RoleName:     "coder",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	withAgentdefStore(t, st, workspace)
	withAgentdefGlobals(t, func() {
		agentStartJSON = true
		var written any
		agentdefWriteJSON = func(v any) error {
			written = v
			return nil
		}

		if err := runAgentStart(nil, []string{"nova"}); err != nil {
			t.Fatalf("runAgentStart() error = %v", err)
		}
		result, ok := written.(agentStateUpdateResult)
		if !ok {
			t.Fatalf("agentdefWriteJSON() value = %T, want agentStateUpdateResult", written)
		}
		if result.Agent == nil ||
			result.Agent.Name != "nova" ||
			result.Agent.DesiredState != domain.AgentDesiredRunning ||
			result.Agent.State != domain.AgentStateActive {
			t.Fatalf("result agent = %+v, want nova active/running", result.Agent)
		}
		if result.Command == nil ||
			result.Command.TargetAgentID != "nova" ||
			result.Command.Type != "start" ||
			result.Command.Status != domain.AgentCommandQueued {
			t.Fatalf("result command = %+v, want queued start command", result.Command)
		}
	})
}

func TestRunAgentStopForceJSONReturnsPayload(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	const workspace = "AGENTSTOP"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "Agent Stop"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         "nova",
		RoleName:     "coder",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	withAgentdefStore(t, st, workspace)
	withAgentdefGlobals(t, func() {
		agentStopJSON = true
		agentStopForce = true
		var written any
		agentdefWriteJSON = func(v any) error {
			written = v
			return nil
		}

		if err := runAgentStop(nil, []string{"nova"}); err != nil {
			t.Fatalf("runAgentStop() error = %v", err)
		}
		result, ok := written.(agentStateUpdateResult)
		if !ok {
			t.Fatalf("agentdefWriteJSON() value = %T, want agentStateUpdateResult", written)
		}
		if result.Agent == nil ||
			result.Agent.DesiredState != domain.AgentDesiredStopped ||
			result.Agent.State != domain.AgentStateStopped {
			t.Fatalf("result agent = %+v, want stopped", result.Agent)
		}
		if result.Command == nil ||
			result.Command.Type != "stop" ||
			result.Command.Payload["force"] != "true" {
			t.Fatalf("result command = %+v, want force stop payload", result.Command)
		}
	})
}

func withAgentdefStore(t *testing.T, st store.Store, workspace string) {
	t.Helper()
	old := agentdefWithActiveWorkspace
	agentdefWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, workspace)
	}
	t.Cleanup(func() { agentdefWithActiveWorkspace = old })
}

func withAgentdefGlobals(t *testing.T, fn func()) {
	t.Helper()
	oldAddRole, oldAddBackend, oldAddParent := agentAddRole, agentAddBackend, agentAddParent
	oldAddMode, oldAddTaskFilter, oldAddBudget := agentAddMode, agentAddTaskFilter, agentAddBudget
	oldAddTask, oldAddOrchestrator := agentAddTask, agentAddOrchestrator
	oldAddAuto, oldAddCrossRepo := agentAddAuto, agentAddCrossRepo
	oldAddMaxConc := agentAddMaxConc
	oldAddRepos, oldAddRepoGroups := agentAddRepos, agentAddRepoGroups
	oldAddJSON, oldListJSON, oldShowJSON := agentAddJSON, agentListJSON, agentShowJSON
	oldRemoveJSON, oldStartJSON, oldStopJSON, oldStopForce := agentRemoveJSON, agentStartJSON, agentStopJSON, agentStopForce
	oldWriteJSON := agentdefWriteJSON
	t.Cleanup(func() {
		agentAddRole, agentAddBackend, agentAddParent = oldAddRole, oldAddBackend, oldAddParent
		agentAddMode, agentAddTaskFilter, agentAddBudget = oldAddMode, oldAddTaskFilter, oldAddBudget
		agentAddTask, agentAddOrchestrator = oldAddTask, oldAddOrchestrator
		agentAddAuto, agentAddCrossRepo = oldAddAuto, oldAddCrossRepo
		agentAddMaxConc = oldAddMaxConc
		agentAddRepos, agentAddRepoGroups = oldAddRepos, oldAddRepoGroups
		agentAddJSON, agentListJSON, agentShowJSON = oldAddJSON, oldListJSON, oldShowJSON
		agentRemoveJSON, agentStartJSON, agentStopJSON, agentStopForce = oldRemoveJSON, oldStartJSON, oldStopJSON, oldStopForce
		agentdefWriteJSON = oldWriteJSON
	})
	fn()
}
