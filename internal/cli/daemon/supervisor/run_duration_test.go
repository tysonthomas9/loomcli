package supervisor

import (
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newRunCapFixture builds a supervisor holding one agent that has been running
// for ranFor and produced output just now. The fresh activity is the point: the
// idle kill can never be the thing that fires in this file, so any StopReason
// these tests observe was set by the duration cap. The PID is fake, so StopAgent
// short-circuits and StopReason is the observable verdict (the same shape
// newInputWaitFixture uses).
func newRunCapFixture(t *testing.T, ranFor time.Duration) (*Supervisor, *AgentProcess) {
	t.Helper()

	cfg := makeSupervisorConfig([]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}}, nil)
	cfg.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60)

	now := time.Now()
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
		Pid:          99999999, // fake PID that won't exist
		LastStart:    now.Add(-ranFor),
		LastActivity: now,
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

func runCapKilled(ap *AgentProcess) bool {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.StopReason == StopReasonRunDurationExceeded
}

func stopReasonOf(ap *AgentProcess) StopReason {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	return ap.StopReason
}

// setRoleCap pins a per-role max_run_duration on the agent.
func setRoleCap(ap *AgentProcess, seconds int) {
	ap.RoleConfig.MaxRunDuration = &seconds
}

// ---------------------------------------------------------------------------
// the cap itself
// ---------------------------------------------------------------------------

// The whole point: a run that outlives the ceiling is stopped, even though it is
// chattering away and the idle kill has no complaint about it.
func TestCheckWatchdog_KillsARunPastTheDurationCap(t *testing.T) {
	s, ap := newRunCapFixture(t, 5*time.Hour) // past the 4-hour default

	s.checkAgentHealth()

	if !runCapKilled(ap) {
		t.Errorf("StopReason = %q, want %q (a run past the cap must be stopped even while producing output)",
			stopReasonOf(ap), StopReasonRunDurationExceeded)
	}
}

// The mirror: the cap must be unreachable by a healthy run, or it is just a
// slower way to lose work.
func TestCheckWatchdog_LeavesARunInsideTheDurationCapAlone(t *testing.T) {
	s, ap := newRunCapFixture(t, 3*time.Hour) // inside the 4-hour default

	s.checkAgentHealth()

	if got := stopReasonOf(ap); got != "" {
		t.Errorf("StopReason = %q, want unset (a run inside the cap must not be touched)", got)
	}
}

// The ordering this change exists for. applyIdleKill suspends the silence kill
// while a prompt is outstanding, so during a wait the duration cap is the ONLY
// bound left over the run. If the cap consulted the same counter, an unanswered
// prompt would once again hold a worker slot without limit.
func TestCheckWatchdog_DurationCapFiresThroughAPendingInputWait(t *testing.T) {
	t.Run("past the cap, still killed", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 5*time.Hour)

		s.RecordAgentInputWait("test", true)
		// Silent for 20 minutes against a 60s output timeout, but only 1 minute
		// into the wait: the idle kill is actively suspended right now.
		backdate(ap, 20*time.Minute, time.Minute)

		s.checkAgentHealth()

		if !runCapKilled(ap) {
			t.Errorf("StopReason = %q, want %q (the input-wait suspension must not shield a run from its duration cap)",
				stopReasonOf(ap), StopReasonRunDurationExceeded)
		}
	})

	// The control that makes the assertion above mean something: with the same
	// pending wait but inside the cap, nothing kills the agent — proving the
	// suspension really is in force in this fixture, so the kill above came from
	// the cap and not from the watchdog leaking through.
	t.Run("inside the cap, the suspension still holds", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 1*time.Hour)

		s.RecordAgentInputWait("test", true)
		backdate(ap, 20*time.Minute, time.Minute)

		s.checkAgentHealth()

		if got := stopReasonOf(ap); got != "" {
			t.Errorf("StopReason = %q, want unset (an agent waiting on a human, inside its cap, is not hung)", got)
		}
	})

	// And the cap does not care how long the wait has been open — expired or
	// fresh, age is age. Here the wait is still well inside inputWaitMax.
	t.Run("the cap does not wait for the input-wait bound to expire", func(t *testing.T) {
		t.Setenv(envInputWaitMaxSeconds, "86400") // a full day of grace for the prompt
		s, ap := newRunCapFixture(t, 5*time.Hour)

		s.RecordAgentInputWait("test", true)
		backdate(ap, 20*time.Minute, 10*time.Minute)

		s.checkAgentHealth()

		if !runCapKilled(ap) {
			t.Errorf("StopReason = %q, want %q (the cap must not be gated on inputWaitMax)",
				stopReasonOf(ap), StopReasonRunDurationExceeded)
		}
	})
}

