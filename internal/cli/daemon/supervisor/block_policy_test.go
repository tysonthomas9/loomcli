package supervisor

import (
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// TestShouldRestart_Exhaustion_BlocksThenFreshBurst verifies the block cycle:
// failures count up to maxRetries, the exhausting failure blocks (resetting
// the budget), and blocking grants a fresh burst rather than an immediate
// re-block — so a transient root cause that clears mid-burst recovers.
func TestShouldRestart_Exhaustion_BlocksThenFreshBurst(t *testing.T) {
	maxRetries := 2
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
	}

	// Failures within budget count up and do not block.
	for i := 1; i <= maxRetries; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("iteration %d: shouldRestart = false, want true (within budget)", i)
		}
		if ap.StopReason == StopReasonMaxRetriesBlocked {
			t.Fatalf("iteration %d: blocked too early (RestartCount=%d, max=%d)", i, ap.RestartCount, maxRetries)
		}
	}

	// The exhausting failure blocks and resets the budget.
	if !s.shouldRestart(ap) {
		t.Fatal("exhausting failure: shouldRestart = false, want true (blocks)")
	}
	if ap.StopReason != StopReasonMaxRetriesBlocked {
		t.Fatalf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetriesBlocked)
	}
	if ap.RestartCount != 0 {
		t.Fatalf("RestartCount = %d, want 0 (reset on block)", ap.RestartCount)
	}

	// Block granted a fresh burst: the next failure counts from 1, not an
	// immediate re-block.
	if !s.shouldRestart(ap) {
		t.Fatal("post-block: shouldRestart = false, want true")
	}
	if ap.StopReason == StopReasonMaxRetriesBlocked {
		t.Fatal("post-block: re-blocked immediately; budget did not reset")
	}
	if ap.RestartCount != 1 {
		t.Fatalf("post-block RestartCount = %d, want 1 (fresh burst)", ap.RestartCount)
	}
}

// TestShouldRestart_BlockBudgetEscalatesToFastFail is the determinism guard:
// an Unknown-class crash that keeps exhausting its budget without ever making
// progress blocks only BlockBudget times, then fast-fails instead of blocking
// forever.
func TestShouldRestart_BlockBudgetEscalatesToFastFail(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
	}

	blocks := 0
	for cycle := 0; cycle < 50; cycle++ { // bound the loop; escalation must hit first
		if !s.shouldRestart(ap) {
			// Terminal: must be the fast-fail escalation, after exactly
			// BlockBudget block cycles.
			if ap.StopReason != StopReasonFastFail {
				t.Fatalf("terminal StopReason = %q, want %q", ap.StopReason, StopReasonFastFail)
			}
			want := agentpolicyDefaultBlockBudget(t)
			if blocks != want {
				t.Fatalf("escalated after %d blocks, want %d", blocks, want)
			}
			return
		}
		if ap.StopReason == StopReasonMaxRetriesBlocked {
			blocks++
		}
	}
	t.Fatal("never escalated to FastFail; blocked forever")
}

// agentpolicyDefaultBlockBudget resolves the Unknown-class BlockBudget from the
// policy table so the escalation test tracks the policy, not a copied number.
func agentpolicyDefaultBlockBudget(t *testing.T) int {
	t.Helper()
	budget := agentpolicy.Decide(agenterr.OutcomeFromHarness(wrapper.ErrUnknown)).BlockBudget
	if budget <= 0 {
		t.Fatal("Unknown BlockBudget must be a finite cap")
	}
	return budget
}

// TestShouldRestart_CleanRunResetsBlockBudget verifies "progress" (a clean
// run) resets the block-escalation counter, so an agent that recovers between
// block spirals gets a full budget again.
func TestShouldRestart_CleanRunResetsBlockBudget(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		BlockCount:   2, // mid-spiral
	}

	// Clean success: counters including BlockCount reset.
	ap.LastExitCode = 0
	ap.LastError = nil
	if !s.shouldRestart(ap) {
		t.Fatal("clean run: shouldRestart = false, want true")
	}
	if ap.BlockCount != 0 {
		t.Fatalf("BlockCount = %d after clean run, want 0", ap.BlockCount)
	}
}

