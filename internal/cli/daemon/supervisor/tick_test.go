package supervisor

import (
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func newAgent(worktree string) *AgentProcess {
	return &AgentProcess{
		Entry:  cfgpkg.AgentEntry{Worktree: worktree, Role: "task"},
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
}

// TestDeregisterIsIdentitySafe is the race PR #113's bare Ticks.Delete(name)
// left open: a departing goroutine must not delete a same-named successor's
// slot. After agent X is re-registered (re-added on the same worktree), the
// FIRST instance's deregister must be a no-op, leaving the successor watched.
func TestDeregisterIsIdentitySafe(t *testing.T) {
	s := newHarnessSupervisor()

	first := newAgent("wt-reused")
	s.registerAgentTick(first)
	firstSlot := first.tick

	// Successor re-registers under the same name (overwrites the map entry).
	second := newAgent("wt-reused")
	s.registerAgentTick(second)

	if first.tick != firstSlot {
		t.Fatal("precondition: first agent's slot pointer changed unexpectedly")
	}
	if second.tick == firstSlot {
		t.Fatal("precondition: successor reused the same slot pointer")
	}

	// The outgoing first instance deregisters — must NOT remove the successor.
	s.deregister(first.tick)

	if _, ok := s.LoadTick(agentTickName(second)); !ok {
		t.Error("identity-unsafe: first instance's deregister deleted the successor's live slot")
	}

	// And the successor's own deregister cleans up.
	s.deregister(second.tick)
	if _, ok := s.LoadTick(agentTickName(second)); ok {
		t.Error("successor slot still present after its own deregister")
	}
}

// TestRecordIsIdentitySafe guards the record-by-name aliasing bug: an outgoing
// goroutine recording its tick must refresh its OWN (stale, soon-deregistered)
// slot, never the successor's. ap.recordTick records by slot identity.
func TestRecordIsIdentitySafe(t *testing.T) {
	s := newHarnessSupervisor()

	first := newAgent("wt-alias")
	s.registerAgentTick(first)

	second := newAgent("wt-alias")
	s.registerAgentTick(second)

	// Freeze the successor's slot in the past, then let the OLD instance record.
	setTickForTest(s, agentTickName(second), time.Now().Add(-1*time.Hour))
	secondBefore, _ := s.LoadTick(agentTickName(second))

	first.recordTick() // must touch first's slot, not the map entry (second's)

	secondAfter, _ := s.LoadTick(agentTickName(second))
	if !secondAfter.Equal(secondBefore) {
		t.Error("record-by-identity violated: outgoing instance refreshed the successor's slot")
	}
}

// TestSupervisedAgentBodyDeregistersOnExit is the lifecycle fix (the part PR
// #113 also covered), end to end through the real defer chain: once the
// supervise goroutine exits, its tick is gone and a far-future scan is quiet.
func TestSupervisedAgentBodyDeregistersOnExit(t *testing.T) {
	s := newHarnessSupervisor()
	ap := newAgent("wt-exit")
	name := agentTickName(ap)

	s.registerAgentTick(ap)
	s.Wg.Add(1)
	close(s.Shutdown) // superviseAgent returns on its first stop-signal check
	go s.supervisedAgentBody(name, ap)

	select {
	case <-ap.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervise goroutine did not exit within 2s")
	}

	if _, ok := s.LoadTick(name); ok {
		t.Fatalf("agent tick %q still registered after supervise goroutine exit", name)
	}

	s.scanTicks(time.Now().Add(12 * time.Minute))
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("watchdog fataled on a departed agent: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestQuarantineDoesNotBlockOnAgentMutex documents the lock-free contract: the
// watchdog's quarantine path must complete even while ap.Mu is held (the wedged
// goroutine may own it). If quarantine took ap.Mu this would deadlock the test.
func TestQuarantineDoesNotBlockOnAgentMutex(t *testing.T) {
	s := newHarnessSupervisor()
	ap := newAgent("wt-locked")
	s.registerAgentTick(ap)

	ap.Mu.Lock() // simulate the wedged goroutine holding its own mutex
	defer ap.Mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.quarantineAgent(ap, ap.tick)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("quarantineAgent blocked on ap.Mu — watchdog is not lock-free")
	}

	select {
	case <-ap.StopCh:
	default:
		t.Error("quarantine did not signal the agent to stop")
	}
}
