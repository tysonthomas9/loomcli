package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type roleCreateRaceStore struct {
	store.Store
	roles store.RoleStore
}

type agentCreateOverrideStore struct {
	store.Store
	agents store.AgentStore
}

func (s agentCreateOverrideStore) Agents() store.AgentStore { return s.agents }

type agentCommandOverrideStore struct {
	store.Store
	commands store.AgentCommandStore
}

func (s agentCommandOverrideStore) AgentCommands() store.AgentCommandStore { return s.commands }

type failingAgentCommandCreateStore struct {
	store.AgentCommandStore
	err error
}

func (s failingAgentCommandCreateStore) Create(context.Context, store.AgentCommandCreate) (*domain.AgentCommand, error) {
	return nil, s.err
}

type failingAgentCreateStore struct {
	store.AgentStore
	err error
}

func (s failingAgentCreateStore) Create(context.Context, store.AgentCreate) (*domain.Agent, error) {
	return nil, s.err
}

type commitThenErrorAgentCommandStore struct {
	store.AgentCommandStore
	createErr          error
	transientGetErrors int
	createCalls        int
	getCalls           int
	ackAfterCreate     bool
	listCalls          int
	defaultListCap     int
	lastListFilter     store.AgentCommandFilter
}

type hashOnlyGetAgentCommandStore struct {
	store.AgentCommandStore
	command     *domain.AgentCommand
	createErr   error
	createCalls int
	getCalls    int
	listCalls   int
	cursor      int64
}

type forbidAgentUpdateStore struct {
	store.AgentStore
	mu    sync.Mutex
	calls int
}

func (s *forbidAgentUpdateStore) Update(
	context.Context,
	string,
	string,
	store.AgentUpdate,
) (*domain.Agent, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return nil, errors.New("request-side agent projection is forbidden")
}

func (s *forbidAgentUpdateStore) updateCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *commitThenErrorAgentCommandStore) Create(
	ctx context.Context,
	create store.AgentCommandCreate,
) (*domain.AgentCommand, error) {
	s.createCalls++
	command, err := s.AgentCommandStore.Create(ctx, create)
	if err != nil {
		return nil, err
	}
	if s.ackAfterCreate && s.createCalls == 1 {
		if _, err := s.AgentCommandStore.Ack(
			ctx,
			create.WorkspaceKey,
			command.CommandID,
			store.AgentCommandAck{
				NodeID:  "node-bound-after-create",
				OwnerID: "stable-owner",
			},
		); err != nil {
			return nil, err
		}
	}
	return nil, s.createErr
}

func (s *commitThenErrorAgentCommandStore) Get(
	ctx context.Context,
	workspaceKey string,
	commandID string,
) (*domain.AgentCommand, error) {
	s.getCalls++
	if s.transientGetErrors > 0 {
		s.transientGetErrors--
		return nil, errors.New("transient command read failure")
	}
	return s.AgentCommandStore.Get(ctx, workspaceKey, commandID)
}

func (s *commitThenErrorAgentCommandStore) List(
	ctx context.Context,
	workspaceKey string,
	filter store.AgentCommandFilter,
) ([]*domain.AgentCommand, error) {
	s.listCalls++
	s.lastListFilter = filter
	if filter.Limit == 0 && s.defaultListCap > 0 {
		filter.Limit = s.defaultListCap
	}
	return s.AgentCommandStore.List(ctx, workspaceKey, filter)
}

func (s *hashOnlyGetAgentCommandStore) Create(
	_ context.Context,
	create store.AgentCommandCreate,
) (*domain.AgentCommand, error) {
	s.createCalls++
	if s.command == nil {
		s.command = &domain.AgentCommand{
			WorkspaceKey:  create.WorkspaceKey,
			CommandID:     create.CommandID,
			TargetAgentID: create.TargetAgentID,
			TargetNodeID:  create.TargetNodeID,
			SessionID:     create.SessionID,
			Type:          create.Type,
			Payload:       create.Payload,
			Cursor:        s.cursor,
			Status:        domain.AgentCommandQueued,
		}
	}
	return nil, s.createErr
}

func (s *hashOnlyGetAgentCommandStore) Get(
	_ context.Context,
	workspaceKey string,
	commandID string,
) (*domain.AgentCommand, error) {
	s.getCalls++
	if s.command == nil ||
		s.command.WorkspaceKey != workspaceKey ||
		s.command.CommandID != commandID {
		return nil, domain.ErrNotFound
	}
	command := *s.command
	return &command, nil
}

func (s *hashOnlyGetAgentCommandStore) List(
	context.Context,
	string,
	store.AgentCommandFilter,
) ([]*domain.AgentCommand, error) {
	s.listCalls++
	return []*domain.AgentCommand{}, nil
}

