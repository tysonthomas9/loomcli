package supervisor

import (
	"os/exec"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// newWorkerWiringSupervisor builds a Supervisor with the minimal fields the
// supervise lifecycle touches, backed by a counting WorkerStore so the test
// observes the renew/deregister wire calls. It reuses countingWorkerStore /
// workerCountingStore from worker_heartbeat_test.go (same package).
func newWorkerWiringSupervisor() (*Supervisor, *countingWorkerStore) {
	cw := &countingWorkerStore{}
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		ControlStore:   &workerCountingStore{Store: memstore.New(), workers: cw},
		WorkspaceID:    "WS",
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*AgentProcess, 0),
		EmitEvent:      func(events.Event) {},
	}
	return s, cw
}

// TestWaitForAgent_RenewsWorkerLease guards the renew half of the fix at the
// lifecycle level: waitForAgent must start the worker-lease heartbeat for the
// duration of the agent process, so a live daemon agent is not reaped by the
// server-side TTL. A unit test of startWorkerHeartbeatEvery alone would not
// catch the supervise loop dropping the s.startWorkerHeartbeat(ap) call.
func TestWaitForAgent_RenewsWorkerLease(t *testing.T) {
	old := workerHeartbeatInterval
	workerHeartbeatInterval = 10 * time.Millisecond
	t.Cleanup(func() { workerHeartbeatInterval = old })

	s, cw := newWorkerWiringSupervisor()

	cmd := exec.Command("sleep", "0.2") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent process: %v", err)
	}
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Worktree: "agent-wt"},
		Cmd:   cmd,
		Pid:   cmd.Process.Pid,
	}

	s.waitForAgent(ap)

	if got := cw.heartbeats.Load(); got < 1 {
		t.Fatalf("waitForAgent fired no worker heartbeats — the lease renewal is not wired into the supervise loop (got %d)", got)
	}
}

// TestCompleteControlPlaneAgentSession_DeregistersWorker guards the deregister
// half of the fix at the lifecycle level: the completion chokepoint must
// deregister the agent's worker so a stopped/drained agent clears the board
// immediately. A unit test of deregisterWorker alone would not catch the
// chokepoint dropping the s.deregisterWorker(ap) call.
func TestCompleteControlPlaneAgentSession_DeregistersWorker(t *testing.T) {
	s, cw := newWorkerWiringSupervisor()
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "agent-wt"}}

	// sessionID must be non-empty (the method early-returns otherwise); taskID
	// and lease fields stay empty so claim/lease release are skipped — we are
	// only asserting the worker deregister is reached.
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID: "sess-1",
		exitCode:  0,
	})

	if got := cw.deregisters.Load(); got != 1 {
		t.Fatalf("completeControlPlaneAgentSession did not deregister the worker — deregister is not wired into the completion chokepoint (got %d, want 1)", got)
	}
}
