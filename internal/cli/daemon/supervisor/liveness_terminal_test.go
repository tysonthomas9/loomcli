package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// newTerminalStopAgent builds an AgentProcess with a registered supervise tick,
// backdated well past any staleness threshold, and the given stop reason.
func newTerminalStopAgent(s *Supervisor, worktree string, reason StopReason) *AgentProcess {
	ap := &AgentProcess{
		Entry:  cfgpkg.AgentEntry{Worktree: worktree},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	ap.StopReason = reason
	s.RegisterTick(agentTickName(ap))
	setTickForTest(s, agentTickName(ap), time.Now().Add(-30*time.Minute))
	return ap
}

func newLivenessHarness() *Supervisor {
	s := newHarnessSupervisor()
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} }
	return s
}

// TestRetiredAgentTickNeverTripsWatchdog is the regression test for the
// measured 2026-08-26 outage: an agent that fatal-stopped on a genuine auth
// error stopped ticking BECAUSE it had correctly stopped, and ~12 minutes later
// the liveness watchdog read that as a wedged goroutine and killed the whole
// daemon, SIGTERMing every healthy sibling's in-flight run.
func TestRetiredAgentTickNeverTripsWatchdog(t *testing.T) {
	for _, reason := range []StopReason{
		StopReasonFatalError,
		StopReasonFastFail,
		StopReasonConfigRemoved,
		StopReasonShutdown,
		StopReasonEphemeralDone,
		StopReasonMaxRetries,
	} {
		t.Run(string(reason), func(t *testing.T) {
			s := newLivenessHarness()
			ap := newTerminalStopAgent(s, "tester", reason)

			s.retireAgentSupervision(ap, agentTickName(ap))
			scanRepeatedN(s, livenessStaleScansBeforeFatal+2)

			select {
			case err := <-s.FatalChannel():
				t.Fatalf("watchdog fataled on an agent stopped for %q: %v", reason, err)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

// TestHungAgentStillTripsWatchdog is the other half of the contract: the fix
// must exempt agents that STOPPED, never agents that should be working. An
// agent whose supervise goroutine is wedged has never returned, so it is never
// retired, and the daemon-level restart that recovers it must still happen.
func TestHungAgentStillTripsWatchdog(t *testing.T) {
	s := newLivenessHarness()
	ap := newTerminalStopAgent(s, "worker", "") // still running: no stop reason

	scanRepeatedN(s, livenessStaleScansBeforeFatal)

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), agentTickName(ap)) {
			t.Errorf("fatal did not name the hung agent: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("watchdog did not fatal for a genuinely hung agent")
	}
}

// TestRetiredTickDoesNotMaskASiblingHang guards the exemption's blast radius:
// retiring one agent must not quiet the scan for anyone else.
func TestRetiredTickDoesNotMaskASiblingHang(t *testing.T) {
	s := newLivenessHarness()
	stopped := newTerminalStopAgent(s, "tester", StopReasonFatalError)
	hung := newTerminalStopAgent(s, "worker", "")

	s.retireAgentSupervision(stopped, agentTickName(stopped))
	scanRepeatedN(s, livenessStaleScansBeforeFatal)

	select {
	case err := <-s.FatalChannel():
		if strings.Contains(err.Error(), agentTickName(stopped)) {
			t.Errorf("fatal named the retired agent: %v", err)
		}
		if !strings.Contains(err.Error(), agentTickName(hung)) {
			t.Errorf("fatal did not name the hung sibling: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("watchdog did not fatal for the hung sibling")
	}
}

// TestRegisterTickUnretiresAName covers the drain-then-re-add path: the new
// goroutine runs under the same tick name and must be watched again.
func TestRegisterTickUnretiresAName(t *testing.T) {
	s := newLivenessHarness()
	ap := newTerminalStopAgent(s, "worker", StopReasonConfigRemoved)
	name := agentTickName(ap)

	s.retireAgentSupervision(ap, name)
	if !s.tickRetired(name) {
		t.Fatal("tick not retired after supervision ended")
	}

	s.RegisterTick(name)
	if s.tickRetired(name) {
		t.Fatal("re-registering a tick left it retired")
	}

	setTickForTest(s, name, time.Now().Add(-30*time.Minute))
	scanRepeatedN(s, livenessStaleScansBeforeFatal)

	select {
	case <-s.FatalChannel():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("a re-registered tick was not watched again")
	}
}

// TestSupervisorInitiatedKillIsNotLogClassified covers the cascade's second
// half: a healthy agent SIGTERMed by the daemon's own shutdown must not be
// filed from whatever its log tail happened to say — the class the policy
// treats as StopFatal and human-actionable.
func TestSupervisorInitiatedKillIsNotLogClassified(t *testing.T) {
	cases := []struct {
		name    string
		reason  StopReason
		arrange func(*Supervisor, *AgentProcess)
	}{
		{"explicit shutdown reason", StopReasonShutdown, func(*Supervisor, *AgentProcess) {}},
		{"operator stop", StopReasonManualStop, func(*Supervisor, *AgentProcess) {}},
		{"config removed", StopReasonConfigRemoved, func(*Supervisor, *AgentProcess) {}},
		{
			// The reason is recorded on the way OUT of the supervise loop,
			// which is after classification runs, so the drain that daemon
			// shutdown performs reaches the classifier with no reason set.
			"shutdown signaled, reason not yet recorded", "",
			func(s *Supervisor, _ *AgentProcess) { close(s.Shutdown) },
		},
		{
			"per-agent stop signaled, reason not yet recorded", "",
			func(_ *Supervisor, ap *AgentProcess) { close(ap.StopCh) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newLivenessHarness()
			ap := &AgentProcess{
				Entry:  cfgpkg.AgentEntry{Worktree: "worker"},
				StopCh: make(chan struct{}),
			}
			ap.StopReason = tc.reason
			tc.arrange(s, ap)

			if !s.supervisorEndedRun(ap, tc.reason) {
				t.Fatal("supervisorEndedRun did not recognize a supervisor-initiated stop")
			}

			s.markSupervisorStop(ap, 143, "claude", tc.reason)
			if got := ap.LastError.Class; !got.Is(agenterr.SupervisorStopOutcome) {
				t.Errorf("class = %v, want SupervisorStop", got)
			}
			if ap.LastNoWork {
				t.Error("a supervisor-initiated stop was recorded as no-work")
			}
		})
	}
}

// TestAgentFailureIsStillLogClassified is the guard on the arm above: only OUR
// kills bypass log classification. A silence-watchdog kill is a verdict about
// the agent, and an ordinary crash has nothing supervisor-initiated about it.
func TestAgentFailureIsStillLogClassified(t *testing.T) {
	for _, reason := range []StopReason{"", StopReasonWatchdog, StopReasonRateLimited} {
		s := newLivenessHarness()
		ap := &AgentProcess{
			Entry:  cfgpkg.AgentEntry{Worktree: "worker"},
			StopCh: make(chan struct{}),
		}
		if s.supervisorEndedRun(ap, reason) {
			t.Errorf("stop reason %q was wrongly read as supervisor-initiated", reason)
		}
	}
}

// TestSupervisedAgentBodyRetiresTickOnReturn is the wiring test: the exemption
// only helps if the supervise goroutine actually claims it on its way out. The
// agent here is stopped before it can spawn anything, so superviseAgent returns
// on its first pass — the same shape as the fatal stop that caused the outage.
//
// It also pins the defer ORDER. DrainAgent waits on ap.Done and a re-add
// re-registers the same tick name, so a retirement that landed after the close
// could retire the replacement goroutine's tick instead.
func TestSupervisedAgentBodyRetiresTickOnReturn(t *testing.T) {
	s := newLivenessHarness()
	ap := newTerminalStopAgent(s, "tester", "")
	name := agentTickName(ap)
	close(ap.StopCh) // superviseAgent returns on its first stop-signal check

	s.Wg.Add(1)
	go s.supervisedAgentBody(name, ap)

	select {
	case <-ap.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervise goroutine did not exit")
	}
	if !s.tickRetired(name) {
		t.Fatal("tick was not retired by the time ap.Done closed")
	}
	s.Wg.Wait()

	setTickForTest(s, name, time.Now().Add(-30*time.Minute))
	scanRepeatedN(s, livenessStaleScansBeforeFatal+2)
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("watchdog fataled on an exited supervise goroutine: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}
