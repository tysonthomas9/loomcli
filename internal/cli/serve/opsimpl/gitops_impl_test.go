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
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

func runtimeAgent(t *testing.T, workspace, name, role string, repos, groups []string) *agents.Agent {
	t.Helper()
	metadata, err := agents.WithRuntimeMetadata(nil, agents.RuntimeMetadata{Repos: repos, RepoGroups: groups})
	if err != nil {
		t.Fatalf("runtime metadata: %v", err)
	}
	return &agents.Agent{
		WorkspaceKey: workspace,
		AgentID:      name,
		Name:         name,
		Behavior:     agents.BehaviorReference{RoleName: role},
		DesiredState: agents.DesiredRunning,
		MaxInstances: 1,
		Metadata:     metadata,
	}
}

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
	nova := runtimeAgent(t, "WS1", "nova", "task", nil, []string{"backend"})

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

	got, err := NewGitOps().WithStore(st).WithAgentQueries(testutil.StaticAgentQueries{Agents: []*agents.Agent{nova}}).ResolveAgentWorktree("WS1", "nova")
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
	nova := runtimeAgent(t, "WS1", "nova", "task", nil, []string{"backend"})
	any := runtimeAgent(t, "WS1", "any", "task", nil, nil)

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

	g := NewGitOps().WithStore(st).WithAgentQueries(testutil.StaticAgentQueries{Agents: []*agents.Agent{nova, any}})
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
	broken := runtimeAgent(t, "WS1", "broken", "task", nil, []string{"backend"})

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

	g := NewGitOps().WithStore(st).WithAgentQueries(testutil.StaticAgentQueries{Agents: []*agents.Agent{broken}})
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
