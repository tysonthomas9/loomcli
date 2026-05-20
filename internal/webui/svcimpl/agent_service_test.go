package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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

func TestCreateAgentRejectsLocalWorkspaceWithoutReposAndRollsBack(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "LOCAL",
		Name:          "Local",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: "LOCAL",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"LOCAL": {Path: t.TempDir()},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	_, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "LOCAL",
		Name:         "worker",
		RoleName:     "task",
		CrossRepo:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "no repos") {
		t.Fatalf("CreateAgent err = %v, want no repos validation", err)
	}
	if _, getErr := st.Agents().Get(ctx, "LOCAL", "worker"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("agent rollback error = %v, want not found", getErr)
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

func TestAgentServiceAdditionalStoreErrorBranches(t *testing.T) {
	ctx := context.Background()

	listErr := errors.New("list agents failed")
	svc := NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store:  memstore.New(),
		agents: fakeAgentStore{listErr: listErr},
	})
	if _, err := svc.ListAgents(ctx, "WS"); err == nil || !strings.Contains(err.Error(), "list agents") {
		t.Fatalf("ListAgents err = %v, want list agents error", err)
	}

	if _, err := NewAgentService(nil, nil, nil, memstore.New()).CreateAgent(ctx, service.AgentCreateInput{
		Name:     "missing-workspace",
		RoleName: "task",
	}); err == nil || !strings.Contains(err.Error(), "workspace_key required") {
		t.Fatalf("CreateAgent validation err = %v", err)
	}

	roleLoadErr := errors.New("role load failed")
	svc = NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store: memstore.New(),
		roles: fakeRoleStore{getErr: roleLoadErr},
	})
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "WS",
		Name:         "lead-one",
		RoleName:     "lead",
	}); err == nil || !strings.Contains(err.Error(), "load lead role") {
		t.Fatalf("CreateAgent role load err = %v", err)
	}

	roleCreateErr := errors.New("role create failed")
	svc = NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store: memstore.New(),
		roles: fakeRoleStore{
			getErr:    fmt.Errorf("missing role: %w", domain.ErrNotFound),
			createErr: roleCreateErr,
		},
	})
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "WS",
		Name:         "lead-two",
		RoleName:     "lead",
	}); err == nil || !strings.Contains(err.Error(), "create lead role") {
		t.Fatalf("CreateAgent role create err = %v", err)
	}

	createErr := errors.New("create agent failed")
	svc = NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store:  memstore.New(),
		agents: fakeAgentStore{createErr: createErr},
	})
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "WS",
		Name:         "worker",
		RoleName:     "task",
	}); err == nil || !strings.Contains(err.Error(), "create agent") {
		t.Fatalf("CreateAgent create err = %v", err)
	}

	impl := NewAgentService(nil, nil, nil, memstore.New()).(*agentServiceImpl)
	if err := impl.ensureLocalAgentWorktrees(ctx, domain.Agent{
		WorkspaceKey: "MISSING",
		Name:         "worker",
		RoleName:     "task",
	}); err == nil || !strings.Contains(err.Error(), "load workspace") {
		t.Fatalf("ensureLocalAgentWorktrees err = %v", err)
	}

	updateErr := errors.New("update failed")
	svc = NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store:  memstore.New(),
		agents: fakeAgentStore{updateErr: updateErr},
	})
	if _, err := svc.RequestAgentLifecycle(ctx, "WS", "worker", service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "start",
	}); err == nil || !strings.Contains(err.Error(), "update agent") {
		t.Fatalf("RequestAgentLifecycle update err = %v", err)
	}

	svc = NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store:    memstore.New(),
		agents:   fakeAgentStore{updated: &domain.Agent{WorkspaceKey: "WS", Name: "worker"}},
		commands: nil,
	})
	if updated, err := svc.RequestAgentLifecycle(ctx, "WS", "worker", service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "stop",
	}); err != nil || updated == nil || updated.Name != "worker" {
		t.Fatalf("RequestAgentLifecycle nil commands updated=%+v err=%v", updated, err)
	}

	commandErr := errors.New("command failed")
	svc = NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store:    memstore.New(),
		agents:   fakeAgentStore{updated: &domain.Agent{WorkspaceKey: "WS", Name: "worker"}},
		commands: fakeAgentCommandStore{createErr: commandErr},
	})
	if _, err := svc.RequestAgentLifecycle(ctx, "WS", "worker", service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "restart",
	}); err == nil || !strings.Contains(err.Error(), "create agent command") {
		t.Fatalf("RequestAgentLifecycle command err = %v", err)
	}

	deleteErr := errors.New("delete failed")
	svc = NewAgentService(nil, nil, nil, &agentServiceStoreOverride{
		Store:  memstore.New(),
		agents: fakeAgentStore{deleteErr: deleteErr},
	})
	if err := svc.DeleteAgent(ctx, "WS", "worker"); err == nil || !strings.Contains(err.Error(), "delete agent") {
		t.Fatalf("DeleteAgent err = %v", err)
	}
}

type agentServiceStoreOverride struct {
	store.Store
	agents   store.AgentStore
	roles    store.RoleStore
	commands store.AgentCommandStore
}

func (s *agentServiceStoreOverride) Agents() store.AgentStore {
	if s.agents != nil {
		return s.agents
	}
	return s.Store.Agents()
}

func (s *agentServiceStoreOverride) Roles() store.RoleStore {
	if s.roles != nil {
		return s.roles
	}
	return s.Store.Roles()
}

func (s *agentServiceStoreOverride) AgentCommands() store.AgentCommandStore {
	return s.commands
}

type fakeAgentStore struct {
	store.AgentStore
	listErr   error
	createErr error
	updateErr error
	deleteErr error
	updated   *domain.Agent
}

func (s fakeAgentStore) List(context.Context, string) ([]*domain.Agent, error) {
	return nil, s.listErr
}

func (s fakeAgentStore) Create(_ context.Context, in store.AgentCreate) (*domain.Agent, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &domain.Agent{WorkspaceKey: in.WorkspaceKey, Name: in.Name, RoleName: in.RoleName}, nil
}

func (s fakeAgentStore) Update(context.Context, string, string, store.AgentUpdate) (*domain.Agent, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	if s.updated != nil {
		return s.updated, nil
	}
	return &domain.Agent{WorkspaceKey: "WS", Name: "worker"}, nil
}

func (s fakeAgentStore) Delete(context.Context, string, string) error {
	return s.deleteErr
}

type fakeRoleStore struct {
	store.RoleStore
	getErr    error
	createErr error
}

func (s fakeRoleStore) Get(context.Context, string, string) (*domain.Role, error) {
	return nil, s.getErr
}

func (s fakeRoleStore) Create(context.Context, store.RoleCreate) (*domain.Role, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &domain.Role{}, nil
}

type fakeAgentCommandStore struct {
	store.AgentCommandStore
	createErr error
}

func (s fakeAgentCommandStore) Create(context.Context, store.AgentCommandCreate) (*domain.AgentCommand, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &domain.AgentCommand{}, nil
}
