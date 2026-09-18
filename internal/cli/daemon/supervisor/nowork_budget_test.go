package supervisor

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// The rule under test: a NoWork cycle never CHARGES the restart budget (an
// agent that finds nothing to do has not failed), but it never CLEARS it
// either. Only evidence of real progress — a clean success — resets the
// failure budget. Before this rule, applyNoWorkRestart zeroed RestartCount on
// every idle cycle, so an agent whose failures interleaved with idle polls
// could never reach max_retries and respawned forever.

func newMaxRetriesSupervisor(t *testing.T, maxRetries int) *Supervisor {
	t.Helper()
	return newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries},
		},
	})
}

// exitNoWork shapes ap as the supervisor's markNoWork does: exit 0, no task,
// NoWork domain outcome.
func exitNoWork(ap *AgentProcess) {
	ap.LastExitCode = 0
	ap.LastStart = time.Now()
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
		Message: "no claimable tasks",
	}
	ap.LastNoWork = true
}

// exitFailure shapes ap as a counted failure of the given domain class.
func exitFailure(ap *AgentProcess, class agenterr.DomainOutcome) {
	ap.LastExitCode = 1
	ap.LastStart = time.Now()
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromDomain(class),
		Message: "failed",
	}
	ap.LastNoWork = false
}

// A failing agent that is ALSO idle must still spend its restart budget: the
// idle polls between failures must not hand it a fresh budget every cycle.
func TestShouldRestart_FailingIdleAgentReachesMaxRetries(t *testing.T) {
	const maxRetries = 3
	s := newMaxRetriesSupervisor(t, maxRetries)
	ap := &AgentProcess{}

	// maxRetries counted failures, each followed by an idle poll. The budget
	// is not spent yet, so the agent keeps restarting with no stop reason.
	for i := 1; i <= maxRetries; i++ {
		exitFailure(ap, agenterr.SpawnFailureOutcome)
		if !s.shouldRestart(ap) {
			t.Fatalf("failure %d: shouldRestart = false, want true (budget not spent)", i)
		}
		if ap.RestartCount != i {
			t.Fatalf("failure %d: RestartCount = %d, want %d", i, ap.RestartCount, i)
		}

		exitNoWork(ap)
		if !s.shouldRestart(ap) {
			t.Fatalf("idle poll %d: shouldRestart = false, want true (NoWork always restarts)", i)
		}
		if ap.RestartCount != i {
			t.Fatalf("idle poll %d: RestartCount = %d, want %d preserved across the idle cycle",
				i, ap.RestartCount, i)
		}
		if ap.NoWorkCount != 1 {
			t.Fatalf("idle poll %d: NoWorkCount = %d, want 1 (reset by the preceding failure)",
				i, ap.NoWorkCount)
		}
	}

	// The next failure exceeds max_retries. SpawnFailure blocks on exhaustion,
	// so the agent still restarts — but on the fixed block interval, with a
	// visible blocked stop reason, instead of hot-looping invisibly.
	exitFailure(ap, agenterr.SpawnFailureOutcome)
	restart := s.shouldRestart(ap)
	if ap.StopReason != StopReasonMaxRetriesBlocked {
		t.Fatalf("StopReason = %q after %d failures with max_retries=%d, want %q",
			ap.StopReason, maxRetries+1, maxRetries, StopReasonMaxRetriesBlocked)
	}
	if !restart {
		t.Fatalf("shouldRestart = false on budget exhaustion, want true (block-and-retry)")
	}
	if ap.BlockCount != 1 {
		t.Fatalf("BlockCount = %d, want 1", ap.BlockCount)
	}
}

