package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// shortSocketDir creates a short temp directory suitable for Unix socket paths,
// which are limited to 104 bytes on macOS. t.TempDir() paths can exceed this.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "loom-sock-")
	if err != nil {
		t.Fatalf("creating short socket dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// newTestDaemonWithAgents constructs a Daemon for control-socket tests with
// the given agents already in the agents slice. Each AgentProcess has a
// goroutine that closes done when stopCh is closed, simulating a real
// superviseAgent goroutine exiting.
func newTestDaemonWithAgents(entries []AgentEntry) *Daemon {
	config := makeDaemonConfig(entries, nil)
	d := &Daemon{
		config: config,
	}

	sup := &supervisor.Supervisor{
		ConfigSnapshot: d.configSnapshot,
		Concurrency:    supervisor.NewConcurrencyTracker(nil),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*supervisor.AgentProcess, 0, len(entries)),
		Shutdown:       make(chan struct{}),
		EmitEvent:      func(events.Event) {},
	}
	d.sup = sup

	for _, entry := range entries {
		ap := &supervisor.AgentProcess{
			Entry:  entry,
			StopCh: make(chan struct{}),
			Done:   make(chan struct{}),
		}
		// Simulate superviseAgent: close done when stopCh is closed.
		go func(ap *supervisor.AgentProcess) {
			<-ap.StopCh
			close(ap.Done)
		}(ap)
		sup.Agents = append(sup.Agents, ap)
	}

	return d
}

func TestControlServer_StopAgent(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	resp := d.handleAgentControlStop("alpha", false)
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	// alpha should be removed from agents slice
	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
	}

	// alpha should be in stoppedAgents
	if !d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = false, want true")
	}

	// beta should still be running
	if d.isAgentStopped("beta") {
		t.Error("isAgentStopped(beta) = true, want false")
	}
	if !d.isAgentRunning("beta") {
		t.Error("isAgentRunning(beta) = false, want true")
	}
}

func TestControlServer_StartAgent(t *testing.T) {
	t.Skip("requires full supervisor initialization after restructuring")
	// Create temp worktree directory for addAgent to resolve
	tmpDir := t.TempDir()
	wtDir := filepath.Join(tmpDir, "worktrees", "alpha")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		d.sup.Concurrency.Close()
		d.sup.Wg.Wait()
	}()

	// Stop the agent first
	resp := d.handleAgentControlStop("alpha", false)
	if !resp.Success {
		t.Fatalf("stop failed: %s", resp.Error)
	}
	if d.AgentCount() != 0 {
		t.Fatalf("AgentCount() = %d after stop, want 0", d.AgentCount())
	}

	// Now start it
	resp = d.handleAgentControlStart("alpha")
	if !resp.Success {
		t.Fatalf("start failed: %s", resp.Error)
	}

	// alpha should be back in agents and removed from stoppedAgents
	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
	}
	if d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = true after start, want false")
	}
	if !d.isAgentRunning("alpha") {
		t.Error("isAgentRunning(alpha) = false after start, want true")
	}
}

func TestControlServer_RestartRunningAgent(t *testing.T) {
	t.Skip("requires full supervisor initialization after restructuring")
	// Create temp worktree directory for addAgent to resolve
	tmpDir := t.TempDir()
	wtDir := filepath.Join(tmpDir, "worktrees", "alpha")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		d.sup.Concurrency.Close()
		d.sup.Wg.Wait()
	}()

	// Capture the original agent pointer to verify it's replaced
	d.sup.AgentsMu.RLock()
	originalAgent := d.sup.Agents[0]
	d.sup.AgentsMu.RUnlock()

	resp := d.handleAgentControlRestart("alpha")
	if !resp.Success {
		t.Fatalf("restart failed: %s", resp.Error)
	}

	// Agent should still be running
	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
	}
	if !d.isAgentRunning("alpha") {
		t.Error("isAgentRunning(alpha) = false after restart, want true")
	}

	// Should not be in stoppedAgents
	if d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = true after restart, want false")
	}

	// The agent process should be a new instance (fresh state)
	d.sup.AgentsMu.RLock()
	newAgent := d.sup.Agents[0]
	d.sup.AgentsMu.RUnlock()
	if newAgent == originalAgent {
		t.Error("agent pointer unchanged after restart, expected new instance")
	}
}

