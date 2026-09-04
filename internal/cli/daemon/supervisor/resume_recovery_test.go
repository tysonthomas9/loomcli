package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// deadPID is a process id certain not to be running, so CheckLock classifies a
// written lock as a crash remnant (a dead-PID lock is the recovery trigger).
// macOS/Linux PIDs never reach this value, so Kill→ESRCH.
const deadPID = 2000000000

func modeName(m recoveryMode) string {
	switch m {
	case recoverResume:
		return "resume"
	case recoverCheckpoint:
		return "checkpoint"
	default:
		return "cold"
	}
}

// seedLock writes a lock into the worktree's resolved lock dir (the path
// detectRecovery reads via cli.CheckLock).
func seedLock(t *testing.T, worktree string, info *cli.LockInfo) {
	t.Helper()
	lockDir := cli.ResolveLockDir(worktree)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	writeLockFile(t, lockDir, info)
}

// TestDetectRecovery is the crash→restart decision: a surviving crash-remnant
// lock (dead PID + carried task, within TTL) resumes the SAME task while a
// session is available and the resume-failure count is under the ceiling;
// escalates to a checkpoint retry once resume is exhausted; and cold-starts for
// everything else (live agent, no task/session, stale, fully exhausted).
func TestDetectRecovery(t *testing.T) {
	recent := time.Now()
	stale := time.Now().Add(-2 * time.Hour) // well past the 30m default resume TTL

	cases := []struct {
		name     string
		lock     *cli.LockInfo
		fails    int
		wantTask string
		wantMode recoveryMode
	}{
		{
			name:     "crash remnant resumes same task",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-1", ClaudeSessionID: "sess-1", RunID: "run-1", TaskStartedAt: recent},
			wantTask: "T-1",
			wantMode: recoverResume,
		},
		{
			name:     "resume exhausted escalates to checkpoint",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-2", ClaudeSessionID: "sess-2", TaskStartedAt: recent},
			fails:    maxResumeFailures,
			wantTask: "T-2",
			wantMode: recoverCheckpoint,
		},
		{
			name:     "checkpoint exhausted cold-starts a fresh task",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-3", ClaudeSessionID: "sess-3", TaskStartedAt: recent},
			fails:    maxResumeFailures + 1,
			wantTask: "",
			wantMode: recoverCold,
		},
		{
			name:     "live agent is not a crash",
			lock:     &cli.LockInfo{PID: os.Getpid(), TaskID: "T-4", ClaudeSessionID: "sess-4", TaskStartedAt: recent},
			wantTask: "",
			wantMode: recoverCold,
		},
		{
			name:     "no carried session cold-starts (out of scope for resume)",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-5", TaskStartedAt: recent},
			wantTask: "",
			wantMode: recoverCold,
		},
		{
			name:     "no task id cold-starts",
			lock:     &cli.LockInfo{PID: deadPID, ClaudeSessionID: "sess-6", TaskStartedAt: recent},
			wantTask: "",
			wantMode: recoverCold,
		},
		{
			name:     "stale remnant cold-starts",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-7", ClaudeSessionID: "sess-7", TaskStartedAt: stale},
			wantTask: "",
			wantMode: recoverCold,
		},
	}

	s := &Supervisor{} // detectRecovery uses only ap + the lock on disk
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wt := t.TempDir()
			seedLock(t, wt, tc.lock)
			ap := &AgentProcess{
				Entry:          config.AgentEntry{Worktree: "agent"},
				WorktreePath:   wt,
				ResumeFailures: tc.fails,
			}
			gotTask, gotMode := s.detectRecovery(ap)
			if gotTask != tc.wantTask || gotMode != tc.wantMode {
				t.Errorf("detectRecovery = (%q, %s), want (%q, %s)",
					gotTask, modeName(gotMode), tc.wantTask, modeName(tc.wantMode))
			}
		})
	}
}

// TestDetectRecovery_NoLock: a worktree with no lock cold-starts.
func TestDetectRecovery_NoLock(t *testing.T) {
	s := &Supervisor{}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: t.TempDir()}
	if gotTask, gotMode := s.detectRecovery(ap); gotTask != "" || gotMode != recoverCold {
		t.Errorf("detectRecovery with no lock = (%q, %s), want (\"\", cold)", gotTask, modeName(gotMode))
	}
}