// End to end: an agent that only ever fails or idles must eventually stop
// being respawned. Without the budget fix this loop never terminates.
func TestShouldRestart_FailingIdleAgentEventuallyStops(t *testing.T) {
	const maxRetries = 3
	// (maxRetries+1) failures per block cycle × defaultBlockBudget block
	// cycles, plus headroom.
	const maxCycles = 64

	s := newMaxRetriesSupervisor(t, maxRetries)
	ap := &AgentProcess{}

	for i := 0; i < maxCycles; i++ {
		exitFailure(ap, agenterr.SpawnFailureOutcome)
		if !s.shouldRestart(ap) {
			if ap.StopReason != StopReasonFastFail {
				t.Fatalf("stopped with StopReason = %q, want %q", ap.StopReason, StopReasonFastFail)
			}
			return
		}
		exitNoWork(ap)
		if !s.shouldRestart(ap) {
			t.Fatalf("cycle %d: NoWork must always restart", i)
		}
	}
	t.Fatalf("agent still respawning after %d failure/idle cycles: max_retries is unreachable", maxCycles)
}

// The other half of the rule: a genuinely idle, healthy agent must poll
// forever and must never accumulate failures.
func TestShouldRestart_IdleHealthyAgentAccruesNoFailures(t *testing.T) {
	s := newMaxRetriesSupervisor(t, 3)
	ap := &AgentProcess{}

	for i := 1; i <= 50; i++ {
		exitNoWork(ap)
		if !s.shouldRestart(ap) {
			t.Fatalf("idle cycle %d: shouldRestart = false, want true", i)
		}
		if ap.RestartCount != 0 {
			t.Fatalf("idle cycle %d: RestartCount = %d, want 0 (idling is not failing)", i, ap.RestartCount)
		}
		if ap.NoWorkCount != i {
			t.Fatalf("idle cycle %d: NoWorkCount = %d, want %d", i, ap.NoWorkCount, i)
		}
		if ap.BlockCount != 0 {
			t.Fatalf("idle cycle %d: BlockCount = %d, want 0", i, ap.BlockCount)
		}
		if ap.StopReason != "" {
			t.Fatalf("idle cycle %d: StopReason = %q, want empty", i, ap.StopReason)
		}
	}
}

// A NoWork cycle leaves the failure budget exactly where it was — it neither
// charges it nor refunds it.
func TestShouldRestart_NoWorkPreservesFailureBudget(t *testing.T) {
	s := newMaxRetriesSupervisor(t, 3)
	ap := &AgentProcess{RestartCount: 2}

	exitNoWork(ap)
	if !s.shouldRestart(ap) {
		t.Fatalf("shouldRestart = false, want true for NoWork")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (preserved across an idle cycle)", ap.RestartCount)
	}
	if ap.NoWorkCount != 1 {
		t.Errorf("NoWorkCount = %d, want 1", ap.NoWorkCount)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want empty (an idle agent is not stopped)", ap.StopReason)
	}
}

// Progress, not idleness, is what refunds the budget.
func TestShouldRestart_CleanSuccessClearsBudgetAfterNoWork(t *testing.T) {
	s := newMaxRetriesSupervisor(t, 3)
	ap := &AgentProcess{RestartCount: 2}

	exitNoWork(ap)
	s.shouldRestart(ap)

	ap.LastExitCode = 0
	ap.LastError = nil
	ap.LastNoWork = false
	ap.LastStart = time.Now()
	if !s.shouldRestart(ap) {
		t.Fatalf("shouldRestart = false, want true for a clean success")
	}
	if ap.RestartCount != 0 || ap.NoWorkCount != 0 || ap.RateRetryCount != 0 || ap.BlockCount != 0 {
		t.Errorf("counters after clean success = restart %d / nowork %d / rate %d / block %d, want all 0",
			ap.RestartCount, ap.NoWorkCount, ap.RateRetryCount, ap.BlockCount)
	}
}

// Preserving the counter must not change the idle poll cadence: NoWork reads
// the fixed no_work_backoff, never the exponential schedule keyed on
// RestartCount.
func TestComputeBackoff_NoWorkIsFixedDespitePreservedBudget(t *testing.T) {
	noWork := 30
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries:    &maxRetries,
				NoWorkBackoff: &noWork,
			},
		},
	})

	ap := &AgentProcess{RestartCount: 3}
	exitNoWork(ap)

	if got := s.computeBackoff(ap); got != 30*time.Second {
		t.Errorf("computeBackoff = %s, want 30s (fixed no-work poll)", got)
	}
}
