package defs

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTypeScriptFirstAgentApplyCreatesUIVisibleInstance(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	path, err := ScaffoldAgent(root, "hello-world")
	if err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := FindAgent(plan, "hello-world")
	if !ok {
		t.Fatalf("FindAgent() did not find hello-world in %+v", plan.Agents)
	}
	if agent.SourcePath != path || agent.Backend != "echo" || agent.Model != "local/echo" {
		t.Fatalf("agent = %+v, want scaffolded echo/local agent from %s", agent, path)
	}
	if agent.MaxConcurrency != 1 {
		t.Fatalf("MaxConcurrency = %d, want policy maxConcurrency from TypeScript", agent.MaxConcurrency)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSFIRST", Name: "TypeScript First"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSFIRST", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	role, err := st.Roles().Get(ctx, "TSFIRST", "hello-world")
	if err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if role.Backend != "echo" || role.Model != "local/echo" {
		t.Fatalf("role = %+v, want TypeScript backend/model", role)
	}

	instance, err := ApplyAgentInstance(ctx, st, "TSFIRST", agent, "local", true)
	if err != nil {
		t.Fatalf("ApplyAgentInstance() error = %v", err)
	}
	if instance.Name != "local" || instance.RoleName != "hello-world" {
		t.Fatalf("instance = %+v, want local instance for hello-world role", instance)
	}
	if instance.State != domain.AgentStateActive || instance.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("instance state = %s/%s, want active/running", instance.State, instance.DesiredState)
	}
	cmds, err := st.AgentCommands().List(ctx, "TSFIRST", store.AgentCommandFilter{TargetAgentID: "local"})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Type != "start" || cmds[0].Payload["source"] != "typescript-first" {
		t.Fatalf("commands = %+v, want one queued typescript-first start command", cmds)
	}
}
