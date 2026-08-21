package opsimpl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// --- resolveWorkspaceConfigName tests ---

func TestResolveWorkspaceConfigName_DirectNameMatch(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "dev")
	if got != "dev" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'dev') = %q, want %q", got, "dev")
	}
}

func TestResolveWorkspaceConfigName_UUIDMatch(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"staging": {ID: "uuid-staging-123", Path: "/tmp/staging"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "uuid-staging-123")
	if got != "staging" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'uuid-staging-123') = %q, want %q", got, "staging")
	}
}

func TestResolveWorkspaceConfigName_EmptyString(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, '') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_UnknownID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "nonexistent-id")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'nonexistent-id') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_NilConfig(t *testing.T) {
	got := resolveWorkspaceConfigName(nil, "anything")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(nil, 'anything') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_NilWorkspacesMap(t *testing.T) {
	cfg := &config.LoomConfig{}

	got := resolveWorkspaceConfigName(cfg, "dev")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'dev') = %q, want empty", got)
	}
}

func TestDedupeRepoPRQueriesUsesRemoteURLIdentity(t *testing.T) {
	repos := []ops.WorkspaceRepo{
		{Name: "one", Path: "/tmp/one", Remote: "origin", RemoteURL: "https://github.com/acme/one.git"},
		{Name: "two", Path: "/tmp/two", Remote: "origin", RemoteURL: "https://github.com/acme/two.git"},
	}

	queries := dedupeRepoPRQueries(repos)
	if len(queries) != 2 {
		t.Fatalf("queries = %d, want one per remote URL", len(queries))
	}
}

func TestDedupeRepoPRQueriesNormalizesGitHubRemoteURL(t *testing.T) {
	repos := []ops.WorkspaceRepo{
		{Name: "one", Path: "/tmp/one", RemoteURL: "https://github.com/Acme/Repo.git"},
		{Name: "two", Path: "/tmp/two", RemoteURL: "https://github.com/acme/repo"},
	}

	queries := dedupeRepoPRQueries(repos)
	if len(queries) != 1 {
		t.Fatalf("queries = %d, want equivalent GitHub URLs deduplicated", len(queries))
	}
}

func TestCollectRepoQueryPRsAddsWorkspaceSourceRepo(t *testing.T) {
	queries := []*prRepoQuery{{
		repo: ops.WorkspaceRepo{
			Name:      "loomcli",
			RemoteURL: "https://github.com/tysonthomas9/loomcli.git",
		},
		prs: []ops.GitPullRequest{{
			Number: 205,
			URL:    "https://github.com/tysonthomas9/loomcli/pull/205",
		}},
	}}

	got := collectRepoQueryPRs(queries, &ops.GitPullRequestList{})
	if len(got) != 1 {
		t.Fatalf("pull requests = %+v, want one", got)
	}
	if got[0].RepoName != "tysonthomas9/loomcli" {
		t.Fatalf("repo_name = %q, want GitHub owner/repo", got[0].RepoName)
	}
	if got[0].SourceRepo != "loomcli" {
		t.Fatalf("source_repo = %q, want workspace repo name", got[0].SourceRepo)
	}
}

func TestCollectRepoQueryPRsLeavesSourceRepoEmptyWhenWorkspaceRepoUnknown(t *testing.T) {
	queries := []*prRepoQuery{{
		prs: []ops.GitPullRequest{{
			Number: 7,
			URL:    "https://github.com/octocat/hello/pull/7",
		}},
	}}

	got := collectRepoQueryPRs(queries, &ops.GitPullRequestList{})
	if len(got) != 1 {
		t.Fatalf("pull requests = %+v, want one", got)
	}
	if got[0].RepoName != "octocat/hello" {
		t.Fatalf("repo_name = %q, want GitHub owner/repo derived from PR URL", got[0].RepoName)
	}
	if got[0].SourceRepo != "" {
		t.Fatalf("source_repo = %q, want empty for unknown workspace repo", got[0].SourceRepo)
	}
}

// --- scopeResolverToWorkspace tests ---

func TestScopeResolverToWorkspace_EmptyWorkspaceID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Workspace should remain unchanged (no-op).
	if resolver.WorkspaceName() != "dev" {
		t.Errorf("workspace = %q, want %q (should be unchanged)", resolver.WorkspaceName(), "dev")
	}
}