// ---------------------------------------------------------------------------
// configuration
// ---------------------------------------------------------------------------

func TestCheckWatchdog_NonPositiveCapDisablesIt(t *testing.T) {
	t.Run("daemon-wide 0", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "0")
		s, ap := newRunCapFixture(t, 100*time.Hour)

		s.checkAgentHealth()

		if got := stopReasonOf(ap); got != "" {
			t.Errorf("StopReason = %q, want unset (0 must mean 'no ceiling')", got)
		}
	})

	t.Run("daemon-wide negative", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "-1")
		s, ap := newRunCapFixture(t, 100*time.Hour)

		s.checkAgentHealth()

		if got := stopReasonOf(ap); got != "" {
			t.Errorf("StopReason = %q, want unset (a negative cap must disable, not wrap)", got)
		}
	})

	t.Run("per-role 0 opts one role out of the daemon default", func(t *testing.T) {
		s, ap := newRunCapFixture(t, 100*time.Hour)
		setRoleCap(ap, 0)

		s.checkAgentHealth()

		if got := stopReasonOf(ap); got != "" {
			t.Errorf("StopReason = %q, want unset (an explicit per-role 0 must opt out)", got)
		}
	})
}

func TestCheckWatchdog_DurationCapHonorsEnvOverride(t *testing.T) {
	t.Run("a narrower bound kills a run the default would allow", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "60")
		s, ap := newRunCapFixture(t, 10*time.Minute) // far inside the 4-hour default

		s.checkAgentHealth()

		if !runCapKilled(ap) {
			t.Errorf("StopReason = %q, want %q (env override must narrow the cap)",
				stopReasonOf(ap), StopReasonRunDurationExceeded)
		}
	})

	t.Run("a wider bound spares a run the default would kill", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "86400")
		s, ap := newRunCapFixture(t, 5*time.Hour) // past the 4-hour default

		s.checkAgentHealth()

		if got := stopReasonOf(ap); got != "" {
			t.Errorf("StopReason = %q, want unset (env override must widen the cap)", got)
		}
	})
}

func TestGetMaxRunDuration(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		if got := s.GetMaxRunDuration(); got != defaultMaxRunDurationSeconds {
			t.Errorf("GetMaxRunDuration() = %d, want %d", got, defaultMaxRunDurationSeconds)
		}
	})

	// The chosen default, asserted as a number so a silent edit has to be
	// deliberate: 4 hours cannot plausibly hit a healthy run, and the spend
	// ceiling bites long before it.
	t.Run("the default is four hours", func(t *testing.T) {
		if got := time.Duration(defaultMaxRunDurationSeconds) * time.Second; got != 4*time.Hour {
			t.Errorf("default cap = %v, want 4h", got)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "42")
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		if got := s.GetMaxRunDuration(); got != 42 {
			t.Errorf("GetMaxRunDuration() = %d, want 42 (env override)", got)
		}
	})

	t.Run("unparseable env falls back to the default", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "four hours")
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		if got := s.GetMaxRunDuration(); got != defaultMaxRunDurationSeconds {
			t.Errorf("GetMaxRunDuration() = %d, want %d (a bad env must not remove the ceiling)",
				got, defaultMaxRunDurationSeconds)
		}
	})
}