// TestRecordResumeOutcome verifies the resume-first / cold-start-fallback
// accounting: a failed recovery run (resume OR checkpoint) advances the counter
// toward the escalation ceiling, a clean recovery resets it, and a non-recovery
// (cold) cycle never touches it.
func TestRecordResumeOutcome(t *testing.T) {
	s := &Supervisor{}

	t.Run("cold cycle untouched", func(t *testing.T) {
		ap := &AgentProcess{RecoveryMode: recoverCold, ResumeFailures: 1, LastExitCode: 1}
		s.recordResumeOutcome(ap)
		if ap.ResumeFailures != 1 {
			t.Errorf("ResumeFailures = %d, want 1 (untouched)", ap.ResumeFailures)
		}
	})

	t.Run("failed resume advances counter", func(t *testing.T) {
		ap := &AgentProcess{RecoveryMode: recoverResume, ResumeFailures: 0, LastExitCode: 1}
		s.recordResumeOutcome(ap)
		if ap.ResumeFailures != 1 {
			t.Errorf("ResumeFailures = %d, want 1", ap.ResumeFailures)
		}
	})

	t.Run("failed checkpoint advances counter (toward cold-start)", func(t *testing.T) {
		ap := &AgentProcess{RecoveryMode: recoverCheckpoint, ResumeFailures: maxResumeFailures, LastExitCode: 1}
		s.recordResumeOutcome(ap)
		if ap.ResumeFailures != maxResumeFailures+1 {
			t.Errorf("ResumeFailures = %d, want %d", ap.ResumeFailures, maxResumeFailures+1)
		}
	})

	t.Run("clean recovery resets counter", func(t *testing.T) {
		ap := &AgentProcess{RecoveryMode: recoverResume, ResumeFailures: maxResumeFailures, LastExitCode: 0}
		s.recordResumeOutcome(ap)
		if ap.ResumeFailures != 0 {
			t.Errorf("ResumeFailures = %d, want 0", ap.ResumeFailures)
		}
	})
}

func TestPrepareCheckpointRetryClearsStaleOwnerSessionID(t *testing.T) {
	wt := t.TempDir()
	seedLock(t, wt, &cli.LockInfo{
		PID:             deadPID,
		TaskID:          "T-checkpoint",
		ClaudeSessionID: "sess-stale",
		TaskStartedAt:   time.Now(),
	})

	s := &Supervisor{}
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "agent"},
		WorktreePath: wt,
	}

	s.prepareCheckpointRetry(ap, "T-checkpoint")

	info, err := cli.ReadLockFile(wt)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.ClaudeSessionID != "" {
		t.Fatalf("ClaudeSessionID = %q, want cleared so checkpoint retry cold-starts", info.ClaudeSessionID)
	}
	if ap.ResumeTaskID != "T-checkpoint" {
		t.Fatalf("ResumeTaskID = %q, want T-checkpoint", ap.ResumeTaskID)
	}
}

// ---------------------------------------------------------------------------
// Claim release after a kill
//
// The lock's TaskID is not an authoritative record of the claim: automode
// clears it mid-run while the fleet-db claim is still held. These tests pin the
// two paths that must release it anyway — the post-exit chain, and the cold
// pre-flight after a daemon restart.
// ---------------------------------------------------------------------------

// installMockIssueBackend makes cli.DefaultIssueBackend (what RecoverWorktree
// resolves) return a mock for the duration of the test.
func installMockIssueBackend(t *testing.T, status string) *clitest.MockIssueBackend {
	t.Helper()
	m := clitest.NewMockIssueBackend()
	m.GetResult = &backend.IssueDetailData{
		IssueData: backend.IssueData{ID: "T-1", Status: status, Assignee: "falcon"},
	}
	m.ListResult = []backend.IssueData{}
	cli.SetDefaultIssueBackend(m)
	t.Cleanup(cli.ResetDefaultIssueBackend)
	return m
}

// resetToOpen reports whether the mock saw the task put back on the queue.
func resetToOpen(m *clitest.MockIssueBackend, id string) bool {
	for _, c := range m.Calls {
		if c.Method != "Update" || len(c.Args) < 2 {
			continue
		}
		if got, _ := c.Args[0].(string); got != id {
			continue
		}
		params, ok := c.Args[1].(backend.UpdateParams)
		if ok && params.Status != nil && *params.Status == "open" {
			return true
		}
	}
	return false
}

func seedCheckpoint(t *testing.T, worktree string, cp *config.Checkpoint) {
	t.Helper()
	lockDir := cli.ResolveLockDir(worktree)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := config.SaveCheckpoint(lockDir, cp); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
}

func TestPostMortemRecovery_EscalatedYieldRunsRecovery(t *testing.T) {
	// An escalated yield is a kill: the claim must be released and the task
	// requeued, even though a yield was requested for this run.
	m := installMockIssueBackend(t, "in_progress")
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "falcon"},
		WorktreePath:   t.TempDir(),
		AssignedTaskID: "T-1",
		YieldRequested: true,
		YieldEscalated: true,
	}
	writeYieldFile(t, ap.WorktreePath, "drained")

	s.postMortemRecovery(ap, -1)

	if m.CallCount("ReleaseIssueLock") == 0 {
		t.Error("no ReleaseIssueLock; a SIGTERMed agent leaks its fleet-db claim")
	}
	if !resetToOpen(m, "T-1") {
		t.Error("task was not reset to open; it would wedge in in_progress with no session")
	}
}

