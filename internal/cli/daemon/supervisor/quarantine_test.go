package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newQuarantineSupervisor returns a Supervisor wired with the given mock
// backend (nil is allowed: fetchIssueBaseline treats it as "unknown").
func newQuarantineSupervisor(mock *clitest.MockIssueBackend) *Supervisor {
	cfg := &config.DaemonConfig{}
	s := &Supervisor{ConfigSnapshot: func() *config.DaemonConfig { return cfg }}
	if mock != nil {
		s.IssueBackend = mock
	}
	return s
}

// newKilledAgent builds an AgentProcess in its post-classifyAgentExit shape
// for a task-holding kill: lock file written with taskID, LastError set.
func newKilledAgent(t *testing.T, name, taskID string, class agenterr.Outcome) *AgentProcess {
	t.Helper()
	dir := t.TempDir()
	if taskID != "" {
		writeLockFile(t, dir, &cli.LockInfo{
			PID:             os.Getpid(),
			AgentName:       name,
			TaskID:          taskID,
			ClaudeSessionID: "claude-" + taskID,
			RunID:           "run-" + taskID,
			StartedAt:       time.Now(),
		})
	}
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: name},
		WorktreePath: dir,
	}
	if class != (agenterr.Outcome{}) {
		ap.LastError = &agenterr.AgentError{Class: class, ExitCode: 137}
	}
	return ap
}

func timeoutOutcome() agenterr.Outcome {
	return agenterr.OutcomeFromHarness(wrapper.ErrTimeout)
}

// record returns the ledger record for taskID (nil if absent).
func record(s *Supervisor, taskID string) *taskFailureRecord {
	q := s.qrec()
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.rec[taskID]
}

func recordCount(s *Supervisor, taskID string) int {
	if r := record(s, taskID); r != nil {
		return r.Count
	}
	return 0
}

// gitCommit makes an (empty) commit in dir and returns the new HEAD.
func gitCommit(t *testing.T, dir, msg string) string {
	t.Helper()
	for _, args := range [][]string{
		{"commit", "--allow-empty", "-q", "-m", msg},
	} {
		runGit(t, dir, args...)
	}
	head := automode.CaptureHEADRef(dir)
	if head == "" {
		t.Fatal("CaptureHEADRef returned empty after commit")
	}
	return head
}

func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	return gitCommit(t, dir, "c0")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// ---------------------------------------------------------------------------
// record hook
// ---------------------------------------------------------------------------

func TestRecordTaskExit_IncrementsOnEligibleKillWithTask(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-1", timeoutOutcome())
	ap.StopReason = StopReasonWatchdog
	ap.AgentSessionID = "fleet-sess-1"

	s.recordTaskExitForQuarantine(ap, 137)

	rec := record(s, "T-1")
	if rec == nil {
		t.Fatal("expected a failure record for T-1")
	}
	if rec.Count != 1 {
		t.Fatalf("Count = %d, want 1", rec.Count)
	}
	if len(rec.Kills) != 1 {
		t.Fatalf("len(Kills) = %d, want 1", len(rec.Kills))
	}
	ev := rec.Kills[0]
	if ev.Agent != "falcon" || ev.StopReason != "watchdog" || ev.ErrClass != "Timeout" || ev.ExitCode != 137 {
		t.Errorf("killEvent = %+v, want falcon/watchdog/Timeout/137", ev)
	}
	if ev.FleetSessionID != "fleet-sess-1" {
		t.Errorf("FleetSessionID = %q, want fleet-sess-1 (captured before finalize clears it)", ev.FleetSessionID)
	}
	if ev.ClaudeSessionID != "claude-T-1" || ev.RunID != "run-T-1" {
		t.Errorf("lock identifiers = %q/%q, want claude-T-1/run-T-1", ev.ClaudeSessionID, ev.RunID)
	}
	if rec.LastKillReason != "watchdog/Timeout" {
		t.Errorf("LastKillReason = %q, want watchdog/Timeout", rec.LastKillReason)
	}
}

