package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// gitWorktreeWithWIP creates a real repository with one committed file, then
// leaves an untracked file behind. `git clean` is the only thing that removes
// that file, and captureGitDiff (git diff HEAD) never records it, so its
// survival is the load-bearing signal that recovery took its preserving form.
func gitWorktreeWithWIP(t *testing.T, wip string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // the fixture needs a real repository: git clean is the behavior under test
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(dir, wip), []byte("uncommitted work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustSaveCheckpoint(t *testing.T, worktree, taskID string) {
	t.Helper()
	if err := config.SaveCheckpoint(cli.ResolveLockDir(worktree), &config.Checkpoint{
		AgentName: "worker", TaskID: taskID, Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func statusBackend(status string) *clitest.MockIssueBackend {
	mock := clitest.NewMockIssueBackend()
	mock.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: status}}, nil
	}
	return mock
}

func recoveryTestAgent(worktree string) *AgentProcess {
	return &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "worker", Role: "task"},
		RoleConfig:   config.RoleConfig{TaskFilter: "has_design"},
		WorktreePath: worktree,
	}
}

func newRecoverySupervisor(mock *clitest.MockIssueBackend) *Supervisor {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.IssueBackend = mock
	s.EmitEvent = func(events.Event) {}
	return s
}

// Quarantine writes blocked + unassigned, which stops the router but not the
// crashed agent's own recovery: blocked is still claimable, so the agent whose
// kills caused the quarantine re-claimed the same task on its next cycle. The
// guard must refuse that — without destroying the interrupted run's work, which
// is exactly what makes the task recoverable once a human unblocks it.
func TestPreFlightSetup_QuarantinedRecoveryColdStarts(t *testing.T) {
	worktree := gitWorktreeWithWIP(t, "handoff-notes.md")
	mock := statusBackend("blocked")
	mock.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		if id == "T-blocked" {
			return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "blocked"}}, nil
		}
		return nil, nil
	}
	mock.ReadyResult = []backend.IssueData{{ID: "T-other", Status: "open", IssueType: "task", Priority: 1, Title: "Other", Design: "plan"}}

	seedLock(t, worktree, &cli.LockInfo{
		PID: deadPID, TaskID: "T-blocked", ClaudeSessionID: "session", TaskStartedAt: time.Now(),
	})
	mustSaveCheckpoint(t, worktree, "T-blocked")

	s := newRecoverySupervisor(mock)
	ap := recoveryTestAgent(worktree)
	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false, want cold-start claim")
	}
	if ap.AssignedTaskID != "T-other" {
		t.Fatalf("AssignedTaskID = %q, want T-other", ap.AssignedTaskID)
	}
	for _, call := range mock.Calls {
		if call.Method == "ClaimIssue" && call.Args[0] == "T-blocked" {
			t.Fatalf("quarantined task was claimed: %#v", mock.Calls)
		}
	}
	// The lock is released so the next cycle starts clean, but everything that
	// carries the interrupted run's work must survive the refusal.
	lockPath := filepath.Join(cli.ResolveLockDir(worktree), cli.LockFileName)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock %s still exists: %v", lockPath, err)
	}
	checkpoint := filepath.Join(cli.ResolveLockDir(worktree), config.CheckpointFileName)
	if _, err := os.Stat(checkpoint); err != nil {
		t.Fatalf("checkpoint did not survive the refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "handoff-notes.md")); err != nil {
		t.Fatalf("untracked work-in-progress did not survive the refusal: %v", err)
	}
}

// Once the task is unblocked the same remnant recovers normally: nothing was
// destroyed, so the guard needs no un-parking step.
func TestPreFlightSetup_UnblockedRecoveryResumesAgain(t *testing.T) {
	worktree := gitWorktreeWithWIP(t, "handoff-notes.md")
	mock := statusBackend("open")
	seedLock(t, worktree, &cli.LockInfo{
		PID: deadPID, TaskID: "T-released", ClaudeSessionID: "session", TaskStartedAt: time.Now(),
	})
	mustSaveCheckpoint(t, worktree, "T-released")

	s := newRecoverySupervisor(mock)
	ap := recoveryTestAgent(worktree)
	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false, want recovery claim")
	}
	if ap.AssignedTaskID != "T-released" || ap.RecoveryMode != recoverResume {
		t.Fatalf("task=%q mode=%s, want T-released/resume", ap.AssignedTaskID, modeName(ap.RecoveryMode))
	}
}

