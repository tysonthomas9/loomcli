package supervisor

import (
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

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

func TestSupervisor_ShouldRestart_MaxRetriesExceeded_Blocks(t *testing.T) {
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

	// Exhausting the budget blocks-and-retries instead of giving up.
	if !s.shouldRestart(ap) {
		t.Error("shouldRestart should return true (block) when the budget is exhausted")
	}
	if ap.StopReason != StopReasonMaxRetriesBlocked {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetriesBlocked)
	}
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (reset on block)", ap.RestartCount)
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
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome)},
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
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
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
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
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
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
	}

	// Two NoWork exits
	s.shouldRestart(ap)
	s.shouldRestart(ap)
	if ap.NoWorkCount != 2 {
		t.Fatalf("NoWorkCount = %d, want 2 after two NoWork exits", ap.NoWorkCount)
	}

	// Timeout error
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}
	s.shouldRestart(ap)

	if ap.NoWorkCount != 0 {
		t.Errorf("NoWorkCount = %d, want 0 after Timeout error", ap.NoWorkCount)
	}
}

// ---------------------------------------------------------------------------
// NoWork no longer resets RestartCount; post-spawn NoWork backs
// off exponentially via NoWorkSpawnCount.
// ---------------------------------------------------------------------------

func TestShouldRestart_NoWork_PreservesRestartCount(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})

	ap := &AgentProcess{
		RestartCount: 2,
		LastExitCode: 0,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
	}

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart() = false, want true for NoWork")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (preserved, not reset, across a NoWork exit)", ap.RestartCount)
	}
}

func TestShouldRestart_NoWork_PostSpawn_IncrementsSpawnCount(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})

	ap := &AgentProcess{
		LastExitCode:        0,
		LastStart:           time.Now(),
		LastError:           &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
		LastNoWorkPostSpawn: true,
	}

	for i := 1; i <= 3; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("shouldRestart should return true for NoWork (iteration %d)", i)
		}
		if ap.NoWorkSpawnCount != i {
			t.Errorf("NoWorkSpawnCount = %d after %d post-spawn NoWork exits, want %d", ap.NoWorkSpawnCount, i, i)
		}
		if ap.NoWorkCount != i {
			t.Errorf("NoWorkCount = %d after %d NoWork exits, want %d", ap.NoWorkCount, i, i)
		}
	}
}

func TestShouldRestart_NoWork_PreSpawn_DoesNotGrow(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})

	ap := &AgentProcess{
		LastExitCode:        0,
		LastStart:           time.Now(),
		LastError:           &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
		LastNoWorkPostSpawn: false,
	}

	for i := 1; i <= 3; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("shouldRestart should return true for NoWork (iteration %d)", i)
		}
		if ap.NoWorkSpawnCount != 0 {
			t.Errorf("NoWorkSpawnCount = %d after pre-spawn NoWork exit %d, want 0 (only post-spawn grows it)", ap.NoWorkSpawnCount, i)
		}
	}
}

func TestShouldRestart_CleanSuccess_ResetsNoWorkSpawnCount(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})

	ap := &AgentProcess{
		NoWorkSpawnCount: 4,
		LastExitCode:     0,
		LastStart:        time.Now().Add(-2 * time.Minute),
		LastError:        nil,
	}

	s.shouldRestart(ap)
	if ap.NoWorkSpawnCount != 0 {
		t.Errorf("NoWorkSpawnCount = %d, want 0 after a clean success", ap.NoWorkSpawnCount)
	}
}

func TestShouldRestart_CountedError_ResetsNoWorkSpawnCount(t *testing.T) {
	maxRetries := 10
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})

	ap := &AgentProcess{
		NoWorkSpawnCount: 3,
		LastExitCode:     1,
		LastStart:        time.Now(),
		LastError:        &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)},
	}

	s.shouldRestart(ap)
	if ap.NoWorkSpawnCount != 0 {
		t.Errorf("NoWorkSpawnCount = %d, want 0 after a counted (Timeout) error", ap.NoWorkSpawnCount)
	}
}