// TestShouldRestart_FastFailClasses verifies deterministic classes stop
// immediately (no retry, no block): ContextOverflow today.
func TestShouldRestart_FastFailClasses(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrContextOverflow)},
	}
	if s.shouldRestart(ap) {
		t.Error("shouldRestart = true for ContextOverflow, want false (fast-fail)")
	}
	if ap.StopReason != StopReasonFastFail {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonFastFail)
	}
}

// TestShouldRestart_FailoverExhausted_FastFails verifies the caller's fallback
// attempt is authoritative: if shouldRestart sees a failover-only class, there
// was no fallback left, so the agent must stop immediately instead of retrying
// the same deterministic bad backend.
func TestShouldRestart_FailoverExhausted_FastFails(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrModelNotFound)},
	}

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart = true for exhausted ModelNotFound failover, want false")
	}
	if ap.StopReason != StopReasonFastFail {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonFastFail)
	}
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (no retry-in-place for failover-only errors)", ap.RestartCount)
	}
}

// TestShouldRestart_Fatal_StillStops_NotBlocked is the critical regression
// guard: a fatal error (auth/billing) must stop the agent even when the
// budget is well past exhausted — block must never swallow a fatal.
func TestShouldRestart_Fatal_StillStops_NotBlocked(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})

	for _, class := range []wrapper.ErrorClass{wrapper.ErrAuth, wrapper.ErrBilling} {
		ap := &AgentProcess{
			Entry:        config.AgentEntry{Worktree: "wt"},
			LastExitCode: 1,
			LastStart:    time.Now(),
			LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(class)},
			RestartCount: maxRetries + 5, // well past the budget
		}
		if s.shouldRestart(ap) {
			t.Errorf("%v: shouldRestart = true, want false (fatal must stop, never block)", class)
		}
		if ap.StopReason != StopReasonFatalError {
			t.Errorf("%v: StopReason = %q, want %q", class, ap.StopReason, StopReasonFatalError)
		}
	}
}

// TestComputeBackoff_Blocked_ReturnsBlockInterval verifies a blocked agent
// sleeps the fixed block interval (keyed on StopReason, not error class)
// rather than a huge exponential backoff, and that the private override wins.
func TestComputeBackoff_Blocked_ReturnsBlockInterval(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.maxRetriesBlockInterval = 50 * time.Millisecond // test override

	ap := &AgentProcess{
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
		RestartCount: 99, // would be a maxed exponential backoff if not blocked
		StopReason:   StopReasonMaxRetriesBlocked,
	}
	if got := s.computeBackoff(ap); got != 50*time.Millisecond {
		t.Errorf("computeBackoff(blocked) = %v, want 50ms (override)", got)
	}

	s.maxRetriesBlockInterval = 0 // fall back to the package default
	if got := s.computeBackoff(ap); got != defaultMaxRetriesBlockInterval {
		t.Errorf("computeBackoff(blocked, default) = %v, want %v", got, defaultMaxRetriesBlockInterval)
	}
}

// TestBlock_PreservesObservability verifies the durable signals survive a
// block: even though RestartCount resets to 0, the status payload still
// carries the blocked stop reason, the block-cycle count, and the error class
// that caused it.
func TestBlock_PreservesObservability(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt", Role: "plan"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
		RestartCount: maxRetries, // next failure exhausts → block
	}
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true (block)")
	}

	s.Agents = []*AgentProcess{ap}
	statuses := s.GetAgents()
	if len(statuses) != 1 {
		t.Fatalf("len(GetAgents()) = %d, want 1", len(statuses))
	}
	st := statuses[0]
	if st.StopReason != StopReasonMaxRetriesBlocked {
		t.Errorf("status.StopReason = %q, want %q", st.StopReason, StopReasonMaxRetriesBlocked)
	}
	if st.BlockCount != 1 {
		t.Errorf("status.BlockCount = %d, want 1", st.BlockCount)
	}
	if st.LastErrorClass == "" {
		t.Error("status.LastErrorClass is empty, want the class that caused the block")
	}
}

