package supervisor

import (
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
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

// --- bounded recovery ladder (PUPPET-127) ---

// TestRecordRecoveryFailure: a recovery cycle that aborts during pre-flight
// (before any spawn) still advances the ladder, so a claim that keeps failing
// for an unclassifiable reason cannot loop forever.
func TestRecordRecoveryFailure(t *testing.T) {
	s := &Supervisor{}

	t.Run("cold cycle untouched", func(t *testing.T) {
		ap := &AgentProcess{RecoveryMode: recoverCold, ResumeFailures: 1}
		s.recordRecoveryFailure(ap)
		if ap.ResumeFailures != 1 {
			t.Errorf("ResumeFailures = %d, want 1 (untouched)", ap.ResumeFailures)
		}
	})

	for _, mode := range []recoveryMode{recoverResume, recoverCheckpoint} {
		t.Run(modeName(mode)+" advances counter", func(t *testing.T) {
			ap := &AgentProcess{RecoveryMode: mode, ResumeFailures: 1}
			s.recordRecoveryFailure(ap)
			if ap.ResumeFailures != 2 {
				t.Errorf("ResumeFailures = %d, want 2", ap.ResumeFailures)
			}
		})
	}
}

// TestRecoveryLadderTerminatesWithoutAbandon: even when the lock cannot be
// cleared (a live PID reclaimed the worktree, a read-only lock dir, ...), the
// counter alone caps the retries at resume ×maxResumeFailures → checkpoint ×1 →
// cold-start.
func TestRecoveryLadderTerminatesWithoutAbandon(t *testing.T) {
	wt := t.TempDir()
	seedLock(t, wt, &cli.LockInfo{
		PID: deadPID, TaskID: "T-stuck", ClaudeSessionID: "sess-1", TaskStartedAt: time.Now(),
	})

	s := &Supervisor{}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: wt}

	want := []recoveryMode{recoverResume, recoverResume, recoverCheckpoint, recoverCold}
	for i, wantMode := range want {
		gotTask, gotMode := s.detectRecovery(ap)
		if gotMode != wantMode {
			t.Fatalf("cycle %d: mode = %s, want %s", i, modeName(gotMode), modeName(wantMode))
		}
		if wantMode == recoverCold && gotTask != "" {
			t.Fatalf("cycle %d: cold cycle still names task %q", i, gotTask)
		}
		ap.RecoveryMode = gotMode
		s.recordRecoveryFailure(ap)
	}
}

func TestAbandonResumeTarget(t *testing.T) {
	wt := t.TempDir()
	seedLock(t, wt, &cli.LockInfo{
		PID: deadPID, TaskID: "T-gone", ClaudeSessionID: "sess-1", TaskStartedAt: time.Now(),
	})

	s := &Supervisor{}
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "agent"},
		WorktreePath:   wt,
		ResumeFailures: maxResumeFailures,
	}

	s.abandonResumeTarget(ap, "T-gone")

	if ap.ResumeFailures != 0 {
		t.Errorf("ResumeFailures = %d, want 0 (the target is gone)", ap.ResumeFailures)
	}
	info, err := cli.ReadLockFile(wt)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if info.TaskID != "" {
		t.Errorf("TaskID = %q, want cleared", info.TaskID)
	}
}
