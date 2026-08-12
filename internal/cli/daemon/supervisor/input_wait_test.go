package supervisor

import (
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newInputWaitFixture builds a supervisor holding one agent that has been silent
// for 20 minutes against a 60-second output timeout — i.e. an agent the watchdog
// kills on the next health tick unless something suspends that kill. The PID is
// fake, so StopAgent short-circuits and StopReason is the observable verdict
// (the same shape TestCheckWatchdog_DoesNotKillBusyAgentOnFreshHeartbeat uses).
func newInputWaitFixture(t *testing.T) (*Supervisor, *AgentProcess) {
	t.Helper()

	cfg := makeSupervisorConfig([]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}}, nil)
	cfg.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60)

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
		Pid:          99999999, // fake PID that won't exist
		LastStart:    time.Now().Add(-25 * time.Minute),
		LastActivity: time.Now().Add(-20 * time.Minute),
	}
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		Agents:         []*AgentProcess{ap},
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	return s, ap
}

// backdate rewrites the agent's silence and wait clocks so a test can describe
// the state it wants without sleeping. RecordAgentInputWait stamps LastActivity
// on every edge by design (see input_wait.go), so any test that drives the
// counter through it has to set the clocks afterwards.
func backdate(ap *AgentProcess, silentFor, waitingFor time.Duration) {
	now := time.Now()
	ap.Mu.Lock()
	ap.LastActivity = now.Add(-silentFor)
	if ap.InputWaitPending > 0 {
		ap.InputWaitSince = now.Add(-waitingFor)
	}
	ap.Mu.Unlock()
}

func watchdogKilled(ap *AgentProcess) bool {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.StopReason == StopReasonWatchdog
}

func pendingCount(ap *AgentProcess) int {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.InputWaitPending
}

// The baseline the suspension must not weaken: a silent agent with nothing
// pending is still killed at output_timeout.
func TestCheckWatchdog_KillsIdleAgentWhenNothingIsPending(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.checkAgentHealth()

	if !watchdogKilled(ap) {
		t.Error("StopReason unset, want Watchdog (an idle agent with no pending prompt must still be killed)")
	}
}

// The core behavior: an agent parked on an interactive prompt is silent by
// design, and must not be killed for it.
func TestCheckWatchdog_DoesNotKillWhileAnInteractivePromptIsPending(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.RecordAgentInputWait("test", true)
	backdate(ap, 20*time.Minute, time.Minute)

	s.checkAgentHealth()

	if watchdogKilled(ap) {
		t.Error("StopReason = Watchdog, want unset (an agent waiting on a human is not hung)")
	}
}

// The suspension is temporary, not a permanent exemption: once the prompt is
// answered and the agent goes quiet again, the normal watchdog resumes.
func TestCheckWatchdog_KillsAgainOncePendingReturnsToZero(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.RecordAgentInputWait("test", true)
	s.RecordAgentInputWait("test", false)
	if got := pendingCount(ap); got != 0 {
		t.Fatalf("InputWaitPending = %d, want 0 after the answer", got)
	}
	backdate(ap, 20*time.Minute, 0)

	s.checkAgentHealth()

	if !watchdogKilled(ap) {
		t.Error("StopReason unset, want Watchdog (the suspension must lift when the prompt is answered)")
	}
}

// The wait is independently bounded. Nothing else would ever end this run —
// there is no wall-clock or run-duration cap on an agent (the invoke context is
// context.WithCancel, not WithTimeout) — so an unanswered prompt has to stop
// suspending the kill on its own.
func TestCheckWatchdog_KillsAnywayOnceTheWaitBoundExpires(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.RecordAgentInputWait("test", true)
	// Silent and waiting for 20 minutes, past the 15-minute default bound.
	backdate(ap, 20*time.Minute, 20*time.Minute)

	s.checkAgentHealth()

	if !watchdogKilled(ap) {
		t.Error("StopReason unset, want Watchdog (a wait past its bound must stop suspending the kill)")
	}
	// The count is deliberately left standing so a late answer still decrements
	// cleanly; expiry lifts the suspension, it does not rewrite the counter.
	if got := pendingCount(ap); got != 1 {
		t.Errorf("InputWaitPending = %d, want 1 (bound expiry must not mutate the counter)", got)
	}
}

