package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

// The run-turn-deadline quarantine bucket.
//
// A deadline expiry is loom's OWN designed clean stop, so it must not advance
// the no-progress crash counter — one expiry says nothing about the ticket, and
// counting it there parked innocent tickets at 1/3. It is still counted, on a
// separate and higher threshold, because a task that overruns its budget every
// single time boomerangs across siblings forever and each cycle costs two hours.

func deadlineOutcome() agenterr.Outcome {
	return agenterr.OutcomeFromDomain(agenterr.RunTurnDeadlineOutcome)
}

// deadlineCount returns the deadline bucket's counter for taskID.
func deadlineCount(s *Supervisor, taskID string) int {
	if r := record(s, taskID); r != nil {
		return r.DeadlineCount
	}
	return 0
}

// newExpiredAgent builds the post-classifyAgentExit shape of a turn ended by
// loom's own per-turn deadline: RunTurnDeadline class and NO StopReason (the
// supervisor did not kill it — the child stopped itself).
func newExpiredAgent(t *testing.T, name, taskID string) *AgentProcess {
	t.Helper()
	ap := newKilledAgent(t, name, taskID, deadlineOutcome())
	ap.AgentSessionID = "fleet-sess-" + taskID
	return ap
}

// TestQuarantineBucketFor_EveryOutcome pins the supervisor's view of the policy
// seat: exactly one domain outcome is eligible, and it is NOT on the crash
// counter. A table here (rather than only in agentpolicy) is deliberate — this
// package is what acts on the answer.
func TestQuarantineBucketFor_EveryOutcome(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   agenterr.Outcome
		want agentpolicy.QuarantineBucket
	}{
		{"timeout", agenterr.OutcomeFromHarness(wrapper.ErrTimeout), agentpolicy.QuarantineNoProgress},
		{"transient", agenterr.OutcomeFromHarness(wrapper.ErrTransient), agentpolicy.QuarantineNoProgress},
		{"unknown", agenterr.OutcomeFromHarness(wrapper.ErrUnknown), agentpolicy.QuarantineNoProgress},
		{"context-overflow", agenterr.OutcomeFromHarness(wrapper.ErrContextOverflow), agentpolicy.QuarantineNoProgress},
		{"rate-limited", agenterr.OutcomeFromHarness(wrapper.ErrRateLimited), agentpolicy.QuarantineNone},
		{"auth", agenterr.OutcomeFromHarness(wrapper.ErrAuth), agentpolicy.QuarantineNone},
		{"billing", agenterr.OutcomeFromHarness(wrapper.ErrBilling), agentpolicy.QuarantineNone},
		{"model-not-found", agenterr.OutcomeFromHarness(wrapper.ErrModelNotFound), agentpolicy.QuarantineNone},
		{"none", agenterr.OutcomeFromHarness(wrapper.ErrNone), agentpolicy.QuarantineNone},
		{"no-work", agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), agentpolicy.QuarantineNone},
		{"lock-conflict", agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome), agentpolicy.QuarantineNone},
		{"spawn-failure", agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome), agentpolicy.QuarantineNone},
		{"backend-unavailable", agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome), agentpolicy.QuarantineNone},
		{"completion-hook-failure", agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome), agentpolicy.QuarantineNone},
		{"incomplete-run", agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome), agentpolicy.QuarantineNone},
		{"run-turn-deadline", deadlineOutcome(), agentpolicy.QuarantineDeadline},
		{"zero", agenterr.Outcome{}, agentpolicy.QuarantineNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentpolicy.QuarantineBucketFor(tc.in); got != tc.want {
				t.Fatalf("QuarantineBucketFor(%s) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestDeadlineExpiries_DoNotAdvanceTheCrashCounter is the headline regression.
// Before this change both expiries landed on Count and two more would have
// parked the ticket at 3.
func TestDeadlineExpiries_DoNotAdvanceTheCrashCounter(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newExpiredAgent(t, "falcon", "T-D1")

	s.recordTaskExitForQuarantine(ap, 1)
	s.recordTaskExitForQuarantine(ap, 1)

	if got := deadlineCount(s, "T-D1"); got != 2 {
		t.Fatalf("DeadlineCount = %d, want 2", got)
	}
	if got := recordCount(s, "T-D1"); got != 0 {
		t.Fatalf("Count = %d, want 0 — a clean deadline stop is not a no-progress crash", got)
	}

	// Two expiries are far short of the deadline threshold, so nothing is due.
	if due := s.qrec().takeDue(s.quarantineThreshold(), s.deadlineQuarantineThreshold()); len(due) != 0 {
		t.Fatalf("takeDue returned %d tasks after 2 expiries, want 0", len(due))
	}
}

// TestDeadlineExpiries_QuarantineAtTheirOwnThreshold: counted, just further out.
func TestDeadlineExpiries_QuarantineAtTheirOwnThreshold(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newExpiredAgent(t, "falcon", "T-D2")

	threshold := s.deadlineQuarantineThreshold()
	if threshold != defaultDeadlineQuarantineThreshold {
		t.Fatalf("deadlineQuarantineThreshold() = %d, want the default %d", threshold, defaultDeadlineQuarantineThreshold)
	}
	for i := 0; i < threshold; i++ {
		s.recordTaskExitForQuarantine(ap, 1)
	}

	due := s.qrec().takeDue(s.quarantineThreshold(), threshold)
	if len(due) != 1 {
		t.Fatalf("takeDue returned %d tasks after %d expiries, want 1", len(due), threshold)
	}
	if due[0].bucket != agentpolicy.QuarantineDeadline {
		t.Errorf("bucket = %v, want QuarantineDeadline", due[0].bucket)
	}
	if due[0].threshold != threshold {
		t.Errorf("threshold = %d, want the deadline threshold %d", due[0].threshold, threshold)
	}
	if due[0].count != threshold {
		t.Errorf("count = %d, want %d", due[0].count, threshold)
	}
}

// TestDeadlineKill_RendersAsExpiryNotCrash: "crash" is the fallback when
// StopReason is empty, which would render loom's own clean stop as
// "crash/RunTurnDeadline" — the same misnomer one level down.
func TestDeadlineKill_RendersAsExpiryNotCrash(t *testing.T) {
	s := newQuarantineSupervisor(nil)
	ap := newExpiredAgent(t, "falcon", "T-D3")

	s.recordTaskExitForQuarantine(ap, 1)

	rec := record(s, "T-D3")
	if rec == nil {
		t.Fatal("expected a failure record for T-D3")
	}
	if got, want := rec.LastKillReason, "expiry/RunTurnDeadline"; got != want {
		t.Fatalf("LastKillReason = %q, want %q", got, want)
	}
	if strings.Contains(rec.LastKillReason, "crash") {
		t.Errorf("LastKillReason = %q, must not read as a crash", rec.LastKillReason)
	}

	// The quarantine comment renders the same way, and says what to do about it.
	text := formatKillTimeline("T-D3", agentpolicy.QuarantineDeadline, defaultDeadlineQuarantineThreshold, 6, rec.Kills)
	for _, want := range []string{
		"| 1 | ",
		"| expiry | RunTurnDeadline |",
		"run-turn deadline expiries",
		"max_run_duration",
		"re-quarantine after 6 fresh deadline expiries",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("timeline missing %q:\n%s", want, text)
		}
	}
	for i, r := range text {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q at byte %d (daemon-generated text must be ASCII)", r, i)
		}
	}
}

// TestDeadlineThreshold_EnvOverride pins LOOM_TASK_DEADLINE_QUARANTINE_THRESHOLD.
func TestDeadlineThreshold_EnvOverride(t *testing.T) {
	t.Setenv("LOOM_TASK_DEADLINE_QUARANTINE_THRESHOLD", "2")
	s := newQuarantineSupervisor(nil)
	ap := newExpiredAgent(t, "falcon", "T-D4")

	s.recordTaskExitForQuarantine(ap, 1)
	if due := s.qrec().takeDue(s.quarantineThreshold(), s.deadlineQuarantineThreshold()); len(due) != 0 {
		t.Fatalf("due after 1 expiry with threshold 2, want none")
	}
	s.recordTaskExitForQuarantine(ap, 1)

	due := s.qrec().takeDue(s.quarantineThreshold(), s.deadlineQuarantineThreshold())
	if len(due) != 1 || due[0].threshold != 2 {
		t.Fatalf("takeDue = %+v, want one task at threshold 2", due)
	}
}

// TestDeadlineBucket_DisabledIndependently: <= 0 disables THIS bucket only.
func TestDeadlineBucket_DisabledIndependently(t *testing.T) {
	t.Setenv("LOOM_TASK_DEADLINE_QUARANTINE_THRESHOLD", "0")
	s := newQuarantineSupervisor(nil)

	expired := newExpiredAgent(t, "falcon", "T-D5")
	for i := 0; i < 10; i++ {
		s.recordTaskExitForQuarantine(expired, 1)
	}
	if rec := record(s, "T-D5"); rec != nil {
		t.Fatalf("deadline bucket disabled but a record exists: %+v", rec)
	}

	// The no-progress bucket keeps counting exactly as before.
	killed := newKilledAgent(t, "falcon", "T-D6", timeoutOutcome())
	killed.StopReason = StopReasonWatchdog
	s.recordTaskExitForQuarantine(killed, 137)
	if got := recordCount(s, "T-D6"); got != 1 {
		t.Fatalf("no-progress Count = %d, want 1 — the deadline switch must not disable it", got)
	}
}

// TestQuarantineKillSwitch_DisablesBothBuckets: the documented operator
// kill-switch keeps meaning "all of it", new bucket included.
func TestQuarantineKillSwitch_DisablesBothBuckets(t *testing.T) {
	t.Setenv("LOOM_TASK_QUARANTINE_THRESHOLD", "0")
	s := newQuarantineSupervisor(nil)

	expired := newExpiredAgent(t, "falcon", "T-D7")
	killed := newKilledAgent(t, "falcon", "T-D8", timeoutOutcome())
	killed.StopReason = StopReasonWatchdog
	for i := 0; i < 10; i++ {
		s.recordTaskExitForQuarantine(expired, 1)
		s.recordTaskExitForQuarantine(killed, 137)
	}

	for _, id := range []string{"T-D7", "T-D8"} {
		if rec := record(s, id); rec != nil {
			t.Errorf("%s: record exists with the kill-switch on: %+v", id, rec)
		}
	}
}

// TestBothBuckets_QuarantineOnWhicheverTopsOutFirst pins edge case 5: a task
// alternating expiries and watchdog kills advances both counters independently.
func TestBothBuckets_QuarantineOnWhicheverTopsOutFirst(t *testing.T) {
	s := newQuarantineSupervisor(nil)

	expired := newExpiredAgent(t, "falcon", "T-D9")
	killed := newKilledAgent(t, "falcon", "T-D9", timeoutOutcome())
	killed.StopReason = StopReasonWatchdog

	// Interleave: 3 watchdog kills tops out the no-progress threshold first,
	// while the deadline bucket sits at 3 of 6.
	for i := 0; i < 3; i++ {
		s.recordTaskExitForQuarantine(expired, 1)
		s.recordTaskExitForQuarantine(killed, 137)
	}
	if got := recordCount(s, "T-D9"); got != 3 {
		t.Fatalf("Count = %d, want 3", got)
	}
	if got := deadlineCount(s, "T-D9"); got != 3 {
		t.Fatalf("DeadlineCount = %d, want 3", got)
	}

	due := s.qrec().takeDue(s.quarantineThreshold(), s.deadlineQuarantineThreshold())
	if len(due) != 1 {
		t.Fatalf("takeDue returned %d tasks, want 1", len(due))
	}
	if due[0].bucket != agentpolicy.QuarantineNoProgress {
		t.Errorf("bucket = %v, want QuarantineNoProgress (it reached its threshold first)", due[0].bucket)
	}
}

// TestProgressBetweenExpiries_ResetsBothCounters: a commit or a design/notes
// delta means the task IS moving, so neither counter may survive it.
func TestProgressBetweenExpiries_ResetsBothCounters(t *testing.T) {
	s := newQuarantineSupervisor(nil)

	// Commit progress: the record is evicted outright, both counters with it.
	ap := newExpiredAgent(t, "falcon", "T-DA")
	initGitRepo(t, ap.WorktreePath)
	ap.BeforeRef = gitCommit(t, ap.WorktreePath, "before")
	s.recordTaskExitForQuarantine(ap, 1)
	if got := deadlineCount(s, "T-DA"); got != 1 {
		t.Fatalf("DeadlineCount = %d, want 1", got)
	}
	gitCommit(t, ap.WorktreePath, "the agent committed something")
	s.recordTaskExitForQuarantine(ap, 1)
	if rec := record(s, "T-DA"); rec != nil {
		t.Fatalf("commit progress must evict the record, got %+v", rec)
	}

	// Field-delta progress: same, via the design/notes baseline.
	design := "v1"
	mock := clitest.NewMockIssueBackend()
	mock.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{Design: design}}, nil
	}
	s2 := newQuarantineSupervisor(mock)
	ap2 := newExpiredAgent(t, "falcon", "T-DB")
	s2.recordTaskExitForQuarantine(ap2, 1)
	s2.recordTaskExitForQuarantine(ap2, 1)
	if got := deadlineCount(s2, "T-DB"); got != 2 {
		t.Fatalf("DeadlineCount = %d, want 2", got)
	}
	design = "v2 -- the planner wrote more"
	s2.recordTaskExitForQuarantine(ap2, 1)
	if rec := record(s2, "T-DB"); rec != nil {
		t.Fatalf("design delta must drop the record, got %+v", rec)
	}
}
