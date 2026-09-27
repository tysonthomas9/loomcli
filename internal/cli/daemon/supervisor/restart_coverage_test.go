package supervisor

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
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
		// Exponential arm requires the classified failure a crash always carries.
		ap := &AgentProcess{
			RestartCount: tc.restartCount,
			LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		}
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
	ap := &AgentProcess{
		RestartCount: 100,
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
	}
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
			LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)},
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

// captureSlogDebug is captureSlog with the handler level lowered to Debug:
// the default TextHandler starts at Info, which would drop exactly the
// records the idle-state behavior is made of.
func captureSlogDebug(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// idleTestHarness drives sleepBeforeRestart without waiting out a real
// backoff: the call is run on its own goroutine, the test waits for
// BackoffUntil to be published, then releases the sleep through StopCh.
type idleTestHarness struct {
	s      *Supervisor
	events *capturedEvents
}

type capturedEvents struct {
	mu   sync.Mutex
	list []events.Event
}

func (c *capturedEvents) add(e events.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, e)
}

func (c *capturedEvents) countOf(t events.EventType) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.list {
		if e.Type == t {
			n++
		}
	}
	return n
}

func newIdleTestHarness(t *testing.T, noWorkBackoff, maxRetries int) (*idleTestHarness, *AgentProcess) {
	t.Helper()
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries:    &maxRetries,
				NoWorkBackoff: &noWorkBackoff,
			},
		},
	})
	captured := &capturedEvents{}
	s.EmitEvent = captured.add

	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "alpha", Role: "worker"},
		LastExitCode: 1,
		LastStart:    time.Now(),
	}
	return &idleTestHarness{s: s, events: captured}, ap
}

// runCycle runs one sleepBeforeRestart and reports whether BackoffUntil was
// set while the agent was waiting.
func (h *idleTestHarness) runCycle(t *testing.T, ap *AgentProcess) bool {
	t.Helper()
	ap.StopCh = make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.s.sleepBeforeRestart(ap)
	}()

	backoffSet := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ap.Mu.Lock()
		set := !ap.BackoffUntil.IsZero()
		ap.Mu.Unlock()
		if set {
			backoffSet = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(ap.StopCh)
	<-done
	return backoffSet
}

func TestSupervisor_SleepBeforeRestart_IdleIsAStateNotARestart(t *testing.T) {
	h, ap := newIdleTestHarness(t, 10, 3)
	logs := captureSlogDebug(t)

	noWork := func() {
		ap.LastExitCode = 1
		ap.LastError = &agenterr.AgentError{
			Class:   agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
			Message: "no claimable tasks",
		}
	}

	// Three consecutive idle polls.
	for i := 1; i <= 3; i++ {
		noWork()
		if !h.s.shouldRestart(ap) {
			t.Fatalf("shouldRestart = false on NoWork cycle %d, want true", i)
		}
		if !h.runCycle(t, ap) {
			t.Errorf("BackoffUntil was not set during idle cycle %d", i)
		}
	}

	out := logs.String()
	if got := strings.Count(out, "agent idle"); got != 1 {
		t.Errorf("Info \"agent idle\" count = %d, want 1\nlogs:\n%s", got, out)
	}
	if got := strings.Count(out, "agent still idle"); got != 2 {
		t.Errorf("Debug \"agent still idle\" count = %d, want 2\nlogs:\n%s", got, out)
	}
	if strings.Contains(out, "waiting before restart") {
		t.Errorf("idle polls must not log \"waiting before restart\"\nlogs:\n%s", out)
	}
	if got := h.events.countOf(events.AgentRestarted); got != 1 {
		t.Errorf("AgentRestarted events = %d, want 1 for one idle streak", got)
	}

	// A successful run ends the streak; the next idle poll starts a new one
	// and must announce itself again.
	ap.LastExitCode = 0
	ap.LastError = nil
	h.s.shouldRestart(ap)
	ap.Mu.Lock()
	streakReset := ap.NoWorkCount == 0 && ap.IdleSince.IsZero()
	ap.Mu.Unlock()
	if !streakReset {
		t.Fatal("NoWorkCount and IdleSince must both reset after a successful run")
	}

	noWork()
	if !h.s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false on the second streak's first NoWork, want true")
	}
	if !h.runCycle(t, ap) {
		t.Error("BackoffUntil was not set on the second streak's first idle cycle")
	}

	out = logs.String()
	if got := strings.Count(out, "agent idle"); got != 2 {
		t.Errorf("Info \"agent idle\" count = %d after second streak, want 2\nlogs:\n%s", got, out)
	}
	if got := h.events.countOf(events.AgentRestarted); got != 2 {
		t.Errorf("AgentRestarted events = %d after second streak, want 2", got)
	}
}

// A genuine failure restart must keep its Info line, its event and its span:
// the idle branch must not swallow real restarts.
func TestSupervisor_SleepBeforeRestart_RealRestartStillAnnounced(t *testing.T) {
	h, ap := newIdleTestHarness(t, 10, 3)
	logs := captureSlogDebug(t)

	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}
	if !h.s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false for a retryable timeout, want true")
	}
	ap.Mu.Lock()
	attempt := ap.RestartCount
	ap.Mu.Unlock()
	if attempt == 0 {
		t.Fatalf("RestartCount = %d, want non-zero for a genuine failure", attempt)
	}

	if !h.runCycle(t, ap) {
		t.Error("BackoffUntil was not set during a genuine failure restart")
	}

	out := logs.String()
	if !strings.Contains(out, "waiting before restart") {
		t.Errorf("a genuine failure must log \"waiting before restart\"\nlogs:\n%s", out)
	}
	if !strings.Contains(out, "attempt=1") {
		t.Errorf("restart line should carry a non-zero attempt\nlogs:\n%s", out)
	}
	if strings.Contains(out, "agent idle") {
		t.Errorf("a genuine failure must not be logged as idle\nlogs:\n%s", out)
	}
	if got := h.events.countOf(events.AgentRestarted); got != 1 {
		t.Errorf("AgentRestarted events = %d, want 1 for a real restart", got)
	}
}