func TestPreFlightSetup_OpenRecoveryStillResumes(t *testing.T) {
	worktree := t.TempDir()
	mock := statusBackend("open")
	s := newRecoverySupervisor(mock)
	seedLock(t, worktree, &cli.LockInfo{
		PID: deadPID, TaskID: "T-open", ClaudeSessionID: "session", TaskStartedAt: time.Now(),
	})
	ap := recoveryTestAgent(worktree)
	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false, want recovery claim")
	}
	if ap.AssignedTaskID != "T-open" || ap.RecoveryMode != recoverResume {
		t.Fatalf("task=%q mode=%s, want T-open/resume", ap.AssignedTaskID, modeName(ap.RecoveryMode))
	}
}

// Statuses other than blocked are ordinary outcomes of an incomplete exit-0 run
// — the shapes resume and checkpoint recovery exist for — so the guard must not
// refuse them.
func TestRecoveryTaskAvailable_RefusesOnlyBlocked(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{"open", true},
		{"in_progress", true},
		{"review", true},
		{"deferred", true},
		{"hooked", true},
		{"closed", true},
		{"blocked", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			s := newRecoverySupervisor(statusBackend(tc.status))
			if got := s.recoveryTaskAvailable(recoveryTestAgent(t.TempDir()), "T-1"); got != tc.want {
				t.Fatalf("recoveryTaskAvailable(status=%s) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// A remnant is the best evidence of an interrupted run, so a task the guard
// cannot read is recovered rather than abandoned.
func TestRecoveryTaskAvailable_PermissiveWhenUnreadable(t *testing.T) {
	t.Run("no backend", func(t *testing.T) {
		s := newTestSupervisorWithConfig(&config.DaemonConfig{})
		s.EmitEvent = func(events.Event) {}
		if !s.recoveryTaskAvailable(recoveryTestAgent(t.TempDir()), "T-1") {
			t.Fatal("recoveryTaskAvailable = false with no issue backend, want true")
		}
	})
	t.Run("read fails", func(t *testing.T) {
		mock := clitest.NewMockIssueBackend()
		mock.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
			return nil, errors.New("control plane unreachable")
		}
		s := newRecoverySupervisor(mock)
		if !s.recoveryTaskAvailable(recoveryTestAgent(t.TempDir()), "T-1") {
			t.Fatal("recoveryTaskAvailable = false on a failed read, want true")
		}
	})
}

// The checkpoint-only remnant (no surviving lock) is the shape a daemon restart
// leaves behind. It must be guarded too, and just as non-destructively.
func TestPreFlightSetup_QuarantinedCheckpointOnlyRemnantColdStarts(t *testing.T) {
	worktree := gitWorktreeWithWIP(t, "handoff-notes.md")
	mock := statusBackend("blocked")
	mock.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		if id == "T-blocked" {
			return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "blocked"}}, nil
		}
		return nil, nil
	}
	mock.ReadyResult = []backend.IssueData{{ID: "T-other", Status: "open", IssueType: "task", Priority: 1, Title: "Other", Design: "plan"}}
	mustSaveCheckpoint(t, worktree, "T-blocked")

	s := newRecoverySupervisor(mock)
	ap := recoveryTestAgent(worktree)
	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false, want cold-start claim")
	}
	if ap.AssignedTaskID != "T-other" {
		t.Fatalf("AssignedTaskID = %q, want T-other", ap.AssignedTaskID)
	}
	if _, err := os.Stat(filepath.Join(cli.ResolveLockDir(worktree), config.CheckpointFileName)); err != nil {
		t.Fatalf("checkpoint did not survive the refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "handoff-notes.md")); err != nil {
		t.Fatalf("untracked work-in-progress did not survive the refusal: %v", err)
	}
}
