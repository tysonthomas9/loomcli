package storeadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// gitCheckout creates a git work tree at dir (git init) and, when originURL is
// non-empty, sets its origin remote — enough for GitRemoteURL verification.
func gitCheckout(t *testing.T, dir, originURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...) //nolint:norawexec // fixed test helper command (git in a temp dir).
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if originURL != "" {
		run("remote", "add", "origin", originURL)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func loadLocal(t *testing.T, key string) bootstrap.WorkspaceLocalState {
	t.Helper()
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	return sc.Workspaces[key]
}

func TestResolveOrHealWorkspacePath_BindsOnMatchingRemote(t *testing.T) {
	requireGit(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	const url = "https://github.com/owner/repo.git"

	st := memstore.New()
	mustCreateWS(t, ctx, st, "WS1", "demo")
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{WorkspaceKey: "WS1", Name: "repo", Remote: "origin", RemoteURL: url})

	wsDir := filepath.Join(bootstrap.LoomDir(), "workspaces", "demo")
	repoDir := filepath.Join(wsDir, "repo")
	gitCheckout(t, repoDir, url)

	got := ResolveOrHealWorkspacePath(ctx, st, "WS1")
	if got != wsDir {
		t.Fatalf("resolved path = %q, want %q", got, wsDir)
	}
	local := loadLocal(t, "WS1")
	if local.Path != wsDir {
		t.Errorf("state.json Path = %q, want %q (write-back missing)", local.Path, wsDir)
	}
	if local.Repos["repo"] != repoDir {
		t.Errorf("state.json Repos[repo] = %q, want %q", local.Repos["repo"], repoDir)
	}
}

func TestResolveOrHealWorkspacePath_ReturnsEmptyOnRemoteMismatch(t *testing.T) {
	requireGit(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()

	st := memstore.New()
	mustCreateWS(t, ctx, st, "WS1", "demo")
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{
		WorkspaceKey: "WS1", Name: "repo", Remote: "origin",
		RemoteURL: "https://github.com/owner/repo.git",
	})

	repoDir := filepath.Join(bootstrap.LoomDir(), "workspaces", "demo", "repo")
	gitCheckout(t, repoDir, "https://github.com/someone/else.git") // wrong remote

	if got := ResolveOrHealWorkspacePath(ctx, st, "WS1"); got != "" {
		t.Fatalf("resolved path = %q, want \"\" on remote mismatch", got)
	}
	if local := loadLocal(t, "WS1"); local.Path != "" {
		t.Errorf("state.json Path = %q, want empty (must not bind the wrong dir)", local.Path)
	}
}

func TestResolveOrHealWorkspacePath_ReturnsEmptyWhenDirAbsent(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()

	st := memstore.New()
	mustCreateWS(t, ctx, st, "WS1", "demo")
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{WorkspaceKey: "WS1", Name: "repo", RemoteURL: "x"})

	if got := ResolveOrHealWorkspacePath(ctx, st, "WS1"); got != "" {
		t.Fatalf("resolved path = %q, want \"\" when no checkout on disk", got)
	}
	if local := loadLocal(t, "WS1"); local.Path != "" {
		t.Errorf("state.json Path = %q, want empty", local.Path)
	}
}