func (s roleCreateRaceStore) Roles() store.RoleStore { return s.roles }

type alreadyExistsAfterWinnerRoleStore struct {
	store.RoleStore
	winner store.RoleCreate
}

type fakeInteractiveRuntime struct {
	live   map[terminal.SessionKey]bool
	closed map[terminal.SessionKey]bool
	owned  map[string][]InteractiveRuntimeSession
	killed []terminal.SessionKey
}

type blockingFirstKillInteractiveRuntime struct {
	key              terminal.SessionKey
	firstKillStarted chan struct{}
	releaseFirstKill chan struct{}
	killed           []terminal.SessionKey
}

func (r *blockingFirstKillInteractiveRuntime) OwnedAgentSessions(
	context.Context,
	string,
	string,
) ([]InteractiveRuntimeSession, error) {
	return []InteractiveRuntimeSession{{Key: r.key, Live: true}}, nil
}

func (r *blockingFirstKillInteractiveRuntime) Kill(key terminal.SessionKey) error {
	r.killed = append(r.killed, key)
	if len(r.killed) == 1 {
		close(r.firstKillStarted)
		<-r.releaseFirstKill
	}
	return nil
}

func (r *fakeInteractiveRuntime) OwnedAgentSessions(
	_ context.Context,
	workspace string,
	agentID string,
) ([]InteractiveRuntimeSession, error) {
	return append([]InteractiveRuntimeSession(nil), r.owned[workspace+"\x00"+agentID]...), nil
}

func (r *fakeInteractiveRuntime) HasSession(key terminal.SessionKey) bool {
	return r.live[key]
}

func (r *fakeInteractiveRuntime) SessionClosed(key terminal.SessionKey) bool {
	return r.closed[key]
}

func (r *fakeInteractiveRuntime) Kill(key terminal.SessionKey) error {
	r.killed = append(r.killed, key)
	delete(r.live, key)
	if r.closed == nil {
		r.closed = make(map[terminal.SessionKey]bool)
	}
	r.closed[key] = true
	return nil
}

type fakeInteractiveTabSource struct {
	tabs []InteractiveRuntimeTab
}

func (s fakeInteractiveTabSource) ListInteractiveRuntimeTabs(context.Context, string) ([]InteractiveRuntimeTab, error) {
	return append([]InteractiveRuntimeTab(nil), s.tabs...), nil
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "lead-two",
		RoleName:     "lead",
		Backend:      "codex",
	}); err != nil {
		t.Fatalf("CreateAgent with seeded default lead role: %v", err)
	}
}

func TestCreateAgentFailureRetainsRetryableInteractiveRole(t *testing.T) {
	tests := []struct {
		name     string
		seedRole bool
	}{
		{name: "new role is retained"},
		{name: "pre-existing role is preserved", seedRole: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
			ctx := context.Background()
			base := memstore.New()
			if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{
				Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
			}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			if tt.seedRole {
				if _, err := base.Roles().Create(ctx, store.RoleCreate{
					WorkspaceKey: "TEST2", Name: "reviewer", Kind: string(domain.RoleKindInteractive),
					PromptFile: "builtin:pr-review",
				}); err != nil {
					t.Fatalf("seed role: %v", err)
				}
			}
			st := agentCreateOverrideStore{
				Store: base,
				agents: failingAgentCreateStore{
					AgentStore: base.Agents(),
					err:        fmt.Errorf("injected create failure: %w", domain.ErrConflict),
				},
			}
			svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
			if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
				WorkspaceKey: "TEST2", Name: "reviewer-a", RoleName: "reviewer",
				Kind: "interactive", PromptFile: "builtin:pr-review", Backend: "codex",
			}); err == nil {
				t.Fatal("CreateAgent error = nil, want injected failure")
			}
			_, err := base.Roles().Get(ctx, "TEST2", "reviewer")
			if err != nil {
				t.Fatalf("retryable role was removed: %v", err)
			}
		})
	}
}

