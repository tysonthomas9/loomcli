package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
// cold-starts the SAME task through the checkpoint path when a backend did not
// capture a resumable session; escalates to a checkpoint retry once resume is
// exhausted; and cold-starts a fresh task for everything else. A cold result
// still returns a surviving lock task as a cleanup signal so preflight resets
// it before selecting fresh work.
func TestDetectRecovery(t *testing.T) {
	t.Setenv("LOOM_RESUME_TTL", "30m")
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
			wantTask: "T-3",
			wantMode: recoverCold,
		},
		{
			name:     "live agent is not a crash",
			lock:     &cli.LockInfo{PID: os.Getpid(), TaskID: "T-4", ClaudeSessionID: "sess-4", TaskStartedAt: recent},
			wantTask: "T-4",
			wantMode: recoverCold,
		},
		{
			name:     "no carried session checkpoint-retries same task",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-5", TaskStartedAt: recent},
			wantTask: "T-5",
			wantMode: recoverCheckpoint,
		},
		{
			name:     "legacy no-session remnant uses recent process start",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-legacy-recent", StartedAt: recent},
			wantTask: "T-legacy-recent",
			wantMode: recoverCheckpoint,
		},
		{
			name:     "legacy no-session remnant uses stale process start",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-legacy-stale", StartedAt: stale},
			wantTask: "T-legacy-stale",
			wantMode: recoverCold,
		},
		{
			name:     "no-session remnant without timestamps cold-starts",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-no-clock"},
			wantTask: "T-no-clock",
			wantMode: recoverCold,
		},
		{
			name:     "no-session checkpoint failures eventually cold-start",
			lock:     &cli.LockInfo{PID: deadPID, TaskID: "T-5-exhausted", TaskStartedAt: recent},
			fails:    maxResumeFailures + 1,
			wantTask: "T-5-exhausted",
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
			wantTask: "T-7",
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

type ownClaimConflictBackend struct {
	*clitest.MockIssueBackend
	actorClaims []struct {
		taskID string
		actor  string
	}
}

func (b *ownClaimConflictBackend) ClaimIssueAsActor(_ context.Context, taskID string, _ time.Duration, actor string) error {
	b.actorClaims = append(b.actorClaims, struct {
		taskID string
		actor  string
	}{taskID: taskID, actor: actor})
	return &backend.BackendError{
		Kind:    backend.KindConflict,
		Op:      "ClaimIssueAsActor",
		Message: "task is already claimed by this agent",
		Meta:    map[string]string{"existing_owner": actor},
	}
}

// TestPreFlightSetup_NoSessionCrashReclaimsSameTask models the daemon-restart
// regression seen with Codex: the dead active lock has a task and run id, but no
// ClaudeSessionID, while fleet-db still considers the task in_progress and
// claimed by this worktree. The recovery must bypass Ready (which cannot return
// an in_progress task), retain the worktree, and re-use the existing claim.
func TestPreFlightSetup_NoSessionCrashReclaimsSameTask(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	wt := t.TempDir()
	seedLock(t, wt, &cli.LockInfo{
		PID:           deadPID,
		Command:       "plan",
		AgentName:     "codex-planner",
		TaskID:        "LOCALMODE-8",
		RunID:         "run-codex-without-resume-session",
		TaskStartedAt: time.Now(),
		State:         cli.StateActive,
	})
	sentinel := filepath.Join(wt, "interrupted-work.txt")
	if err := os.WriteFile(sentinel, []byte("preserve this worktree state"), 0o600); err != nil {
		t.Fatalf("write interrupted work sentinel: %v", err)
	}

	mock := clitest.NewMockIssueBackend()
	// An in_progress task is deliberately absent from Ready. Any Ready call
	// therefore proves the supervisor fell back to the stranding path.
	mock.ReadyResult = nil
	issues := &ownClaimConflictBackend{MockIssueBackend: mock}
	s := &Supervisor{
		IssueBackend: issues,
		ConfigSnapshot: func() *config.DaemonConfig {
			return &config.DaemonConfig{}
		},
	}
	ap := &AgentProcess{
		Entry: config.AgentEntry{
			Worktree: "codex-planner",
			Role:     "plan",
		},
		RoleConfig:   config.RoleConfig{TaskFilter: "no_design"},
		WorktreePath: wt,
	}

	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false; dead no-session task was not recovered")
	}
	if ap.RecoveryMode != recoverCheckpoint {
		t.Fatalf("RecoveryMode = %s, want checkpoint", modeName(ap.RecoveryMode))
	}
	if ap.AssignedTaskID != "LOCALMODE-8" {
		t.Fatalf("AssignedTaskID = %q, want interrupted task LOCALMODE-8", ap.AssignedTaskID)
	}
	if len(issues.actorClaims) != 1 || issues.actorClaims[0].taskID != "LOCALMODE-8" || issues.actorClaims[0].actor != "codex-planner" {
		t.Fatalf("actor claims = %#v, want one same-task claim by codex-planner", issues.actorClaims)
	}
	for _, call := range mock.Calls {
		if call.Method == "Ready" {
			t.Fatalf("Ready was called for an in_progress recovery task: %#v", mock.Calls)
		}
	}

	info, err := cli.ReadLockFile(wt)
	if err != nil {
		t.Fatalf("ReadLockFile after recovery: %v", err)
	}
	if info.TaskID != "LOCALMODE-8" || info.RunID != "run-codex-without-resume-session" {
		t.Fatalf("recovery lock identity changed: %+v", info)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("interrupted worktree state was removed: %v", err)
	}
	if string(got) != "preserve this worktree state" {
		t.Fatalf("interrupted worktree state changed: %q", got)
	}
}