func TestCheckWatchdog_WaitBoundHonorsEnvOverride(t *testing.T) {
	t.Run("inside the overridden bound the kill stays suspended", func(t *testing.T) {
		t.Setenv(envInputWaitMaxSeconds, "3600")
		s, ap := newInputWaitFixture(t)

		s.RecordAgentInputWait("test", true)
		backdate(ap, 20*time.Minute, 20*time.Minute) // past the default, inside 3600s

		s.checkAgentHealth()

		if watchdogKilled(ap) {
			t.Error("StopReason = Watchdog, want unset (env override must widen the bound)")
		}
	})

	t.Run("past the overridden bound the kill fires", func(t *testing.T) {
		t.Setenv(envInputWaitMaxSeconds, "1")
		s, ap := newInputWaitFixture(t)

		s.RecordAgentInputWait("test", true)
		backdate(ap, 20*time.Minute, 30*time.Second) // inside the default, past 1s

		s.checkAgentHealth()

		if !watchdogKilled(ap) {
			t.Error("StopReason unset, want Watchdog (env override must narrow the bound)")
		}
	})

	t.Run("a non-positive bound disables the suspension entirely", func(t *testing.T) {
		t.Setenv(envInputWaitMaxSeconds, "0")
		s, ap := newInputWaitFixture(t)

		s.RecordAgentInputWait("test", true)
		backdate(ap, 20*time.Minute, time.Second)

		s.checkAgentHealth()

		if !watchdogKilled(ap) {
			t.Error("StopReason unset, want Watchdog (0 must mean 'never suspend', not 'suspend forever')")
		}
	})
}

// Why a counter and not a boolean: a multi-question dialog can surface a new
// request while the previous one is still settling. With a flag, the first
// answer would un-suspend the watchdog underneath the second, still-open prompt.
func TestCheckWatchdog_OverlappingPromptsKeepTheSuspension(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.RecordAgentInputWait("test", true)  // first question
	s.RecordAgentInputWait("test", true)  // second question, first still open
	s.RecordAgentInputWait("test", false) // first answered
	if got := pendingCount(ap); got != 1 {
		t.Fatalf("InputWaitPending = %d, want 1 (2 in, 1 out)", got)
	}
	backdate(ap, 20*time.Minute, time.Minute)

	s.checkAgentHealth()

	if watchdogKilled(ap) {
		t.Error("StopReason = Watchdog, want unset (one answer must not lift a suspension the other prompt still needs)")
	}

	// The last answer lifts it.
	s.RecordAgentInputWait("test", false)
	if got := pendingCount(ap); got != 0 {
		t.Fatalf("InputWaitPending = %d, want 0 after the last answer", got)
	}
	backdate(ap, 20*time.Minute, 0)

	s.checkAgentHealth()

	if !watchdogKilled(ap) {
		t.Error("StopReason unset, want Watchdog (the suspension must lift with the last answer)")
	}
}

// A prompt joining an already-open wait must not restart the bound, or a harness
// that re-prompts on a timer could hold the kill off forever, one question at a
// time.
func TestRecordAgentInputWait_LaterPromptDoesNotRestartTheBound(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.RecordAgentInputWait("test", true)
	backdate(ap, 20*time.Minute, 20*time.Minute)
	anchored := func() time.Time {
		ap.Mu.Lock()
		defer ap.Mu.Unlock()
		return ap.InputWaitSince
	}
	before := anchored()

	s.RecordAgentInputWait("test", true) // second question joins the open wait

	if got := anchored(); !got.Equal(before) {
		t.Errorf("InputWaitSince = %v, want %v (a joining prompt must not re-anchor the bound)", got, before)
	}
	// The bound is still expired, so the watchdog still kills — but only after
	// re-backdating LastActivity, which the second edge just refreshed.
	backdate(ap, 20*time.Minute, 20*time.Minute)
	s.checkAgentHealth()
	if !watchdogKilled(ap) {
		t.Error("StopReason unset, want Watchdog (a re-prompt must not buy a fresh bound)")
	}
}

// The wait is silent by construction, so at the instant the answer lands
// LastActivity is already older than output_timeout. Without stamping the edge,
// the very next health tick would kill the agent it just un-blocked.
func TestRecordAgentInputWait_EndEdgeRefreshesActivity(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.RecordAgentInputWait("test", true)
	backdate(ap, 20*time.Minute, time.Minute)
	s.RecordAgentInputWait("test", false)

	s.checkAgentHealth()

	if watchdogKilled(ap) {
		t.Error("StopReason = Watchdog, want unset (an agent that just got its answer must get a full output_timeout)")
	}
}

