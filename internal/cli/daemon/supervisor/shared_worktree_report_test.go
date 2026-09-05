package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// writeContract writes an integration.yaml declaring one shared worktree and
// points the scan at it. Driving the real contract reader keeps this test on
// the path the daemon actually takes.
func writeContract(t *testing.T, repo, path string) {
	t.Helper()
	body := fmt.Sprintf("repos:\n  %s:\n    local_integration:\n      branch: local/union\n      worktree: %s\n", repo, path)
	file := filepath.Join(t.TempDir(), "integration.yaml")
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	t.Setenv("LOOM_INTEGRATION_CONTRACT", file)
}

func testAgent(worktree string) *AgentProcess {
	return &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: worktree}}
}

// midMergeRepo builds a real repo left in a conflicted merge.
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
	// Expected to conflict; the failure IS the setup.
	_ = exec.Command("git", "-C", dir, "merge", "side").Run() //nolint:norawexec
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("precondition: repo is not mid-merge: %v", err)
	}
	return dir
}

// The 2026-09-05 incident in miniature: an agent exits, a shared union
// worktree is sitting mid-merge, and the daemon log must say so.
func TestReportSharedWorktreeStateWarnsOnStalledMerge(t *testing.T) {
	dir := midMergeRepo(t)
	// Backdate the marker past the threshold — a merge started seconds ago is
	// a live integrator, not a stalled one.
	backdateGitMarker(t, filepath.Join(dir, ".git", "MERGE_HEAD"))

	writeContract(t, "loomcli", dir)
	buf := captureSlog(t)

	s := &Supervisor{}
	s.reportSharedWorktreeState(testAgent("integrator-2"))

	out := buf.String()
	if !strings.Contains(out, "shared integration worktree is stuck mid-operation") {
		t.Fatalf("no warning emitted:\n%s", out)
	}
	for _, want := range []string{"repo=loomcli", "op=merge", "after_exit_of=integrator-2", "loom doctor --fix"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log line missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "stuck mid-operation") != 1 {
		t.Fatalf("expected exactly one warning:\n%s", out)
	}
}

// A merge that started moments ago is normal; reporting it every agent exit
// would bury the real ones.
func TestReportSharedWorktreeStateSkipsFreshMerge(t *testing.T) {
	dir := midMergeRepo(t)
	writeContract(t, "loomcli", dir)
	buf := captureSlog(t)

	(&Supervisor{}).reportSharedWorktreeState(testAgent("worker"))
	if strings.Contains(buf.String(), "stuck mid-operation") {
		t.Fatalf("a fresh merge should not warn:\n%s", buf.String())
	}
}

func TestReportSharedWorktreeStateIsQuietWhenNothingToReport(t *testing.T) {
	clean := t.TempDir()
	tests := []struct {
		name     string
		contract func(t *testing.T)
		agent    *AgentProcess
	}{
		{"no contract file", func(t *testing.T) {
			t.Setenv("LOOM_INTEGRATION_CONTRACT", filepath.Join(t.TempDir(), "absent.yaml"))
		}, testAgent("worker")},
		{"declared path is gone", func(t *testing.T) {
			writeContract(t, "gone", filepath.Join(t.TempDir(), "not-here"))
		}, testAgent("worker")},
		{"declared path is not a repo", func(t *testing.T) {
			writeContract(t, "loomcli", clean)
		}, testAgent("worker")},
		{"nil agent (spawn-failure path)", func(t *testing.T) {
			t.Setenv("LOOM_INTEGRATION_CONTRACT", filepath.Join(t.TempDir(), "absent.yaml"))
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.contract(t)
			buf := captureSlog(t)
			(&Supervisor{}).reportSharedWorktreeState(tc.agent)
			if strings.Contains(buf.String(), "stuck mid-operation") {
				t.Fatalf("unexpected warning:\n%s", buf.String())
			}
		})
	}
}

// A spawn failure leaves WorktreePath and the agent name empty; the shared scan
// does not depend on either, so it must still run.
func TestReportSharedWorktreeStateHandlesEmptyAgentName(t *testing.T) {
	dir := midMergeRepo(t)
	backdateGitMarker(t, filepath.Join(dir, ".git", "MERGE_HEAD"))
	writeContract(t, "loomcli", dir)
	buf := captureSlog(t)

	(&Supervisor{}).reportSharedWorktreeState(&AgentProcess{})
	if !strings.Contains(buf.String(), "stuck mid-operation") {
		t.Fatalf("the scan should not depend on the agent name:\n%s", buf.String())
	}
}

// backdateGitMarker ages a git state marker past the staleness threshold.
func backdateGitMarker(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