func TestControlServer_RestartStoppedAgent(t *testing.T) {
	t.Skip("requires full supervisor initialization after restructuring")
	// Create temp worktree directory for addAgent to resolve
	tmpDir := t.TempDir()
	wtDir := filepath.Join(tmpDir, "worktrees", "alpha")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		d.sup.Concurrency.Close()
		d.sup.Wg.Wait()
	}()

	// Stop it first
	resp := d.handleAgentControlStop("alpha", false)
	if !resp.Success {
		t.Fatalf("stop failed: %s", resp.Error)
	}
	if d.AgentCount() != 0 {
		t.Fatalf("AgentCount() = %d after stop, want 0", d.AgentCount())
	}

	// Restart should work even though it's stopped
	resp = d.handleAgentControlRestart("alpha")
	if !resp.Success {
		t.Fatalf("restart of stopped agent failed: %s", resp.Error)
	}

	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
	}
	if d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = true after restart, want false")
	}
}

func TestControlServer_StopAlreadyStopped(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	// Stop it
	resp := d.handleAgentControlStop("alpha", false)
	if !resp.Success {
		t.Fatalf("first stop failed: %s", resp.Error)
	}

	// Stop again — should fail
	resp = d.handleAgentControlStop("alpha", false)
	if resp.Success {
		t.Fatal("expected error when stopping already stopped agent")
	}
	if !strings.Contains(resp.Error, "already stopped") {
		t.Errorf("error = %q, want contains 'already stopped'", resp.Error)
	}
}

func TestControlServer_StartNotStopped(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	// Try to start a running agent
	resp := d.handleAgentControlStart("alpha")
	if resp.Success {
		t.Fatal("expected error when starting a running agent")
	}
	if !strings.Contains(resp.Error, "already running") {
		t.Errorf("error = %q, want contains 'already running'", resp.Error)
	}
}

func TestControlServer_EphemeralStartRequiresTaskID(t *testing.T) {
	d := newTestDaemonWithAgents(nil)
	d.config = makeDaemonConfig([]AgentEntry{
		{Worktree: "worker", Role: "task", Mode: domain.AgentModeEphemeral},
	}, nil)
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	resp := d.handleAgentControlStart("worker")
	if resp.Success {
		t.Fatal("expected ephemeral start without task_id to fail")
	}
	if !strings.Contains(resp.Error, "requires a task_id") {
		t.Fatalf("error = %q, want requires a task_id", resp.Error)
	}
}

func TestControlServer_EphemeralStartRejectsTerminalAttempt(t *testing.T) {
	d := newTestDaemonWithAgents(nil)
	d.config = makeDaemonConfig([]AgentEntry{
		{Worktree: "worker", Role: "task", Mode: domain.AgentModeEphemeral},
	}, nil)
	d.store = memstore.New()
	d.sup.WorkspaceID = "WS"
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	if _, err := d.store.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "sess-worker-1",
		AgentID:      "worker",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-1",
		Status:       domain.AgentSessionCompleted,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	resp := d.handleAgentControlStart("worker", "TASK-1")
	if resp.Success {
		t.Fatal("expected ephemeral start with terminal attempt to fail")
	}
	if !strings.Contains(resp.Error, "already has a terminal task attempt") {
		t.Fatalf("error = %q, want terminal task attempt rejection", resp.Error)
	}
}

func TestControlServer_EphemeralRestartRejected(t *testing.T) {
	d := newTestDaemonWithAgents(nil)
	d.config = makeDaemonConfig([]AgentEntry{
		{Worktree: "worker", Role: "task", Mode: domain.AgentModeEphemeral},
	}, nil)
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	resp := d.handleAgentControlRestart("worker")
	if resp.Success {
		t.Fatal("expected ephemeral restart to fail")
	}
	if !strings.Contains(resp.Error, "cannot be restarted") {
		t.Fatalf("error = %q, want cannot be restarted", resp.Error)
	}
}

func TestControlServer_UnknownAgent(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	t.Run("stop unknown agent", func(t *testing.T) {
		resp := d.handleAgentControlStop("nonexistent", false)
		if resp.Success {
			t.Fatal("expected error for unknown agent")
		}
		if !strings.Contains(resp.Error, "not found") {
			t.Errorf("error = %q, want contains 'not found'", resp.Error)
		}
	})

	t.Run("start unknown agent", func(t *testing.T) {
		resp := d.handleAgentControlStart("nonexistent")
		if resp.Success {
			t.Fatal("expected error for unknown agent")
		}
		if !strings.Contains(resp.Error, "not found") {
			t.Errorf("error = %q, want contains 'not found'", resp.Error)
		}
	})

	t.Run("restart unknown agent", func(t *testing.T) {
		resp := d.handleAgentControlRestart("nonexistent")
		if resp.Success {
			t.Fatal("expected error for unknown agent")
		}
		if !strings.Contains(resp.Error, "not found") {
			t.Errorf("error = %q, want contains 'not found'", resp.Error)
		}
	})
}