func TestShouldRestart_FatalStop_ResetsNoWorkSpawnCount(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})

	ap := &AgentProcess{
		NoWorkSpawnCount: 3,
		LastExitCode:     1,
		LastStart:        time.Now(),
		LastError:        &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth)},
	}

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart() = true, want false for a fatal error")
	}
	if ap.NoWorkSpawnCount != 0 {
		t.Errorf("NoWorkSpawnCount = %d, want 0 after a fatal stop", ap.NoWorkSpawnCount)
	}
}

func TestShouldRestart_MaxRetriesBlock_ResetsNoWorkSpawnCount(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})

	ap := &AgentProcess{
		RestartCount:     1, // already at maxRetries; next counted failure exhausts the budget
		NoWorkSpawnCount: 5,
		LastExitCode:     1,
		LastStart:        time.Now(),
		LastError:        &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)},
	}

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart() = false, want true (blocked agents keep retrying)")
	}
	if ap.StopReason != StopReasonMaxRetriesBlocked {
		t.Fatalf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetriesBlocked)
	}
	if ap.NoWorkSpawnCount != 0 {
		t.Errorf("NoWorkSpawnCount = %d, want 0 once the agent blocks", ap.NoWorkSpawnCount)
	}
}

func TestGetNoWorkBackoffMax_Default(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if got := s.getNoWorkBackoffMax(); got != 900 {
		t.Errorf("getNoWorkBackoffMax() = %d, want 900 (default)", got)
	}
}

func TestGetNoWorkBackoffMax_Custom(t *testing.T) {
	val := 600
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{NoWorkBackoffMax: &val}},
	})
	if got := s.getNoWorkBackoffMax(); got != 600 {
		t.Errorf("getNoWorkBackoffMax() = %d, want 600", got)
	}
}

func TestGetNoWorkBackoffMax_ZeroOrNegative_FallsBackToDefault(t *testing.T) {
	// A NoWorkBackoffMax pointer that is explicitly set to <= 0 is a distinct
	// misconfiguration from "unset" (nil): the "> 0" guard rejects it and
	// falls back to the 900s default, same as if the field were never set.
	for _, val := range []int{0, -5, -900} {
		val := val
		s := newTestSupervisorWithConfig(&config.DaemonConfig{
			Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{NoWorkBackoffMax: &val}},
		})
		if got := s.getNoWorkBackoffMax(); got != 900 {
			t.Errorf("getNoWorkBackoffMax() with NoWorkBackoffMax=%d = %d, want 900 (default)", val, got)
		}
	}
}

func TestComputeBackoff_NoWork_MaxBelowBase_ClampsUp(t *testing.T) {
	base := 30
	tooLow := 10
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			NoWorkBackoff:    &base,
			NoWorkBackoffMax: &tooLow,
		}},
	})
	if got := s.getNoWorkBackoffMax(); got != base {
		t.Errorf("getNoWorkBackoffMax() = %d, want %d (clamped up to no_work_backoff, never below it)", got, base)
	}
}

func TestComputeBackoff_NoWork_PreSpawn_Fixed(t *testing.T) {
	base := 30
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{NoWorkBackoff: &base}},
	})

	for _, n := range []int{0, 1, 5, 20} {
		ap := &AgentProcess{
			LastError:           &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
			LastNoWorkPostSpawn: false,
			NoWorkSpawnCount:    n,
		}
		got := s.computeBackoff(ap)
		want := time.Duration(base) * time.Second
		if got != want {
			t.Errorf("computeBackoff() with pre-spawn NoWork (n=%d) = %v, want fixed %v", n, got, want)
		}
	}
}

