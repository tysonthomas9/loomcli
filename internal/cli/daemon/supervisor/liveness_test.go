package supervisor

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// scanRepeated runs scanTicks enough times to cross the consecutive-stale
// threshold, so a tick that stays stale every scan triggers the watchdog's
// fatal. The calls are back-to-back (gap well under livenessFreezeGap and with
// both clock domains in step), so the process-suspension guard never fires.
func scanRepeated(s *Supervisor) {
	scanRepeatedN(s, livenessStaleScansBeforeFatal)
}

// scanRepeatedN runs n back-to-back scans, back-dating each stale streak's
// start between them.
//
// The back-dating is what makes back-to-back scans represent production: the
// watchdog also requires a streak to span livenessMinStaleSpan of REAL runtime
// before it may go fatal, and n scans in a test loop span microseconds. Without
// this the span guard would silently suppress every fatal these tests exist to
// assert, and the whole suite would pass while detection was disabled.
func scanRepeatedN(s *Supervisor, n int) {
	for i := 0; i < n; i++ {
		s.scanTicks(time.Now())
		backdateStreakStarts(s, livenessMinStaleSpan)
	}
}

// backdateStreakStarts shifts every recorded stale-streak start back by d,
// standing in for the real elapsed time production's 10s scan cadence provides.
func backdateStreakStarts(s *Supervisor, d time.Duration) {
	for name, start := range s.livenessStreakStart {
		s.livenessStreakStart[name] = start.Add(-d)
	}
}

func TestScanTicksFlagsStaleCadenceGoroutine(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))

	scanRepeated(s)

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), GoroutineHealthChecker) {
			t.Errorf("error missing stale goroutine name: %v", err)
		}
		if !strings.Contains(err.Error(), "age=") {
			t.Errorf("error missing age detail: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("scanTicks did not signal fatal for stale tick")
	}
}

func TestScanTicksIgnoresFreshTicks(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	s.RegisterTick(GoroutineConfigReconciler)

	s.scanTicks(time.Now())

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks signaled fatal on fresh ticks: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestScanTicksRespectsAgentThreshold(t *testing.T) {
	s := newHarnessSupervisor()
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{}
	}
	name := GoroutineAgentPrefix + "spawning_agent"
	s.RegisterTick(name)
	setTickForTest(s, name, time.Now().Add(-90*time.Second))

	s.scanTicks(time.Now())

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks falsely flagged agent within spawn budget: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestScanTicksFlagsAgentBeyondThreshold(t *testing.T) {
	s := newHarnessSupervisor()
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{}
	}
	name := GoroutineAgentPrefix + "stuck_agent"
	s.RegisterTick(name)
	setTickForTest(s, name, time.Now().Add(-1*time.Hour))

	scanRepeated(s)

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), "stuck_agent") {
			t.Errorf("error missing stuck agent name: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("scanTicks did not flag agent past threshold")
	}
}

func TestLivenessTimeoutOverridesDefaults(t *testing.T) {
	s := newHarnessSupervisor()
	s.LivenessTimeout = 5 * time.Minute
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-2*time.Minute))

	s.scanTicks(time.Now())

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks flagged tick under override timeout: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLivenessTimeoutHonoredAtFloor(t *testing.T) {
	s := newHarnessSupervisor()
	s.LivenessTimeout = 10 * time.Second
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-30*time.Second))

	s.scanTicks(time.Now())
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("floor not honored — flagged 30s tick: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-90*time.Second))
	scanRepeated(s)
	select {
	case <-s.FatalChannel():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("scanTicks did not flag 90s tick under floor=60s")
	}
}