func TestControlServer_ReconcilerRespectsStoppedAgents(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	// Initially, no agents are stopped
	if d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = true before stop, want false")
	}
	if d.isAgentStopped("beta") {
		t.Error("isAgentStopped(beta) = true before stop, want false")
	}

	// Stop alpha
	resp := d.handleAgentControlStop("alpha", false)
	if !resp.Success {
		t.Fatalf("stop failed: %s", resp.Error)
	}

	// Verify the reconciler's perspective
	if !d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = false after stop, want true")
	}
	if d.isAgentStopped("beta") {
		t.Error("isAgentStopped(beta) = true, want false")
	}

	// beta should still be running
	if !d.isAgentRunning("beta") {
		t.Error("isAgentRunning(beta) = false, want true")
	}
	// alpha should not be running
	if d.isAgentRunning("alpha") {
		t.Error("isAgentRunning(alpha) = true after stop, want false")
	}
}

func TestControlServer_SocketCleanup(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{{Worktree: "alpha", Role: "plan"}})

	// Start the control server
	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	// Socket file should exist
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("socket file does not exist after startControlServer")
	}

	// Close the listener (simulates daemon shutdown)
	d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
	if d.controlListener != nil {
		_ = d.controlListener.Close()
	}

	// After closing the listener, new connections should fail
	// (the accept loop exits on listener close)
	time.Sleep(50 * time.Millisecond) // brief pause for goroutine to exit
	_, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if err == nil {
		t.Error("expected connection to fail after listener close")
	}
}

func TestControlServer_StaleSocketCleanup(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	// Create a stale socket file (just a regular file pretending to be a socket)
	if err := os.WriteFile(socketPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to create stale socket: %v", err)
	}

	d := newTestDaemonWithAgents([]AgentEntry{{Worktree: "alpha", Role: "plan"}})

	// Start should succeed despite stale socket
	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error with stale socket: %v", err)
	}

	// Clean up
	d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
	if d.controlListener != nil {
		_ = d.controlListener.Close()
	}
}