func TestScopeResolverToWorkspace_ValidWorkspaceName(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev":     {ID: "uuid-dev", Path: "/tmp/dev"},
			"staging": {ID: "uuid-staging", Path: "/tmp/staging"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolver.WorkspaceName() != "staging" {
		t.Errorf("workspace = %q, want %q", resolver.WorkspaceName(), "staging")
	}
}

func TestScopeResolverToWorkspace_ValidUUID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev":     {ID: "uuid-dev", Path: "/tmp/dev"},
			"staging": {ID: "uuid-staging-456", Path: "/tmp/staging"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "uuid-staging-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolver.WorkspaceName() != "staging" {
		t.Errorf("workspace = %q, want %q", resolver.WorkspaceName(), "staging")
	}
}

func TestScopeResolverToWorkspace_UnknownWorkspaceID(t *testing.T) {
	cfg := &config.LoomConfig{
		Workspaces: map[string]config.WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}
	resolver := &cli.Resolver{
		Mode:      cli.ModeWorkspace,
		Config:    cfg,
		Workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "nonexistent-ws")
	if err == nil {
		t.Fatal("expected error for unknown workspace ID, got nil")
	}
}

func TestResolveAgentWorktree_StoreBackedFleetDB(t *testing.T) {
	ctx := context.Background()
	loomDir := t.TempDir()
	wsRoot := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS1",
		Name:          "api",
		DefaultBranch: "main",
		Remote:        "origin",
		Groups:        []string{"backend"},
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS1", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "nova",
		RoleName:     "task",
		RepoGroups:   []string{"backend"},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	wtPath := filepath.Join(wsRoot, "worktrees", "api", "nova")
	if err := runGit(t, wtPath, "init", "-b", "feature/nova"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{
			Path:  wsRoot,
			Repos: map[string]string{"api": filepath.Join(wsRoot, "api")},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	got, err := NewGitOps().WithStore(st).ResolveAgentWorktree("WS1", "nova")
	if err != nil {
		t.Fatalf("ResolveAgentWorktree: %v", err)
	}
	if got.Path != wtPath {
		t.Fatalf("Path = %q, want %q", got.Path, wtPath)
	}
	if got.RepoName != "api" || got.DefaultBranch != "main" || got.Branch != "feature/nova" || !got.IsWorkspace {
		t.Fatalf("unexpected worktree: %+v", got)
	}
}

func TestResolveAgentWorktreeForRepo_StoreBackedFleetDB(t *testing.T) {
	ctx := context.Background()
	loomDir := t.TempDir()
	wsRoot := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, repo := range []store.RepoCreate{
		{WorkspaceKey: "WS1", Name: "api", DefaultBranch: "main", Remote: "origin", Groups: []string{"backend"}},
		{WorkspaceKey: "WS1", Name: "docs", DefaultBranch: "main", Remote: "origin", Groups: []string{"docs"}},
	} {
		if _, err := st.Repos().Create(ctx, repo); err != nil {
			t.Fatalf("create repo %s: %v", repo.Name, err)
		}
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS1", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "nova",
		RoleName:     "task",
		RepoGroups:   []string{"backend"},
	}); err != nil {
		t.Fatalf("create nova: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "any",
		RoleName:     "task",
	}); err != nil {
		t.Fatalf("create any: %v", err)
	}

	novaAPIPath := filepath.Join(wsRoot, "worktrees", "api", "nova")
	if err := runGit(t, novaAPIPath, "init", "-b", "feature/nova"); err != nil {
		t.Fatalf("git init nova api: %v", err)
	}
	anyDocsPath := filepath.Join(wsRoot, "worktrees", "docs", "any")
	if err := runGit(t, anyDocsPath, "init", "-b", "feature/any-docs"); err != nil {
		t.Fatalf("git init any docs: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{
			Path: wsRoot,
			Repos: map[string]string{
				"api":  filepath.Join(wsRoot, "api"),
				"docs": filepath.Join(wsRoot, "docs"),
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	g := NewGitOps().WithStore(st)
	got, err := g.ResolveAgentWorktreeForRepo("WS1", "nova", "api")
	if err != nil {
		t.Fatalf("ResolveAgentWorktreeForRepo nova/api: %v", err)
	}
	if got.Path != novaAPIPath || got.RepoName != "api" || got.Branch != "feature/nova" {
		t.Fatalf("nova/api = %+v, want path %q repo api branch feature/nova", got, novaAPIPath)
	}

	got, err = g.ResolveAgentWorktreeForRepo("WS1", "any", "docs")
	if err != nil {
		t.Fatalf("ResolveAgentWorktreeForRepo any/docs: %v", err)
	}
	if got.Path != anyDocsPath || got.RepoName != "docs" {
		t.Fatalf("any/docs = %+v, want path %q repo docs", got, anyDocsPath)
	}

	if _, err := g.ResolveAgentWorktreeForRepo("WS1", "nova", "docs"); !errors.Is(err, ops.ErrAgentRepoNotAllowed) {
		t.Fatalf("nova/docs err = %v, want ErrAgentRepoNotAllowed", err)
	}
	if _, err := g.ResolveAgentWorktreeForRepo("WS1", "nova", "missing"); !errors.Is(err, ops.ErrAgentRepoNotAllowed) {
		t.Fatalf("nova/missing err = %v, want ErrAgentRepoNotAllowed", err)
	}
	if _, err := g.ResolveAgentWorktreeForRepo("WS1", "any", "api"); !errors.Is(err, ops.ErrAgentWorktreeNotFound) {
		t.Fatalf("any/api err = %v, want ErrAgentWorktreeNotFound", err)
	}
}

func TestResolveAgentWorktree_BrokenGitMetadataReturnsUnknownBranch(t *testing.T) {
	ctx := context.Background()
	loomDir := t.TempDir()
	wsRoot := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS1",
		Name:          "api",
		DefaultBranch: "main",
		Remote:        "origin",
		Groups:        []string{"backend"},
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS1", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS1",
		Name:         "broken",
		RoleName:     "task",
		RepoGroups:   []string{"backend"},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	wtPath := filepath.Join(wsRoot, "worktrees", "api", "broken")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	missingAdminDir := filepath.Join(wsRoot, ".git", "worktrees", "broken")
	if err := os.WriteFile(filepath.Join(wtPath, ".git"), []byte("gitdir: "+missingAdminDir+"\n"), 0644); err != nil {
		t.Fatalf("write broken git pointer: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.LastWorkspace = "WS1"
		sc.Workspaces["WS1"] = bootstrap.WorkspaceLocalState{
			Path:  wsRoot,
			Repos: map[string]string{"api": filepath.Join(wsRoot, "api")},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	g := NewGitOps().WithStore(st)
	for name, resolve := range map[string]func() (*ops.AgentWorktree, error){
		"ResolveAgentWorktree": func() (*ops.AgentWorktree, error) {
			return g.ResolveAgentWorktree("WS1", "broken")
		},
		"ResolveAgentWorktreeForRepo": func() (*ops.AgentWorktree, error) {
			return g.ResolveAgentWorktreeForRepo("WS1", "broken", "api")
		},
	} {
		got, err := resolve()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Path != wtPath || got.Branch != "unknown" || got.RepoName != "api" {
			t.Fatalf("%s = %+v, want path %q branch unknown repo api", name, got, wtPath)
		}
	}
}

func TestResolveWorkspaceData_StoreBackedUsesTopologyOnly(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	base := memstore.New()
	for _, ws := range []string{"WS1", "WS2", "WS3"} {
		if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
			t.Fatalf("create workspace %s: %v", ws, err)
		}
		if _, err := base.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: ws, Name: "api"}); err != nil {
			t.Fatalf("create repo %s/api: %v", ws, err)
		}
		if _, err := base.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: ws, Name: "task"}); err != nil {
			t.Fatalf("create role %s/task: %v", ws, err)
		}
		if _, err := base.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: ws, Name: "nova", RoleName: "task"}); err != nil {
			t.Fatalf("create agent %s/nova: %v", ws, err)
		}
	}

	counted := newCountingTopologyStore(base)
	got, err := NewGitOps().WithStore(counted).ResolveWorkspaceData("WS1")
	if err != nil {
		t.Fatalf("ResolveWorkspaceData: %v", err)
	}
	if got.ID != "WS1" || len(got.Workspaces) != 0 {
		t.Fatalf("workspace data = id:%q summaries:%d, want WS1 with no switcher summaries", got.ID, len(got.Workspaces))
	}
	if counted.workspaces.get != 1 {
		t.Fatalf("Workspaces.Get count = %d, want 1", counted.workspaces.get)
	}
	if counted.workspaces.list != 0 {
		t.Fatalf("Workspaces.List count = %d, want 0", counted.workspaces.list)
	}
	if counted.daemon.get != 0 {
		t.Fatalf("Daemon.Get count = %d, want 0", counted.daemon.get)
	}
	if got := counted.repos.listByWorkspace["WS1"]; got != 1 {
		t.Fatalf("Repos.List[WS1] count = %d, want 1", got)
	}
	if got := counted.agents.listByWorkspace["WS1"]; got != 1 {
		t.Fatalf("Agents.List[WS1] count = %d, want 1", got)
	}
	if totalCount(counted.repos.listByWorkspace) != 1 {
		t.Fatalf("Repos.List counts = %+v, want only active workspace", counted.repos.listByWorkspace)
	}
	if totalCount(counted.agents.listByWorkspace) != 1 {
		t.Fatalf("Agents.List counts = %+v, want only active workspace", counted.agents.listByWorkspace)
	}
}

