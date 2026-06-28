package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// TestCheckWatchdog_DoesNotKillBusyAgentOnFreshHeartbeat is a regression test
// for the RunTurn liveness bug: on the RunTurn path the stdout log (and hook
// transcript) go stale even while the agent is busy; the live signal is the
// agent's PTY-output heartbeat (ap.LastActivity), delivered over agent IPC. The
// watchdog must honor it instead of killing a working agent.
//
// Extracted from TestCheckAgentHealth_Watchdog into its own file to keep
// daemon_moved_test.go under the LOC ceiling.
func TestCheckWatchdog_DoesNotKillBusyAgentOnFreshHeartbeat(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "task-test.log")
	if err := os.WriteFile(logPath, []byte("old output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-20 * time.Minute) // stale log -> would trip the timeout
	if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	config := makeSupervisorConfig(
		[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
		nil,
	)
	config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
		Pid:          99999999, // fake PID that won't exist
		LogFilePath:  logPath,
		LastStart:    time.Now().Add(-25 * time.Minute),
		LastActivity: time.Now(), // fresh PTY-output heartbeat from the wrapper
	}

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
		Agents:         []*AgentProcess{ap},
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}

	s.checkAgentHealth()

	ap.Mu.Lock()
	pid := ap.Pid
	reason := ap.StopReason
	ap.Mu.Unlock()
	if pid != 99999999 {
		t.Errorf("pid = %d, want 99999999 (busy agent with fresh heartbeat must not be killed)", pid)
	}
	if reason == StopReasonWatchdog {
		t.Error("StopReason = Watchdog, want unset (fresh heartbeat should prevent the kill)")
	}
}