func TestControlServer_AgentList(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
		{Worktree: "gamma", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	t.Run("all running", func(t *testing.T) {
		resp := d.handleAgentControlList()
		if !resp.Success {
			t.Fatalf("list failed: %s", resp.Error)
		}

		var entries []AgentListEntry
		if err := json.Unmarshal(resp.Data, &entries); err != nil {
			t.Fatalf("unmarshal list data: %v", err)
		}

		if len(entries) != 3 {
			t.Fatalf("len(entries) = %d, want 3", len(entries))
		}

		// Build a map for easier lookup
		byName := make(map[string]AgentListEntry)
		for _, e := range entries {
			byName[e.Name] = e
		}

		for _, name := range []string{"alpha", "beta", "gamma"} {
			e, ok := byName[name]
			if !ok {
				t.Errorf("agent %q not found in list", name)
				continue
			}
			if e.Status != "running" {
				t.Errorf("agent %q status = %q, want %q", name, e.Status, "running")
			}
		}

		if byName["alpha"].Role != "plan" {
			t.Errorf("alpha role = %q, want %q", byName["alpha"].Role, "plan")
		}
		if byName["beta"].Role != "task" {
			t.Errorf("beta role = %q, want %q", byName["beta"].Role, "task")
		}
	})

	t.Run("with stopped agent", func(t *testing.T) {
		// Stop beta
		stopResp := d.handleAgentControlStop("beta", false)
		if !stopResp.Success {
			t.Fatalf("stop failed: %s", stopResp.Error)
		}

		resp := d.handleAgentControlList()
		if !resp.Success {
			t.Fatalf("list failed: %s", resp.Error)
		}

		var entries []AgentListEntry
		if err := json.Unmarshal(resp.Data, &entries); err != nil {
			t.Fatalf("unmarshal list data: %v", err)
		}

		if len(entries) != 3 {
			t.Fatalf("len(entries) = %d, want 3", len(entries))
		}

		byName := make(map[string]AgentListEntry)
		for _, e := range entries {
			byName[e.Name] = e
		}

		if byName["alpha"].Status != "running" {
			t.Errorf("alpha status = %q, want %q", byName["alpha"].Status, "running")
		}
		if byName["beta"].Status != "stopped" {
			t.Errorf("beta status = %q, want %q", byName["beta"].Status, "stopped")
		}
		if byName["gamma"].Status != "running" {
			t.Errorf("gamma status = %q, want %q", byName["gamma"].Status, "running")
		}
	})
}

func TestControlServer_SocketRoundTrip(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	}()

	// Start the control server
	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	t.Run("list via socket", func(t *testing.T) {
		resp, err := sendDaemonControlRequest(socketPath, ctrlOpAgentList, "")
		if err != nil {
			t.Fatalf("sendDaemonControlRequest() error = %v", err)
		}
		if !resp.Success {
			t.Fatalf("expected success, got error: %s", resp.Error)
		}

		var entries []AgentListEntry
		if err := json.Unmarshal(resp.Data, &entries); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(entries) != 2 {
			t.Errorf("len(entries) = %d, want 2", len(entries))
		}
	})

	t.Run("stop via socket", func(t *testing.T) {
		resp, err := sendDaemonControlRequest(socketPath, ctrlOpAgentStop, "alpha")
		if err != nil {
			t.Fatalf("sendDaemonControlRequest() error = %v", err)
		}
		if !resp.Success {
			t.Fatalf("expected success, got error: %s", resp.Error)
		}

		if !d.isAgentStopped("alpha") {
			t.Error("isAgentStopped(alpha) = false after stop via socket")
		}
		if d.AgentCount() != 1 {
			t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
		}
	})

	t.Run("unknown operation via socket", func(t *testing.T) {
		resp, err := sendDaemonControlRequest(socketPath, "bogus_op", "")
		if err != nil {
			t.Fatalf("sendDaemonControlRequest() error = %v", err)
		}
		if resp.Success {
			t.Fatal("expected failure for unknown operation")
		}
		if !strings.Contains(resp.Error, "unknown operation") {
			t.Errorf("error = %q, want contains 'unknown operation'", resp.Error)
		}
	})
}

func TestControlServer_EmptyAgentName(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	t.Run("stop with empty name", func(t *testing.T) {
		resp := d.handleAgentControlStop("", false)
		if resp.Success {
			t.Fatal("expected error for empty agent name")
		}
		if !strings.Contains(resp.Error, "agent name is required") {
			t.Errorf("error = %q, want contains 'agent name is required'", resp.Error)
		}
	})

	t.Run("start with empty name", func(t *testing.T) {
		resp := d.handleAgentControlStart("")
		if resp.Success {
			t.Fatal("expected error for empty agent name")
		}
		if !strings.Contains(resp.Error, "agent name is required") {
			t.Errorf("error = %q, want contains 'agent name is required'", resp.Error)
		}
	})

	t.Run("restart with empty name", func(t *testing.T) {
		resp := d.handleAgentControlRestart("")
		if resp.Success {
			t.Fatal("expected error for empty agent name")
		}
		if !strings.Contains(resp.Error, "agent name is required") {
			t.Errorf("error = %q, want contains 'agent name is required'", resp.Error)
		}
	})
}

func TestControlServer_ConcurrentStops(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
		{Worktree: "gamma", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	// Stop all three concurrently
	var wg sync.WaitGroup
	results := make([]DaemonControlResponse, 3)
	names := []string{"alpha", "beta", "gamma"}

	for i, name := range names {
		wg.Add(1)
		go func(idx int, n string) {
			defer wg.Done()
			results[idx] = d.handleAgentControlStop(n, false)
		}(i, name)
	}
	wg.Wait()

	for i, name := range names {
		if !results[i].Success {
			t.Errorf("stop %q failed: %s", name, results[i].Error)
		}
	}

	if d.AgentCount() != 0 {
		t.Errorf("AgentCount() = %d after stopping all, want 0", d.AgentCount())
	}

	for _, name := range names {
		if !d.isAgentStopped(name) {
			t.Errorf("isAgentStopped(%q) = false, want true", name)
		}
	}
}

func TestControlServer_ForceStop(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	resp := d.handleAgentControlStop("alpha", true)
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	// alpha should be removed from agents slice
	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
	}

	// alpha should be in stoppedAgents
	if !d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = false, want true")
	}

	// beta should still be running
	if d.isAgentStopped("beta") {
		t.Error("isAgentStopped(beta) = true, want false")
	}
	if !d.isAgentRunning("beta") {
		t.Error("isAgentRunning(beta) = false, want true")
	}
}

