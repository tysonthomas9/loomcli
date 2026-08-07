package stackpublish

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestProvisionOriginCheckout proves the ephemeral checkout clones a (file://)
// origin and fetches the stack's branches into LOCAL refs/heads — the shape the
// reconciler's PushBranches needs. Offline; no GitHub.
func TestProvisionOriginCheckout(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// A bare repo stands in for origin.
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "--initial-branch=main", origin)

	// Seed it: a default branch plus two stack branches for epic:E.
	seed := filepath.Join(root, "seed")
	git(t, root, "clone", "-q", origin, seed)
	git(t, seed, "commit", "-q", "--allow-empty", "-m", "root")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "push", "-q", "origin", "main")

	id := sl.StackID("epic:E")
	bA := sl.OutputBranchName(id, "A")
	bB := sl.OutputBranchName(id, "B")
	for _, b := range []string{bA, bB} {
		git(t, seed, "checkout", "-q", "-b", b, "main")
		git(t, seed, "commit", "-q", "--allow-empty", "-m", "work "+b)
		git(t, seed, "push", "-q", "origin", b)
		git(t, seed, "checkout", "-q", "main")
	}

	repoPath, cleanup, err := provisionOriginCheckout(ctx, "file://"+origin, "", id)
	if err != nil {
		t.Fatalf("provisionOriginCheckout: %v", err)
	}
	defer cleanup()

	// Both stack branches must be present as LOCAL branches in the checkout.
	for _, b := range []string{bA, bB} {
		out := git(t, repoPath, "branch", "--list", b)
		if !strings.Contains(out, b) {
			t.Fatalf("stack branch %q not fetched as a local branch; got %q", b, out)
		}
	}
	// origin remote is set (repoSlug/Publish reads it).
	if out := git(t, repoPath, "remote", "get-url", "origin"); !strings.Contains(out, "origin.git") {
		t.Fatalf("origin remote not set: %q", out)
	}
}

func TestProvisionOriginCheckoutEmptyURL(t *testing.T) {
	if _, _, err := provisionOriginCheckout(context.Background(), "  ", "", sl.StackID("epic:E")); err == nil {
		t.Fatal("expected error for empty repo url")
	}
}
