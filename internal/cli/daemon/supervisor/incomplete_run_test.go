package supervisor

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newClaimSupervisor returns a Supervisor whose classify path can reach the
// given issue backend. A nil mock models "no backend" — one of the
// cannot-tell cases that must fall back to clean-success behavior.
func newClaimSupervisor(mock *clitest.MockIssueBackend) *Supervisor {
	s := newTestSupervisor()
	if mock != nil {
		s.IssueBackend = mock
	}
	return s
}

// claimStateMock returns a backend whose Get reports the given claim state for
// any task — the only thing the incomplete-run discriminator reads.
func claimStateMock(status, assignee string) *clitest.MockIssueBackend {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = &backend.IssueDetailData{
		IssueData: backend.IssueData{Status: status, Assignee: assignee},
	}
	return mock
}

// newTaskAgent builds an AgentProcess holding taskID on its worktree lock —
// the shape every daemon worker exits in, since pre-flight claims the task and
// writes it to the lock before the agent ever runs.
func newTaskAgent(t *testing.T, name, taskID string) *AgentProcess {
	t.Helper()
	dir := t.TempDir()
	writeLockFile(t, dir, &cli.LockInfo{
		PID:             os.Getpid(),
		Command:         "task",
		AgentName:       name,
		TaskID:          taskID,
		TaskTitle:       "some work",
		ClaudeSessionID: "claude-" + taskID,
		StartedAt:       time.Now(),
	})
	return &AgentProcess{
		Entry:        config.AgentEntry{Worktree: name},
		WorktreePath: dir,
	}
}

func getCallCount(mock *clitest.MockIssueBackend) int {
	n := 0
	for _, c := range mock.Calls {
		if c.Method == "Get" {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// classifyAgentExit: the new outcome
// ---------------------------------------------------------------------------

func TestClassifyAgentExit_ExitZeroWithHeldClaim_IsIncompleteRun(t *testing.T) {
	ap := newTaskAgent(t, "falcon", "loom-77")
	s := newClaimSupervisor(claimStateMock("in_progress", "falcon"))

	s.classifyAgentExit(ap, 0)

	if ap.LastError == nil {
		t.Fatal("LastError = nil; an exit-0 run that never released its claim must not be a clean success")
	}
	if !ap.LastError.Class.Is(agenterr.IncompleteRunOutcome) {
		t.Errorf("class = %s, want IncompleteRun", ap.LastError.Class)
	}
	if ap.LastNoWork {
		t.Error("LastNoWork = true, want false: the agent held a task")
	}
}

// The mirror case: every shape in which the claim IS gone must keep the
// existing clean-success behavior, so a run that legitimately set
// review/closed/blocked is never re-opened or re-queued.
func TestClassifyAgentExit_ExitZeroWithReleasedClaim_StaysCleanSuccess(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		assignee string
	}{
		{"loom complete released it back to open", "open", ""},
		{"agent set review", "review", "falcon"},
		{"agent closed the task", "closed", "falcon"},
		{"agent blocked the task", "blocked", "falcon"},
		{"a sibling already re-claimed it", "in_progress", "ember"},
		{"in_progress but unowned", "in_progress", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ap := newTaskAgent(t, "falcon", "loom-77")
			s := newClaimSupervisor(claimStateMock(tc.status, tc.assignee))

			s.classifyAgentExit(ap, 0)

			if ap.LastError != nil {
				t.Errorf("LastError = %v, want nil (clean success)", ap.LastError)
			}
			if ap.LastNoWork {
				t.Error("LastNoWork = true, want false")
			}
		})
	}
}

// Positive evidence only: when the claim cannot be read we must not invent an
// incomplete run out of a backend blip.
func TestClassifyAgentExit_ExitZeroUnreadableClaim_StaysCleanSuccess(t *testing.T) {
	failing := clitest.NewMockIssueBackend()
	failing.GetErr = errors.New("fleet unreachable")

	missing := clitest.NewMockIssueBackend()
	missing.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) { return nil, nil }

	cases := []struct {
		name string
		mock *clitest.MockIssueBackend
	}{
		{"no issue backend configured", nil},
		{"GET failed", failing},
		{"issue not found", missing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ap := newTaskAgent(t, "falcon", "loom-77")
			s := newClaimSupervisor(tc.mock)

			s.classifyAgentExit(ap, 0)

			if ap.LastError != nil {
				t.Errorf("LastError = %v, want nil: an unreadable claim must keep the established behavior", ap.LastError)
			}
		})
	}
}

// A non-zero exit is log-classified exactly as before, and never pays for the
// claim GET — the discriminator only exists to split the exit-0 case.
func TestClassifyAgentExit_NonZeroExit_Unaffected(t *testing.T) {
	ap := newTaskAgent(t, "falcon", "loom-77")
	mock := claimStateMock("in_progress", "falcon")
	s := newClaimSupervisor(mock)

	s.classifyAgentExit(ap, 137)

	if ap.LastError == nil {
		t.Fatal("LastError = nil, want a classified error for exit 137")
	}
	if ap.LastError.Class.Is(agenterr.IncompleteRunOutcome) {
		t.Error("class = IncompleteRun; a non-zero exit must stay log-classified")
	}
	if n := getCallCount(mock); n != 0 {
		t.Errorf("issue GET count = %d, want 0 on the non-zero-exit path", n)
	}
}