// TestPreFlightSetup_ExhaustedRecoveryResetsInterruptedTask proves the final
// resume/checkpoint failure cannot strand the old task in_progress while the
// agent moves on to fresh work. Cold recovery must treat the lock-owned task
// as failed, release/reset it, and only then consult Ready.
func TestPreFlightSetup_ExhaustedRecoveryResetsInterruptedTask(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	wt := t.TempDir()
	seedLock(t, wt, &cli.LockInfo{
		PID:           deadPID,
		Command:       "plan",
		AgentName:     "codex-planner",
		TaskID:        "LOCALMODE-exhausted",
		RunID:         "run-exhausted",
		TaskStartedAt: time.Now(),
		State:         cli.StateActive,
	})

	mock := clitest.NewMockIssueBackend()
	mock.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID: "LOCALMODE-exhausted", Status: "in_progress", Assignee: "codex-planner",
	}}
	mock.ReadyResult = []backend.IssueData{{
		ID: "LOCALMODE-fresh", Title: "Fresh task", Status: "open", IssueType: "task",
	}}
	cli.SetDefaultIssueBackend(mock)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	s := &Supervisor{
		IssueBackend: mock,
		ConfigSnapshot: func() *config.DaemonConfig {
			return &config.DaemonConfig{}
		},
	}
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "codex-planner", Role: "plan"},
		RoleConfig:     config.RoleConfig{TaskFilter: "any"},
		WorktreePath:   wt,
		ResumeFailures: maxResumeFailures + 1,
	}

	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false after exhausted recovery")
	}
	if ap.RecoveryMode != recoverCold {
		t.Fatalf("RecoveryMode = %s, want cold", modeName(ap.RecoveryMode))
	}
	if ap.AssignedTaskID != "LOCALMODE-fresh" {
		t.Fatalf("AssignedTaskID = %q, want fresh task", ap.AssignedTaskID)
	}
	if ap.ResumeFailures != 0 {
		t.Fatalf("ResumeFailures = %d, want reset to 0", ap.ResumeFailures)
	}

	var released, reset bool
	for _, call := range mock.Calls {
		switch call.Method {
		case "ReleaseIssueLock":
			if call.Args[0] == "LOCALMODE-exhausted" && call.Args[1] == "codex-planner" {
				released = true
			}
		case "Update":
			if call.Args[0] != "LOCALMODE-exhausted" {
				continue
			}
			params, ok := call.Args[1].(backend.UpdateParams)
			if ok && params.Status != nil && *params.Status == "open" &&
				params.Assignee != nil && *params.Assignee == "" {
				reset = true
			}
		}
	}
	if !released || !reset {
		t.Fatalf("exhausted task cleanup calls: released=%v reset=%v calls=%#v", released, reset, mock.Calls)
	}
}