func TestCreateAgentMultiRepoFailureRetainsRetryableWorktrees(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		name := "new checkout retained"
		if preexisting {
			name = "pre-existing checkout preserved"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
			ctx := context.Background()
			wsRoot := t.TempDir()
			validRepo := filepath.Join(wsRoot, "a-valid")
			brokenRepo := filepath.Join(wsRoot, "z-broken")
			if err := os.MkdirAll(validRepo, 0o755); err != nil {
				t.Fatalf("mkdir valid repo: %v", err)
			}
			if err := os.MkdirAll(brokenRepo, 0o755); err != nil {
				t.Fatalf("mkdir broken repo: %v", err)
			}
			initGitRepo(t, validRepo)
			if err := os.WriteFile(filepath.Join(validRepo, "README.md"), []byte("base\n"), 0o644); err != nil {
				t.Fatalf("write valid repo fixture: %v", err)
			}
			commitAll(t, validRepo)

			st := memstore.New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
				Key: "MULTI", Name: "Multi", DefaultBranch: "main",
			}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			if _, err := st.Roles().Create(ctx, store.RoleCreate{
				WorkspaceKey: "MULTI", Name: "task", Kind: string(domain.RoleKindWorker),
			}); err != nil {
				t.Fatalf("create task role: %v", err)
			}
			for _, repo := range []string{"a-valid", "z-broken"} {
				if _, err := st.Repos().Create(ctx, store.RepoCreate{
					WorkspaceKey: "MULTI", Name: repo, DefaultBranch: "main",
				}); err != nil {
					t.Fatalf("create repo %s: %v", repo, err)
				}
			}
			if err := bootstrap.MutateWorkspaceLocalState("MULTI", func(local *bootstrap.WorkspaceLocalState) error {
				local.Path = wsRoot
				local.Repos = map[string]string{"a-valid": validRepo, "z-broken": brokenRepo}
				return nil
			}); err != nil {
				t.Fatalf("seed workspace local state: %v", err)
			}

			target := localworkspace.AgentWorktreePath(wsRoot, "a-valid", "worker")
			sentinel := filepath.Join(target, "preexisting.txt")
			if preexisting {
				if err := localworkspace.EnsureGitWorktree(validRepo, target, "worker"); err != nil {
					t.Fatalf("seed pre-existing worktree: %v", err)
				}
				if err := os.WriteFile(sentinel, []byte("keep\n"), 0o644); err != nil {
					t.Fatalf("write pre-existing sentinel: %v", err)
				}
			}

			svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
			if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
				WorkspaceKey: "MULTI", Name: "worker", RoleName: "task",
				Backend: "codex", CrossRepo: true,
			}); err == nil {
				t.Fatal("CreateAgent error = nil, want second-repo provisioning failure")
			}
			if _, err := st.Agents().Get(ctx, "MULTI", "worker"); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("failed agent assignment survived: %v", err)
			}
			if preexisting {
				if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep\n" {
					t.Fatalf("pre-existing worktree changed = %q err=%v", string(data), err)
				}
			} else if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
				t.Fatalf("retryable first-repo worktree was removed: %v", err)
			}
		})
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)

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
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)

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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "TEST2",
		Name:         "bad-kind",
		RoleName:     "reviewer",
		Kind:         "daemon",
	}); err == nil {
		t.Fatal("CreateAgent invalid kind error = nil, want error")
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
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

func TestRequestAgentLifecycleQueuesStartWithPendingProjection(t *testing.T) {
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

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "start",
		Payload:      map[string]string{"task_id": "TEST2-1"},
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle returned error: %v", err)
	}
	if !result.Pending || result.CommandID == "" {
		t.Fatalf("lifecycle result = %+v, want pending command", result)
	}
	if result.Agent.State != domain.AgentStateIdle || result.Agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("pending agent state = %s/%s, want idle/running until daemon completion", result.Agent.State, result.Agent.DesiredState)
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
	if result.CommandID != cmds[0].CommandID {
		t.Fatalf("result command id = %q, want %q", result.CommandID, cmds[0].CommandID)
	}
}

func TestRequestAgentLifecycleQueuesWorkerStopWithoutClaimingItSettled(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st)
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle returned error: %v", err)
	}
	if !result.Pending || result.CommandID == "" {
		t.Fatalf("lifecycle result = %+v, want pending command", result)
	}
	if result.Agent.State != domain.AgentStateIdle || result.Agent.DesiredState != domain.AgentDesiredDraining {
		t.Fatalf("updated agent state = %s/%s, want idle/draining until daemon completion", result.Agent.State, result.Agent.DesiredState)
	}
	cmds, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{
		Status:        domain.AgentCommandQueued,
		TargetAgentID: "desktopqa",
	})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Type != "stop" {
		t.Fatalf("queued commands = %+v, want one stop", cmds)
	}
}

func TestPendingSupervisedLifecycleProjection(t *testing.T) {
	agent := &domain.Agent{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
	}
	tests := []struct {
		command     string
		wantState   domain.AgentState
		wantDesired domain.AgentDesiredState
	}{
		{command: "start", wantState: domain.AgentStateActive, wantDesired: domain.AgentDesiredRunning},
		{command: "restart", wantState: domain.AgentStateIdle, wantDesired: domain.AgentDesiredRunning},
		{command: "stop", wantState: domain.AgentStateActive, wantDesired: domain.AgentDesiredDraining},
		{command: "yield", wantState: domain.AgentStateActive, wantDesired: domain.AgentDesiredDraining},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := pendingSupervisedLifecycle(agent, service.AgentLifecycleInput{
				State:        domain.AgentStateStopped,
				DesiredState: domain.AgentDesiredStopped,
				CommandType:  tt.command,
			})
			if got.State != tt.wantState || got.DesiredState != tt.wantDesired {
				t.Fatalf("pending projection = %s/%s, want %s/%s", got.State, got.DesiredState, tt.wantState, tt.wantDesired)
			}
		})
	}
}

