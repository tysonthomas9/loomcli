package svcimpl

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestCreateAgentAllowsDistributedWorkspaceWithoutLocalPath(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "TEST2",
		Name:          "Test 2",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "TEST2",
		Name:          "repo",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	created, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "smoke-rebuild",
		RoleName:     "task",
		Backend:      "codex",
		CrossRepo:    true,
	})
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	if created.Name != "smoke-rebuild" {
		t.Fatalf("created.Name = %q, want smoke-rebuild", created.Name)
	}
	if _, err := st.Agents().Get(ctx, "TEST2", "smoke-rebuild"); err != nil {
		t.Fatalf("agent was not persisted: %v", err)
	}
}

func TestCreateAgentLeadEnsuresRoleAndDoesNotRequireRepo(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "TEST2",
		Name:          "Test 2",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	created, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "lead-nova",
		RoleName:     "Lead",
		Backend:      "codex",
	})
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	if created.RoleName != "lead" {
		t.Fatalf("created.RoleName = %q, want lead", created.RoleName)
	}
	if len(created.Repos) != 0 || created.CrossRepo {
		t.Fatalf("lead repo scope = repos %v cross_repo %v, want no repo scope", created.Repos, created.CrossRepo)
	}
	if _, err := st.Roles().Get(ctx, "TEST2", "lead"); err != nil {
		t.Fatalf("lead role was not created: %v", err)
	}
}

func TestCreateAndUpdateAgentPersistMode(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "TEST2",
		Name:          "Test 2",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	created, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "ephem",
		RoleName:     "lead",
		Backend:      "codex",
		Mode:         domain.AgentModeEphemeral,
	})
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	if created.Mode != domain.AgentModeEphemeral {
		t.Fatalf("created.Mode = %q, want ephemeral", created.Mode)
	}

	svcMode := domain.AgentModeService
	updated, err := svc.UpdateAgent(ctx, "TEST2", "ephem", service.AgentUpdateInput{Mode: &svcMode})
	if err != nil {
		t.Fatalf("UpdateAgent returned error: %v", err)
	}
	if updated.Mode != domain.AgentModeService {
		t.Fatalf("updated.Mode = %q, want service", updated.Mode)
	}
}

func TestRequestAgentLifecycleUpdatesStateAndQueuesCommand(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "TEST2",
		Name:          "Test 2",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	updated, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "start",
		Payload:      map[string]string{"task_id": "TEST2-1"},
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle returned error: %v", err)
	}
	if updated.State != domain.AgentStateActive || updated.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("updated agent state = %s/%s, want active/running", updated.State, updated.DesiredState)
	}
	cmds, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{
		Status:        domain.AgentCommandQueued,
		TargetAgentID: "desktopqa",
	})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("queued commands = %d, want 1", len(cmds))
	}
	if cmds[0].Type != "start" {
		t.Fatalf("command type = %q, want start", cmds[0].Type)
	}
	if cmds[0].Payload["task_id"] != "TEST2-1" {
		t.Fatalf("command payload task_id = %q, want TEST2-1", cmds[0].Payload["task_id"])
	}
}
