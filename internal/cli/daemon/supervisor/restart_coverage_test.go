package supervisor

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func newTestSupervisorWithConfig(cfg *config.DaemonConfig) *Supervisor {
	s := &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
	}
	return s
}

func TestSupervisor_GetMaxRetries_Default(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if got := s.getMaxRetries(); got != 3 {
		t.Errorf("getMaxRetries() = %d, want 3 (default)", got)
	}
}

func TestSupervisor_GetMaxRetries_Custom(t *testing.T) {
	val := 10
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &val,
			},
		},
	})
	if got := s.getMaxRetries(); got != 10 {
		t.Errorf("getMaxRetries() = %d, want 10", got)
	}
}

func TestSupervisor_GetBackoffInitial_Default(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if got := s.getBackoffInitial(); got != 2 {
		t.Errorf("getBackoffInitial() = %d, want 2 (default)", got)
	}
}

func TestSupervisor_GetBackoffInitial_Custom(t *testing.T) {
	val := 5
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffInitial: &val,
			},
		},
	})
	if got := s.getBackoffInitial(); got != 5 {
		t.Errorf("getBackoffInitial() = %d, want 5", got)
	}
}

func TestSupervisor_GetBackoffMax_Default(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if got := s.getBackoffMax(); got != 300 {
		t.Errorf("getBackoffMax() = %d, want 300 (default)", got)
	}
}

func TestSupervisor_GetBackoffMax_Custom(t *testing.T) {
	val := 600
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffMax: &val,
			},
		},
	})
	if got := s.getBackoffMax(); got != 600 {
		t.Errorf("getBackoffMax() = %d, want 600", got)
	}
}

func TestSupervisor_ComputeBackoff(t *testing.T) {
	initial := 2
	maxBack := 300
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffInitial: &initial,
				BackoffMax:     &maxBack,
			},
		},
	})

	tests := []struct {
		restartCount int
		expect       time.Duration
	}{
		{0, 2 * time.Second},     // 2 * 2^0 = 2
		{1, 4 * time.Second},     // 2 * 2^1 = 4
		{2, 8 * time.Second},     // 2 * 2^2 = 8
		{3, 16 * time.Second},    // 2 * 2^3 = 16
		{10, 2048 * time.Second}, // 2 * 2^10 = 2048 but capped to 300... wait 2048 < 300? no, 2048 > 300
	}

	for _, tc := range tests {
		ap := &AgentProcess{RestartCount: tc.restartCount}
		got := s.computeBackoff(ap)
		// For large restart counts, result is capped at maxBackoff
		if tc.restartCount >= 8 {
			// 2 * 2^8 = 512 > 300, should be capped
			if got != 300*time.Second {
				t.Errorf("computeBackoff(restart=%d) = %v, want %v (capped)", tc.restartCount, got, 300*time.Second)
			}
		} else {
			if got != tc.expect {
				t.Errorf("computeBackoff(restart=%d) = %v, want %v", tc.restartCount, got, tc.expect)
			}
		}
	}
}

func TestSupervisor_ComputeBackoff_OverflowProtection(t *testing.T) {
	initial := 2
	maxBack := 300
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffInitial: &initial,
				BackoffMax:     &maxBack,
			},
		},
	})

	// Very high restart count should be capped and not overflow
	ap := &AgentProcess{RestartCount: 100}
	got := s.computeBackoff(ap)
	if got != 300*time.Second {
		t.Errorf("computeBackoff(restart=100) = %v, want %v (capped)", got, 300*time.Second)
	}
}

func TestSupervisor_ShouldRestart_SuccessfulLongRun(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 0,
		LastStart:    time.Now().Add(-5 * time.Minute), // ran for 5 minutes
		RestartCount: 2,
	}

	if !s.shouldRestart(ap) {
		t.Error("shouldRestart should return true for successful long run")
	}

	// Restart count should be reset to 0 after successful long run
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount should be reset to 0, got %d", ap.RestartCount)
	}
}

func TestSupervisor_ShouldRestart_MaxRetriesExceeded_Parks(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		RestartCount: 3, // Already at max; the next failure exhausts the budget
	}

	// Exhausting the budget parks-and-retries instead of giving up.
	if !s.shouldRestart(ap) {
		t.Error("shouldRestart should return true (park) when the budget is exhausted")
	}
	if ap.StopReason != StopReasonMaxRetriesParked {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetriesParked)
	}
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (reset on park)", ap.RestartCount)
	}
}

func TestSupervisor_ShouldRestart_BelowMax(t *testing.T) {
	maxRetries := 5
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		RestartCount: 2,
	}

	if !s.shouldRestart(ap) {
		t.Error("shouldRestart should return true when below max retries")
	}
}