func TestResolveOrHealWorkspacePath_FastPathNoSideEffects(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()

	// Pre-seed a path; the fast path must return it WITHOUT touching the store
	// (passed nil) or the filesystem (the path need not exist).
	if err := bootstrap.MutateWorkspaceLocalState("WS1", func(l *bootstrap.WorkspaceLocalState) error {
		l.Path = "/preexisting/path"
		return nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if got := ResolveOrHealWorkspacePath(ctx, nil, "WS1"); got != "/preexisting/path" {
		t.Fatalf("fast-path resolved = %q, want /preexisting/path", got)
	}
}

func TestResolveOrHealWorkspacePath_EmptyRemoteURLBindsGitDir(t *testing.T) {
	requireGit(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()

	st := memstore.New()
	mustCreateWS(t, ctx, st, "WS1", "demo")
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{WorkspaceKey: "WS1", Name: "repo"}) // no RemoteURL

	wsDir := filepath.Join(bootstrap.LoomDir(), "workspaces", "demo")
	gitCheckout(t, filepath.Join(wsDir, "repo"), "") // real git dir, no origin

	if got := ResolveOrHealWorkspacePath(ctx, st, "WS1"); got != wsDir {
		t.Fatalf("resolved = %q, want %q (empty RemoteURL should accept a real git dir)", got, wsDir)
	}
}

func TestResolveOrHealWorkspacePath_MultiRepoBindsVerifiedSubset(t *testing.T) {
	requireGit(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	const urlA = "https://github.com/owner/a.git"

	st := memstore.New()
	mustCreateWS(t, ctx, st, "WS1", "demo")
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{WorkspaceKey: "WS1", Name: "a", Remote: "origin", RemoteURL: urlA})
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{WorkspaceKey: "WS1", Name: "b", Remote: "origin", RemoteURL: "https://github.com/owner/b.git"})

	wsDir := filepath.Join(bootstrap.LoomDir(), "workspaces", "demo")
	gitCheckout(t, filepath.Join(wsDir, "a"), urlA)                                 // matches
	gitCheckout(t, filepath.Join(wsDir, "b"), "https://github.com/owner/WRONG.git") // mismatches

	if got := ResolveOrHealWorkspacePath(ctx, st, "WS1"); got != wsDir {
		t.Fatalf("resolved = %q, want %q", got, wsDir)
	}
	local := loadLocal(t, "WS1")
	if _, ok := local.Repos["a"]; !ok {
		t.Error("repo a should be bound (matching remote)")
	}
	if _, ok := local.Repos["b"]; ok {
		t.Error("repo b must NOT be bound (mismatching remote)")
	}
}

func TestListWorkspacePathsOrHeal_HealsEmptyEntries(t *testing.T) {
	requireGit(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	const url = "https://github.com/owner/repo.git"

	st := memstore.New()
	// Already-cached workspace.
	mustCreateWS(t, ctx, st, "CACHED", "cached")
	if err := bootstrap.MutateWorkspaceLocalState("CACHED", func(l *bootstrap.WorkspaceLocalState) error {
		l.Path = "/already/known"
		return nil
	}); err != nil {
		t.Fatalf("seed cached: %v", err)
	}
	// Healable workspace (path missing, clone on disk).
	mustCreateWS(t, ctx, st, "HEAL", "healme")
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{WorkspaceKey: "HEAL", Name: "repo", Remote: "origin", RemoteURL: url})
	healDir := filepath.Join(bootstrap.LoomDir(), "workspaces", "healme")
	gitCheckout(t, filepath.Join(healDir, "repo"), url)

	out, err := ListWorkspacePathsOrHeal(ctx, st)
	if err != nil {
		t.Fatalf("ListWorkspacePathsOrHeal: %v", err)
	}
	if out["CACHED"] != "/already/known" {
		t.Errorf("CACHED = %q, want /already/known", out["CACHED"])
	}
	if out["HEAL"] != healDir {
		t.Errorf("HEAL = %q, want %q (should self-heal)", out["HEAL"], healDir)
	}
}

func TestResolveOrHealWorkspacePath_Idempotent(t *testing.T) {
	requireGit(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	const url = "https://github.com/owner/repo.git"

	st := memstore.New()
	mustCreateWS(t, ctx, st, "WS1", "demo")
	mustCreateRepo(t, ctx, st, workspaceowner.RepoCreate{WorkspaceKey: "WS1", Name: "repo", Remote: "origin", RemoteURL: url})
	wsDir := filepath.Join(bootstrap.LoomDir(), "workspaces", "demo")
	gitCheckout(t, filepath.Join(wsDir, "repo"), url)

	first := ResolveOrHealWorkspacePath(ctx, st, "WS1")
	second := ResolveOrHealWorkspacePath(ctx, st, "WS1") // now hits the fast path
	if first != wsDir || second != wsDir {
		t.Fatalf("results = %q, %q; want both %q", first, second, wsDir)
	}
}

func mustCreateWS(t *testing.T, ctx context.Context, s *memstore.Store, key, name string) {
	t.Helper()
	if _, err := s.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: key, Name: name}); err != nil {
		t.Fatalf("create workspace %s: %v", key, err)
	}
}

func mustCreateRepo(t *testing.T, ctx context.Context, s *memstore.Store, rc workspaceowner.RepoCreate) {
	t.Helper()
	if _, err := s.Repos().Create(ctx, rc); err != nil {
		t.Fatalf("create repo %s: %v", rc.Name, err)
	}
}