func TestPostMortemRecovery_GracefulYieldKeepsClaim(t *testing.T) {
	// The regression guard: a yield the agent honored keeps its claim so the
	// next cycle resumes the same task from the yield checkpoint.
	m := installMockIssueBackend(t, "in_progress")
	s := newDrainTestSupervisor(&config.DaemonConfig{})
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "falcon"},
		WorktreePath:   t.TempDir(),
		AssignedTaskID: "T-1",
		YieldRequested: true,
	}

	s.postMortemRecovery(ap, 0)

	if len(m.Calls) != 0 {
		t.Errorf("issue backend was called on a graceful yield: %#v", m.Calls)
	}
}

func TestCheckpointRecoveryTask(t *testing.T) {
	fresh := time.Now()
	cases := []struct {
		name     string
		cp       *config.Checkpoint
		lock     *cli.LockInfo
		wantTask string
		wantExit int
	}{
		{
			name:     "fresh checkpoint for this agent",
			cp:       &config.Checkpoint{AgentName: "falcon", TaskID: "T-1", ExitCode: -1, Timestamp: fresh},
			wantTask: "T-1",
			wantExit: -1,
		},
		{
			name: "checkpoint older than the resume TTL",
			cp:   &config.Checkpoint{AgentName: "falcon", TaskID: "T-1", ExitCode: -1, Timestamp: time.Now().Add(-2 * time.Hour)},
		},
		{
			name: "checkpoint written by another agent",
			cp:   &config.Checkpoint{AgentName: "ember", TaskID: "T-1", ExitCode: -1, Timestamp: fresh},
		},
		{
			name: "checkpoint with no task",
			cp:   &config.Checkpoint{AgentName: "falcon", ExitCode: -1, Timestamp: fresh},
		},
		{
			name: "no checkpoint at all",
		},
		{
			name:     "lock names a different task",
			cp:       &config.Checkpoint{AgentName: "falcon", TaskID: "T-1", ExitCode: -1, Timestamp: fresh},
			lock:     &cli.LockInfo{PID: deadPID, AgentName: "falcon", TaskID: "T-2"},
			wantTask: "",
		},
		{
			name:     "lock agrees but carries no task",
			cp:       &config.Checkpoint{AgentName: "falcon", TaskID: "T-1", ExitCode: 0, Timestamp: fresh},
			lock:     &cli.LockInfo{PID: deadPID, AgentName: "falcon"},
			wantTask: "T-1",
			wantExit: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ap := &AgentProcess{
				Entry:        config.AgentEntry{Worktree: "falcon"},
				WorktreePath: t.TempDir(),
			}
			if tc.cp != nil {
				seedCheckpoint(t, ap.WorktreePath, tc.cp)
			}
			if tc.lock != nil {
				seedLock(t, ap.WorktreePath, tc.lock)
			}
			task, exit := checkpointRecoveryTask(ap)
			if task != tc.wantTask || exit != tc.wantExit {
				t.Errorf("checkpointRecoveryTask() = (%q, %d), want (%q, %d)", task, exit, tc.wantTask, tc.wantExit)
			}
		})
	}
}

func TestCheckpointRecoveryTask_CorruptCheckpoint(t *testing.T) {
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: t.TempDir(),
	}
	lockDir := cli.ResolveLockDir(ap.WorktreePath)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, config.CheckpointFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	if task, exit := checkpointRecoveryTask(ap); task != "" || exit != 0 {
		t.Errorf("checkpointRecoveryTask() = (%q, %d), want (\"\", 0) for a corrupt checkpoint", task, exit)
	}
}

func TestPreFlightSetup_ColdWithCheckpointReleasesClaim(t *testing.T) {
	// The cross-restart case: the daemon restarted, so AssignedTaskID is gone,
	// and the surviving lock carries no task (automode cleared it). The
	// checkpoint is the only record of the claim that must be released.
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: true}, nil
	})
	m := installMockIssueBackend(t, "in_progress")

	s := newBackendUnavailableSupervisor()
	s.IssueBackend = m
	ap := newBackendUnavailableAgentProcess()
	ap.Entry.Worktree = "falcon"
	ap.WorktreePath = t.TempDir()
	seedLock(t, ap.WorktreePath, &cli.LockInfo{PID: deadPID, AgentName: "falcon"})
	seedCheckpoint(t, ap.WorktreePath, &config.Checkpoint{
		AgentName: "falcon", TaskID: "T-1", ExitCode: -1, Timestamp: time.Now(),
	})

	// No claimable work, so pre-flight ends without a task — but recovery has
	// already run by then, which is what this asserts.
	if s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned true, want false (no claimable task)")
	}

	if m.CallCount("ReleaseIssueLock") == 0 {
		t.Error("no ReleaseIssueLock; the claim survives the daemon restart and the task stays invisible to the ready queue")
	}
	if !resetToOpen(m, "T-1") {
		t.Error("task was not reset to open by cold pre-flight recovery")
	}
}
