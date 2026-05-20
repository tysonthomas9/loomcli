package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnsureRepoLocalCheckoutAdditionalBranches(t *testing.T) {
	t.Run("cached path exists but is not git", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		cachedPath := t.TempDir()
		if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
			local.Path = t.TempDir()
			local.Repos = map[string]string{"hello-world": cachedPath}
			return nil
		}); err != nil {
			t.Fatalf("MutateWorkspaceLocalState: %v", err)
		}

		path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://github.com/octocat/Hello-World")
		if err == nil || !strings.Contains(err.Error(), "cached repo checkout is not a git repo") {
			t.Fatalf("path=%q cloned=%t err=%v, want cached non-git error", path, cloned, err)
		}
	})

	t.Run("target already has git checkout", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		root := t.TempDir()
		target, err := localworkspace.RepoCheckoutPath(root, "hello-world")
		if err != nil {
			t.Fatalf("RepoCheckoutPath: %v", err)
		}
		if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
			local.Path = root
			return nil
		}); err != nil {
			t.Fatalf("MutateWorkspaceLocalState: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(target, ".git"), 0o755); err != nil {
			t.Fatalf("mkdir .git: %v", err)
		}

		path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://github.com/octocat/Hello-World")
		if err != nil {
			t.Fatalf("ensureRepoLocalCheckout: %v", err)
		}
		if path != target || cloned {
			t.Fatalf("path=%q cloned=%t, want existing git target %q without clone", path, cloned, target)
		}
	})
}

func TestRememberRepoLocalPathStoresState(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	repoPath := filepath.Join(t.TempDir(), "api")
	if err := rememberRepoLocalPath("WS", "api", repoPath); err != nil {
		t.Fatalf("rememberRepoLocalPath: %v", err)
	}
	state, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	if got := state.Workspaces["WS"].Repos["api"]; got != repoPath {
		t.Fatalf("remembered repo path = %q, want %q", got, repoPath)
	}
}

func TestRepoJSONListAndMinimalShow(t *testing.T) {
	handle := setupRepoFleetWorkspace(t)
	defer handle.Close()
	ctx := context.Background()
	if _, err := handle.Store.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: "WS",
		Name:         "api",
		RemoteURL:    "/tmp/api",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	resetRepoFlagGlobals(t)
	repoListJSON = true
	out := captureRepoStdout(t, func() {
		if err := runRepoList(nil, nil); err != nil {
			t.Fatalf("runRepoList json: %v", err)
		}
	})
	if !strings.Contains(out, `"name": "api"`) {
		t.Fatalf("json list output = %q", out)
	}

	repoShowJSON = false
	out = captureRepoStdout(t, func() {
		if err := runRepoShow(nil, []string{"api"}); err != nil {
			t.Fatalf("runRepoShow minimal: %v", err)
		}
	})
	if strings.Contains(out, "Default branch:") || strings.Contains(out, "Groups:") || !strings.Contains(out, "Remote URL:") {
		t.Fatalf("minimal show output = %q", out)
	}
}
