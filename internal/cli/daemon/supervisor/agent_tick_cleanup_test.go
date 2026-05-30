package supervisor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// storeAgedTick plants a tick slot whose last stamp is `age` in the past,
// simulating a frozen goroutine the watchdog would flag.
func storeAgedTick(s *Supervisor, name string, age time.Duration) {
	tick := new(atomic.Int64)
	tick.Store(time.Now().Add(-age).UnixNano())
	s.Ticks.Store(name, tick)
}

func fatalPending(s *Supervisor) bool {
	select {
	case <-s.FatalChannel():
		return true
	default:
		return false
	}
}

func TestDeleteTick_RemovesSlotAndIsIdempotent(t *testing.T) {
	s := newHarnessSupervisor()

	s.RegisterTick("loop_x")
	if _, ok := s.LoadTick("loop_x"); !ok {
		t.Fatal("LoadTick !ok right after RegisterTick")
	}

	s.DeleteTick("loop_x")
	if _, ok := s.LoadTick("loop_x"); ok {
		t.Error("LoadTick still ok after DeleteTick")
	}

	var visited int
	s.RangeTicks(func(string, time.Time) { visited++ })
	if visited != 0 {
		t.Errorf("RangeTicks visited %d slots after delete, want 0", visited)
	}

	s.DeleteTick("loop_x")        // already gone
	s.DeleteTick("never_existed") // never registered
}

// TestStaleAgentTickWithoutDeleteFatals reproduces the crash: a per-agent
// supervise goroutine that exited but left its tick behind freezes, and the
// watchdog FATAL-kills the whole daemon once the tick ages past the agent
// threshold (~11m30s with defaults).
func TestStaleAgentTickWithoutDeleteFatals(t *testing.T) {
	s := newHarnessSupervisor()
	storeAgedTick(s, GoroutineAgentPrefix+"wt-abandoned", 12*time.Minute)

	s.scanTicks(time.Now())

	if !fatalPending(s) {
		t.Fatal("expected SignalFatal for a frozen agent tick, got none")
	}
}

// TestSupervisedAgentBodyDeletesTickOnExit is the fix, end to end: once the
// supervise goroutine exits (here via shutdown on its first iteration), its
// tick is deregistered, so a later watchdog scan — even 12m on — finds nothing
// stale and never signals fatal.
func TestSupervisedAgentBodyDeletesTickOnExit(t *testing.T) {
	s := newHarnessSupervisor()
	ap := &AgentProcess{
		Entry:  config.AgentEntry{Worktree: "wt-e2e"},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	name := agentTickName(ap)

	s.RegisterTick(name)
	s.Wg.Add(1)
	// Closed before launch so superviseAgent returns on its first stop-signal
	// check, exercising the real exit path through supervisedAgentBody's defers.
	close(s.Shutdown)
	go s.supervisedAgentBody(name, ap)

	select {
	case <-ap.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervise goroutine did not exit within 2s")
	}

	if _, ok := s.LoadTick(name); ok {
		t.Fatalf("agent tick %q still registered after supervise goroutine exit", name)
	}

	// The frozen-tick hazard is gone: scanning far in the future is quiet.
	s.scanTicks(time.Now().Add(12 * time.Minute))
	if fatalPending(s) {
		t.Fatal("watchdog signaled fatal for a departed agent — tick not deregistered")
	}

	done := make(chan struct{})
	go func() { s.Wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wg did not drain after supervise goroutine exit")
	}
}

// TestWatchdogStillFatalsOnStaleCriticalTick guards the watchdog's real job:
// deregistering agent ticks must not weaken detection of a genuinely wedged
// core daemon goroutine.
func TestWatchdogStillFatalsOnStaleCriticalTick(t *testing.T) {
	s := newHarnessSupervisor()
	storeAgedTick(s, GoroutineStateUpdater, 2*time.Minute) // threshold 60s

	s.scanTicks(time.Now())

	if !fatalPending(s) {
		t.Fatal("expected SignalFatal for a wedged core goroutine, got none")
	}
}
