package storeadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestBuildActiveWorkspaceData_FallsBackToFirstWorkspace(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BETA", Name: "Beta"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ALPHA", Name: "Alpha"}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "ALPHA", Name: "api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	got, err := BuildActiveWorkspaceData(ctx, st)
	if err != nil {
		t.Fatalf("BuildActiveWorkspaceData returned error: %v", err)
	}
	if got == nil {
		t.Fatal("BuildActiveWorkspaceData returned nil")
	}
	if got.ID != "ALPHA" {
		t.Fatalf("active workspace ID = %q, want ALPHA", got.ID)
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != "api" {
		t.Fatalf("repos = %+v, want api repo", got.Repos)
	}
}

func TestEnsureWorkspacePathCreatesDefaultLocalState(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	got, err := EnsureWorkspacePath("ACME")
	if err != nil {
		t.Fatalf("EnsureWorkspacePath returned error: %v", err)
	}
	want := filepath.Join(loomDir, "workspaces", "ACME")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("workspace path was not created as directory: info=%v err=%v", info, err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache returned error: %v", err)
	}
	if sc.Workspaces["ACME"].Path != want {
		t.Fatalf("cached path = %q, want %q", sc.Workspaces["ACME"].Path, want)
	}
}

func TestListWorkspacePathsSelfHealsMissingLocalState(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ACME", Name: "ACME"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	got, err := ListWorkspacePaths(ctx, st)
	if err != nil {
		t.Fatalf("ListWorkspacePaths returned error: %v", err)
	}
	want := filepath.Join(loomDir, "workspaces", "ACME")
	if got["ACME"] != want {
		t.Fatalf("workspace path = %q, want %q", got["ACME"], want)
	}
}

func TestBuildWorkspaceDataForKeySelfHealsMissingRepoCheckout(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := createGitRepo(t, filepath.Join(t.TempDir(), "api-origin"))
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ACME", Name: "ACME"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "ACME",
		Name:          "api",
		RemoteURL:     src,
		Remote:        "origin",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	got, err := BuildWorkspaceDataForKey(ctx, st, "ACME")
	if err != nil {
		t.Fatalf("BuildWorkspaceDataForKey returned error: %v", err)
	}
	wantRepoPath := filepath.Join(loomDir, "workspaces", "ACME", "api")
	if len(got.Repos) != 1 {
		t.Fatalf("repos len = %d, want 1", len(got.Repos))
	}
	if got.Repos[0].Path != wantRepoPath {
		t.Fatalf("repo path = %q, want %q", got.Repos[0].Path, wantRepoPath)
	}
	if _, err := os.Stat(filepath.Join(wantRepoPath, ".git")); err != nil {
		t.Fatalf("repo checkout was not cloned: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache returned error: %v", err)
	}
	if got := sc.Workspaces["ACME"].Repos["api"]; got != wantRepoPath {
		t.Fatalf("cached repo path = %q, want %q", got, wantRepoPath)
	}
}

func TestBuildWorkspaceDataForKeyLeavesRepoUnboundWithoutRemoteURL(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ACME", Name: "ACME"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "ACME", Name: "api"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	got, err := BuildWorkspaceDataForKey(ctx, st, "ACME")
	if err != nil {
		t.Fatalf("BuildWorkspaceDataForKey returned error: %v", err)
	}
	if len(got.Repos) != 1 {
		t.Fatalf("repos len = %d, want 1", len(got.Repos))
	}
	if got.Repos[0].Path != "" {
		t.Fatalf("repo path = %q, want empty without remote URL", got.Repos[0].Path)
	}
	if _, err := os.Stat(filepath.Join(loomDir, "workspaces", "ACME", "api")); !os.IsNotExist(err) {
		t.Fatalf("repo checkout should not be created without remote URL, stat err=%v", err)
	}
}

func createGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
