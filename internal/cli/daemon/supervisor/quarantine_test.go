package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
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
	cmd := exec.Command("git", args...) //nolint:norawexec // real repo needed: commit-progress detection shells out via CaptureHEADRef (same pattern as config/checkpoint_test.go)
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

// ---------------------------------------------------------------------------
// sweep: the quarantine write
// ---------------------------------------------------------------------------

// openIssueMock returns a mock whose Get always reports an open issue with
// the given (mutable) design pointer, so record-hook baselines and sweep
// read-backs see consistent state.
func openIssueMock(status, design *string) *clitest.MockIssueBackend {
	mock := clitest.NewMockIssueBackend()
	mock.GetFn = func(_ context.Context, _ string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{Status: *status, Design: *design}}, nil
	}
	return mock
}

// killNTimes drives the record hook n times for the agent.
func killNTimes(s *Supervisor, ap *AgentProcess, n int) {
	for i := 0; i < n; i++ {
		s.recordTaskExitForQuarantine(ap, 137)
	}
}

// updateParamsOf extracts the UpdateParams from a recorded mock call.
func updateParamsOf(t *testing.T, c clitest.MockBackendCall) backend.UpdateParams {
	t.Helper()
	params, ok := c.Args[1].(backend.UpdateParams)
	if !ok {
		t.Fatalf("Update call args = %+v, want UpdateParams at [1]", c.Args)
	}
	return params
}

func updateCalls(mock *clitest.MockIssueBackend) []clitest.MockBackendCall {
	var out []clitest.MockBackendCall
	for _, c := range mock.Calls {
		if c.Method == "Update" {
			out = append(out, c)
		}
	}
	return out
}

func TestSweep_FiresExactlyOnceAtThreshold(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-20", timeoutOutcome())

	killNTimes(s, ap, 2)
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 0 {
		t.Fatalf("Update called %d times below threshold, want 0", got)
	}

	killNTimes(s, ap, 1) // reaches threshold 3
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 1 {
		t.Fatalf("Update called %d times at threshold, want 1", got)
	}

	rec := record(s, "T-20")
	if rec == nil {
		t.Fatal("record must be retained after quarantine (status visibility)")
	}
	if rec.QuarantinedAt.IsZero() || rec.Count != 0 || !rec.DaemonWrote {
		t.Errorf("latch state = {QuarantinedAt:%v Count:%d DaemonWrote:%v}, want latched/0/true",
			rec.QuarantinedAt, rec.Count, rec.DaemonWrote)
	}

	// The latch keeps it out of later sweeps: no second write.
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 1 {
		t.Fatalf("Update called %d times after latch, want still 1", got)
	}
}

func TestSweep_ReQuarantineNeedsNFreshKills(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-21", timeoutOutcome())

	killNTimes(s, ap, 3)
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 1 {
		t.Fatalf("setup: Update count = %d, want 1", got)
	}

	// Human released the task; one fresh kill must NOT re-quarantine.
	killNTimes(s, ap, 1)
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 1 {
		t.Fatalf("Update count = %d after 1 fresh kill, want still 1", got)
	}

	killNTimes(s, ap, 2) // back to threshold
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 2 {
		t.Fatalf("Update count = %d after N fresh kills, want 2 (re-quarantined)", got)
	}
}

func TestSweep_SingleUpdateBlocksClearsAssigneeAddsLabel(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-22", timeoutOutcome())

	killNTimes(s, ap, 3)
	s.sweepQuarantineDue(ap)

	calls := updateCalls(mock)
	if len(calls) != 1 {
		t.Fatalf("Update calls = %d, want exactly 1 (one load-bearing write)", len(calls))
	}
	if id := calls[0].Args[0].(string); id != "T-22" {
		t.Errorf("Update id = %q, want T-22", id)
	}
	params := updateParamsOf(t, calls[0])
	if params.Status == nil || *params.Status != "blocked" {
		t.Errorf("Status = %v, want blocked", params.Status)
	}
	if params.Assignee == nil || *params.Assignee != "" {
		t.Errorf("Assignee = %v, want explicit empty (unassign)", params.Assignee)
	}
	if len(params.AddLabels) != 1 || params.AddLabels[0] != quarantineLabel {
		t.Errorf("AddLabels = %v, want [%s]", params.AddLabels, quarantineLabel)
	}
}

