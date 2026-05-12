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
	// Force the tick into the past so scanTicks treats it as stale.
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
	// All ticks recent — no fatal.

	s.scanTicks(time.Now())

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks signaled fatal on fresh ticks: %v", err)
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

func TestScanTicksRespectsAgentThreshold(t *testing.T) {
	// Agent threshold is 2*(no_work_backoff + spawn_budget) + slack — much
	// larger than minLivenessThreshold. A tick 90s old must not be flagged.
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
		// expected
	}
}

func TestScanTicksFlagsAgentBeyondThreshold(t *testing.T) {
	s := newHarnessSupervisor()
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{}
	}
	name := GoroutineAgentPrefix + "stuck_agent"
	s.RegisterTick(name)
	// Agent threshold = 2*(30s + 5min) + 30s = 11min30s. 1 hour > threshold.
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
	// Caller-imposed 5min threshold for everything. A 2min-old tick is still
	// fresh under this override even though it would be stale under the 2min
	// default for health_checker.
	s.LivenessTimeout = 5 * time.Minute
	s.RegisterTick(GoroutineHealthChecker)
	setTickForTest(s, GoroutineHealthChecker, time.Now().Add(-2*time.Minute))

	s.scanTicks(time.Now())

	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks flagged tick under override timeout: %v", err)
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

func TestLivenessTimeoutHonoredAtFloor(t *testing.T) {
	s := newHarnessSupervisor()
	// Setting LivenessTimeout below the floor (60s) must be clamped to 60s,
	// so a 30s-old tick stays fresh, but a 90s-old one is stale.
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
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("scanTicks did not flag 90s tick under floor=60s")
	}
}

// setTickForTest overrides a registered tick to a specific time. Tests use it
// to simulate staleness without sleeping.
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