// TestSupervisor_ShouldRestart_BackendUnavailable_PreservesBudget is the
// regression guard for LOOM-4: a missing backend CLI detected at runtime must
// keep the agent retrying (it recovers when the binary returns) without eroding
// max_retries, so it never escalates to a permanent failure.
func TestSupervisor_ShouldRestart_BackendUnavailable_PreservesBudget(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.BackendUnavailable},
		RestartCount: maxRetries, // already at the limit; a generic error would now fail
	}

	// Repeated BackendUnavailable exits must keep restarting without eroding the
	// budget or tripping the max-retries stop reason.
	for i := 0; i < 5; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("shouldRestart should keep restarting on BackendUnavailable (iteration %d)", i)
		}
		if ap.RestartCount != maxRetries {
			t.Fatalf("RestartCount = %d after BackendUnavailable, want %d (budget must not erode)", ap.RestartCount, maxRetries)
		}
		if ap.StopReason == StopReasonMaxRetries {
			t.Fatalf("StopReason set to max-retries on BackendUnavailable (iteration %d)", i)
		}
	}

	// Backoff is the fixed recheck interval, not exponential off RestartCount.
	if got := s.computeBackoff(ap); got != backendUnavailableRecheckInterval {
		t.Errorf("computeBackoff = %v, want %v (fixed recheck)", got, backendUnavailableRecheckInterval)
	}
}

// TestSupervisor_ShouldRestart_Exhaustion_ParksThenFreshBurst verifies the full
// Layer-2 cycle: failures count up to maxRetries, the exhausting failure parks
// (resetting the budget), and parking grants a fresh burst rather than an
// immediate re-park — so a transient root cause that clears mid-burst recovers.
func TestSupervisor_ShouldRestart_Exhaustion_ParksThenFreshBurst(t *testing.T) {
	maxRetries := 2
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.Transient},
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

// TestSupervisor_ComputeBackoff_Parked_ReturnsParkInterval verifies a parked
// agent sleeps the fixed park interval (keyed on StopReason, not error class)
// rather than a huge exponential backoff, and that the private override wins.
func TestSupervisor_ComputeBackoff_Parked_ReturnsParkInterval(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.maxRetriesParkInterval = 50 * time.Millisecond // test override

	ap := &AgentProcess{
		LastError:    &agenterr.AgentError{Class: agenterr.Transient},
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

// TestSupervisor_ShouldRestart_Fatal_StillStops_NotParked is the critical
// regression guard: a fatal error (auth/billing) must stop the agent even when
// the budget is well past exhausted — park must never swallow a fatal.
func TestSupervisor_ShouldRestart_Fatal_StillStops_NotParked(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})

	for _, class := range []agenterr.ErrorClass{agenterr.AuthFailure, agenterr.BillingError} {
		ap := &AgentProcess{
			Entry:        config.AgentEntry{Worktree: "wt"},
			LastExitCode: 1,
			LastStart:    time.Now(),
			LastError:    &agenterr.AgentError{Class: class},
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

// TestSupervisor_Park_PreservesObservability verifies the durable signals
// survive a park: even though RestartCount resets to 0, the status payload still
// carries the parked stop reason and the error class that caused it.
func TestSupervisor_Park_PreservesObservability(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt", Role: "plan"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.Transient},
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
	if st.LastErrorClass == "" {
		t.Error("status.LastErrorClass is empty, want the class that caused the park")
	}
}

func TestSupervisor_AgentCount(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.Agents = []*AgentProcess{
		{Entry: config.AgentEntry{Worktree: "a"}},
		{Entry: config.AgentEntry{Worktree: "b"}},
		{Entry: config.AgentEntry{Worktree: "c"}},
	}

	if got := s.AgentCount(); got != 3 {
		t.Errorf("AgentCount() = %d, want 3", got)
	}
}

func TestSupervisor_GetAgents_Snapshot(t *testing.T) {
	cfg := &config.DaemonConfig{}
	s := newTestSupervisorWithConfig(cfg)
	s.Agents = []*AgentProcess{
		{
			Entry:        config.AgentEntry{Worktree: "alpha", Role: "plan"},
			WorktreePath: "/path/alpha",
			Pid:          12345,
			RestartCount: 2,
			LastExitCode: 0,
		},
		{
			Entry:        config.AgentEntry{Worktree: "beta", Role: "task"},
			WorktreePath: "/path/beta",
			Pid:          0,
			RestartCount: 0,
		},
	}

	statuses := s.GetAgents()

	if len(statuses) != 2 {
		t.Fatalf("len(GetAgents()) = %d, want 2", len(statuses))
	}

	if statuses[0].Worktree != "alpha" {
		t.Errorf("statuses[0].Worktree = %q, want %q", statuses[0].Worktree, "alpha")
	}
	if statuses[0].Role != "plan" {
		t.Errorf("statuses[0].Role = %q, want %q", statuses[0].Role, "plan")
	}
	if statuses[0].PID != 12345 {
		t.Errorf("statuses[0].PID = %d, want 12345", statuses[0].PID)
	}
	if statuses[0].RestartCount != 2 {
		t.Errorf("statuses[0].RestartCount = %d, want 2", statuses[0].RestartCount)
	}

	if statuses[1].Worktree != "beta" {
		t.Errorf("statuses[1].Worktree = %q, want %q", statuses[1].Worktree, "beta")
	}
	if statuses[1].PID != 0 {
		t.Errorf("statuses[1].PID = %d, want 0 (not running)", statuses[1].PID)
	}
}

func TestSupervisor_ShouldRestart_NoWorkCount_Increments(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.NoWork},
	}

	// Three consecutive NoWork exits
	for i := 1; i <= 3; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("shouldRestart should return true for NoWork (iteration %d)", i)
		}
		if ap.NoWorkCount != i {
			t.Errorf("NoWorkCount = %d after %d NoWork exits, want %d", ap.NoWorkCount, i, i)
		}
	}
}