func TestRecordTaskExit_NoIncrementForDomainOutcome(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-2", agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome))

	s.recordTaskExitForQuarantine(ap, 1)

	if rec := record(s, "T-2"); rec != nil {
		t.Fatalf("domain outcome must not create a record, got %+v", rec)
	}
}

func TestRecordTaskExit_NoIncrementWhenNoTask(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "", timeoutOutcome())

	s.recordTaskExitForQuarantine(ap, 137)

	q := s.qrec()
	q.mu.Lock()
	n := len(q.rec)
	q.mu.Unlock()
	if n != 0 {
		t.Fatalf("ledger has %d records, want 0 (no task attached)", n)
	}
}

func TestRecordTaskExit_NoIncrementForRateLimitOrAuth(t *testing.T) {
	for _, class := range []wrapper.ErrorClass{wrapper.ErrRateLimited, wrapper.ErrAuth, wrapper.ErrBilling, wrapper.ErrModelNotFound} {
		s := newQuarantineSupervisor(nil)
		ap := newKilledAgent(t, "falcon", "T-3", agenterr.OutcomeFromHarness(class))
		s.recordTaskExitForQuarantine(ap, 1)
		if rec := record(s, "T-3"); rec != nil {
			t.Errorf("%v: must not create a record, got %+v", class, rec)
		}
	}
}

func TestRecordTaskExit_ResetsOnCleanExit(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-4", timeoutOutcome())

	s.recordTaskExitForQuarantine(ap, 137)
	s.recordTaskExitForQuarantine(ap, 137)
	if got := recordCount(s, "T-4"); got != 2 {
		t.Fatalf("setup: Count = %d, want 2", got)
	}

	// Clean exit: exit 0, no error.
	ap.Mu.Lock()
	ap.LastError = nil
	ap.Mu.Unlock()
	s.recordTaskExitForQuarantine(ap, 0)

	if rec := record(s, "T-4"); rec != nil {
		t.Fatalf("clean exit must evict the record, got %+v", rec)
	}
}

func TestRecordTaskExit_ResetsOnCommitProgress(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-5", timeoutOutcome())
	base := initGitRepo(t, ap.WorktreePath)
	ap.BeforeRef = base

	// Kill with HEAD == BeforeRef: no progress, increments.
	s.recordTaskExitForQuarantine(ap, 137)
	if got := recordCount(s, "T-5"); got != 1 {
		t.Fatalf("setup: Count = %d, want 1", got)
	}

	// Advance HEAD past BeforeRef: progress, evicts.
	gitCommit(t, ap.WorktreePath, "work")
	s.recordTaskExitForQuarantine(ap, 137)

	if rec := record(s, "T-5"); rec != nil {
		t.Fatalf("commit progress must evict the record, got %+v", rec)
	}
}

func TestRecordTaskExit_UnknownBaselineNotProgress(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-6", timeoutOutcome())
	initGitRepo(t, ap.WorktreePath) // HEAD readable, but BeforeRef stays ""

	s.recordTaskExitForQuarantine(ap, 137)

	// BeforeRef=="" (session creation failed) must NOT read as progress —
	// that would suppress quarantine for every session-creation-failure exit.
	if got := recordCount(s, "T-6"); got != 1 {
		t.Fatalf("Count = %d, want 1 (unknown baseline is not progress)", got)
	}
}

func TestRecordTaskExit_DesignOrNotesDeltaCountsAsProgress(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	design := "v1"
	mock.GetFn = func(_ context.Context, _ string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{Description: "x", Notes: "n", IssueData: backend.IssueData{Design: design}}, nil
	}
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-7", timeoutOutcome())

	s.recordTaskExitForQuarantine(ap, 137) // establishes baseline (design v1)
	if rec := record(s, "T-7"); rec == nil || !rec.BaselineKnown || rec.Count != 1 {
		t.Fatalf("setup: record = %+v, want Count 1 with known baseline", rec)
	}

	design = "v2" // plan agent inched the design forward between kills
	s.recordTaskExitForQuarantine(ap, 137)

	if rec := record(s, "T-7"); rec != nil {
		t.Fatalf("design delta must evict the record, got %+v", rec)
	}
}

