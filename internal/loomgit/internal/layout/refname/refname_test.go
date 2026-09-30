package refname

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/errcode"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

func TestBuilders(t *testing.T) {
	builders := []struct {
		name   string
		fn     func(string) (string, error)
		want   string
		branch bool
	}{
		{"interactive branch", func(id string) (string, error) { return InteractiveBranch("W1", id) }, "loom/ws/W1/interactive/L1", true},
		{"change branch", func(id string) (string, error) { return ChangeBranch("W1", id) }, "loom/ws/W1/change/C1", true},
		{"attempt base", func(id string) (string, error) { return AttemptBase("W1", id) }, "refs/loom/ws/W1/attempt/A1/base", false},
		{"attempt capture", func(id string) (string, error) { return AttemptCapture("W1", id) }, "refs/loom/ws/W1/attempt/A1/capture", false},
		{"revision base", func(id string) (string, error) { return RevisionBase("W1", id, "r1") }, "refs/loom/ws/W1/change/C1/r1/base", false},
		{"revision head", func(id string) (string, error) { return RevisionHead("W1", id, "r1") }, "refs/loom/ws/W1/change/C1/r1/head", false},
		{"publication", func(id string) (string, error) { return Publication("W1", id) }, "refs/loom/ws/W1/pub/C1", false},
		{"PR head", func(id string) (string, error) { return PRHead("W1", id) }, "refs/loom/ws/W1/pr/123/head", false},
		{"interactive backup", func(id string) (string, error) { return InteractiveBackup("W1", id) }, "refs/loom/ws/W1/interactive/L1", false},
		{"WIP", func(id string) (string, error) { return WIP("W1", id, "id1") }, "refs/loom/ws/W1/wip/L1/id1", false},
		{"task copy", func(id string) (string, error) { return TaskCopyPath("W1", id) }, filepath.FromSlash("refs/loom/ws/W1/task-copy/T1"), false},
	}
	ids := []string{"L1", "C1", "A1", "A1", "C1", "C1", "C1", "123", "L1", "L1", "T1"}
	for i, tc := range builders {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(ids[i])
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			if err := gitexec.CheckRefFormat(filepath.ToSlash(got), tc.branch); err != nil {
				t.Fatalf("Git rejected %q: %v", got, err)
			}
			for _, bad := range []string{"a/b", "a..b", "a b", "a~b", "a^b", "a:b", "a@{b"} {
				got, err := tc.fn(bad)
				if err == nil || got != "" {
					t.Fatalf("%q yielded %q, %v", bad, got, err)
				}
			}
		})
	}
}

func TestEveryIDPositionRejectsBadInput(t *testing.T) {
	for _, fn := range []func() (string, error){
		func() (string, error) { return RevisionHead("W1", "C1", "r/1") },
		func() (string, error) { return WIP("W1", "L1", "a/b") },
		func() (string, error) { return AttemptCapture("W/1", "A1") },
	} {
		if got, err := fn(); err == nil || got != "" {
			t.Fatalf("got %q, %v", got, err)
		}
	}
}

func TestCheckNamespace(t *testing.T) {
	for _, branch := range []string{"loom", "loom/ws", "loom/ws/W1"} {
		t.Run(branch, func(t *testing.T) {
			repo := t.TempDir()
			git(t, repo, "init", "-q")
			git(t, repo, "commit", "--allow-empty", "-qm", "initial")
			git(t, repo, "branch", branch)
			err := CheckNamespace(repo, "W1")
			if !errors.Is(err, &errcode.Error{Kind: errcode.RefNamespaceConflict}) || !strings.Contains(err.Error(), branch) {
				t.Fatalf("got %v, want named namespace conflict", err)
			}
		})
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	if err := CheckNamespace(repo, "W1"); err != nil {
		t.Fatal(err)
	}
}

func TestTaskCopiesAreWorkspaceScoped(t *testing.T) {
	a, err := TaskCopyPath("W1", "T1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := TaskCopyPath("W2", "T1")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || strings.Contains(a, "alice") || strings.Contains(b, "alice") {
		t.Fatalf("paths %q and %q", a, b)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test fixture runs real Git to verify ref names.
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
