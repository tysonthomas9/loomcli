package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func writeContract(t *testing.T, repo, worktree string) {
	t.Helper()
	body := "repos:\n  " + repo + ":\n    local_integration:\n      branch: local/union\n      worktree: " + worktree + "\n"
	file := filepath.Join(t.TempDir(), "integration.yaml")
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	t.Setenv("LOOM_INTEGRATION_CONTRACT", file)
}

func backdateMergeHead(t *testing.T, dir string) {
	t.Helper()
	old := time.Now().Add(-2 * StalledOpThreshold)
	if err := os.Chtimes(filepath.Join(dir, ".git", "MERGE_HEAD"), old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestStalledSharedWorktreesReportsAnAgedMerge(t *testing.T) {
	dir := midMergeRepo(t)
	backdateMergeHead(t, dir)
	writeContract(t, "loomcli", dir)

	got := StalledSharedWorktrees()
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(got), got)
	}
	sw := got[0]
	if sw.Repo != "loomcli" || sw.Path != dir || sw.Branch != "local/union" {
		t.Fatalf("wrong identity: %+v", sw)
	}
	if sw.Op != "merge" || sw.Head == "" || sw.Unmerged != 1 {
		t.Fatalf("wrong state: %+v", sw)
	}
	if !sw.AgeKnown || sw.Age < StalledOpThreshold {
		t.Fatalf("age not carried through: %+v", sw)
	}
	if !strings.Contains(sw.Summary, "merge in progress") {
		t.Fatalf("summary = %q", sw.Summary)
	}
}

// A merge that started moments ago is a live integrator, not a stalled one.
func TestStalledSharedWorktreesSkipsAFreshMerge(t *testing.T) {
	writeContract(t, "loomcli", midMergeRepo(t))
	if got := StalledSharedWorktrees(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestStalledSharedWorktreesIsNilWhenNothingIsDeclared(t *testing.T) {
	t.Setenv("LOOM_INTEGRATION_CONTRACT", filepath.Join(t.TempDir(), "absent.yaml"))
	if got := StalledSharedWorktrees(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestAbortInProgressOpClearsAnAgentWorktree(t *testing.T) {
	dir := midMergeRepo(t)
	line, err := AbortInProgressOp(dir)
	if err != nil {
		t.Fatalf("AbortInProgressOp: %v", err)
	}
	if !strings.Contains(line, "aborted in-progress merge") {
		t.Fatalf("line = %q", line)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); statErr == nil {
		t.Fatal("still mid-merge after abort")
	}
}

// Nothing to abort is not an error, and says nothing.
func TestAbortInProgressOpIsSilentOnACleanOrMissingPath(t *testing.T) {
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "gone")} {
		line, err := AbortInProgressOp(path)
		if err != nil || line != "" {
			t.Fatalf("AbortInProgressOp(%q) = (%q, %v)", path, line, err)
		}
	}
}