func TestRecordTaskExit_GetFailureIsNotProgress(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetErr = errors.New("fleet down")
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-8", timeoutOutcome())

	s.recordTaskExitForQuarantine(ap, 137)
	s.recordTaskExitForQuarantine(ap, 137)

	rec := record(s, "T-8")
	if rec == nil || rec.Count != 2 {
		t.Fatalf("record = %+v, want Count 2 (GET failure never blocks the increment)", rec)
	}
	if rec.BaselineKnown {
		t.Error("BaselineKnown = true, want false (failed GETs never write hashes)")
	}
}

func TestRecordTaskExit_BaselineEstablishedLateIsNotProgress(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	getErr := errors.New("fleet down")
	design := "v9"
	mock.GetFn = func(_ context.Context, _ string) (*backend.IssueDetailData, error) {
		if getErr != nil {
			return nil, getErr
		}
		return &backend.IssueDetailData{IssueData: backend.IssueData{Design: design}}, nil
	}
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-9", timeoutOutcome())

	s.recordTaskExitForQuarantine(ap, 137) // creation GET fails: no baseline
	getErr = nil
	s.recordTaskExitForQuarantine(ap, 137) // late GET establishes baseline, no eviction

	rec := record(s, "T-9")
	if rec == nil || rec.Count != 2 {
		t.Fatalf("record = %+v, want Count 2 (late baseline is not progress)", rec)
	}
	if !rec.BaselineKnown {
		t.Fatal("BaselineKnown = false, want true after the first successful GET")
	}

	s.recordTaskExitForQuarantine(ap, 137) // unchanged design: still no progress
	if got := recordCount(s, "T-9"); got != 3 {
		t.Fatalf("Count = %d, want 3", got)
	}

	design = "v10" // now a real delta against the late-established baseline
	s.recordTaskExitForQuarantine(ap, 137)
	if rec := record(s, "T-9"); rec != nil {
		t.Fatalf("delta against late baseline must evict, got %+v", rec)
	}
}

func TestRecordTaskExit_ReArmsLatchedRecordOnFreshKill(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-10", timeoutOutcome())

	// Simulate a resolved quarantine: latched, Count zeroed, daemon-written.
	q := s.qrec()
	q.mu.Lock()
	q.rec["T-10"] = &taskFailureRecord{
		Count:         0,
		QuarantinedAt: time.Now(),
		DaemonWrote:   true,
		WriteFailed:   true,
		LastUpdated:   time.Now(),
	}
	q.mu.Unlock()

	s.recordTaskExitForQuarantine(ap, 137)

	rec := record(s, "T-10")
	if rec == nil {
		t.Fatal("expected the record to survive re-arm")
	}
	if !rec.QuarantinedAt.IsZero() {
		t.Error("QuarantinedAt not cleared: fresh kill must re-arm the latch")
	}
	if rec.DaemonWrote || rec.WriteFailed {
		t.Errorf("DaemonWrote/WriteFailed = %v/%v, want false/false after re-arm", rec.DaemonWrote, rec.WriteFailed)
	}
	if rec.Count != 1 {
		t.Errorf("Count = %d, want 1 (re-quarantine needs N fresh kills)", rec.Count)
	}
}

func TestRecordTaskExit_SiblingAgentsShareCounter(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap1 := newKilledAgent(t, "falcon", "T-11", timeoutOutcome())
	ap2 := newKilledAgent(t, "hawk", "T-11", agenterr.OutcomeFromHarness(wrapper.ErrUnknown))

	s.recordTaskExitForQuarantine(ap1, 137)
	s.recordTaskExitForQuarantine(ap2, -1)

	rec := record(s, "T-11")
	if rec == nil || rec.Count != 2 {
		t.Fatalf("record = %+v, want Count 2 (kills from siblings accumulate on one record)", rec)
	}
	if rec.Kills[0].Agent != "falcon" || rec.Kills[1].Agent != "hawk" {
		t.Errorf("kill agents = %q,%q, want falcon,hawk", rec.Kills[0].Agent, rec.Kills[1].Agent)
	}
}

