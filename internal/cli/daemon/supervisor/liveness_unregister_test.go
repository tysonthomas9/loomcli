package supervisor

import (
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// TestSupervisedAgentBodyUnregistersTickOnExit guards the fix for a daemon
// crash seen in marathon trial team-cursor-113455: a worker's supervise
// goroutine returned (fast-fail terminal stop) but its liveness tick stayed
// registered, so ~12 minutes later the watchdog flagged the frozen stamp and
// exited the whole daemon. A supervisor that has exited must not be watched.
func TestSupervisedAgentBodyUnregistersTickOnExit(t *testing.T) {
	maxConc := 1
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{
				Daemon: cfgpkg.DaemonSettings{
					RestartPolicy: cfgpkg.RestartPolicy{
						MaxRetries:     cfgpkg.IntPtr(0),
						BackoffInitial: cfgpkg.IntPtr(1),
						BackoffMax:     cfgpkg.IntPtr(1),
					},
				},
				Roles: map[string]cfgpkg.RoleConfig{"plan": {MaxConcurrency: &maxConc}},
			}
		},
		Shutdown:      make(chan struct{}),
		FatalCh:       make(chan error, 1),
		Concurrency:   NewConcurrencyTracker(map[string]cfgpkg.RoleConfig{"plan": {MaxConcurrency: &maxConc}}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		EmitEvent:     func(events.Event) {},
	}
	// Hold the only slot so the supervise loop blocks in Acquire; closing the
	// tracker then makes it return normally, like any terminal stop.
	if !s.Concurrency.Acquire("plan") {
		t.Fatal("failed to pre-acquire slot")
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "exits-cleanly", Role: "plan"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
		Done:         make(chan struct{}),
	}
	name := agentTickName(ap)
	s.RegisterTick(name)
	if _, ok := s.LoadTick(name); !ok {
		t.Fatal("tick not registered before start")
	}
	s.Wg.Add(1)
	go s.supervisedAgentBody(name, ap)
	s.Concurrency.Close()

	select {
	case <-ap.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervise goroutine did not exit")
	}
	s.Wg.Wait()

	if _, ok := s.LoadTick(name); ok {
		t.Fatalf("tick %q still registered after the supervise goroutine exited; the watchdog would crash the daemon once it went stale", name)
	}
	// The watchdog must now see nothing stale from this agent, however old
	// the exit is.
	s.scanTicks(time.Now().Add(time.Hour))
	select {
	case err := <-s.FatalCh:
		t.Fatalf("watchdog signaled fatal for an exited supervisor: %v", err)
	default:
	}
}