func TestRequestAgentLifecycleLeavesWorkerStateUnchangedWhenCommandCreateFails(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	createErr := errors.New("command store unavailable")
	wrapped := agentCommandOverrideStore{
		Store: st,
		commands: failingAgentCommandCreateStore{
			AgentCommandStore: st.AgentCommands(),
			err:               createErr,
		},
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, wrapped)
	if _, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	}); err == nil {
		t.Fatal("RequestAgentLifecycle error = nil, want command creation failure")
	}
	agent, err := st.Agents().Get(ctx, "TEST2", "desktopqa")
	if err != nil {
		t.Fatalf("get rolled-back agent: %v", err)
	}
	if agent.State != domain.AgentStateIdle || agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent after failed command = %s/%s, want original idle/running", agent.State, agent.DesiredState)
	}
	commands, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("commands after failed create = %+v, want none", commands)
	}
}

func TestRequestAgentLifecycleRecoversCommitThenErrorWithClientCommandID(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	commands := &commitThenErrorAgentCommandStore{
		AgentCommandStore:  st.AgentCommands(),
		createErr:          errors.New("synthetic response lost after commit"),
		transientGetErrors: 2,
	}
	wrapped := agentCommandOverrideStore{
		Store:    st,
		commands: commands,
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, wrapped)
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle returned error after committed Create: %v", err)
	}
	if !result.Pending || result.CommandID == "" {
		t.Fatalf("lifecycle result = %+v, want accepted pending command", result)
	}
	if !strings.HasPrefix(result.CommandID, "agent-lifecycle-") {
		t.Fatalf("command id = %q, want client-generated lifecycle id", result.CommandID)
	}
	if commands.createCalls != 4 || commands.getCalls != 3 {
		t.Fatalf("create/get calls = %d/%d, want same-ID Create retried before each recovery Get", commands.createCalls, commands.getCalls)
	}

	stored, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(stored) != 1 || stored[0].CommandID != result.CommandID {
		t.Fatalf("commands = %+v, want exactly recovered command %q", stored, result.CommandID)
	}
}

func TestRequestAgentLifecycleAnchorsListProofAtRecoveredCursor(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	for i := range 60 {
		if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "TEST2",
			CommandID:     fmt.Sprintf("earlier-%02d", i),
			TargetAgentID: "desktopqa",
			Type:          "stop",
		}); err != nil {
			t.Fatalf("create earlier command %d: %v", i, err)
		}
	}

	commands := &commitThenErrorAgentCommandStore{
		AgentCommandStore: st.AgentCommands(),
		createErr:         errors.New("synthetic response lost after commit"),
		defaultListCap:    50,
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, agentCommandOverrideStore{
		Store: st, commands: commands,
	})
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle with high-cursor recovery: %v", err)
	}
	if !result.Pending || result.CommandID == "" {
		t.Fatalf("lifecycle result = %+v, want accepted high-cursor command", result)
	}
	if commands.listCalls != 1 {
		t.Fatalf("recovery list calls = %d, want 1", commands.listCalls)
	}
	if got := commands.lastListFilter; got.TargetAgentID != "desktopqa" ||
		got.AfterCursor != 60 || got.Limit != 1 {
		t.Fatalf("recovery list filter = %+v, want target desktopqa after_cursor=60 limit=1", got)
	}
}

func TestRequestAgentLifecycleRejectsHashOnlyGetRecovery(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	commands := &hashOnlyGetAgentCommandStore{
		AgentCommandStore: st.AgentCommands(),
		createErr:         errors.New("synthetic index write failure"),
		cursor:            1,
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, agentCommandOverrideStore{
		Store: st, commands: commands,
	})
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	})
	if err == nil {
		t.Fatalf("RequestAgentLifecycle result = %+v, nil; want hash-only admission failure", result)
	}
	var serviceErr *service.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != service.KindUnavailable {
		t.Fatalf("RequestAgentLifecycle error = %v, want unavailable", err)
	}
	if commands.createCalls != 2 || commands.getCalls != 1 || commands.listCalls != 1 {
		t.Fatalf(
			"create/get/list calls = %d/%d/%d, want same-ID repair then Get+List admission proof",
			commands.createCalls,
			commands.getCalls,
			commands.listCalls,
		)
	}
}