// TestAgentWaitHeartbeatKeepsTickFresh is the regression guard for the
// false-positive daemon crash: a healthy agent that runs longer than the agent
// threshold must not be flagged, because waitForAgent's heartbeat keeps the
// supervise tick fresh while cmd.Wait() blocks.
func TestAgentWaitHeartbeatKeepsTickFresh(t *testing.T) {
	s := newHarnessSupervisor()
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "hb_agent", Role: "task"}}
	name := agentTickName(ap)
	s.RegisterTick(name)

	// Simulate a healthy long-running agent: the supervise loop recorded its
	// tick once at the top, then blocked in cmd.Wait() for an hour. Without the
	// heartbeat this stale tick would trip the watchdog and crash the daemon.
	setTickForTest(s, name, time.Now().Add(-1*time.Hour))

	stop := s.startAgentWaitHeartbeatEvery(ap, 2*time.Millisecond)

	fresh := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if last, ok := s.LoadTick(name); ok && time.Since(last) < agentThreshold(s) {
			fresh = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !fresh {
		stop()
		t.Fatal("heartbeat did not refresh the agent tick while waiting")
	}

	// A heartbeating agent must not be flagged as a wedged supervise goroutine.
	s.scanTicks(time.Now())
	select {
	case err := <-s.FatalChannel():
		stop()
		t.Fatalf("scanTicks flagged a heartbeating agent: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// stop() must terminate the heartbeat goroutine; no ticks may follow.
	stop()
	last, _ := s.LoadTick(name)
	time.Sleep(20 * time.Millisecond)
	again, _ := s.LoadTick(name)
	if !again.Equal(last) {
		t.Fatalf("heartbeat kept ticking after stop: %v -> %v", last, again)
	}
}

// TestScanTicksToleratesTransientStall is the regression guard for the
// daemon-self-kill fix: a tick that is stale for fewer than
// livenessStaleScansBeforeFatal scans in a row, then recovers, must not crash
// the daemon. A single slow control-plane cycle or brief lock contention is a
// transient stall, not a wedged goroutine.
func TestScanTicksToleratesTransientStall(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)

	// Stale for (threshold-1) consecutive scans — one short of fatal — with the
	// real-time span requirement already satisfied, so the scan COUNT is the
	// only thing holding the fatal back.
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))
	scanRepeatedN(s, livenessStaleScansBeforeFatal-1)
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks killed daemon before sustained staleness: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// The goroutine recovers (fresh tick); the streak must reset so a later
	// brief stall does not inherit the earlier count.
	setTickForTest(s, GoroutineHealthChecker, time.Now())
	s.scanTicks(time.Now())
	if n := s.livenessStreak[GoroutineHealthChecker]; n != 0 {
		t.Fatalf("streak not reset after recovery: got %d", n)
	}

	// One more stale scan after recovery must not be enough to fatal.
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))
	s.scanTicks(time.Now())
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks fatal after streak should have reset: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestScanTicksSkipsFatalOnProcessSuspension guards the sleep/swap/SIGSTOP case:
// when the watchdog itself did not run for far longer than its interval, every
// tick looks ancient through no fault of its goroutine, so the scan must skip
// the fatal and clear streaks rather than crash the daemon on resume.
func TestScanTicksSkipsFatalOnProcessSuspension(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))

	// Prime a stale streak that is one short of fatal...
	scanRepeatedN(s, livenessStaleScansBeforeFatal-1)
	// ...then simulate the process being suspended between scans. The value
	// carries a monotonic reading, so this is the "monotonic clock kept running"
	// freeze (SIGSTOP/swap thrash), not the sleep case.
	s.lastLivenessScan = time.Now().Add(-2 * livenessFreezeGap)
	s.scanTicks(time.Now())

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks crashed daemon on process-suspension gap: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if len(s.livenessStreak) != 0 {
		t.Fatalf("streaks not cleared after suspension: %v", s.livenessStreak)
	}
	// Clearing streaks is not enough on its own — the tick must also have been
	// re-primed, or the next scans re-flag it immediately.
	last, ok := s.LoadTick(GoroutineHealthChecker)
	if !ok {
		t.Fatal("health checker tick disappeared")
	}
	if age := time.Since(last); age > time.Second {
		t.Fatalf("tick not re-primed after suspension: age %s", age)
	}
}

func setTickForTest(s *Supervisor, name string, when time.Time) {
	v, ok := s.Ticks.Load(name)
	if !ok {
		panic("tick not registered: " + name)
	}
	tick, ok := v.(*atomic.Int64)
	if !ok {
		panic("tick is not *atomic.Int64")
	}
	tick.Store(when.UnixNano())
}

// TestUnregisterTickStopsWatchdogScanningIt is the unit-level guard for the
// leak: once a slot is released the watchdog must not see it at all, however
// old its last stamp was.
func TestUnregisterTickStopsWatchdogScanningIt(t *testing.T) {
	s := newHarnessSupervisor()
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{}
	}
	name := GoroutineAgentPrefix + "stopped_agent"
	s.RegisterTick(name)
	setTickForTest(s, name, time.Now().Add(-1*time.Hour))

	s.UnregisterTick(name)

	if _, ok := s.LoadTick(name); ok {
		t.Fatal("tick slot survived UnregisterTick")
	}

	scanRepeated(s)

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("watchdog fataled on an unregistered tick: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestTerminallyStoppedAgentDoesNotFatalDaemon reproduces the production crash
// loop: an agent whose supervise loop returns terminally (an AuthFailure fatal
// stop) left its tick slot registered and frozen, so the watchdog fataled the
// whole daemon one threshold later — and again after every pm2 restart, because
// the stopped agent stopped ticking again immediately.
func TestTerminallyStoppedAgentDoesNotFatalDaemon(t *testing.T) {
	s := newHarnessSupervisor()
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{}
	}

	healthy := GoroutineAgentPrefix + "healthy_agent"
	s.RegisterTick(healthy)

	ap := &AgentProcess{
		Entry:  cfgpkg.AgentEntry{Worktree: "auth_stopped_agent"},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	stopped := agentTickName(ap)
	s.RegisterTick(stopped)

	// Closing StopCh drives superviseAgent out through checkAgentStopSignals —
	// the same terminal `return` an auth fatal stop takes, exercising the real
	// deferred release rather than calling UnregisterTick directly.
	close(ap.StopCh)
	s.Wg.Add(1)
	s.supervisedAgentBody(stopped, ap)

	if _, ok := s.LoadTick(stopped); ok {
		// Reproduce what production does next: nothing stamps the leaked slot,
		// so it ages past the threshold while the rest of the fleet is healthy.
		setTickForTest(s, stopped, time.Now().Add(-1*time.Hour))
	}

	setTickForTest(s, healthy, time.Now())
	scanRepeated(s)

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("a terminally stopped agent fataled the daemon: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if _, ok := s.LoadTick(stopped); ok {
		t.Fatal("stopped agent left its tick slot registered")
	}
}