type countingTopologyStore struct {
	store.Store
	workspaces *countingWorkspaceStore
	repos      *countingRepoStore
	agents     *countingAgentStore
	daemon     *countingDaemonStore
}

func newCountingTopologyStore(base store.Store) *countingTopologyStore {
	return &countingTopologyStore{
		Store:      base,
		workspaces: &countingWorkspaceStore{WorkspaceStore: base.Workspaces()},
		repos:      &countingRepoStore{RepoStore: base.Repos(), listByWorkspace: map[string]int{}},
		agents:     &countingAgentStore{AgentStore: base.Agents(), listByWorkspace: map[string]int{}},
		daemon:     &countingDaemonStore{DaemonProfileStore: base.Daemon()},
	}
}

func (s *countingTopologyStore) Workspaces() store.WorkspaceStore { return s.workspaces }
func (s *countingTopologyStore) Repos() store.RepoStore           { return s.repos }
func (s *countingTopologyStore) Agents() store.AgentStore         { return s.agents }
func (s *countingTopologyStore) Daemon() store.DaemonProfileStore { return s.daemon }

type countingWorkspaceStore struct {
	store.WorkspaceStore
	get  int
	list int
}

func (s *countingWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	s.get++
	return s.WorkspaceStore.Get(ctx, key)
}

