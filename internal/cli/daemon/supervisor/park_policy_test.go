package supervisor

import (
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// TestShouldRestart_Exhaustion_ParksThenFreshBurst verifies the park cycle:
// failures count up to maxRetries, the exhausting failure parks (resetting
// the budget), and parking grants a fresh burst rather than an immediate
// re-park — so a transient root cause that clears mid-burst recovers.
func TestShouldRestart_Exhaustion_ParksThenFreshBurst(t *testing.T) {
	maxRetries := 2
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
	}

	// Failures within budget count up and do not park.
	for i := 1; i <= maxRetries; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("iteration %d: shouldRestart = false, want true (within budget)", i)
		}
		if ap.StopReason == StopReasonMaxRetriesParked {
			t.Fatalf("iteration %d: parked too early (RestartCount=%d, max=%d)", i, ap.RestartCount, maxRetries)
		}
	}

	// The exhausting failure parks and resets the budget.
	if !s.shouldRestart(ap) {
		t.Fatal("exhausting failure: shouldRestart = false, want true (parks)")
	}
	if ap.StopReason != StopReasonMaxRetriesParked {
		t.Fatalf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetriesParked)
	}
	if ap.RestartCount != 0 {
		t.Fatalf("RestartCount = %d, want 0 (reset on park)", ap.RestartCount)
	}

	// Park granted a fresh burst: the next failure counts from 1, not an
	// immediate re-park.
	if !s.shouldRestart(ap) {
		t.Fatal("post-park: shouldRestart = false, want true")
	}
	if ap.StopReason == StopReasonMaxRetriesParked {
		t.Fatal("post-park: re-parked immediately; budget did not reset")
	}
	if ap.RestartCount != 1 {
		t.Fatalf("post-park RestartCount = %d, want 1 (fresh burst)", ap.RestartCount)
	}
}

// TestShouldRestart_ParkBudgetEscalatesToFastFail is the determinism guard:
// an Unknown-class crash that keeps exhausting its budget without ever making
// progress parks only ParkBudget times, then fast-fails instead of parking
// forever.
func TestShouldRestart_ParkBudgetEscalatesToFastFail(t *testing.T) {
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

	parks := 0
	for cycle := 0; cycle < 50; cycle++ { // bound the loop; escalation must hit first
		if !s.shouldRestart(ap) {
			// Terminal: must be the fast-fail escalation, after exactly
			// ParkBudget park cycles.
			if ap.StopReason != StopReasonFastFail {
				t.Fatalf("terminal StopReason = %q, want %q", ap.StopReason, StopReasonFastFail)
			}
			want := agentpolicyDefaultParkBudget(t)
			if parks != want {
				t.Fatalf("escalated after %d parks, want %d", parks, want)
			}
			return
		}
		if ap.StopReason == StopReasonMaxRetriesParked {
			parks++
		}
	}
	t.Fatal("never escalated to FastFail; parked forever")
}

// agentpolicyDefaultParkBudget resolves the Unknown-class ParkBudget from the
// policy table so the escalation test tracks the policy, not a copied number.
func agentpolicyDefaultParkBudget(t *testing.T) int {
	t.Helper()
	budget := agentpolicy.Decide(agenterr.OutcomeFromHarness(wrapper.ErrUnknown)).ParkBudget
	if budget <= 0 {
		t.Fatal("Unknown ParkBudget must be a finite cap")
	}
	return budget
}

// TestShouldRestart_CleanRunResetsParkBudget verifies "progress" (a clean
// run) resets the park-escalation counter, so an agent that recovers between
// park spirals gets a full budget again.
func TestShouldRestart_CleanRunResetsParkBudget(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		ParkCount:    2, // mid-spiral
	}

	// Clean success: counters including ParkCount reset.
	ap.LastExitCode = 0
	ap.LastError = nil
	if !s.shouldRestart(ap) {
		t.Fatal("clean run: shouldRestart = false, want true")
	}
	if ap.ParkCount != 0 {
		t.Fatalf("ParkCount = %d after clean run, want 0", ap.ParkCount)
	}
}

// TestShouldRestart_FastFailClasses verifies deterministic classes stop
// immediately (no retry, no park): ContextOverflow today.
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

// TestShouldRestart_Fatal_StillStops_NotParked is the critical regression
// guard: a fatal error (auth/billing) must stop the agent even when the
// budget is well past exhausted — park must never swallow a fatal.
func TestShouldRestart_Fatal_StillStops_NotParked(t *testing.T) {
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
			t.Errorf("%v: shouldRestart = true, want false (fatal must stop, never park)", class)
		}
		if ap.StopReason != StopReasonFatalError {
			t.Errorf("%v: StopReason = %q, want %q", class, ap.StopReason, StopReasonFatalError)
		}
	}
}

// TestComputeBackoff_Parked_ReturnsParkInterval verifies a parked agent
// sleeps the fixed park interval (keyed on StopReason, not error class)
// rather than a huge exponential backoff, and that the private override wins.
func TestComputeBackoff_Parked_ReturnsParkInterval(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.maxRetriesParkInterval = 50 * time.Millisecond // test override

	ap := &AgentProcess{
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
		RestartCount: 99, // would be a maxed exponential backoff if not parked
		StopReason:   StopReasonMaxRetriesParked,
	}
	if got := s.computeBackoff(ap); got != 50*time.Millisecond {
		t.Errorf("computeBackoff(parked) = %v, want 50ms (override)", got)
	}

	s.maxRetriesParkInterval = 0 // fall back to the package default
	if got := s.computeBackoff(ap); got != defaultMaxRetriesParkInterval {
		t.Errorf("computeBackoff(parked, default) = %v, want %v", got, defaultMaxRetriesParkInterval)
	}
}

// TestPark_PreservesObservability verifies the durable signals survive a
// park: even though RestartCount resets to 0, the status payload still
// carries the parked stop reason, the park-cycle count, and the error class
// that caused it.
func TestPark_PreservesObservability(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt", Role: "plan"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
		RestartCount: maxRetries, // next failure exhausts → park
	}
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true (park)")
	}

	s.Agents = []*AgentProcess{ap}
	statuses := s.GetAgents()
	if len(statuses) != 1 {
		t.Fatalf("len(GetAgents()) = %d, want 1", len(statuses))
	}
	st := statuses[0]
	if st.StopReason != StopReasonMaxRetriesParked {
		t.Errorf("status.StopReason = %q, want %q", st.StopReason, StopReasonMaxRetriesParked)
	}
	if st.ParkCount != 1 {
		t.Errorf("status.ParkCount = %d, want 1", st.ParkCount)
	}
	if st.LastErrorClass == "" {
		t.Error("status.LastErrorClass is empty, want the class that caused the park")
	}
}
