package supervisor

import (
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// deadPID is a process id certain not to be running, so CheckLock classifies a
// written lock as a crash remnant (a dead-PID lock + carried session is the
// resume trigger). macOS/Linux PIDs never reach this value, so Kill→ESRCH.
const deadPID = 2000000000

// seedLock writes a lock into the worktree's resolved lock dir (the path
// detectResumableTask reads via cli.CheckLock).
func seedLock(t *testing.T, worktree string, info *cli.LockInfo) {
	t.Helper()
	lockDir := cli.ResolveLockDir(worktree)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	writeLockFile(t, lockDir, info)
}

// TestDetectResumableTask is the crash→restart→resume DECISION: a surviving
// crash-remnant lock (dead PID + carried Claude session + task, within TTL,
// under the failure ceiling) resumes the SAME task; everything else cold-starts.
func TestDetectResumableTask(t *testing.T) {
	recent := time.Now()
	stale := time.Now().Add(-2 * time.Hour) // well past the 30m default resume TTL

	cases := []struct {
		name     string
		lock     *cli.LockInfo
		fails    int
		wantTask string
	}{
		{
			name:     "crash remnant resumes same task",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-1", ClaudeSessionID: "sess-1", RunID: "run-1", TaskStartedAt: recent},
			wantTask: "T-1",
		},
		{
			name:     "live agent is not a crash",
			lock:     &cli.LockInfo{PID: os.Getpid(), TaskID: "T-2", ClaudeSessionID: "sess-2", TaskStartedAt: recent},
			wantTask: "",
		},
		{
			name:     "no carried session cold-starts",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-3", TaskStartedAt: recent},
			wantTask: "",
		},
		{
			name:     "no task id cold-starts",
			lock:     &cli.LockInfo{PID: deadPID, ClaudeSessionID: "sess-4", TaskStartedAt: recent},
			wantTask: "",
		},
		{
			name:     "stale session cold-starts",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-5", ClaudeSessionID: "sess-5", TaskStartedAt: stale},
			wantTask: "",
		},
		{
			name:     "failure ceiling cold-starts (fall back from resume)",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-6", ClaudeSessionID: "sess-6", TaskStartedAt: recent},
			fails:    maxResumeFailures,
			wantTask: "",
		},
	}

	s := &Supervisor{} // detectResumableTask uses only ap + the lock on disk
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wt := t.TempDir()
			seedLock(t, wt, tc.lock)
			ap := &AgentProcess{
				Entry:          config.AgentEntry{Worktree: "agent"},
				WorktreePath:   wt,
				ResumeFailures: tc.fails,
			}
			if got := s.detectResumableTask(ap); got != tc.wantTask {
				t.Errorf("detectResumableTask = %q, want %q", got, tc.wantTask)
			}
		})
	}
}

// TestDetectResumableTask_NoLock: a worktree with no lock cold-starts.
func TestDetectResumableTask_NoLock(t *testing.T) {
	s := &Supervisor{}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: t.TempDir()}
	if got := s.detectResumableTask(ap); got != "" {
		t.Errorf("detectResumableTask with no lock = %q, want \"\"", got)
	}
}

// TestRecordResumeOutcome verifies the resume-first / cold-start-fallback
// accounting: a failed resume advances the counter (toward the ceiling), a clean
// resume resets it, and a non-resume cycle never touches it.
func TestRecordResumeOutcome(t *testing.T) {
	s := &Supervisor{}

	t.Run("non-resume cycle untouched", func(t *testing.T) {
		ap := &AgentProcess{ResumeTaskID: "", ResumeFailures: 1, LastExitCode: 1}
		s.recordResumeOutcome(ap)
		if ap.ResumeFailures != 1 {
			t.Errorf("ResumeFailures = %d, want 1 (untouched)", ap.ResumeFailures)
		}
	})

	t.Run("failed resume advances counter", func(t *testing.T) {
		ap := &AgentProcess{ResumeTaskID: "T-1", ResumeFailures: 0, LastExitCode: 1}
		s.recordResumeOutcome(ap)
		if ap.ResumeFailures != 1 {
			t.Errorf("ResumeFailures = %d, want 1", ap.ResumeFailures)
		}
	})

	t.Run("clean resume resets counter", func(t *testing.T) {
		ap := &AgentProcess{ResumeTaskID: "T-1", ResumeFailures: maxResumeFailures, LastExitCode: 0}
		s.recordResumeOutcome(ap)
		if ap.ResumeFailures != 0 {
			t.Errorf("ResumeFailures = %d, want 0", ap.ResumeFailures)
		}
	})
}
