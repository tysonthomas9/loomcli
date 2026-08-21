package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type roleCreateRaceStore struct {
	store.Store
	roles store.RoleStore
}

func (s roleCreateRaceStore) Roles() store.RoleStore { return s.roles }

type alreadyExistsAfterWinnerRoleStore struct {
	store.RoleStore
	winner store.RoleCreate
}

func (s alreadyExistsAfterWinnerRoleStore) Create(ctx context.Context, _ store.RoleCreate) (*domain.Role, error) {
	if _, err := s.RoleStore.Create(ctx, s.winner); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("concurrent role create: %w", domain.ErrAlreadyExists)
}

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
	role, err := st.Roles().Get(ctx, "TEST2", "lead")
	if err != nil {
		t.Fatalf("load lead role: %v", err)
	}
	if role.Kind != domain.RoleKindInteractive {
		t.Fatalf("lead role kind = %q, want interactive", role.Kind)
	}
	if role.PromptFile != "" {
		t.Fatalf("lead prompt_file = %q, want empty", role.PromptFile)
	}
}

func TestCreateAgentLeadReusesSeededDefaultRole(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2",
		Name:         "lead",
		Kind:         string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("seed lead role: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "lead-two",
		RoleName:     "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("CreateAgent with seeded default lead role: %v", err)
	}
}

func TestCreateAgentInteractiveKindEnsuresRoleWithPromptFile(t *testing.T) {
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
		Name:         "review-nova",
		RoleName:     "pr-review",
		Kind:         "interactive",
		PromptFile:   "builtin:pr-review",
		Backend:      "codex",
	})
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	if created.RoleName != "pr-review" {
		t.Fatalf("created.RoleName = %q, want pr-review", created.RoleName)
	}
	role, err := st.Roles().Get(ctx, "TEST2", "pr-review")
	if err != nil {
		t.Fatalf("interactive role was not created: %v", err)
	}
	if role.Kind != domain.RoleKindInteractive {
		t.Fatalf("role kind = %q, want interactive", role.Kind)
	}
	if role.PromptFile != "builtin:pr-review" {
		t.Fatalf("role prompt_file = %q, want builtin:pr-review", role.PromptFile)
	}
}

func TestCreateAgentInteractiveKindEnsuresRoleWithInlinePrompt(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "custom-nova",
		RoleName:     "custom-nova",
		Kind:         "interactive",
		Prompt:       "  Literal {{ marker }}  ",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	role, err := st.Roles().Get(ctx, "TEST2", "custom-nova")
	if err != nil {
		t.Fatalf("load interactive role: %v", err)
	}
	if role.Prompt != "Literal {{ marker }}" {
		t.Fatalf("role prompt = %q, want trimmed literal prompt", role.Prompt)
	}
	if role.PromptFile != "" {
		t.Fatalf("role prompt_file = %q, want empty", role.PromptFile)
	}
}

func TestCreateAgentInteractiveRoleCreationIsIdempotent(t *testing.T) {
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
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2",
		Name:         "reviewer",
		Kind:         string(domain.RoleKindInteractive),
		PromptFile:   "existing.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)
	// Creating another agent on the same interactive role with the SAME prompt
	// is a no-op on the role (idempotent), never a mutation.
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "reviewer-one",
		RoleName:     "reviewer",
		Kind:         "interactive",
		PromptFile:   "existing.md",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	role, err := st.Roles().Get(ctx, "TEST2", "reviewer")
	if err != nil {
		t.Fatalf("load role: %v", err)
	}
	if role.PromptFile != "existing.md" {
		t.Fatalf("existing role prompt_file = %q, want unchanged existing.md", role.PromptFile)
	}
}

// TestCreateAgentInteractiveRoleConflict guards the reconcile path: creating an
// interactive agent whose role name collides with a pre-existing role of a
// different kind, or an interactive role with a different prompt, must surface a
// validation error rather than silently launching under the wrong role/prompt.
func TestCreateAgentInteractiveRoleConflict(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "TEST3",
		Name:          "Test 3",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// A seeded worker role, and an interactive role with a fixed prompt.
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST3", Name: "task", Kind: string(domain.RoleKindWorker),
	}); err != nil {
		t.Fatalf("create worker role: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST3", Name: "reviewer",
		Kind: string(domain.RoleKindInteractive), PromptFile: "builtin:pr-review",
	}); err != nil {
		t.Fatalf("create interactive role: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST3", Name: "blank-reviewer",
		Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create empty-prompt interactive role: %v", err)
	}

	svc := NewAgentService(nil, nil, nil, st)

	// Interactive agent colliding with the worker role "task" → error.
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST3", Name: "task", RoleName: "task",
		Kind: "interactive", PromptFile: "prompts/x.md", Backend: "codex",
	}); err == nil {
		t.Fatal("CreateAgent on worker-role collision: error = nil, want validation error")
	}

	// Interactive agent on an existing interactive role but a different prompt → error.
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST3", Name: "reviewer-two", RoleName: "reviewer",
		Kind: "interactive", PromptFile: "prompts/other.md", Backend: "codex",
	}); err == nil {
		t.Fatal("CreateAgent on prompt conflict: error = nil, want validation error")
	}

	// A requested inline prompt must not silently reuse an existing role whose
	// stored inline prompt is empty.
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST3", Name: "blank-reviewer-two", RoleName: "blank-reviewer",
		Kind: "interactive", Prompt: "requested inline prompt", Backend: "codex",
	}); err == nil {
		t.Fatal("CreateAgent on empty stored inline prompt conflict: error = nil, want validation error")
	}
}