func TestSupervisor_ShouldRestart_NoWorkCount_ResetOnCleanSuccess(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.NoWork},
	}

	// Two NoWork exits
	s.shouldRestart(ap)
	s.shouldRestart(ap)
	if ap.NoWorkCount != 2 {
		t.Fatalf("NoWorkCount = %d, want 2 after two NoWork exits", ap.NoWorkCount)
	}

	// Clean success exit
	ap.LastExitCode = 0
	ap.LastError = nil
	s.shouldRestart(ap)

	if ap.NoWorkCount != 0 {
		t.Errorf("NoWorkCount = %d, want 0 after clean success", ap.NoWorkCount)
	}
}

func TestSupervisor_ShouldRestart_NoWorkCount_ResetOnError(t *testing.T) {
	maxRetries := 10
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.NoWork},
	}

	// Two NoWork exits
	s.shouldRestart(ap)
	s.shouldRestart(ap)
	if ap.NoWorkCount != 2 {
		t.Fatalf("NoWorkCount = %d, want 2 after two NoWork exits", ap.NoWorkCount)
	}

	// Timeout error
	ap.LastError = &agenterr.AgentError{Class: agenterr.Timeout}
	s.shouldRestart(ap)

	if ap.NoWorkCount != 0 {
		t.Errorf("NoWorkCount = %d, want 0 after Timeout error", ap.NoWorkCount)
	}
}

func TestSupervisor_GetAgents_Snapshot_NewFields(t *testing.T) {
	backoffTime := time.Now().Add(30 * time.Second)
	cfg := &config.DaemonConfig{}
	s := newTestSupervisorWithConfig(cfg)
	s.Agents = []*AgentProcess{
		{
			Entry:        config.AgentEntry{Worktree: "alpha", Role: "plan"},
			WorktreePath: "/path/alpha",
			Pid:          12345,
			NoWorkCount:  5,
			BackoffUntil: backoffTime,
			LastError:    &agenterr.AgentError{Class: agenterr.RateLimited},
		},
	}

	statuses := s.GetAgents()

	if len(statuses) != 1 {
		t.Fatalf("len(GetAgents()) = %d, want 1", len(statuses))
	}

	st := statuses[0]
	if st.NoWorkCount != 5 {
		t.Errorf("NoWorkCount = %d, want 5", st.NoWorkCount)
	}
	if !st.BackoffUntil.Equal(backoffTime) {
		t.Errorf("BackoffUntil = %v, want %v", st.BackoffUntil, backoffTime)
	}
	if st.LastErrorClass != "RateLimited" {
		t.Errorf("LastErrorClass = %q, want %q", st.LastErrorClass, "RateLimited")
	}
	if st.RemoteBranch != "origin/main" {
		t.Errorf("RemoteBranch = %q, want %q", st.RemoteBranch, "origin/main")
	}
}

func TestSupervisor_WaitForAgent_NilCmd(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})

	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "test"},
		// Cmd is nil
	}

	exitCode := s.waitForAgent(ap)
	if exitCode != -1 {
		t.Errorf("waitForAgent with nil cmd = %d, want -1", exitCode)
	}
}

func TestSupervisor_ResolveRoleConfig_BuiltIn(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.ProjectDir = "/tmp"

	// Built-in roles should resolve without error
	for _, role := range []string{"plan", "task"} {
		rc, err := s.resolveRoleConfig(role, 0)
		if err != nil {
			t.Errorf("resolveRoleConfig(%q) error = %v", role, err)
		}
		if rc.Description == "" {
			t.Errorf("resolveRoleConfig(%q) description should not be empty", role)
		}
	}
}

func TestSupervisor_ResolveRoleConfig_UnknownRole(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.ProjectDir = "/tmp"

	_, err := s.resolveRoleConfig("nonexistent", 0)
	if err == nil {
		t.Error("expected error for unknown role, got nil")
	}
}
