package supervisor

import (
	"os/exec"
	"strings"
	"syscall"
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

// TestScanTicksQuarantinesStaleAgentInsteadOfFatal is the architectural core of
// this alternative: a wedged per-agent supervise goroutine must be CONTAINED,
// not escalated to a daemon-fatal. After the scan the agent's tick is gone
// (quarantined, so it isn't re-flagged) and its StopCh is closed, while the
// FatalChannel stays empty — the fleet keeps running.
func TestScanTicksQuarantinesStaleAgentInsteadOfFatal(t *testing.T) {
	s := newHarnessSupervisor()
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{}
	}
	ap := &AgentProcess{
		Entry:  cfgpkg.AgentEntry{Worktree: "stuck_agent", Role: "task"},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	name := agentTickName(ap)
	s.registerAgentTick(ap)
	setTickForTest(s, name, time.Now().Add(-1*time.Hour))

	s.scanTicks(time.Now())

	// No fatal — one wedged agent must never crash the daemon.
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("stale agent tick escalated to fatal (should quarantine): %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// Quarantined: tick deregistered so the next scan does not re-flag it.
	if _, ok := s.LoadTick(name); ok {
		t.Error("stale agent tick still registered after quarantine")
	}

	// That single agent was signaled to stop.
	select {
	case <-ap.StopCh:
	default:
		t.Error("quarantined agent's StopCh was not closed")
	}

	ap.Mu.Lock()
	stopReason := ap.StopReason
	ap.Mu.Unlock()
	if stopReason != StopReasonWatchdog {
		t.Errorf("quarantined agent stop reason = %q, want %q", stopReason, StopReasonWatchdog)
	}
}

// TestScanTicksStopsStaleAgentProcess proves the quarantine path is not just
// metadata: when a stale agent has a real subprocess, the watchdog starts an
// asynchronous StopAgent attempt that terminates it while the daemon stays up.
func TestScanTicksStopsStaleAgentProcess(t *testing.T) {
	s := newHarnessSupervisor()
	sigtermTimeout := 1
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{
			Daemon: cfgpkg.DaemonSettings{
				RestartPolicy: cfgpkg.RestartPolicy{SigtermTimeout: &sigtermTimeout},
			},
		}
	}

	cmd := exec.Command("sleep", "60") //nolint:norawexec,gosec // test subprocess
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	waitDone := make(chan struct{})
	ap := &AgentProcess{
		Entry:  cfgpkg.AgentEntry{Worktree: "stuck_process_agent", Role: "task"},
		Cmd:    cmd,
		Pid:    cmd.Process.Pid,
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-waitDone
	})
	go func() {
		_ = cmd.Wait()
		ap.Mu.Lock()
		ap.Cmd = nil
		ap.Pid = 0
		ap.Mu.Unlock()
		close(waitDone)
	}()

	name := agentTickName(ap)
	s.registerAgentTick(ap)
	setTickForTest(s, name, time.Now().Add(-1*time.Hour))

	s.scanTicks(time.Now())

	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("quarantine did not stop the stale agent subprocess")
	}

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("stale agent process escalated to fatal (should quarantine): %v", err)
	default:
	}
}

// TestScanTicksFlagsStaleCoreGoroutine is the regression guard: the watchdog's
// real job is preserved — a wedged daemon-lifetime singleton still FATALs.
func TestScanTicksFlagsStaleCoreGoroutine(t *testing.T) {
	s := newHarnessSupervisor()
	s.RegisterTick(GoroutineStateUpdater)
	setTickForTest(s, GoroutineStateUpdater, time.Now().Add(-1*time.Hour))

	s.scanTicks(time.Now())

	select {
	case err := <-s.FatalChannel():
		if !strings.Contains(err.Error(), GoroutineStateUpdater) {
			t.Errorf("error missing stale core goroutine name: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("scanTicks did not fatal on a wedged core goroutine")
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
	s.registerAgentTick(ap)

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
	sl, ok := v.(*tickSlot)
	if !ok {
		panic("tick is not *tickSlot")
	}
	// Mirror record()'s storage: a monotonic duration from monoBase. Callers must
	// pass a time.Now()-derived `when` (all current callers do), so it carries a
	// monotonic reading and when.Sub(monoBase) stays on the monotonic clock and
	// round-trips to the intended age.
	sl.stamp.Store(int64(when.Sub(monoBase)))
}