func TestCreateSupervisedLifecycleCommandRejectsZeroCursorRecovery(t *testing.T) {
	st := memstore.New()
	commands := &hashOnlyGetAgentCommandStore{
		AgentCommandStore: st.AgentCommands(),
		createErr:         errors.New("synthetic response lost"),
	}
	svc := &agentServiceImpl{}
	command, err := svc.createSupervisedLifecycleCommand(
		context.Background(),
		commands,
		store.AgentCommandCreate{
			WorkspaceKey:  "TEST2",
			CommandID:     "agent-lifecycle-zero-cursor",
			TargetAgentID: "desktopqa",
			Type:          "stop",
		},
	)
	if err == nil {
		t.Fatalf("zero-cursor recovery = %+v, nil; want unavailable", command)
	}
	var serviceErr *service.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != service.KindUnavailable {
		t.Fatalf("zero-cursor recovery error = %v, want unavailable", err)
	}
	if commands.listCalls != 0 {
		t.Fatalf("zero-cursor recovery list calls = %d, want fail closed before List", commands.listCalls)
	}
}

func TestRequestAgentLifecycleRecoversCreateAfterCommandWasAcknowledged(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	commands := &commitThenErrorAgentCommandStore{
		AgentCommandStore: st.AgentCommands(),
		createErr:         errors.New("synthetic response lost after commit"),
		ackAfterCreate:    true,
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, agentCommandOverrideStore{
		Store: st, commands: commands,
	})
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle after progressed Create: %v", err)
	}
	if !result.Pending || result.CommandID == "" || result.Status != domain.AgentCommandAcked {
		t.Fatalf("lifecycle result = %+v, want accepted acknowledged command", result)
	}
	if commands.createCalls != 2 || commands.getCalls != 1 {
		t.Fatalf("create/get calls = %d/%d, want one same-ID retry then recovery Get", commands.createCalls, commands.getCalls)
	}
	persisted, err := st.AgentCommands().Get(ctx, "TEST2", result.CommandID)
	if err != nil {
		t.Fatalf("get recovered command: %v", err)
	}
	if persisted.Status != domain.AgentCommandAcked ||
		persisted.TargetNodeID != "node-bound-after-create" ||
		persisted.AckedBy != "stable-owner" {
		t.Fatalf("recovered command = %+v, want progressed Ack binding preserved", persisted)
	}
}

func TestOverlappingStopStartRequestsCannotRepairOlderProjectionOverNewerTerminalState(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2",
		Name:         "desktopqa",
		RoleName:     "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "TEST2", "desktopqa", store.AgentUpdate{
		State: &active,
	}); err != nil {
		t.Fatalf("set active state: %v", err)
	}

	forbidUpdates := &forbidAgentUpdateStore{AgentStore: st.Agents()}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, agentCreateOverrideStore{
		Store:  st,
		agents: forbidUpdates,
	})
	stopResult, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	})
	if err != nil {
		t.Fatalf("queue stop: %v", err)
	}
	startResult, err := svc.RequestAgentLifecycle(ctx, "TEST2", "desktopqa", service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "start",
	})
	if err != nil {
		t.Fatalf("queue overlapping start: %v", err)
	}
	if !stopResult.Pending || !startResult.Pending {
		t.Fatalf("stop/start results = %+v / %+v, want pending commands", stopResult, startResult)
	}
	if got := forbidUpdates.updateCalls(); got != 0 {
		t.Fatalf("request-side Agent updates = %d, want zero; daemon FIFO owns projection", got)
	}

	// Request handling owns no persistent Agent projection. The daemon applies
	// both terminal states in cursor order, and there is no delayed request-side
	// repair left that can write stopped/stopped after the newer Start settles.
	stopped := domain.AgentStateStopped
	desiredStopped := domain.AgentDesiredStopped
	if _, err := st.Agents().Update(ctx, "TEST2", "desktopqa", store.AgentUpdate{
		State: &stopped, DesiredState: &desiredStopped,
	}); err != nil {
		t.Fatalf("project stop terminal state: %v", err)
	}
	desiredRunning := domain.AgentDesiredRunning
	if _, err := st.Agents().Update(ctx, "TEST2", "desktopqa", store.AgentUpdate{
		State: &active, DesiredState: &desiredRunning,
	}); err != nil {
		t.Fatalf("project start terminal state: %v", err)
	}

	persisted, err := st.Agents().Get(ctx, "TEST2", "desktopqa")
	if err != nil {
		t.Fatalf("get persisted agent: %v", err)
	}
	if persisted.State != domain.AgentStateActive || persisted.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("persisted agent = %s/%s, want newer Start terminal state", persisted.State, persisted.DesiredState)
	}
	queued, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{
		TargetAgentID: "desktopqa",
	})
	if err != nil {
		t.Fatalf("list lifecycle commands: %v", err)
	}
	if len(queued) != 2 || queued[0].Type != "stop" || queued[1].Type != "start" ||
		queued[0].Cursor >= queued[1].Cursor {
		t.Fatalf("queued lifecycle FIFO = %+v, want stop then start", queued)
	}
}