// lockConflictAgent is an agent whose last exit was a lost claim race against
// a live sibling of the same role.
func lockConflictAgent() *AgentProcess {
	return &AgentProcess{
		LastExitCode: 0,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome)},
	}
}

// TestShouldRestart_LockConflictIsUncounted pins the disposition at the
// supervisor layer: a lost claim race restarts without eroding max_retries,
// even from an already-exhausted budget.
func TestShouldRestart_LockConflictIsUncounted(t *testing.T) {
	maxRetries := 2
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := lockConflictAgent()
	ap.RestartCount = maxRetries

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true (contention is not a fault)")
	}
	// Uncounted means NOT INCREMENTED, not reset. Zeroing here would refund the
	// budget of an agent that had already failed twice for real, which is the
	// laundering this function refuses for BlockCount below and for
	// RestartCount since the no-work refund was removed: an idle cycle is no
	// evidence a failing agent recovered, and it is not even evidence the agent
	// ran (the pre-spawn claim gate decides the outcome before any process
	// starts). Only progress refunds — applyCleanSuccessRestart.
	if ap.RestartCount != maxRetries {
		t.Errorf("RestartCount = %d, want %d (uncounted: unchanged, not refunded)", ap.RestartCount, maxRetries)
	}
	if ap.BlockCount != 0 {
		t.Errorf("BlockCount = %d, want 0 (no escalation)", ap.BlockCount)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want empty (alive, idle, losing races)", ap.StopReason)
	}
	if ap.NoWorkCount != 1 {
		t.Errorf("NoWorkCount = %d, want 1 (idle-cycle counter)", ap.NoWorkCount)
	}
}

// TestShouldRestart_LockConflictNeverFastFails is the regression test for
// PUPPET-623: three coder agents against one claimable task used to leave two
// of them Stopped: fast_fail after 3 block cycles, so surplus capacity
// destroyed itself and stayed dead until a daemon restart.
func TestShouldRestart_LockConflictNeverFastFails(t *testing.T) {
	maxRetries := 2
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := lockConflictAgent()

	// Well past max_retries * (block budget + 1), i.e. past the old fast-fail.
	for i := 1; i <= (maxRetries+1)*4+5; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("cycle %d: shouldRestart = false, want true (StopReason=%q)", i, ap.StopReason)
		}
		if ap.StopReason == StopReasonFastFail {
			t.Fatalf("cycle %d: StopReason = %q, want never fast-fail", i, ap.StopReason)
		}
		if ap.StopReason == StopReasonMaxRetriesBlocked {
			t.Fatalf("cycle %d: agent blocked; contention must not escalate", i)
		}
	}
}

// TestShouldRestart_LockConflictPreservesBlockCount guards against laundering
// the escalation budget: only a clean run counts as progress, so idling after
// a genuine fault must not zero BlockCount.
func TestShouldRestart_LockConflictPreservesBlockCount(t *testing.T) {
	maxRetries := 2
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := lockConflictAgent()
	ap.BlockCount = 2 // earlier genuine fault

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true")
	}
	if ap.BlockCount != 2 {
		t.Errorf("BlockCount = %d, want 2 (preserved)", ap.BlockCount)
	}
}

// TestShouldRestart_LockConflictIgnoresRateLimitNoCount proves the uncounted
// arm dispatches on the outcome, not on "not NoWork": with the rate-limit
// opt-out set, a lock conflict must still take the idle path rather than
// falling through to applyCountedRestart.
func TestShouldRestart_LockConflictIgnoresRateLimitNoCount(t *testing.T) {
	maxRetries := 2
	cfg := &config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	}
	cfg.Daemon.RestartPolicy.RateLimitNoCount = config.BoolPtr(false)
	s := newTestSupervisorWithConfig(cfg)
	ap := lockConflictAgent()
	ap.RestartCount = maxRetries

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true")
	}
	if ap.RestartCount != maxRetries {
		t.Errorf("RestartCount = %d, want %d (still uncounted: unchanged, not refunded)", ap.RestartCount, maxRetries)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want empty", ap.StopReason)
	}
	if ap.NoWorkCount != 1 {
		t.Errorf("NoWorkCount = %d, want 1 (idle path taken)", ap.NoWorkCount)
	}
}
