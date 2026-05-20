package opsimpl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
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
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Version:       1,
		LastWorkspace: "WS1",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS1": {
				Path:  wsRoot,
				Repos: map[string]string{"api": filepath.Join(wsRoot, "api")},
			},
		},
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
	list, err := NewGitOps().WithStore(st).ListAgentWorktrees("WS1")
	if err != nil {
		t.Fatalf("ListAgentWorktrees: %v", err)
	}
	if len(list) != 1 || list[0].Name != "nova" || list[0].Path != wtPath {
		t.Fatalf("ListAgentWorktrees = %+v", list)
	}
}

func TestResolveAgentWorktreeStoreBackedErrorBranches(t *testing.T) {
	ctx := context.Background()
	if _, err := (*GitOpsImpl)(nil).resolveAgentWorktreeFromStore(ctx, "WS", "nova"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nil GitOps error = %v, want ErrNotFound", err)
	}
	if _, err := (&GitOpsImpl{}).resolveAgentWorktreeFromStore(ctx, "WS", "nova"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nil store error = %v, want ErrNotFound", err)
	}
	if _, err := NewGitOps().WithStore(memstore.New()).resolveAgentWorktreeFromStore(ctx, "", "nova"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty workspace error = %v, want ErrNotFound", err)
	}
	if _, err := NewGitOps().WithStore(memstore.New()).resolveAgentWorktreeFromStore(ctx, "WS", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty agent error = %v, want ErrNotFound", err)
	}

	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	wsRoot := t.TempDir()
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Version: 1,
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: wsRoot},
		},
	}); err != nil {
		t.Fatalf("save empty state cache: %v", err)
	}

	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	g := NewGitOps().WithStore(st)
	if _, err := g.resolveAgentWorktreeFromStore(ctx, "MISSING", "nova"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing workspace error = %v, want ErrNotFound", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := g.resolveAgentWorktreeFromStore(ctx, "WS", "nova"); err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("missing agent error = %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "WS", Name: "nova", RoleName: "task"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := g.resolveAgentWorktreeFromStore(ctx, "WS", "nova"); err == nil || !strings.Contains(err.Error(), "no repos") {
		t.Fatalf("no repos error = %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS", Name: "api", DefaultBranch: "", Remote: "origin"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := g.resolveAgentWorktreeFromStore(ctx, "WS", "nova"); err == nil || !strings.Contains(err.Error(), "not checked out") {
		t.Fatalf("missing checkout error = %v", err)
	}
	if _, err := g.ListAgentWorktrees("MISSING"); err == nil || !strings.Contains(err.Error(), "load fleet-db workspace") {
		t.Fatalf("ListAgentWorktrees missing workspace error = %v", err)
	}
}

func TestGitOpsPureHelpers(t *testing.T) {
	st := memstore.New()
	if got := NewGitOps().WithStore(st); got.store != st {
		t.Fatal("WithStore did not install store")
	}

	repos := []ops.WorkspaceRepo{
		{Name: "api", Groups: []string{"backend"}, DefaultBranch: "trunk", Remote: "upstream"},
		{Name: "ui", Groups: []string{"frontend"}},
	}
	repo, err := selectAgentRepo(repos, ops.WorkspaceAgentInfo{Name: "nova"})
	if err != nil || repo.Name != "api" {
		t.Fatalf("default repo = %+v err=%v", repo, err)
	}
	repo, err = selectAgentRepo(repos, ops.WorkspaceAgentInfo{Name: "nova", Repos: []string{"ui"}})
	if err != nil || repo.Name != "ui" {
		t.Fatalf("explicit repo = %+v err=%v", repo, err)
	}
	repo, err = selectAgentRepo(repos, ops.WorkspaceAgentInfo{Name: "nova", RepoGroups: []string{"backend"}})
	if err != nil || repo.Name != "api" {
		t.Fatalf("group repo = %+v err=%v", repo, err)
	}
	if _, err := selectAgentRepo(repos, ops.WorkspaceAgentInfo{Name: "nova", Repos: []string{"missing"}}); err == nil {
		t.Fatal("missing repo affinity should fail")
	}
	if _, err := selectAgentRepo(nil, ops.WorkspaceAgentInfo{Name: "nova"}); err == nil {
		t.Fatal("empty workspace repos should fail")
	}

	wt := toAgentWorktree(cli.WorktreeInfo{
		Name: "nova", Path: "/tmp/nova", Branch: "feature",
		Repo: &config.RepoConfig{Name: "api", DefaultBranch: "trunk", Remote: "upstream"},
	})
	if wt.DefaultBranch != "trunk" || wt.Remote != "upstream" || wt.RepoName != "api" || !wt.IsWorkspace {
		t.Fatalf("agent worktree = %+v", wt)
	}
	plain := toAgentWorktree(cli.WorktreeInfo{Name: "plain", Path: "/tmp/plain", Branch: "main"})
	if plain.DefaultBranch != "main" || plain.IsWorkspace {
		t.Fatalf("plain worktree = %+v", plain)
	}

	var target *git.LockedError
	if !isLockedError(&git.LockedError{AgentName: "nova", Duration: time.Second}, &target) || target.AgentName != "nova" {
		t.Fatalf("locked error target = %+v", target)
	}
	if isLockedError(errors.New("other"), &target) {
		t.Fatal("non locked error matched")
	}
}

func TestGitOpsStatusAndDiffWrappersWithRealRepo(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(t, repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := runGit(t, repo, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := runGit(t, repo, "config", "user.name", "Test User"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := runGit(t, repo, "add", "a.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(t, repo, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	beforeCmd := exec.Command("git", "rev-parse", "HEAD") //nolint:norawexec // test reads HEAD from an isolated git repo.
	beforeCmd.Dir = repo
	beforeOut, err := beforeCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatalf("update a: %v", err)
	}
	if err := runGit(t, repo, "add", "a.txt"); err != nil {
		t.Fatalf("git add update: %v", err)
	}
	if err := runGit(t, repo, "commit", "-m", "update"); err != nil {
		t.Fatalf("git commit update: %v", err)
	}

	g := NewGitOps()
	branch, err := g.GetCurrentBranch(repo)
	if err != nil || branch != "main" {
		t.Fatalf("GetCurrentBranch = %q err=%v", branch, err)
	}
	stat := g.DiffStat(repo, string(beforeOut[:len(beforeOut)-1]))
	if stat.FilesChanged != 1 || stat.LinesAdded != 1 {
		t.Fatalf("DiffStat = %+v", stat)
	}
	status, err := g.Status(repo, "main")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Branch != "main" || !status.IsClean || status.HasConflicts {
		t.Fatalf("Status = %+v", status)
	}
	commits, err := g.DiffCommits(context.Background(), repo, string(beforeOut[:len(beforeOut)-1]), 10)
	if err != nil {
		t.Fatalf("DiffCommits: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("commits = %+v", commits)
	}
	files, err := g.DiffFiles(context.Background(), repo, string(beforeOut[:len(beforeOut)-1]), "HEAD")
	if err != nil {
		t.Fatalf("DiffFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("files = %+v", files)
	}
	patch, err := g.DiffFilePatch(context.Background(), repo, string(beforeOut[:len(beforeOut)-1]), "HEAD", "a.txt")
	if err != nil {
		t.Fatalf("DiffFilePatch: %v", err)
	}
	if patch == nil || patch.Patch == "" {
		t.Fatalf("patch = %+v", patch)
	}
}

func TestGitOpsMutationWrappersWithRealRemote(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "origin.git")
	if err := runGit(t, remote, "init", "--bare", "-b", "main"); err != nil {
		t.Fatalf("git init bare: %v", err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	clone := exec.Command("git", "clone", remote, repo) //nolint:norawexec // isolated test repository
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v output=%s", err, out)
	}
	if err := runGit(t, repo, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := runGit(t, repo, "config", "user.name", "Test User"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := runGit(t, repo, "add", "a.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(t, repo, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	if err := runGit(t, repo, "push", "-u", "origin", "main"); err != nil {
		t.Fatalf("git push main: %v", err)
	}
	if err := runGit(t, repo, "checkout", "-b", "feature"); err != nil {
		t.Fatalf("git checkout feature: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\nfeature\n"), 0600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	if err := runGit(t, repo, "commit", "-am", "feature"); err != nil {
		t.Fatalf("git commit feature: %v", err)
	}

	g := NewGitOps()
	push, err := g.Push(repo, "feature", "main", "origin")
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !push.Success {
		t.Fatalf("Push result = %+v", push)
	}
	pr, err := g.CreatePR(repo, "main", "main", "origin")
	if err != nil {
		t.Fatalf("CreatePR no-commits path: %v", err)
	}
	if !pr.NoCommits {
		t.Fatalf("CreatePR no-commits result = %+v", pr)
	}

	if err := runGit(t, repo, "checkout", "-b", "other", "origin/main"); err != nil {
		t.Fatalf("git checkout other: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("other\n"), 0600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if err := runGit(t, repo, "add", "b.txt"); err != nil {
		t.Fatalf("git add b: %v", err)
	}
	if err := runGit(t, repo, "commit", "-m", "other"); err != nil {
		t.Fatalf("git commit other: %v", err)
	}
	if err := runGit(t, repo, "push", "-u", "origin", "other"); err != nil {
		t.Fatalf("git push other: %v", err)
	}
	if err := runGit(t, repo, "checkout", "feature"); err != nil {
		t.Fatalf("git checkout feature again: %v", err)
	}
	pull, err := g.Pull(repo, "feature", "other", "origin")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !pull.Success {
		t.Fatalf("Pull result = %+v", pull)
	}
	reset, err := g.Reset(repo, "feature", "main", true, false)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !reset.Success || reset.PreviousBranch != "feature" {
		t.Fatalf("Reset result = %+v", reset)
	}
	if mergeBase, err := g.ResolveMergeBase(repo, "main"); err != nil || mergeBase == "" {
		t.Fatalf("ResolveMergeBase = %q err=%v", mergeBase, err)
	}
	_ = g.CheckGhInstalled()
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