// TestPreFlightSetup_ColdRecoveryReleaseFailureIsNonDestructive models a stale
// agent A lock after task ownership moved to agent B (conflict), plus an
// indeterminate backend failure. Both must fail closed before Get/Update,
// orphan scanning, Ready, or worktree cleanup; otherwise stale recovery can
// reopen and unassign B's active task.
func TestPreFlightSetup_ColdRecoveryReleaseFailureIsNonDestructive(t *testing.T) {
	cases := []struct {
		name       string
		releaseErr error
	}{
		{
			name:       "task now claimed by another agent",
			releaseErr: backend.ErrConflict("ReleaseIssueLock", "lock is owned by agent-b"),
		},
		{
			name:       "release result is unknown",
			releaseErr: errors.New("fleet-db connection dropped"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
			cli.ResetWorkspaceRuntimeDirCache()
			t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

			wt := t.TempDir()
			seedLock(t, wt, &cli.LockInfo{
				PID:           deadPID,
				Command:       "plan",
				AgentName:     "agent-a",
				TaskID:        "TASK-owned-by-b",
				TaskStartedAt: time.Now(),
				State:         cli.StateActive,
			})
			sentinel := filepath.Join(wt, "agent-a-work.txt")
			if err := os.WriteFile(sentinel, []byte("do not clean on uncertain recovery"), 0o600); err != nil {
				t.Fatalf("write worktree sentinel: %v", err)
			}

			mock := clitest.NewMockIssueBackend()
			mock.ReleaseIssueLockErr = tc.releaseErr
			mock.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{
				ID: "TASK-owned-by-b", Status: "in_progress", Assignee: "agent-b",
			}}
			mock.ReadyResult = []backend.IssueData{{ID: "TASK-fresh", Status: "open", IssueType: "task"}}
			cli.SetDefaultIssueBackend(mock)
			t.Cleanup(cli.ResetDefaultIssueBackend)

			s := &Supervisor{
				IssueBackend: mock,
				ConfigSnapshot: func() *config.DaemonConfig {
					return &config.DaemonConfig{}
				},
			}
			ap := &AgentProcess{
				Entry:          config.AgentEntry{Worktree: "agent-a", Role: "plan"},
				RoleConfig:     config.RoleConfig{TaskFilter: "any"},
				WorktreePath:   wt,
				ResumeFailures: maxResumeFailures + 1,
			}

			if s.preFlightSetup(ap) {
				t.Fatal("preFlightSetup succeeded despite an unproven release")
			}
			if mock.CallCount("ReleaseIssueLock") != 1 {
				t.Fatalf("ReleaseIssueLock calls = %d, want 1", mock.CallCount("ReleaseIssueLock"))
			}
			for _, method := range []string{"Get", "Update", "List", "Ready"} {
				if mock.Called(method) {
					t.Fatalf("%s called after release failure; calls=%#v", method, mock.Calls)
				}
			}
			if _, err := cli.ReadLockFile(wt); err != nil {
				t.Fatalf("recovery lock was not preserved: %v", err)
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "do not clean on uncertain recovery" {
				t.Fatalf("worktree state changed after release failure: data=%q err=%v", got, err)
			}
			if ap.ResumeFailures != maxResumeFailures+1 {
				t.Fatalf("ResumeFailures = %d, want cold-recovery sentinel %d", ap.ResumeFailures, maxResumeFailures+1)
			}
		})
	}
}

// TestPreFlightSetup_ColdRecoveryRechecksOwnershipAfterRelease covers the
// release-to-reset handoff: even after agent A successfully releases its old
// lock, recovery must re-read the task and skip the destructive reset if agent
// B now owns it. Release must precede that ownership read.
func TestPreFlightSetup_ColdRecoveryRechecksOwnershipAfterRelease(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	wt := t.TempDir()
	seedLock(t, wt, &cli.LockInfo{
		PID:           deadPID,
		Command:       "plan",
		AgentName:     "agent-a",
		TaskID:        "TASK-handoff",
		TaskStartedAt: time.Now(),
		State:         cli.StateActive,
	})

	mock := clitest.NewMockIssueBackend()
	mock.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID: "TASK-handoff", Status: "in_progress", Assignee: "agent-b",
	}}
	mock.ReadyResult = []backend.IssueData{{
		ID: "TASK-fresh", Title: "Fresh task", Status: "open", IssueType: "task",
	}}
	cli.SetDefaultIssueBackend(mock)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	s := &Supervisor{
		IssueBackend: mock,
		ConfigSnapshot: func() *config.DaemonConfig {
			return &config.DaemonConfig{}
		},
	}
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "agent-a", Role: "plan"},
		RoleConfig:     config.RoleConfig{TaskFilter: "any"},
		WorktreePath:   wt,
		ResumeFailures: maxResumeFailures + 1,
	}

	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false after safe ownership handoff")
	}
	if ap.AssignedTaskID != "TASK-fresh" {
		t.Fatalf("AssignedTaskID = %q, want TASK-fresh", ap.AssignedTaskID)
	}

	releaseIndex, getIndex := -1, -1
	for i, call := range mock.Calls {
		switch call.Method {
		case "ReleaseIssueLock":
			if call.Args[0] == "TASK-handoff" {
				releaseIndex = i
			}
		case "Get":
			if call.Args[0] == "TASK-handoff" {
				getIndex = i
			}
		case "Update":
			if call.Args[0] == "TASK-handoff" {
				t.Fatalf("stale recovery reset agent B's task: calls=%#v", mock.Calls)
			}
		}
	}
	if releaseIndex < 0 || getIndex <= releaseIndex {
		t.Fatalf("guard order release=%d get=%d calls=%#v", releaseIndex, getIndex, mock.Calls)
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