func TestRequestAgentLifecycleStopsInteractiveRuntimeWithoutDaemonCommand(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2", Name: "pr-review", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2", Name: "reviewer", RoleName: "pr-review",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "TEST2", SessionID: "lead-session", AgentID: "reviewer",
		Kind: domain.AgentSessionKindOrchestration, TerminalID: "term_reviewer",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create orchestration session: %v", err)
	}

	key := terminal.SessionKey{Workspace: "TEST2", Name: "term_reviewer"}
	runtime := &fakeInteractiveRuntime{
		live: map[terminal.SessionKey]bool{key: true},
		owned: map[string][]InteractiveRuntimeSession{
			"TEST2\x00reviewer": {{Key: key, Live: true}},
		},
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st, runtime)
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "reviewer", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle returned error: %v", err)
	}
	if result.Pending || result.CommandID != "" {
		t.Fatalf("interactive lifecycle result = %+v, want synchronous completion", result)
	}
	if result.Agent.State != domain.AgentStateStopped || result.Agent.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("updated agent state = %s/%s, want stopped/stopped", result.Agent.State, result.Agent.DesiredState)
	}
	if len(runtime.killed) != 1 || runtime.killed[0] != key {
		t.Fatalf("killed runtimes = %+v, want one lifecycle-boundary kill of %+v", runtime.killed, key)
	}
	session, err := st.AgentSessions().Get(ctx, "TEST2", "lead-session")
	if err != nil {
		t.Fatalf("get orchestration session: %v", err)
	}
	if session.Status != domain.AgentSessionRunning || session.FinishedAt != nil {
		t.Fatalf("orchestration session = %+v, want Interaction-owned lifecycle untouched", session)
	}
	commands, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{
		TargetAgentID: "reviewer",
		Status:        domain.AgentCommandQueued,
	})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("interactive stop queued daemon commands: %+v", commands)
	}
}

func TestRequestAgentLifecycleStopsOwnedPTYBeforeFleetSessionRegistration(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2", Name: "lead", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2", Name: "startup-lead", RoleName: "lead",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	key := terminal.SessionKey{Workspace: "TEST2", Name: "term_starting"}
	runtime := &fakeInteractiveRuntime{
		live: map[terminal.SessionKey]bool{key: true},
		owned: map[string][]InteractiveRuntimeSession{
			"TEST2\x00startup-lead": {{Key: key, Live: true}},
		},
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st, runtime)
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "startup-lead", service.AgentLifecycleInput{
		State: domain.AgentStateStopped, DesiredState: domain.AgentDesiredStopped, CommandType: "stop",
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle returned error: %v", err)
	}
	if result.Pending || result.Agent.State != domain.AgentStateStopped || result.Agent.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("lifecycle result = %+v, want synchronous stopped/stopped", result)
	}
	if len(runtime.killed) != 1 || runtime.killed[0] != key {
		t.Fatalf("startup PTY kills = %+v, want one lifecycle-boundary kill of %+v", runtime.killed, key)
	}
}

func TestRequestAgentLifecycleRestartDelegatesOnceToCanonicalPTYBoundary(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2", Name: "lead", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2", Name: "restart-lead", RoleName: "lead",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "TEST2", SessionID: "session-canonical",
		AgentID: "restart-lead", Kind: domain.AgentSessionKindInteractive,
		TerminalID: "terminal-canonical", Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create canonical interactive session: %v", err)
	}
	key := terminal.SessionKey{Workspace: "TEST2", Name: "term_restart"}
	runtime := &fakeInteractiveRuntime{
		live: map[terminal.SessionKey]bool{key: true},
		owned: map[string][]InteractiveRuntimeSession{
			"TEST2\x00restart-lead": {{
				Key: key, Live: true,
				InteractionSessionID:  "session-canonical",
				InteractionTerminalID: "terminal-canonical",
				StreamRef:             "terminal:TEST2/term_restart",
			}},
		},
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st, runtime)
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "restart-lead", service.AgentLifecycleInput{
		State: domain.AgentStateActive, DesiredState: domain.AgentDesiredRunning,
		CommandType: "restart",
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle restart: %v", err)
	}
	if result.Pending || result.Agent.State != domain.AgentStateActive ||
		result.Agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("restart result = %+v", result)
	}
	if len(runtime.killed) != 1 || runtime.killed[0] != key {
		t.Fatalf("runtime kills = %+v, want one kill of %+v", runtime.killed, key)
	}
}