func TestCreateAgentRoleCreateRaceRefetchesAndReconciles(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	base := memstore.New()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST4", Name: "Test 4", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	racingRoles := alreadyExistsAfterWinnerRoleStore{
		RoleStore: base.Roles(),
		winner: store.RoleCreate{
			WorkspaceKey: "TEST4",
			Name:         "operator",
			Kind:         string(domain.RoleKindInteractive),
			Prompt:       "concurrent prompt",
		},
	}
	st := roleCreateRaceStore{Store: base, roles: racingRoles}
	svc := NewAgentService(nil, nil, nil, st)

	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST4",
		Name:         "operator-a",
		RoleName:     "operator",
		Kind:         "interactive",
		Prompt:       "requested prompt",
		Backend:      "codex",
	}); err == nil {
		t.Fatal("CreateAgent race with conflicting winner: error = nil, want validation error")
	}
	role, err := base.Roles().Get(ctx, "TEST4", "operator")
	if err != nil {
		t.Fatalf("load race winner role: %v", err)
	}
	if role.Prompt != "concurrent prompt" {
		t.Fatalf("race winner prompt = %q, want concurrent prompt", role.Prompt)
	}
}

func TestCreateAgentRejectsInvalidKind(t *testing.T) {
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
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "bad-kind",
		RoleName:     "reviewer",
		Kind:         "daemon",
	}); err == nil {
		t.Fatal("CreateAgent invalid kind error = nil, want error")
	}
}

func TestCreateAgentRuntimeProvider(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	tests := []struct {
		name     string
		provider domain.RuntimeProvider
		wantErr  bool
	}{
		{name: "workspace default", provider: ""},
		{name: "local", provider: domain.RuntimeProviderLocal},
		{name: "e2b", provider: domain.RuntimeProviderE2B},
		{name: "kubernetes", provider: domain.RuntimeProviderKubernetes},
		{name: "daytona", provider: domain.RuntimeProviderDaytona},
		{name: "ci", provider: domain.RuntimeProviderCI},
		{name: "other", provider: domain.RuntimeProviderOther},
		{name: "invalid", provider: domain.RuntimeProvider("unknown"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
				Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
			}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}

			svc := NewAgentService(nil, nil, nil, st)
			created, err := svc.CreateAgent(ctx, service.AgentCreateInput{
				WorkspaceKey:    "TEST2",
				Name:            "runtime-agent",
				RoleName:        "lead",
				Backend:         "codex",
				RuntimeProvider: tt.provider,
			})
			if tt.wantErr {
				var serviceErr *service.ServiceError
				if !errors.As(err, &serviceErr) || serviceErr.Kind != service.KindValidation {
					t.Fatalf("CreateAgent error = %v, want validation error", err)
				}
				if serviceErr.Message != "invalid runtime provider" {
					t.Fatalf("CreateAgent error message = %q, want invalid runtime provider", serviceErr.Message)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateAgent returned error: %v", err)
			}
			if created.RuntimeProvider != tt.provider {
				t.Fatalf("created.RuntimeProvider = %q, want %q", created.RuntimeProvider, tt.provider)
			}
		})
	}
}

func TestCreateAgentNormalizesMixedCaseName(t *testing.T) {
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
		Name:         "Test-lead",
		RoleName:     "lead",
		Backend:      "codex",
	})
	if err != nil {
		t.Fatalf("CreateAgent returned error: %v", err)
	}
	if created.Name != "test-lead" {
		t.Fatalf("created.Name = %q, want test-lead", created.Name)
	}
}

// Regression: Update/Delete/Lifecycle must accept the same (dot-allowing,
// case-normalized) charset as Create. Previously they used a dot-rejecting
// validator, so an agent created with a dot in its name became permanently
// unmanageable (could not be updated, started/stopped, or deleted).
func TestUpdateAndDeleteAgentAcceptDottedStoredName(t *testing.T) {
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
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "foo.bar",
		RoleName:     "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("CreateAgent(foo.bar): %v", err)
	}

	auto := true
	if _, err := svc.UpdateAgent(ctx, "TEST2", "foo.bar", service.AgentUpdateInput{Auto: &auto}); err != nil {
		t.Fatalf("UpdateAgent(foo.bar) should accept the dotted name: %v", err)
	}

	// Case-insensitive: the stored name is normalized, so a differently-cased
	// reference resolves to the same agent.
	if err := svc.DeleteAgent(ctx, "TEST2", "FOO.BAR"); err != nil {
		t.Fatalf("DeleteAgent(FOO.BAR) should normalize + delete: %v", err)
	}
	if _, err := st.Agents().Get(ctx, "TEST2", "foo.bar"); err == nil {
		t.Fatal("agent foo.bar should have been deleted")
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
