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
