package git

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestEpicBranchName(t *testing.T) {
	cases := map[string]string{
		"PROJ-42":     "loom/epic-PROJ-42",
		"epic_1.2":    "loom/epic-epic_1.2",
		"a b/c":       "loom/epic-a-b-c", // spaces + slash → '-'
		"WS-7":        "loom/epic-WS-7",
		"weird!@#$ch": "loom/epic-weird----ch", // 4 specials → 4 dashes
	}
	for in, want := range cases {
		if got := epicBranchName(in); got != want {
			t.Errorf("epicBranchName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsClosedStatus(t *testing.T) {
	for _, s := range []string{"closed", "Closed", "done", "tombstone", " DONE "} {
		if !isClosedStatus(s) {
			t.Errorf("isClosedStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"open", "in_progress", "review", ""} {
		if isClosedStatus(s) {
			t.Errorf("isClosedStatus(%q) = true, want false", s)
		}
	}
}

func epicGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestCreateRemoteBranchFromBase creates the epic branch off the remote's base
// tip without touching the worktree, and is idempotent.
func TestCreateRemoteBranchFromBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	epicGit(t, root, "init", "-q", "--bare", bare)
	epicGit(t, root, "clone", "-q", bare, work)
	epicGit(t, work, "checkout", "-q", "-b", "master")
	epicGit(t, work, "-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "base")
	epicGit(t, work, "push", "-q", "origin", "master")

	deps := cli.GetDeps(nil)
	created, err := createRemoteBranchFromBase(deps, work, "origin", "master", "loom/epic-test")
	if err != nil {
		t.Fatalf("createRemoteBranchFromBase: %v", err)
	}
	if !created {
		t.Error("expected created=true on first call")
	}
	// The branch now exists on the remote at the base tip.
	baseSha := epicGit(t, work, "rev-parse", "origin/master")
	got := epicGit(t, bare, "rev-parse", "refs/heads/loom/epic-test")
	if got != baseSha {
		t.Errorf("epic branch sha = %q, want base %q", got, baseSha)
	}

	// Idempotent: a second call is a no-op.
	created, err = createRemoteBranchFromBase(deps, work, "origin", "master", "loom/epic-test")
	if err != nil {
		t.Fatalf("second createRemoteBranchFromBase: %v", err)
	}
	if created {
		t.Error("expected created=false when the branch already exists")
	}
}