func TestRequestAgentLifecycleHoldsTerminalBoundaryThroughStoppedStateUpdate(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2", Name: "lead", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2", Name: "racing-lead", RoleName: "lead",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "TEST2", "racing-lead", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("activate agent: %v", err)
	}

	key := terminal.SessionKey{Workspace: "TEST2", Name: "term_racing_lead"}
	runtime := &blockingFirstKillInteractiveRuntime{
		key:              key,
		firstKillStarted: make(chan struct{}),
		releaseFirstKill: make(chan struct{}),
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st, runtime)
	type lifecycleResult struct {
		result *service.AgentLifecycleResult
		err    error
	}
	stopResult := make(chan lifecycleResult, 1)
	go func() {
		result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "racing-lead", service.AgentLifecycleInput{
			State:        domain.AgentStateStopped,
			DesiredState: domain.AgentDesiredStopped,
			CommandType:  "stop",
		})
		stopResult <- lifecycleResult{result: result, err: err}
	}()

	select {
	case <-runtime.firstKillStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not reach the post-ownership-snapshot kill")
	}
	beforeUpdate, err := st.Agents().Get(ctx, "TEST2", "racing-lead")
	if err != nil {
		t.Fatalf("get agent before stopped update: %v", err)
	}
	if beforeUpdate.State != domain.AgentStateActive || beforeUpdate.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent before Stop update = %+v, want active/running", beforeUpdate)
	}

	lockAttempted := make(chan struct{})
	lockAcquired := make(chan struct{})
	releaseContender := make(chan struct{})
	go func() {
		close(lockAttempted)
		unlock := terminal.LockAgentLifecycle("TEST2", "racing-lead")
		close(lockAcquired)
		<-releaseContender
		unlock()
	}()
	<-lockAttempted
	select {
	case <-lockAcquired:
		close(releaseContender)
		close(runtime.releaseFirstKill)
		t.Fatal("terminal operation entered after Stop's key snapshot but before stopped persisted")
	case <-time.After(200 * time.Millisecond):
	}

	close(runtime.releaseFirstKill)
	var result lifecycleResult
	select {
	case result = <-stopResult:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish after the first kill was released")
	}
	if result.err != nil {
		t.Fatalf("RequestAgentLifecycle returned error: %v", result.err)
	}
	if result.result.Pending || result.result.Agent.State != domain.AgentStateStopped || result.result.Agent.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("lifecycle result = %+v, want synchronous stopped/stopped", result.result)
	}
	select {
	case <-lockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal operation did not enter after Stop released the lifecycle boundary")
	}
	afterUpdate, err := st.Agents().Get(ctx, "TEST2", "racing-lead")
	if err != nil {
		t.Fatalf("get agent after stopped update: %v", err)
	}
	if afterUpdate.State != domain.AgentStateActive || afterUpdate.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("agent visible at terminal boundary = %+v, want runtime state untouched and desired stopped", afterUpdate)
	}
	close(releaseContender)
	if len(runtime.killed) != 1 || runtime.killed[0] != key {
		t.Fatalf("runtime kills = %+v, want one lifecycle-boundary kill of %+v", runtime.killed, key)
	}
}

func TestRequestAgentLifecycleStartsInteractiveWithoutDaemonCommandAndRejectsYield(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2", Name: "lead", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2", Name: "ui-lead", RoleName: "lead",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st, nil)
	result, err := svc.RequestAgentLifecycle(ctx, "TEST2", "ui-lead", service.AgentLifecycleInput{
		State:        domain.AgentStateActive,
		DesiredState: domain.AgentDesiredRunning,
		CommandType:  "start",
	})
	if err != nil {
		t.Fatalf("interactive Start returned error: %v", err)
	}
	if result.Pending || result.Agent.State != domain.AgentStateActive || result.Agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("lifecycle result after Start = %+v, want synchronous active/running", result)
	}

	if _, err := svc.RequestAgentLifecycle(ctx, "TEST2", "ui-lead", service.AgentLifecycleInput{
		State:        domain.AgentStateIdle,
		DesiredState: domain.AgentDesiredDraining,
		CommandType:  "yield",
	}); err == nil {
		t.Fatal("interactive Yield error = nil, want clear unsupported-action error")
	}
	unchanged, err := st.Agents().Get(ctx, "TEST2", "ui-lead")
	if err != nil {
		t.Fatalf("get agent after Yield: %v", err)
	}
	if unchanged.State != domain.AgentStateIdle || unchanged.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent changed after rejected Yield: %+v", unchanged)
	}
	commands, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{
		TargetAgentID: "ui-lead",
		Status:        domain.AgentCommandQueued,
	})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("interactive Start/Yield queued daemon commands: %+v", commands)
	}
}