func TestRecordTaskExit_CapsKillTimeline(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newKilledAgent(t, "falcon", "T-12", timeoutOutcome())

	for i := 0; i < maxKillEventsRetained+3; i++ {
		ap.Mu.Lock()
		ap.LastError = &agenterr.AgentError{Class: timeoutOutcome(), ExitCode: 100 + i}
		ap.Mu.Unlock()
		s.recordTaskExitForQuarantine(ap, 100+i)
	}

	rec := record(s, "T-12")
	if rec == nil {
		t.Fatal("expected a record")
	}
	if rec.Count != maxKillEventsRetained+3 {
		t.Errorf("Count = %d, want %d (count keeps accruing past the timeline cap)", rec.Count, maxKillEventsRetained+3)
	}
	if len(rec.Kills) != maxKillEventsRetained {
		t.Fatalf("len(Kills) = %d, want %d", len(rec.Kills), maxKillEventsRetained)
	}
	// The retained events are the LAST maxKillEventsRetained (oldest dropped).
	if got, want := rec.Kills[0].ExitCode, 103; got != want {
		t.Errorf("oldest retained ExitCode = %d, want %d", got, want)
	}
	if got, want := rec.Kills[len(rec.Kills)-1].ExitCode, 100+maxKillEventsRetained+2; got != want {
		t.Errorf("newest retained ExitCode = %d, want %d", got, want)
	}
}

func TestRecordTaskExit_EvictsOldestByLastUpdated(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	q := s.qrec()

	// Fill the ledger to capacity with synthetic records. T-old is the
	// oldest; T-pinned is older still but inFlight (must survive).
	q.mu.Lock()
	now := time.Now()
	q.rec["T-pinned"] = &taskFailureRecord{Count: 1, inFlight: true, LastUpdated: now.Add(-48 * time.Hour)}
	q.rec["T-old"] = &taskFailureRecord{Count: 1, LastUpdated: now.Add(-24 * time.Hour)}
	for i := 0; len(q.rec) < maxTrackedQuarantineTasks; i++ {
		q.rec[fmt.Sprintf("T-fill-%d", i)] = &taskFailureRecord{Count: 1, LastUpdated: now}
	}
	q.mu.Unlock()

	ap := newKilledAgent(t, "falcon", "T-fresh", timeoutOutcome())
	s.recordTaskExitForQuarantine(ap, 137)

	if rec := record(s, "T-fresh"); rec == nil {
		t.Fatal("fresh record not inserted")
	}
	if rec := record(s, "T-old"); rec != nil {
		t.Error("oldest non-inFlight record should have been evicted")
	}
	if rec := record(s, "T-pinned"); rec == nil {
		t.Error("inFlight record must never be evicted")
	}
}

func TestQuarantineThreshold_EnvOverrideAndDisable(t *testing.T) {
	s := newQuarantineSupervisor(nil)

	if got := s.quarantineThreshold(); got != defaultQuarantineThreshold {
		t.Errorf("default threshold = %d, want %d", got, defaultQuarantineThreshold)
	}

	t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "5")
	if got := s.quarantineThreshold(); got != 5 {
		t.Errorf("threshold = %d, want 5 (env override)", got)
	}

	t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "not-a-number")
	if got := s.quarantineThreshold(); got != defaultQuarantineThreshold {
		t.Errorf("threshold = %d, want default %d on unparsable env", got, defaultQuarantineThreshold)
	}

	// <= 0 is the operator kill-switch: the record hook becomes a no-op.
	t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "0")
	ap := newKilledAgent(t, "falcon", "T-13", timeoutOutcome())
	s.recordTaskExitForQuarantine(ap, 137)
	if rec := record(s, "T-13"); rec != nil {
		t.Fatalf("threshold 0 must disable the record hook, got %+v", rec)
	}
}