// The per-role knob wins over the daemon-wide value in BOTH directions — a role
// can tighten its own ceiling or raise it — and a role that says nothing
// inherits.
func TestMaxRunDurationFor_RoleOverridesTheDaemonDefault(t *testing.T) {
	t.Run("role tightens", func(t *testing.T) {
		s, ap := newRunCapFixture(t, time.Minute)
		setRoleCap(ap, 600)
		if got := s.maxRunDurationFor(ap); got != 10*time.Minute {
			t.Errorf("maxRunDurationFor() = %v, want 10m (role value)", got)
		}
	})

	t.Run("role raises, beating the env override", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "60")
		s, ap := newRunCapFixture(t, time.Minute)
		setRoleCap(ap, 36000)
		if got := s.maxRunDurationFor(ap); got != 10*time.Hour {
			t.Errorf("maxRunDurationFor() = %v, want 10h (deliberate per-agent config outranks a blanket override)", got)
		}
	})

	t.Run("nil inherits the daemon value", func(t *testing.T) {
		t.Setenv(envMaxRunDurationSeconds, "120")
		s, ap := newRunCapFixture(t, time.Minute)
		if got := s.maxRunDurationFor(ap); got != 2*time.Minute {
			t.Errorf("maxRunDurationFor() = %v, want 2m (a role naming nothing must inherit)", got)
		}
	})
}

// End to end through checkAgentHealth, not just the resolver: a role-scoped cap
// actually kills.
func TestCheckWatchdog_PerRoleCapKillsARunTheDefaultWouldAllow(t *testing.T) {
	s, ap := newRunCapFixture(t, 90*time.Minute) // well inside the 4-hour default
	setRoleCap(ap, 3600)                         // but past this role's 1 hour

	s.checkAgentHealth()

	if !runCapKilled(ap) {
		t.Errorf("StopReason = %q, want %q (a per-role cap must override the daemon default)",
			stopReasonOf(ap), StopReasonRunDurationExceeded)
	}
}

func TestMergeRoleConfig_MaxRunDuration(t *testing.T) {
	t.Run("overlay value propagates", func(t *testing.T) {
		cap := 7200
		got := MergeRoleConfig(cfgpkg.RoleConfig{}, cfgpkg.RoleConfig{MaxRunDuration: &cap})
		if got.MaxRunDuration == nil || *got.MaxRunDuration != cap {
			t.Errorf("MaxRunDuration = %v, want %d", got.MaxRunDuration, cap)
		}
	})

	t.Run("base survives an empty overlay", func(t *testing.T) {
		base := 300
		got := MergeRoleConfig(cfgpkg.RoleConfig{MaxRunDuration: &base}, cfgpkg.RoleConfig{})
		if got.MaxRunDuration == nil || *got.MaxRunDuration != base {
			t.Errorf("MaxRunDuration = %v, want %d", got.MaxRunDuration, base)
		}
	})

	// The reason the field is a pointer: an explicit opt-out must survive the
	// merge instead of looking like "unset, inherit".
	t.Run("an explicit zero overrides a non-zero base", func(t *testing.T) {
		base, off := 300, 0
		got := MergeRoleConfig(cfgpkg.RoleConfig{MaxRunDuration: &base}, cfgpkg.RoleConfig{MaxRunDuration: &off})
		if got.MaxRunDuration == nil || *got.MaxRunDuration != 0 {
			t.Errorf("MaxRunDuration = %v, want 0 (an explicit opt-out must not be read as unset)", got.MaxRunDuration)
		}
	})
}

// Disabling the silence check must not disable the duration cap. Before the cap
// existed, output_timeout: 0 left the daemon with no ceiling at all; the two are
// switched off independently now.
func TestCheckAgentHealth_DurationCapSurvivesADisabledOutputTimeout(t *testing.T) {
	s, ap := newRunCapFixture(t, 5*time.Hour)
	s.ConfigSnapshot().Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(0)

	s.checkAgentHealth()

	if !runCapKilled(ap) {
		t.Errorf("StopReason = %q, want %q (output_timeout: 0 opts out of the idle kill, not the duration cap)",
			stopReasonOf(ap), StopReasonRunDurationExceeded)
	}
}

// ---------------------------------------------------------------------------
// classification
// ---------------------------------------------------------------------------

// A capped run is a failure that costs the agent something. Not NoWork, which
// would retry it uncounted forever and make the cap decorative.
func TestClassifyAgentExit_RunDurationExceeded_IsNotNoWork(t *testing.T) {
	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon"},
		WorktreePath: t.TempDir(), // no lock file: no task was ever claimed
		StopReason:   StopReasonRunDurationExceeded,
	}

	s.classifyAgentExit(ap, 143)

	if ap.LastError == nil {
		t.Fatal("LastError = nil; a run killed for length is not a clean exit")
	}
	if ap.LastError.Class.Is(agenterr.NoWorkOutcome) {
		t.Error("class = NoWork; a four-hour run is not an idle agent with nothing to do")
	}
	if ap.LastNoWork {
		t.Error("LastNoWork = true, want false")
	}
	if !ap.LastError.Class.IsClass(wrapper.ErrTimeout) {
		t.Errorf("class = %s, want Timeout", ap.LastError.Class)
	}
}