func TestSweep_PostsKillTimelineComment(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-23", timeoutOutcome())
	ap.StopReason = StopReasonWatchdog
	ap.AgentSessionID = "fleet-sess-9"

	killNTimes(s, ap, 3)
	s.sweepQuarantineDue(ap)

	if got := mock.CallCount("AddComment"); got != 1 {
		t.Fatalf("AddComment count = %d, want 1", got)
	}
	var params backend.CommentAddParams
	for _, c := range mock.Calls {
		if c.Method == "AddComment" {
			params = c.Args[0].(backend.CommentAddParams)
		}
	}
	if params.IssueID != "T-23" {
		t.Errorf("comment IssueID = %q, want T-23", params.IssueID)
	}
	if params.Author != "falcon" {
		t.Errorf("comment Author = %q, want falcon (the sweeping agent)", params.Author)
	}
	for _, want := range []string{
		"Task quarantined by loom daemon",
		"| 1 |", "| 3 |", // timeline rows
		"watchdog", "Timeout", "fleet-se", "claude-T",
		"loom data update T-23 --status open",
		quarantineLabel,
	} {
		if !strings.Contains(params.Text, want) {
			t.Errorf("comment text missing %q:\n%s", want, params.Text)
		}
	}
}

func TestSweep_ReadBackSkipsClosedReviewBlockedDeferred(t *testing.T) {
	for _, terminal := range []string{"closed", "tombstone", "review", "blocked", "deferred"} {
		t.Run(terminal, func(t *testing.T) {
			status, design := "open", "d1"
			mock := openIssueMock(&status, &design)
			s := newQuarantineSupervisor(mock)
			ap := newKilledAgent(t, "falcon", "T-24", timeoutOutcome())

			killNTimes(s, ap, 3)
			status = terminal // the read-back must observe the new state
			s.sweepQuarantineDue(ap)

			if got := mock.CallCount("Update"); got != 0 {
				t.Fatalf("Update count = %d for %s, want 0 (guard latches without writing)", got, terminal)
			}
			if got := mock.CallCount("AddComment"); got != 0 {
				t.Fatalf("AddComment count = %d for %s, want 0", got, terminal)
			}
			rec := record(s, "T-24")
			if rec == nil || rec.QuarantinedAt.IsZero() {
				t.Fatalf("record = %+v, want latched", rec)
			}
			if rec.DaemonWrote {
				t.Error("DaemonWrote = true, want false (guard latch, not a daemon write)")
			}
			if rec.Count != 0 {
				t.Errorf("Count = %d, want 0 (latch zeroes the re-arm baseline)", rec.Count)
			}
		})
	}
}

func TestSweep_InProgressSkipsWithoutLatch(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-25", timeoutOutcome())

	killNTimes(s, ap, 3)
	status = "in_progress" // re-picked between the kills and the sweep
	s.sweepQuarantineDue(ap)

	if got := mock.CallCount("Update"); got != 0 {
		t.Fatalf("Update count = %d, want 0 (never block a task mid-run)", got)
	}
	rec := record(s, "T-25")
	if rec == nil {
		t.Fatal("record must survive an in_progress skip")
	}
	if !rec.QuarantinedAt.IsZero() {
		t.Error("record latched on in_progress skip; must stay due")
	}
	if rec.Count != 3 {
		t.Errorf("Count = %d, want 3 (preserved)", rec.Count)
	}
	if rec.inFlight {
		t.Error("inFlight not released after skip")
	}

	// Once that run exits and the task is open again, the sweep proceeds.
	status = "open"
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 1 {
		t.Fatalf("Update count = %d after task re-opened, want 1", got)
	}
}