func (s *countingWorkspaceStore) List(ctx context.Context) ([]*domain.Workspace, error) {
	s.list++
	return s.WorkspaceStore.List(ctx)
}

type countingRepoStore struct {
	store.RepoStore
	listByWorkspace map[string]int
}

func (s *countingRepoStore) List(ctx context.Context, workspaceKey string) ([]*domain.Repo, error) {
	s.listByWorkspace[workspaceKey]++
	return s.RepoStore.List(ctx, workspaceKey)
}

type countingAgentStore struct {
	store.AgentStore
	listByWorkspace map[string]int
}

func (s *countingAgentStore) List(ctx context.Context, workspaceKey string) ([]*domain.Agent, error) {
	s.listByWorkspace[workspaceKey]++
	return s.AgentStore.List(ctx, workspaceKey)
}

type countingDaemonStore struct {
	store.DaemonProfileStore
	get int
}

func (s *countingDaemonStore) Get(ctx context.Context, workspaceKey string) (*domain.DaemonProfile, error) {
	s.get++
	return s.DaemonProfileStore.Get(ctx, workspaceKey)
}

func totalCount(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func runGit(t *testing.T, dir string, args ...string) error {
	t.Helper()
	if args[0] == "init" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	cmd := exec.Command("git", args...) //nolint:norawexec // test helper uses fixed git command with caller-supplied args
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("git %v output: %s", args, out)
	}
	return err
}

func TestGitShowFileAtRevPreservesNotFoundKind(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if err := runGit(t, repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("tracked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-m", "seed"}} {
		if err := runGit(t, repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("untracked\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := NewGitOps().GitShowFileAtRev(context.Background(), repo, "HEAD", "untracked.txt", 1024)
	var inspectionErr interface{ InspectionKind() string }
	if !errors.As(err, &inspectionErr) || inspectionErr.InspectionKind() != "not_found" {
		t.Fatalf("error = %T %v, want preserved not_found inspection kind", err, err)
	}
}
