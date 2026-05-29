package repo

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

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

func TestEnsureRepoLocalCheckoutRejectsMissingLocalWorkspacePath(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	path, cloned, err := ensureRepoLocalCheckout(context.Background(), "TEST", "hello-world", "https://github.com/octocat/Hello-World")
	if err == nil {
		t.Fatalf("ensureRepoLocalCheckout returned path=%q cloned=%t, want local path error", path, cloned)
	}
	if !strings.Contains(err.Error(), "has no local path") {
		t.Fatalf("ensureRepoLocalCheckout error = %q, want local path message", err)
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