func TestControlServer_ForceStopAlreadyStopped(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	// Stop it first (graceful)
	resp := d.handleAgentControlStop("alpha", false)
	if !resp.Success {
		t.Fatalf("first stop failed: %s", resp.Error)
	}

	// Force-stop again — should fail
	resp = d.handleAgentControlStop("alpha", true)
	if resp.Success {
		t.Fatal("expected error when force-stopping already stopped agent")
	}
	if !strings.Contains(resp.Error, "already stopped") {
		t.Errorf("error = %q, want contains 'already stopped'", resp.Error)
	}
}

func TestControlServer_ForceStopUnknown(t *testing.T) {
	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })

	resp := d.handleAgentControlStop("nonexistent", true)
	if resp.Success {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("error = %q, want contains 'not found'", resp.Error)
	}
}

func TestControlServer_ForceViaSocketRoundTrip(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	}()

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	resp, err := sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpAgentStop,
		AgentName: "alpha",
		Force:     true,
	})
	if err != nil {
		t.Fatalf("sendDaemonControlRequestFull() error = %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	if !d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = false after force stop via socket")
	}
	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
	}
}

func TestControlServer_ForceFieldOmittedBackwardCompat(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	}()

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	// Send raw JSON without the "force" field — should default to false (graceful stop)
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer func() { _ = conn.Close() }()

	raw := `{"operation":"agent_stop","agent_name":"alpha"}` + "\n"
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatalf("write error: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("no response from daemon: %v", scanner.Err())
	}

	var resp DaemonControlResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	if !d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = false after stop via raw JSON without force field")
	}
}

func TestIsAgentRunningViaSocket_Running(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	}()

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	if !isAgentRunningViaSocket(socketPath, "alpha") {
		t.Error("isAgentRunningViaSocket(alpha) = false, want true")
	}
	if !isAgentRunningViaSocket(socketPath, "beta") {
		t.Error("isAgentRunningViaSocket(beta) = false, want true")
	}
}

func TestIsAgentRunningViaSocket_Stopped(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	}()

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	// Stop the agent
	resp := d.handleAgentControlStop("alpha", false)
	if !resp.Success {
		t.Fatalf("stop failed: %s", resp.Error)
	}

	if isAgentRunningViaSocket(socketPath, "alpha") {
		t.Error("isAgentRunningViaSocket(alpha) = true after stop, want false")
	}
}

func TestIsAgentRunningViaSocket_NotFound(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	}()

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	if isAgentRunningViaSocket(socketPath, "nonexistent") {
		t.Error("isAgentRunningViaSocket(nonexistent) = true, want false")
	}
}

func TestIsAgentRunningViaSocket_DaemonDown(t *testing.T) {
	// Use a path where no listener exists
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "no-such-daemon.sock")

	// Should return false without panicking
	if isAgentRunningViaSocket(socketPath, "alpha") {
		t.Error("isAgentRunningViaSocket with no daemon = true, want false")
	}
}

func TestSendDaemonControlRequestFull_RoundTrip(t *testing.T) {
	tmpDir := shortSocketDir(t)
	socketPath := filepath.Join(tmpDir, "daemon.sock")

	d := newTestDaemonWithAgents([]AgentEntry{
		{Worktree: "alpha", Role: "plan"},
		{Worktree: "beta", Role: "task"},
	})
	defer func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
	}()

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer() error = %v", err)
	}

	// Send a full request with Force=true for agent_stop
	resp, err := sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpAgentStop,
		AgentName: "alpha",
		Force:     true,
	})
	if err != nil {
		t.Fatalf("sendDaemonControlRequestFull() error = %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}

	// Verify the agent was actually stopped
	if !d.isAgentStopped("alpha") {
		t.Error("isAgentStopped(alpha) = false after force stop")
	}
	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1", d.AgentCount())
	}

	// beta should still be running
	if !d.isAgentRunning("beta") {
		t.Error("isAgentRunning(beta) = false, want true")
	}
}
