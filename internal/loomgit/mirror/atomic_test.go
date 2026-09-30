package mirror

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

func TestPushAtomicRejectsOneStaleLeaseWithoutChangingOtherBranch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	remote := filepath.Join(root, "remote.git")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	gitAtomic(t, repo, "init", "-q", "-b", "main")
	gitAtomic(t, repo, "config", "user.name", "Test")
	gitAtomic(t, repo, "config", "user.email", "test@example.test")
	gitAtomic(t, root, "init", "-q", "--bare", remote)
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	gitAtomic(t, repo, "add", "file")
	gitAtomic(t, repo, "commit", "-qm", "base")
	first := gitAtomic(t, repo, "rev-parse", "HEAD")
	gitAtomic(t, repo, "push", remote, first+":refs/heads/one", first+":refs/heads/two")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("next"), 0600); err != nil {
		t.Fatal(err)
	}
	gitAtomic(t, repo, "commit", "-qam", "next")
	second := gitAtomic(t, repo, "rev-parse", "HEAD")
	runner, err := gitexec.New(repo, gitexec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	refs := []LeasedRef{{Ref: "refs/heads/one", Head: second, Expected: first}, {Ref: "refs/heads/two", Head: second, Expected: "0000000000000000000000000000000000000000"}}
	if err := PushAtomic(context.Background(), runner, remote, refs); err == nil {
		t.Fatal("stale lease succeeded")
	}
	if got := gitAtomic(t, repo, "ls-remote", remote, "refs/heads/one"); !strings.HasPrefix(got, first+"\t") {
		t.Fatalf("first ref changed after stale second lease: %s", got)
	}
	refs[1].Expected = first
	if err := PushAtomic(context.Background(), runner, remote, refs); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if got := gitAtomic(t, repo, "ls-remote", remote, "refs/heads/"+name); !strings.HasPrefix(got, second+"\t") {
			t.Fatalf("%s = %s, want %s", name, got, second)
		}
	}
}

func gitAtomic(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:norawexec // Disposable Git repositories are the test subject.
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
