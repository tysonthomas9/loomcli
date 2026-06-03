package supervisor

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newParkTestSupervisor builds a supervisor wired for driving the real
// sleepBeforeRestart park path: a config snapshot (computeBackoff), an event
// sink, shutdown/fatal channels, and a large park interval so the wait blocks
// until interrupted. The interval is >= agentWaitHeartbeatInterval so the wait
// also exercises the heartbeat-start path.
func newParkTestSupervisor() *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		Shutdown:       make(chan struct{}),
		FatalCh:        make(chan error, 1),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
		// Long enough to block the select and to clear the heartbeat gate.
		maxRetriesParkInterval: 30 * time.Second,
	}
}

func newParkedAgent() *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "park_agent", Role: "task"},
		StopCh:     make(chan struct{}),
		StopReason: StopReasonMaxRetriesParked,
		LastError:  &agenterr.AgentError{Class: agenterr.Transient},
	}
}

// TestSleepBeforeRestart_ParkedInterruptibleByStopCh verifies a parked agent's
// fixed-interval wait unblocks immediately on a per-agent stop (config removal /
// drain) rather than blocking out the full park interval — so a parked agent
// still drains cleanly.
func TestSleepBeforeRestart_ParkedInterruptibleByStopCh(t *testing.T) {
	s := newParkTestSupervisor()
	ap := newParkedAgent()
	s.registerAgentTick(ap)

	done := make(chan bool, 1)
	go func() { done <- s.sleepBeforeRestart(ap) }()

	close(ap.StopCh) // signal this agent to stop mid-park
	select {
	case keepGoing := <-done:
		if keepGoing {
			t.Error("sleepBeforeRestart = true, want false (interrupted by StopCh)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sleepBeforeRestart did not return after StopCh closed (park not interruptible)")
	}
}

// TestSleepBeforeRestart_ParkedInterruptibleByShutdown verifies a parked agent's
// wait unblocks immediately on daemon shutdown, so a parked agent never blocks
// drain. This also exercises the heartbeat-start/cleanup path (the 30s park
// interval clears the heartbeat gate); a leaked or blocking heartbeat goroutine
// would hang the deferred stop and trip the timeout.
func TestSleepBeforeRestart_ParkedInterruptibleByShutdown(t *testing.T) {
	s := newParkTestSupervisor()
	ap := newParkedAgent()
	s.registerAgentTick(ap)

	done := make(chan bool, 1)
	go func() { done <- s.sleepBeforeRestart(ap) }()

	close(s.Shutdown) // daemon shutting down mid-park
	select {
	case keepGoing := <-done:
		if keepGoing {
			t.Error("sleepBeforeRestart = true, want false (interrupted by Shutdown)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sleepBeforeRestart did not return after Shutdown closed (park not interruptible)")
	}
}

// TestPark_HeartbeatKeepsParkedAgentOutOfQuarantine is the liveness integration
// guard: while parked, sleepBeforeRestart keeps the agent's supervise tick fresh
// via the wait heartbeat, so the class-routed watchdog never mistakes a healthy
// parked agent for a wedged one and quarantines it. Here we drive the same
// heartbeat at a fast interval (sleepBeforeRestart uses the 15s production one)
// and confirm a parked agent with a once-stale tick is refreshed and survives a
// scan untouched — no fatal, and its tick is still registered (not quarantined).
func TestPark_HeartbeatKeepsParkedAgentOutOfQuarantine(t *testing.T) {
	s := newHarnessSupervisor()
	ap := newParkedAgent()
	name := agentTickName(ap)
	s.registerAgentTick(ap)

	// Simulate a long park: the tick was last recorded an hour ago. Without the
	// heartbeat the watchdog would quarantine this (healthy) parked supervisor.
	setTickForTest(s, name, time.Now().Add(-1*time.Hour))

	stop := s.startAgentWaitHeartbeatEvery(ap, 2*time.Millisecond)
	defer stop()

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
		t.Fatal("heartbeat did not refresh the parked agent's tick")
	}

	s.scanTicks(time.Now())
	select {
	case err := <-s.FatalChannel():
		t.Fatalf("scanTicks signaled fatal for a parked, heartbeating agent: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, ok := s.LoadTick(name); !ok {
		t.Error("parked agent was quarantined (tick deregistered) despite a fresh heartbeat")
	}
}
