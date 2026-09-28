package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
)

func writeLockFile(t *testing.T, dir string, info *cli.LockInfo) {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cli.LockFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeYieldFile(t *testing.T, dir, reason string) {
	t.Helper()
	if err := WriteYieldFile(dir, &YieldRequest{Reason: reason, RequestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func newTestSupervisor() *Supervisor {
	return &Supervisor{ConfigSnapshot: func() *config.DaemonConfig { return &config.DaemonConfig{} }}
}

func gitForCaptureTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec,norawexec // Real temporary Git repository verifies capture objects.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func captureRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gitForCaptureTest(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForCaptureTest(t, dir, "add", "main.txt")
	gitForCaptureTest(t, dir, "commit", "-m", "initial")
	return dir
}

func TestAgentExitCapturesLargeTrackedAndUntrackedWork(t *testing.T) {
	dir := captureRepo(t)
	tracked := strings.Repeat("tracked edit\n", 2000)
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte(tracked), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-1", TaskTitle: "Work"})
	s := newTestSupervisor()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir}
	s.handleAgentCheckpoint(ap, 1)
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(dir))
	if err != nil || cp == nil {
		t.Fatalf("checkpoint: %v, %+v", err, cp)
	}
	if cp.CaptureRef == "" || cp.Retained {
		t.Fatalf("capture checkpoint: %+v", cp)
	}
	if got := gitForCaptureTest(t, dir, "show", cp.CaptureRef+":main.txt"); got != strings.TrimSpace(tracked) {
		t.Fatal("tracked edit was not captured in full")
	}
	if got := gitForCaptureTest(t, dir, "show", cp.CaptureRef+":new.txt"); got != "untracked work" {
		t.Fatalf("untracked content: %q", got)
	}
	if got := gitForCaptureTest(t, dir, "status", "--porcelain"); !strings.Contains(got, "main.txt") || !strings.Contains(got, "new.txt") {
		t.Fatalf("worktree changed after capture: %q", got)
	}
}

func TestAgentExitCapturesAfterDrainRemovedYieldFile(t *testing.T) {
	dir := captureRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("yield work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-2"})
	s := newTestSupervisor()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir, YieldReason: "shutdown"}
	s.handleAgentCheckpoint(ap, 0)
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(dir))
	if err != nil || cp == nil || cp.CaptureRef == "" || cp.YieldReason != "shutdown" {
		t.Fatalf("yield checkpoint: %+v, %v", cp, err)
	}
	if got := gitForCaptureTest(t, dir, "show", cp.CaptureRef+":new.txt"); got != "yield work" {
		t.Fatalf("captured yield work: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("yield work was removed: %v", err)
	}
}

func TestAgentExitCaptureFailureRetainsWork(t *testing.T) {
	dir := t.TempDir()
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-3"})
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisor()
	s.captureWorktree = func(context.Context, string, string, string, string, string) (agentcapture.Result, error) {
		return agentcapture.Result{}, errors.New("capture failed")
	}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir}
	s.handleAgentCheckpoint(ap, 1)
	if !ap.CaptureRetained {
		t.Fatal("capture failure must mark worktree retained")
	}
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(dir))
	if err != nil || cp == nil || !cp.Retained {
		t.Fatalf("retained checkpoint: %+v, %v", cp, err)
	}
	s.postMortemRecovery(ap, 1)
	if data, err := os.ReadFile(filepath.Join(dir, "new.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("worktree changed: %q, %v", data, err)
	}
}

func TestCleanExitStillCapturesAndClearsCheckpoint(t *testing.T) {
	dir := captureRepo(t)
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-4"})
	lockDir := cli.ResolveLockDir(dir)
	if err := config.SaveCheckpoint(lockDir, &config.Checkpoint{TaskID: "old", CaptureRef: "refs/loom/old"}); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisor()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir}
	s.handleAgentCheckpoint(ap, 0)
	if ap.CaptureRetained {
		t.Fatal("clean capture unexpectedly retained")
	}
	manifests, err := filepath.Glob(filepath.Join(dir, ".git", "loom", "capture", "agent-*.json"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("clean exit did not run capture: %v, %v", manifests, err)
	}
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil || cp != nil {
		t.Fatalf("clean checkpoint: %+v, %v", cp, err)
	}
}
