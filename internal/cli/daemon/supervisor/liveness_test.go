package supervisor

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestScanTicksFlagsStaleCadenceGoroutine(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-10*time.Minute))

	s.scanTicks(time.Now())

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

	s.scanTicks(time.Now())

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
	s.scanTicks(time.Now())
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

func setTickForTest(s *Supervisor, name string, when time.Time) {
	v, ok := s.Ticks.Load(name)
	if !ok {
		panic("tick not registered: " + name)
	}
	tick, ok := v.(*atomic.Int64)
	if !ok {
		panic("tick is not *atomic.Int64")
	}
	// Mirror RecordTick's storage: a monotonic duration from monoBase. `when`
	// carries a monotonic reading (time.Now()-derived), so when.Sub(monoBase)
	// stays on the monotonic clock and the reconstructed age matches `when`.
	tick.Store(int64(when.Sub(monoBase)))
}