// The trap the surrounding branch sets: a harness that handles SIGTERM cleanly
// exits 0 with its claim still held, which is the IncompleteRun shape. A run
// that was ended late must not be filed as one that ended early.
func TestClassifyAgentExit_RunDurationExceeded_IsNotIncompleteRun(t *testing.T) {
	ap := newTaskAgent(t, "falcon", "loom-77")
	ap.StopReason = StopReasonRunDurationExceeded
	mock := claimStateMock("in_progress", "falcon")
	s := newClaimSupervisor(mock)

	s.classifyAgentExit(ap, 0) // clean SIGTERM handling, claim never released

	if ap.LastError == nil {
		t.Fatal("LastError = nil; a capped run must not read as a clean success")
	}
	if ap.LastError.Class.Is(agenterr.IncompleteRunOutcome) {
		t.Error("class = IncompleteRun; the turn did not end early, it was ended late")
	}
	if !ap.LastError.Class.IsClass(wrapper.ErrTimeout) {
		t.Errorf("class = %s, want Timeout", ap.LastError.Class)
	}
	// The stop reason settles it outright, so the claim GET is never worth
	// paying for.
	if n := getCallCount(mock); n != 0 {
		t.Errorf("issue GET count = %d, want 0 (the stop reason is decisive)", n)
	}
}

// "Consumes budget", spelled out: the policy verdict is a COUNTED retry, and the
// restart path actually charges it.
func TestClassifyAgentExit_RunDurationExceeded_ConsumesRestartBudget(t *testing.T) {
	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon"},
		WorktreePath: t.TempDir(),
		StopReason:   StopReasonRunDurationExceeded,
	}

	s.classifyAgentExit(ap, 143)

	d := agentpolicy.Decide(ap.LastError.Class)
	if d.Decision != agentpolicy.Retry {
		t.Errorf("decision = %s, want Retry (counted): an uncounted verdict lets a capped run recur forever", d.Decision)
	}
	if !agentpolicy.QuarantineEligible(ap.LastError.Class) {
		t.Error("QuarantineEligible = false; repeatedly blowing the ceiling on one task is the no-progress signal quarantine exists for")
	}

	ap.LastExitCode = 143
	ap.RestartCount = 1
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true: a capped run is retryable")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (charged, not reset)", ap.RestartCount)
	}
}

// The exit-0 trap, spelled out separately because it is the one that bites:
// shouldRestart's clean-success arm keys on (exit 0 && LastError == nil) and
// zeroes every counter. A harness that handles SIGTERM gracefully exits 0, so
// without the classification above a capped run would reset the very budget it
// was supposed to consume — and the cap would fire forever, free of charge.
func TestShouldRestart_RunDurationExceededAtExitZero_DoesNotResetBudgets(t *testing.T) {
	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon"},
		WorktreePath: t.TempDir(),
		StopReason:   StopReasonRunDurationExceeded,
	}

	s.classifyAgentExit(ap, 0)

	ap.LastExitCode = 0
	ap.RestartCount = 1
	ap.BlockCount = 2
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true: a capped run is retryable")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (a clean SIGTERM exit must not buy a free reset)", ap.RestartCount)
	}
	if ap.BlockCount != 2 {
		t.Errorf("BlockCount = %d, want 2 (preserved)", ap.BlockCount)
	}
}

// A watchdog stop with no task keeps its NoWork reading — the two reasons are
// classified apart, which is why the duration kill needed its own.
func TestClassifyAgentExit_WatchdogWithNoTask_StillNoWork(t *testing.T) {
	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon"},
		WorktreePath: t.TempDir(),
		StopReason:   StopReasonWatchdog,
	}

	s.classifyAgentExit(ap, 143)

	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.NoWorkOutcome) {
		t.Fatalf("class = %v, want NoWork (the silence path must be untouched)", ap.LastError)
	}
}
