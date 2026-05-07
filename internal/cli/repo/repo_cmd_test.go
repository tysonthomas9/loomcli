package repo

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