func TestRequestAgentLifecycleRefusesUnownedInteractiveRuntime(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2", Name: "custom", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2", Name: "custom-agent", RoleName: "custom",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "TEST2", "custom-agent", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("mark agent active: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "TEST2", SessionID: "remote-session", AgentID: "custom-agent",
		Kind: domain.AgentSessionKindOrchestration, TerminalID: "term_remote",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create orchestration session: %v", err)
	}

	runtime := &fakeInteractiveRuntime{
		live:  map[terminal.SessionKey]bool{},
		owned: map[string][]InteractiveRuntimeSession{},
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st, runtime)
	if _, err := svc.RequestAgentLifecycle(ctx, "TEST2", "custom-agent", service.AgentLifecycleInput{
		State: domain.AgentStateStopped, DesiredState: domain.AgentDesiredStopped, CommandType: "stop",
	}); err == nil {
		t.Fatal("RequestAgentLifecycle error = nil, want unowned-runtime conflict")
	}

	agent, err := st.Agents().Get(ctx, "TEST2", "custom-agent")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.State != domain.AgentStateActive || agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent changed after refused stop: %+v", agent)
	}
	session, err := st.AgentSessions().Get(ctx, "TEST2", "remote-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != domain.AgentSessionRunning || session.FinishedAt != nil {
		t.Fatalf("session changed after refused stop: %+v", session)
	}
}

func TestRequestAgentLifecycleRejectsForgedCrossAgentTerminalID(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "TEST2", Name: "custom", Kind: string(domain.RoleKindInteractive),
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	for _, name := range []string{"agent-a", "agent-b"} {
		if _, err := st.Agents().Create(ctx, store.AgentCreate{
			WorkspaceKey: "TEST2", Name: name, RoleName: "custom",
			DesiredState: domain.AgentDesiredRunning,
		}); err != nil {
			t.Fatalf("create agent %s: %v", name, err)
		}
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "TEST2", "agent-a", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("mark agent-a active: %v", err)
	}

	victimKey := terminal.SessionKey{Workspace: "TEST2", Name: "term_agent_b"}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "TEST2", SessionID: "forged-a-session", AgentID: "agent-a",
		Kind: domain.AgentSessionKindOrchestration, TerminalID: victimKey.Name,
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create forged session: %v", err)
	}
	runtime := &fakeInteractiveRuntime{
		live: map[terminal.SessionKey]bool{victimKey: true},
		owned: map[string][]InteractiveRuntimeSession{
			"TEST2\x00agent-b": {{Key: victimKey, Live: true}},
		},
	}
	svc := newAuthorizedTestAgentService(t, nil, nil, nil, st, runtime)
	if _, err := svc.RequestAgentLifecycle(ctx, "TEST2", "agent-a", service.AgentLifecycleInput{
		State: domain.AgentStateStopped, DesiredState: domain.AgentDesiredStopped, CommandType: "stop",
	}); err == nil {
		t.Fatal("RequestAgentLifecycle error = nil, want ownership conflict")
	}
	if len(runtime.killed) != 0 || !runtime.live[victimKey] {
		t.Fatalf("victim runtime was affected: killed=%+v live=%v", runtime.killed, runtime.live[victimKey])
	}
	agent, err := st.Agents().Get(ctx, "TEST2", "agent-a")
	if err != nil {
		t.Fatalf("get agent-a: %v", err)
	}
	if agent.State != domain.AgentStateActive || agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent-a changed after forged ownership conflict: %+v", agent)
	}
}

func TestInteractiveRuntimeControllerBindsPTYsThroughAgentTabMetadata(t *testing.T) {
	ctx := context.Background()
	keyA := terminal.SessionKey{Workspace: "TEST2", Name: "term_a"}
	keyB := terminal.SessionKey{Workspace: "TEST2", Name: "term_b"}
	ptys := &fakeInteractiveRuntime{
		live: map[terminal.SessionKey]bool{keyA: true, keyB: true},
	}
	controller := NewInteractiveRuntimeController(fakeInteractiveTabSource{tabs: []InteractiveRuntimeTab{
		{SessionName: keyA.Name, Kind: "agent", AgentID: "agent-a", PTYAlive: true},
		{SessionName: keyB.Name, Kind: "agent", AgentID: "agent-b", PTYAlive: true},
	}}, ptys)

	owned, err := controller.OwnedAgentSessions(ctx, "TEST2", "agent-a")
	if err != nil {
		t.Fatalf("OwnedAgentSessions: %v", err)
	}
	if len(owned) != 1 || owned[0].Key != keyA || !owned[0].Live {
		t.Fatalf("agent-a owned sessions = %+v, want only %+v", owned, keyA)
	}
}