func TestComputeBackoff_NoWork_PostSpawn_Exponential(t *testing.T) {
	base := 30
	max := 900
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			NoWorkBackoff:    &base,
			NoWorkBackoffMax: &max,
		}},
	})

	tests := []struct {
		n    int
		want time.Duration
	}{
		{1, 30 * time.Second},
		{2, 30 * time.Second},
		{3, 60 * time.Second},
		{4, 120 * time.Second},
		{5, 240 * time.Second},
		{6, 480 * time.Second},
		{7, 900 * time.Second}, // capped: 30*2^5=960 > 900
		{8, 900 * time.Second}, // stays pinned at the cap
	}
	for _, tt := range tests {
		ap := &AgentProcess{
			LastError:           &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
			LastNoWorkPostSpawn: true,
			NoWorkSpawnCount:    tt.n,
		}
		got := s.computeBackoff(ap)
		if got != tt.want {
			t.Errorf("computeBackoff() with post-spawn NoWork (NoWorkSpawnCount=%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}

func TestSupervisor_GetAgents_Snapshot_NewFields(t *testing.T) {
	backoffTime := time.Now().Add(30 * time.Second)
	cfg := &config.DaemonConfig{}
	s := newTestSupervisorWithConfig(cfg)
	s.Agents = []*AgentProcess{
		{
			Entry:            config.AgentEntry{Worktree: "alpha", Role: "plan"},
			WorktreePath:     "/path/alpha",
			Pid:              12345,
			NoWorkCount:      5,
			NoWorkSpawnCount: 2,
			BackoffUntil:     backoffTime,
			LastError:        &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)},
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
	if st.NoWorkSpawnCount != 2 {
		t.Errorf("NoWorkSpawnCount = %d, want 2", st.NoWorkSpawnCount)
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

// TestShouldRestart_NoWork_FiftyPostSpawnExits drives the full policy loop —
// shouldRestart -> applyNoWorkRestart -> computeBackoff — across a long
// no-work streak, which is the shape of the failure this policy exists to
// bound: a role that never has work respawning forever on a flat 30s poll.
//
// The short streak tests stop at n=3 and the backoff table pokes computeBackoff
// directly at fixed counter values. Neither shows that the two invariants hold
// TOGETHER over a long run, which is exactly what went wrong before: the
// restart budget was reset on every no-work exit (so max_retries was
// unreachable) while the poll interval never grew (so the loop was unbounded).
func TestShouldRestart_NoWork_FiftyPostSpawnExits(t *testing.T) {
	base, maxBackoff := 30, 900
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			NoWorkBackoff:    &base,
			NoWorkBackoffMax: &maxBackoff,
		}},
	})

	ap := &AgentProcess{
		RestartCount:        2, // budget already partly spent by real failures
		LastExitCode:        0,
		LastStart:           time.Now(),
		LastError:           &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
		LastNoWorkPostSpawn: true,
	}

	const exits = 50
	capSleep := time.Duration(maxBackoff) * time.Second
	var prev time.Duration
	var total time.Duration

	for i := 1; i <= exits; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("shouldRestart() = false at no-work exit %d; a no-work exit must never consume the restart budget", i)
		}
		got := s.computeBackoff(ap)

		if got > capSleep {
			t.Fatalf("backoff at exit %d = %v, want <= the configured cap %v", i, got, capSleep)
		}
		if got < prev {
			t.Fatalf("backoff at exit %d = %v, dropped below the previous %v; the cadence must never shrink mid-streak", i, got, prev)
		}
		if ap.RestartCount != 2 {
			t.Fatalf("RestartCount = %d at no-work exit %d, want 2 preserved; resetting it makes max_retries unreachable", ap.RestartCount, i)
		}
		prev = got
		total += got
	}

	if ap.NoWorkSpawnCount != exits {
		t.Errorf("NoWorkSpawnCount = %d after %d post-spawn no-work exits, want %d", ap.NoWorkSpawnCount, exits, exits)
	}
	if prev != capSleep {
		t.Errorf("backoff after %d exits = %v, want the cap %v (the streak must saturate, not keep doubling)", exits, prev, capSleep)
	}
	// Spawns per hour is the number the policy is judged on: on the flat 30s
	// poll, 50 exits fit in 25 minutes. Capped, they span most of a day.
	if total < 10*time.Hour {
		t.Errorf("total idle time across %d exits = %v, want >= 10h; the streak is not being bounded", exits, total)
	}

	// One genuinely clean run ends the streak and restores the cheap poll -- an
	// agent that finally finds work must not stay parked at the 15-minute cap.
	ap.LastError = nil
	ap.LastNoWorkPostSpawn = false
	ap.LastStart = time.Now().Add(-10 * time.Minute)
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart() = false after a clean run")
	}
	if ap.NoWorkSpawnCount != 0 {
		t.Errorf("NoWorkSpawnCount = %d after a clean run, want 0", ap.NoWorkSpawnCount)
	}
}