func TestRecordAgentInputWait_ClampsAtZero(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	// A duplicate or replayed "end" must not drive the count negative, where a
	// later "begin" would leave it at zero and silently fail to suspend.
	s.RecordAgentInputWait("test", false)
	s.RecordAgentInputWait("test", false)
	if got := pendingCount(ap); got != 0 {
		t.Fatalf("InputWaitPending = %d, want 0 (must clamp, never go negative)", got)
	}

	s.RecordAgentInputWait("test", true)
	if got := pendingCount(ap); got != 1 {
		t.Errorf("InputWaitPending = %d, want 1 (a begin after stray ends must still suspend)", got)
	}
	backdate(ap, 20*time.Minute, time.Minute)

	s.checkAgentHealth()

	if watchdogKilled(ap) {
		t.Error("StopReason = Watchdog, want unset (the clamped counter must still suspend)")
	}
}

func TestRecordAgentInputWait_IgnoresUnknownAgent(t *testing.T) {
	s, ap := newInputWaitFixture(t)

	s.RecordAgentInputWait("ghost", true)
	s.RecordAgentInputWait("", true)

	if got := pendingCount(ap); got != 0 {
		t.Errorf("InputWaitPending on unrelated agent = %d, want 0", got)
	}
}

// A child that died while parked on a prompt never sends its "end", so a stale
// count must not survive into the next supervision cycle.
func TestClearAgentSessionState_ResetsInputWaitCounter(t *testing.T) {
	s, ap := newInputWaitFixture(t)
	s.RecordAgentInputWait("test", true)
	s.RecordAgentInputWait("test", true)

	s.clearAgentSessionState(ap)

	if got := pendingCount(ap); got != 0 {
		t.Errorf("InputWaitPending = %d, want 0 (must reset between supervision cycles)", got)
	}
	ap.Mu.Lock()
	since := ap.InputWaitSince
	ap.Mu.Unlock()
	if !since.IsZero() {
		t.Errorf("InputWaitSince = %v, want zero (must reset between supervision cycles)", since)
	}
}

func TestGetInputWaitMax(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		if got := s.GetInputWaitMax(); got != defaultInputWaitMaxSeconds {
			t.Errorf("GetInputWaitMax() = %d, want %d", got, defaultInputWaitMaxSeconds)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv(envInputWaitMaxSeconds, "42")
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		if got := s.GetInputWaitMax(); got != 42 {
			t.Errorf("GetInputWaitMax() = %d, want 42 (env override)", got)
		}
	})

	t.Run("unparseable env falls back to the default", func(t *testing.T) {
		t.Setenv(envInputWaitMaxSeconds, "not-a-number")
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		if got := s.GetInputWaitMax(); got != defaultInputWaitMaxSeconds {
			t.Errorf("GetInputWaitMax() = %d, want %d (bad env must not disable the bound)", got, defaultInputWaitMaxSeconds)
		}
	})
}

// Only the idle kill is suspended. The shutdown/abort path must still take a
// waiting agent down immediately — the counter is not a shield.
func TestStopAgent_InterruptsAnAgentWaitingOnInput(t *testing.T) {
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*AgentProcess, 0),
		EmitEvent:      func(events.Event) {},
	}

	cmd := exec.Command("sleep", "60") //nolint:norawexec // test fixture: stands in for an agent parked on a prompt
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	ap := &AgentProcess{
		Entry:            cfgpkg.AgentEntry{Worktree: "test"},
		Cmd:              cmd,
		Pid:              pid,
		InputWaitPending: 2, // parked on two open prompts
		InputWaitSince:   time.Now(),
	}
	s.Agents = []*AgentProcess{ap}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.waitForAgent(ap)
	}()

	s.StopAgent(ap, 5*time.Second)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout: StopAgent did not interrupt an agent with pending input waits")
	}

	ap.Mu.Lock()
	finalPID := ap.Pid
	ap.Mu.Unlock()
	if finalPID != 0 {
		t.Errorf("pid = %d after StopAgent, want 0 (a pending input wait must not block shutdown)", finalPID)
	}
}

func TestCheckAgentStopSignals_ShutdownWinsOverPendingInputWait(t *testing.T) {
	s, ap := newInputWaitFixture(t)
	s.RecordAgentInputWait("test", true)

	close(s.Shutdown)

	if !s.checkAgentStopSignals(ap) {
		t.Fatal("checkAgentStopSignals() = false, want true (shutdown must interrupt a waiting agent)")
	}
	ap.Mu.Lock()
	reason := ap.StopReason
	ap.Mu.Unlock()
	if reason != StopReasonShutdown {
		t.Errorf("StopReason = %q, want %q", reason, StopReasonShutdown)
	}
}
