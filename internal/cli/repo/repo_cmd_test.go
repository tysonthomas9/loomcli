package repo

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRepoCommandsAgainstLocalStore(t *testing.T) {
	handle := setupRepoFleetWorkspace(t)
	defer handle.Close()

	resetRepoFlagGlobals(t)
	if out := captureRepoStdout(t, func() {
		if err := runRepoList(nil, nil); err != nil {
			t.Fatalf("runRepoList empty: %v", err)
		}
	}); !strings.Contains(out, "No repos in workspace WS") {
		t.Fatalf("empty list output = %q", out)
	}

	repoAddRemote = "upstream"
	repoAddBranch = "main"
	repoAddGroups = []string{"backend", "api"}
	repoAddSourceID = "src-api"
	if out := captureRepoStdout(t, func() {
		if err := runRepoAdd(nil, []string{"api", "/tmp/api"}); err != nil {
			t.Fatalf("runRepoAdd: %v", err)
		}
	}); !strings.Contains(out, "Created repo WS/api") {
		t.Fatalf("add output = %q", out)
	}

	repoListJSON = false
	if out := captureRepoStdout(t, func() {
		if err := runRepoList(nil, nil); err != nil {
			t.Fatalf("runRepoList: %v", err)
		}
	}); !strings.Contains(out, "api") || !strings.Contains(out, "/tmp/api") {
		t.Fatalf("list output = %q", out)
	}

	repoShowJSON = false
	if out := captureRepoStdout(t, func() {
		if err := runRepoShow(nil, []string{"api"}); err != nil {
			t.Fatalf("runRepoShow: %v", err)
		}
	}); !strings.Contains(out, "Remote:       upstream") ||
		!strings.Contains(out, "Default branch: main") ||
		!strings.Contains(out, "Groups:       backend, api") {
		t.Fatalf("show output = %q", out)
	}

	repoShowJSON = true
	if out := captureRepoStdout(t, func() {
		if err := runRepoShow(nil, []string{"api"}); err != nil {
			t.Fatalf("runRepoShow json: %v", err)
		}
	}); !strings.Contains(out, `"name": "api"`) {
		t.Fatalf("show json output = %q", out)
	}

	if out := captureRepoStdout(t, func() {
		if err := runRepoRemove(nil, []string{"api"}); err != nil {
			t.Fatalf("runRepoRemove: %v", err)
		}
	}); !strings.Contains(out, "Removed repo WS/api") {
		t.Fatalf("remove output = %q", out)
	}
}

func TestSafeRepoCheckoutPathReturnsWorkspaceChild(t *testing.T) {
	root := t.TempDir()

	got, err := localworkspace.RepoCheckoutPath(root, "hello-world")
	if err != nil {
		t.Fatalf("RepoCheckoutPath returned error: %v", err)
	}
	want := filepath.Join(root, "hello-world")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestSafeRepoCheckoutPathRejectsPathNames(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"../escape",
		filepath.Join("nested", "repo"),
		filepath.Join(root, "absolute"),
	}

	for _, name := range cases {
		t.Run(strings.ReplaceAll(name, string(filepath.Separator), "_"), func(t *testing.T) {
			if _, err := localworkspace.RepoCheckoutPath(root, name); err == nil {
				t.Fatalf("RepoCheckoutPath(%q) succeeded, want error", name)
			}
		})
	}
}

func TestEnsureRepoLocalCheckoutNoopsWithoutLocalWorkspacePath(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://github.com/octocat/Hello-World")
	if err != nil {
		t.Fatalf("ensureRepoLocalCheckout returned error: %v", err)
	}
	if path != "" || cloned {
		t.Fatalf("path=%q cloned=%t, want no-op", path, cloned)
	}
}

func TestEnsureRepoLocalCheckoutNoopsForNonCloneURL(t *testing.T) {
	path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "/local/path")
	if err != nil {
		t.Fatalf("ensureRepoLocalCheckout returned error: %v", err)
	}
	if path != "" || cloned {
		t.Fatalf("path=%q cloned=%t, want no-op", path, cloned)
	}
}

func TestEnsureRepoLocalCheckoutRejectsInvalidCloneURL(t *testing.T) {
	path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://")
	if err == nil {
		t.Fatalf("ensureRepoLocalCheckout path=%q cloned=%t, want invalid clone URL error", path, cloned)
	}
}

func TestEnsureRepoLocalCheckoutUsesCachedGitCheckout(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	cachedPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(cachedPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = t.TempDir()
		local.Repos = map[string]string{
			"hello-world": cachedPath,
		}
		return nil
	}); err != nil {
		t.Fatalf("MutateWorkspaceLocalState returned error: %v", err)
	}

	path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://github.com/octocat/Hello-World")
	if err != nil {
		t.Fatalf("ensureRepoLocalCheckout returned error: %v", err)
	}
	if path != cachedPath || cloned {
		t.Fatalf("path=%q cloned=%t, want cached path %q without clone", path, cloned, cachedPath)
	}
}

func TestEnsureRepoLocalCheckoutRejectsExistingNonGitTarget(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	target, err := localworkspace.RepoCheckoutPath(root, "hello-world")
	if err != nil {
		t.Fatalf("RepoCheckoutPath: %v", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = root
		return nil
	}); err != nil {
		t.Fatalf("MutateWorkspaceLocalState returned error: %v", err)
	}

	path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://github.com/octocat/Hello-World")
	if err == nil || !strings.Contains(err.Error(), "already exists and is not a git repo") {
		t.Fatalf("ensureRepoLocalCheckout path=%q cloned=%t err=%v, want existing non-git target error", path, cloned, err)
	}
}

func TestEnsureRepoLocalCheckoutRejectsMissingCachedPath(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	missingPath := filepath.Join(t.TempDir(), "missing-checkout")
	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = t.TempDir()
		local.Repos = map[string]string{
			"hello-world": missingPath,
		}
		return nil
	}); err != nil {
		t.Fatalf("MutateWorkspaceLocalState returned error: %v", err)
	}

	path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://github.com/octocat/Hello-World")
	if err == nil {
		t.Fatalf("ensureRepoLocalCheckout returned path=%q cloned=%t, want stale cached checkout error", path, cloned)
	}
	if !strings.Contains(err.Error(), "cached repo checkout does not exist") {
		t.Fatalf("ensureRepoLocalCheckout error = %q, want stale cached checkout message", err)
	}
}

func setupRepoFleetWorkspace(t *testing.T) *bootstrap.StoreHandle {
	t.Helper()
	requireRepoFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(bootstrap.EnvFleetDBActor, "repo-test")
	t.Setenv(bootstrap.EnvFleetDBURL, "")

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		_ = handle.Close()
		t.Fatalf("create workspace: %v", err)
	}
	return handle
}

func requireRepoFleetDB(t *testing.T) {
	t.Helper()
	if os.Getenv("FLEET_DB_BIN") != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}

func resetRepoFlagGlobals(t *testing.T) {
	t.Helper()
	origRemote, origBranch, origSourceID := repoAddRemote, repoAddBranch, repoAddSourceID
	origGroups := repoAddGroups
	origListJSON, origShowJSON := repoListJSON, repoShowJSON
	t.Cleanup(func() {
		repoAddRemote, repoAddBranch, repoAddSourceID = origRemote, origBranch, origSourceID
		repoAddGroups = origGroups
		repoListJSON, repoShowJSON = origListJSON, origShowJSON
	})
}

func captureRepoStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	return b.String()
}
