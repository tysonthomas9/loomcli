package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// midMergeRepo builds a real repo left in a conflicted merge. Real git is the
// point: the states this code reads (MERGE_HEAD, stage-2/3 index entries) have
// no useful mock.
func midMergeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil { //nolint:norawexec
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	write("base\n")
	run("add", "a.txt")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "side")
	write("side\n")
	run("commit", "-q", "-am", "side")
	run("checkout", "-q", "main")
	write("main\n")
	run("commit", "-q", "-am", "main")
	_ = exec.Command("git", "-C", dir, "merge", "side").Run() //nolint:norawexec
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("precondition: repo is not mid-merge: %v", err)
	}
	return dir
}

func mergeHeadExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD"))
	return err == nil
}

// The abort discards the agent's conflict resolutions, so the snapshot has to
// exist, complete, before it.
func TestAbortInProgressOpSnapshotsThenAborts(t *testing.T) {
	dir := midMergeRepo(t)
	root := t.TempDir()

	line, err := AbortInProgressOp(dir, root)
	if err != nil {
		t.Fatalf("AbortInProgressOp: %v", err)
	}
	if !strings.Contains(line, "aborted in-progress merge") || !strings.Contains(line, "snapshot in "+root) {
		t.Fatalf("line = %q", line)
	}
	if mergeHeadExists(dir) {
		t.Fatal("still mid-merge after abort")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want one snapshot dir in %s: %v %v", root, entries, err)
	}
	snap := filepath.Join(root, entries[0].Name())
	for _, name := range []string{"README.txt", "head.txt", "worktree.diff", "unmerged.txt", "MERGE_HEAD"} {
		if _, statErr := os.Stat(filepath.Join(snap, name)); statErr != nil {
			t.Fatalf("snapshot missing %s: %v", name, statErr)
		}
	}
}

// No complete snapshot, no abort: the merge stays exactly as the agent left it.
func TestAbortInProgressOpKeepsTheOpWhenTheSnapshotFails(t *testing.T) {
	dir := midMergeRepo(t)
	// A regular file where the snapshot root should be: nothing can be written
	// under it.
	root := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(root, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	line, err := AbortInProgressOp(dir, root)
	if err == nil {
		t.Fatalf("AbortInProgressOp returned no error (line %q); want the snapshot failure", line)
	}
	if line != "" {
		t.Fatalf("line = %q, want empty on failure", line)
	}
	if !strings.Contains(err.Error(), "untouched") {
		t.Fatalf("error does not say the operation was left in place: %v", err)
	}
	if !mergeHeadExists(dir) {
		t.Fatal("the merge was aborted although no snapshot was written")
	}
}

// Nothing to abort is not an error, and says nothing.
func TestAbortInProgressOpIsSilentOnACleanOrMissingPath(t *testing.T) {
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "gone")} {
		line, err := AbortInProgressOp(path, t.TempDir())
		if err != nil || line != "" {
			t.Fatalf("AbortInProgressOp(%q) = (%q, %v)", path, line, err)
		}
	}
}