func TestSweep_OpenWithChangedBaselineEvictsInsteadOfWrites(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-26", timeoutOutcome())

	killNTimes(s, ap, 3) // baseline anchored at d1
	design = "d2"        // progressed in the open→sweep gap (stale retry shape)
	s.sweepQuarantineDue(ap)

	if got := mock.CallCount("Update"); got != 0 {
		t.Fatalf("Update count = %d, want 0 (progressed task released from the spiral)", got)
	}
	if rec := record(s, "T-26"); rec != nil {
		t.Fatalf("record = %+v, want evicted", rec)
	}
}

func TestSweep_RetriesOnWriteFailureNonFatal(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	mock.UpdateErr = errors.New("fleet down")
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-27", timeoutOutcome())

	killNTimes(s, ap, 3)
	s.sweepQuarantineDue(ap)

	rec := record(s, "T-27")
	if rec == nil {
		t.Fatal("record must survive a failed write")
	}
	if !rec.WriteFailed {
		t.Error("WriteFailed = false, want true")
	}
	if !rec.QuarantinedAt.IsZero() {
		t.Error("record latched despite failed write")
	}
	if rec.inFlight {
		t.Error("inFlight not released after failed write")
	}
	if got := mock.CallCount("AddComment"); got != 0 {
		t.Fatalf("AddComment count = %d after failed status write, want 0", got)
	}

	// Any agent's next sweep retries via the normal predicate.
	mock.UpdateErr = nil
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 2 {
		t.Fatalf("Update count = %d, want 2 (retry succeeded)", got)
	}
	rec = record(s, "T-27")
	if rec == nil || rec.QuarantinedAt.IsZero() || !rec.DaemonWrote || rec.WriteFailed {
		t.Fatalf("record = %+v, want latched daemon-write with WriteFailed cleared", rec)
	}
}

func TestSweep_GetFailureStaysDueAndFlagsFailure(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetErr = errors.New("fleet down")
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-28", timeoutOutcome())

	killNTimes(s, ap, 3)
	s.sweepQuarantineDue(ap)

	if got := mock.CallCount("Update"); got != 0 {
		t.Fatalf("Update count = %d with failing read-back, want 0", got)
	}
	rec := record(s, "T-28")
	if rec == nil || !rec.QuarantinedAt.IsZero() || !rec.WriteFailed || rec.inFlight {
		t.Fatalf("record = %+v, want due + WriteFailed + released", rec)
	}
}

func TestSweep_CommentFailureDoesNotUnlatch(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	mock.AddCommentErr = errors.New("comments route missing")
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-29", timeoutOutcome())

	killNTimes(s, ap, 3)
	s.sweepQuarantineDue(ap)

	rec := record(s, "T-29")
	if rec == nil || rec.QuarantinedAt.IsZero() || !rec.DaemonWrote {
		t.Fatalf("record = %+v, want latched daemon-write despite comment failure", rec)
	}
	s.sweepQuarantineDue(ap)
	if got := mock.CallCount("Update"); got != 1 {
		t.Fatalf("Update count = %d, want 1 (comment failure never re-triggers the write)", got)
	}
}

func TestSweep_ActsOnLedgerNotCurrentAgentTask(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)
	worker := newKilledAgent(t, "falcon", "T-30", timeoutOutcome())

	killNTimes(s, worker, 3)

	// A DIFFERENT agent with no task attached exits; its sweep must still
	// quarantine T-30 (worker-self-picked tasks leave AssignedTaskID empty
	// and the lock cleared by recovery — the ledger is the source of truth).
	bystander := newKilledAgent(t, "hawk", "", agenterr.Outcome{})
	s.sweepQuarantineDue(bystander)

	if got := mock.CallCount("Update"); got != 1 {
		t.Fatalf("Update count = %d from bystander sweep, want 1", got)
	}
}