// noWorkIdleEstimate feeds the "has this failed-over agent been idle long
// enough to re-probe the primary backend?" decision. Modeling elapsed time as
// count x base was correct only while the cadence was flat; under the
// exponential post-spawn schedule it under-reports without bound and pins a
// failed-over agent to its fallback backend far past the cooldown.
func TestNoWorkIdleEstimate_TracksTheExponentialSchedule(t *testing.T) {
	base, maxBackoff := 30, 900
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			NoWorkBackoff:    &base,
			NoWorkBackoffMax: &maxBackoff,
		}},
	})

	tests := []struct {
		name       string
		count      int
		spawnCount int
		want       time.Duration
	}{
		{"no exits yet", 0, 0, 0},
		{"all pre-spawn stays linear", 5, 0, 150 * time.Second},
		{"one post-spawn exit", 1, 1, 30 * time.Second},
		// 30 + 30 + 60 + 120 + 240
		{"five post-spawn exits", 5, 5, 480 * time.Second},
		// linear 2x30 for the pre-spawn pair, then the five-exit schedule
		{"mixed pre- and post-spawn", 7, 5, 540 * time.Second},
		// 30+30+60+120+240+480+900+900 -- saturated at the cap
		{"saturated schedule", 8, 8, 2760 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.noWorkIdleEstimate(tt.count, tt.spawnCount); got != tt.want {
				t.Errorf("noWorkIdleEstimate(%d, %d) = %v, want %v", tt.count, tt.spawnCount, got, tt.want)
			}
		})
	}

	// A very long streak must saturate rather than overflow into a negative
	// duration, which would silently disable the primary re-probe forever.
	if got := s.noWorkIdleEstimate(1<<30, 1<<30); got != noWorkIdleCeiling {
		t.Errorf("noWorkIdleEstimate() on a pathological streak = %v, want the ceiling %v", got, noWorkIdleCeiling)
	}
}

// At the default base (30s) the count threshold is reached before the
// exponential growth starts, so correcting the estimate must not move the
// default behavior. With a small configured base it very much does.
func TestShouldRetryPrimaryAfterNoWork(t *testing.T) {
	defaults := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if defaults.shouldRetryPrimaryAfterNoWork(1, 1) {
		t.Error("shouldRetryPrimaryAfterNoWork(1, 1) = true at defaults, want false (30s < 1m cooldown)")
	}
	if !defaults.shouldRetryPrimaryAfterNoWork(2, 2) {
		t.Error("shouldRetryPrimaryAfterNoWork(2, 2) = false at defaults, want true (60s >= 1m cooldown)")
	}

	smallBase := 1
	fast := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{NoWorkBackoff: &smallBase}},
	})
	// 1+1+2+4+8+16+32+64 = 128s >= the 1m cooldown. The old count x base model
	// reported 8s here and would have waited for a 60-exit streak.
	if !fast.shouldRetryPrimaryAfterNoWork(8, 8) {
		t.Error("shouldRetryPrimaryAfterNoWork(8, 8) = false with a 1s base, want true; " +
			"the estimate is still ignoring the exponential schedule")
	}

	zeroBase := 0
	disabled := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{NoWorkBackoff: &zeroBase}},
	})
	if disabled.shouldRetryPrimaryAfterNoWork(100, 100) {
		t.Error("shouldRetryPrimaryAfterNoWork() = true with a non-positive base, want false")
	}
}