// A worker with no task at all keeps classifying as NoWork rather than being
// dragged into the new branch.
func TestClassifyAgentExit_NoTask_StaysNoWork(t *testing.T) {
	mock := claimStateMock("in_progress", "falcon")
	s := newClaimSupervisor(mock)
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: t.TempDir(),
	}

	s.classifyAgentExit(ap, 0)

	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.NoWorkOutcome) {
		t.Fatalf("class = %v, want NoWork", ap.LastError)
	}
	if !ap.LastNoWork {
		t.Error("LastNoWork = false, want true")
	}
}

// ---------------------------------------------------------------------------
// the checkpoint survives
// ---------------------------------------------------------------------------

func TestHandleAgentCheckpoint_IncompleteRun_KeepsCheckpoint(t *testing.T) {
	ap := newTaskAgent(t, "falcon", "loom-88")
	ap.LastError = &agenterr.AgentError{
		Class: agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome),
	}
	lockDir := cli.ResolveLockDir(ap.WorktreePath)
	if err := config.SaveCheckpoint(lockDir, &config.Checkpoint{
		AgentName: "falcon",
		TaskID:    "loom-88",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SaveCheckpoint (setup) failed: %v", err)
	}

	newTestSupervisor().handleAgentCheckpoint(ap, 0)

	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint was cleared; an unfinished run's checkpoint is the only record of what the turn achieved")
	}
	if cp.TaskID != "loom-88" {
		t.Errorf("TaskID = %q, want loom-88", cp.TaskID)
	}
	if cp.ErrorClass != "IncompleteRun" {
		t.Errorf("ErrorClass = %q, want IncompleteRun", cp.ErrorClass)
	}
	if cp.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0: the run did not crash", cp.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// the restart budgets survive
// ---------------------------------------------------------------------------

func TestShouldRestart_IncompleteRun_DoesNotResetBudgets(t *testing.T) {
	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		LastExitCode: 0,
		RestartCount: 1,
		BlockCount:   2,
		LastError: &agenterr.AgentError{
			Class: agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome),
		},
	}

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true: an unfinished turn is retryable")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (counted, not reset)", ap.RestartCount)
	}
	if ap.BlockCount != 2 {
		t.Errorf("BlockCount = %d, want 2 (preserved)", ap.BlockCount)
	}
}

// The contrast that makes the above meaningful: a genuinely clean exit still
// zeroes everything, so the divergence is exactly the incomplete case.
func TestShouldRestart_CleanSuccess_StillResetsBudgets(t *testing.T) {
	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		LastExitCode: 0,
		RestartCount: 1,
		BlockCount:   2,
		NoWorkCount:  3,
	}

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true")
	}
	if ap.RestartCount != 0 || ap.BlockCount != 0 || ap.NoWorkCount != 0 {
		t.Errorf("counters = (restart %d, block %d, nowork %d), want all 0",
			ap.RestartCount, ap.BlockCount, ap.NoWorkCount)
	}
}

// ---------------------------------------------------------------------------
// the quarantine ledger survives
// ---------------------------------------------------------------------------

func TestRecordTaskExitForQuarantine_IncompleteRun_DoesNotEvictLedger(t *testing.T) {
	status, design := "open", "d"
	s := newQuarantineSupervisor(openIssueMock(&status, &design))
	ap := newKilledAgent(t, "falcon", "loom-99", timeoutOutcome())

	killNTimes(s, ap, 2)
	if got := recordCount(s, "loom-99"); got != 2 {
		t.Fatalf("ledger count after 2 kills = %d, want 2", got)
	}

	// An unfinished turn between the kills: the ledger must neither be wiped
	// (which is what hid this spiral) nor advanced (an unfinished turn is not
	// a task-fault kill).
	ap.LastError = &agenterr.AgentError{
		Class: agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome),
	}
	s.recordTaskExitForQuarantine(ap, 0)

	if got := recordCount(s, "loom-99"); got != 2 {
		t.Fatalf("ledger count after an incomplete run = %d, want 2 (survives, unchanged)", got)
	}
}

// The contrast: a genuinely clean run still evicts, so a completed task never
// carries a stale kill spiral forward.
func TestRecordTaskExitForQuarantine_CleanRun_StillEvictsLedger(t *testing.T) {
	status, design := "open", "d"
	s := newQuarantineSupervisor(openIssueMock(&status, &design))
	ap := newKilledAgent(t, "falcon", "loom-99", timeoutOutcome())

	killNTimes(s, ap, 2)

	ap.LastError = nil
	s.recordTaskExitForQuarantine(ap, 0)

	if r := record(s, "loom-99"); r != nil {
		t.Fatalf("ledger record = %+v, want nil (evicted on a clean run)", r)
	}
}