func TestSweep_ConcurrentSweepsWriteOnce(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	var updates int32
	release := make(chan struct{})
	mock.UpdateFn = func(_ context.Context, _ string, _ backend.UpdateParams) error {
		atomic.AddInt32(&updates, 1)
		<-release // hold the write so the second sweep overlaps it
		return nil
	}
	s := newQuarantineSupervisor(mock)
	ap := newKilledAgent(t, "falcon", "T-31", timeoutOutcome())
	killNTimes(s, ap, 3)

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			s.sweepQuarantineDue(ap)
		}()
	}
	// Give both sweeps time to reach takeDue; the second must see inFlight.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&updates); got != 1 {
		t.Fatalf("Update executed %d times under concurrent sweeps, want 1 (inFlight guard)", got)
	}
}

func TestFormatKillTimeline_RendersASCIIMarkdownTable(t *testing.T) {
	kills := []killEvent{
		{At: time.Date(2026, 6, 5, 14, 2, 11, 0, time.UTC), Agent: "web-extractor-a", StopReason: "watchdog", ErrClass: "Timeout", ExitCode: 137, FleetSessionID: "sess-abc123def", ClaudeSessionID: "9f3e4a5b-1111"},
		{At: time.Date(2026, 6, 5, 14, 40, 0, 0, time.UTC), Agent: "web-extractor-b", StopReason: "", ErrClass: "Unknown", ExitCode: -1},
	}
	text := formatKillTimeline("WEB-49", 3, 2, kills)

	for i, r := range text {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q at byte %d (daemon-generated text must be ASCII)", r, i)
		}
	}
	for _, want := range []string{
		"| # | time (UTC) | agent | kill | class | exit | fleet session | claude session |",
		"| 1 | 2026-06-05T14:02:11Z | web-extractor-a | watchdog | Timeout | 137 | sess-abc | 9f3e4a5b |",
		"| 2 | 2026-06-05T14:40:00Z | web-extractor-b | crash | Unknown | -1 | - | - |",
		"loom data update WEB-49 --status open",
		"loom data label remove WEB-49 loom:quarantined",
		"re-quarantine after 3 fresh no-progress kills",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("timeline missing %q:\n%s", want, text)
		}
	}
}

// ---------------------------------------------------------------------------
// daemon-status snapshot
// ---------------------------------------------------------------------------

func TestSweep_GuardLatchExcludedFromDaemonStatus(t *testing.T) {
	status, design := "open", "d1"
	mock := openIssueMock(&status, &design)
	s := newQuarantineSupervisor(mock)

	// T-40 gets daemon-quarantined; T-41 is guard-latched (human blocked it
	// first); T-42 is pending with a failing write.
	apA := newKilledAgent(t, "falcon", "T-40", timeoutOutcome())
	killNTimes(s, apA, 3)
	s.sweepQuarantineDue(apA)

	apB := newKilledAgent(t, "hawk", "T-41", timeoutOutcome())
	killNTimes(s, apB, 3)
	status = "blocked" // human got there first; read-back guard latches
	s.sweepQuarantineDue(apB)
	status = "open"

	apC := newKilledAgent(t, "ibis", "T-42", timeoutOutcome())
	killNTimes(s, apC, 3)
	mock.UpdateErr = errors.New("fleet down")
	s.sweepQuarantineDue(apC)

	infos := s.QuarantinedTasks()
	if len(infos) != 2 {
		t.Fatalf("QuarantinedTasks() = %+v, want exactly T-40 (daemon-written) and T-42 (pending)", infos)
	}
	if infos[0].TaskID != "T-40" || infos[0].WriteFailed || infos[0].QuarantinedAt.IsZero() {
		t.Errorf("infos[0] = %+v, want daemon-written T-40", infos[0])
	}
	if infos[0].Count != 3 || infos[0].LastKillReason != "crash/Timeout" {
		t.Errorf("infos[0] = %+v, want Count 3 / crash-Timeout", infos[0])
	}
	if infos[1].TaskID != "T-42" || !infos[1].WriteFailed || !infos[1].QuarantinedAt.IsZero() {
		t.Errorf("infos[1] = %+v, want pending write-failed T-42", infos[1])
	}
	for _, info := range infos {
		if info.TaskID == "T-41" {
			t.Error("guard-latched human-blocked task must never surface as daemon-quarantined")
		}
	}
}
