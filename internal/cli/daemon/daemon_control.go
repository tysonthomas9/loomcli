package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// DaemonControlRequest is sent by the CLI client to the daemon control socket.
type DaemonControlRequest struct {
	Operation string          `json:"operation"` // "agent_stop", "agent_start", "agent_restart", "agent_list", "get_mutations", "wait_for_mutations"
	AgentName string          `json:"agent_name,omitempty"`
	Force     bool            `json:"force,omitempty"` // For agent_stop: skip yield, SIGTERM directly
	Args      json.RawMessage `json:"args,omitempty"`  // operation-specific parameters
}

// DaemonControlResponse is sent by the daemon back to the CLI client.
type DaemonControlResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// AgentListEntry is returned by the agent_list operation.
type AgentListEntry struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Status string `json:"status"` // "running", "stopped"
}

const (
	ctrlOpAgentStop    = "agent_stop"
	ctrlOpAgentStart   = "agent_start"
	ctrlOpAgentRestart = "agent_restart"
	ctrlOpAgentList    = "agent_list"
	ctrlOpAgentYield   = "agent_yield"
)

// startControlServer creates a Unix domain socket listener for per-agent control.
// The server accepts connections in a goroutine and dispatches to handlers.
// It closes cleanly when the daemon shuts down.
func (d *Daemon) startControlServer(socketPath string) error {
	// Remove stale socket from a previous crash (safe because daemon.lock prevents
	// concurrent startup — any existing socket file is orphaned).
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("control socket listen: %w", err)
	}

	d.controlListener = ln
	slog.Info("control server started", "socket", socketPath)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// Listener closed (shutdown) — exit silently
				select {
				case <-d.shutdown:
					return
				default:
				}
				slog.Warn("control socket accept error", "err", err)
				return
			}
			go d.handleControlConnection(conn)
		}
	}()

	return nil
}

// handleControlConnection reads one JSON request, dispatches, writes one JSON response.
func (d *Daemon) handleControlConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// Read timeout: 5 seconds for the request
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req DaemonControlRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeControlResponse(conn, DaemonControlResponse{Error: "invalid request: " + err.Error()})
		return
	}

	// Write timeout: 30 seconds for simple operations.
	// agent_stop/agent_restart may block for yield_timeout + SIGTERM grace (~65s+);
	// extend their deadline below.
	writeDeadline := 30 * time.Second
	_ = conn.SetWriteDeadline(time.Now().Add(writeDeadline))

	var resp DaemonControlResponse
	switch req.Operation {
	case ctrlOpAgentStop:
		if req.Force {
			_ = conn.SetWriteDeadline(time.Now().Add(20 * time.Second)) // SIGTERM(5s) + SIGKILL + done drain
		} else {
			_ = conn.SetWriteDeadline(time.Now().Add(d.getYieldTimeout() + 10*time.Second))
		}
		resp = d.handleAgentControlStop(req.AgentName, req.Force)
	case ctrlOpAgentStart:
		resp = d.handleAgentControlStart(req.AgentName)
	case ctrlOpAgentRestart:
		_ = conn.SetWriteDeadline(time.Now().Add(d.getYieldTimeout() + 10*time.Second))
		resp = d.handleAgentControlRestart(req.AgentName)
	case ctrlOpAgentList:
		resp = d.handleAgentControlList()
	case ctrlOpAgentYield:
		resp = d.handleAgentControlYield(req.AgentName)
	case ctrlOpGetMutations:
		resp = d.handleControlGetMutations(req.Args)
	case ctrlOpWaitForMutations:
		// Extend write deadline for long-poll
		_ = conn.SetWriteDeadline(time.Now().Add(70 * time.Second))
		resp = d.handleControlWaitForMutations(req.Args)
	default:
		resp = DaemonControlResponse{Error: fmt.Sprintf("unknown operation: %q", req.Operation)}
	}

	writeControlResponse(conn, resp)
}

// handleAgentControlStop stops a single agent and records it in stoppedAgents.
// When force is true, it skips DrainWithGrace and sends SIGTERM directly.
func (d *Daemon) handleAgentControlStop(name string, force bool) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

	// Validate agent exists in config
	if !d.agentExistsInConfig(name) {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in daemon config", name)}
	}

	// Drain the agent and record in stoppedAgents atomically under drainAddMu
	// to prevent the reconciler from re-adding the agent in the gap.
	// The isAgentStopped check is inside the lock to avoid TOCTOU races.
	d.drainAddMu.Lock()
	if d.isAgentStopped(name) {
		d.drainAddMu.Unlock()
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is already stopped", name)}
	}
	var err error
	if force {
		err = d.drainAgentForceful(name, StopReasonManualStop)
	} else {
		err = d.drainAgentWithReason(name, StopReasonManualStop)
	}
	if err == nil {
		d.agentsMu.Lock()
		d.stoppedAgents[name] = struct{}{}
		d.agentsMu.Unlock()
	}
	d.drainAddMu.Unlock()
	if err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to stop agent %q: %v", name, err)}
	}

	slog.Info("agent stopped via control socket", "worktree", name, "force", force)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlStart starts a previously stopped agent.
func (d *Daemon) handleAgentControlStart(name string) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

	// Validate agent exists in config
	if !d.agentExistsInConfig(name) {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in daemon config", name)}
	}

	// Must be in stoppedAgents set
	if !d.isAgentStopped(name) {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is not stopped (use restart to reset a running agent)", name)}
	}

	// Look up the config.AgentEntry from current config
	entry, ok := d.findAgentEntry(name)
	if !ok {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in current config", name)}
	}

	// Remove from stoppedAgents and add agent
	d.agentsMu.Lock()
	delete(d.stoppedAgents, name)
	d.agentsMu.Unlock()

	d.drainAddMu.Lock()
	err := d.addAgent(entry)
	d.drainAddMu.Unlock()
	if err != nil {
		// Re-add to stoppedAgents on failure
		d.agentsMu.Lock()
		d.stoppedAgents[name] = struct{}{}
		d.agentsMu.Unlock()
		return DaemonControlResponse{Error: fmt.Sprintf("failed to start agent %q: %v", name, err)}
	}

	slog.Info("agent started via control socket", "worktree", name)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlRestart restarts an agent (works for both running and stopped agents).
func (d *Daemon) handleAgentControlRestart(name string) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

	// Validate agent exists in config
	if !d.agentExistsInConfig(name) {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in daemon config", name)}
	}

	isStopped := d.isAgentStopped(name)

	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()

	// If running, drain it first
	if !isStopped {
		if err := d.drainAgentWithReason(name, StopReasonManualStop); err != nil {
			return DaemonControlResponse{Error: fmt.Sprintf("failed to stop agent %q for restart: %v", name, err)}
		}
	}

	// Remove from stoppedAgents if present
	d.agentsMu.Lock()
	delete(d.stoppedAgents, name)
	d.agentsMu.Unlock()

	// Look up the config.AgentEntry from current config
	entry, ok := d.findAgentEntry(name)
	if !ok {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in current config", name)}
	}

	// Add the agent back with fresh state
	if err := d.addAgent(entry); err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to restart agent %q: %v", name, err)}
	}

	slog.Info("agent restarted via control socket", "worktree", name)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlList returns a list of all agents with their status.
func (d *Daemon) handleAgentControlList() DaemonControlResponse {
	cfg := d.configSnapshot()

	entries := make([]AgentListEntry, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		status := "running"
		if d.isAgentStopped(agent.Worktree) {
			status = "stopped"
		} else if !d.isAgentRunning(agent.Worktree) {
			status = "stopped"
		}
		entries = append(entries, AgentListEntry{
			Name:   agent.Worktree,
			Role:   agent.Role,
			Status: status,
		})
	}

	data, _ := json.Marshal(entries)
	return DaemonControlResponse{Success: true, Data: data}
}

// drainAgentWithReason is like drainAgent but sets a specific stop reason.
func (d *Daemon) drainAgentWithReason(name string, reason StopReason) error {
	// Find the agent under lock
	d.agentsMu.Lock()
	var target *AgentProcess
	for _, ap := range d.agents {
		if ap.entry.Worktree == name {
			target = ap
			break
		}
	}
	if target == nil {
		d.agentsMu.Unlock()
		return fmt.Errorf("agent %q not found", name)
	}
	d.agentsMu.Unlock()

	// Fleet mode: no superviseAgent goroutine was launched, stopCh/done are nil.
	if target.stopCh == nil {
		return nil
	}

	// Set stop reason before signaling
	target.mu.Lock()
	target.stopReason = reason
	target.mu.Unlock()

	// Signal the agent to stop (safe against double-close).
	// ORDERING: stopCh must close BEFORE DrainWithGrace — prevents superviseAgent
	// from respawning after the subprocess exits via yield.
	target.stopOnce.Do(func() {
		close(target.stopCh)
	})

	// Yield → wait → SIGTERM → SIGKILL
	d.DrainWithGrace(target, string(reason), d.getYieldTimeout(), d.getSigtermTimeout())

	// Wait for the superviseAgent goroutine to exit
	<-target.done

	// Remove from the agents slice under write lock
	d.agentsMu.Lock()
	for i, ap := range d.agents {
		if ap == target {
			d.agents = append(d.agents[:i], d.agents[i+1:]...)
			break
		}
	}
	d.agentsMu.Unlock()

	slog.Info("agent drained", "worktree", name, "reason", reason)
	return nil
}

// drainAgentForceful is like drainAgentWithReason but skips DrainWithGrace,
// going directly to SIGTERM/SIGKILL. Used by the CLI force-stop path where
// the control socket timeout is a concern.
func (d *Daemon) drainAgentForceful(name string, reason StopReason) error {
	// Find the agent under lock
	d.agentsMu.Lock()
	var target *AgentProcess
	for _, ap := range d.agents {
		if ap.entry.Worktree == name {
			target = ap
			break
		}
	}
	if target == nil {
		d.agentsMu.Unlock()
		return fmt.Errorf("agent %q not found", name)
	}
	d.agentsMu.Unlock()

	// Fleet mode: no superviseAgent goroutine was launched, stopCh/done are nil.
	if target.stopCh == nil {
		return nil
	}

	// Set stop reason before signaling
	target.mu.Lock()
	target.stopReason = reason
	target.mu.Unlock()

	// Signal the agent to stop (safe against double-close)
	target.stopOnce.Do(func() {
		close(target.stopCh)
	})

	// Stop the subprocess directly: SIGTERM → SIGKILL (no yield)
	d.stopAgent(target, d.getSigtermTimeout())

	// Wait for the superviseAgent goroutine to exit
	<-target.done

	// Remove from the agents slice under write lock
	d.agentsMu.Lock()
	for i, ap := range d.agents {
		if ap == target {
			d.agents = append(d.agents[:i], d.agents[i+1:]...)
			break
		}
	}
	d.agentsMu.Unlock()

	slog.Info("agent force-drained", "worktree", name, "reason", reason)
	return nil
}

// agentExistsInConfig returns true if an agent with the given name exists in the current config.
func (d *Daemon) agentExistsInConfig(name string) bool {
	cfg := d.configSnapshot()
	for _, agent := range cfg.Agents {
		if agent.Worktree == name {
			return true
		}
	}
	return false
}

// findAgentEntry looks up an config.AgentEntry by worktree name in the current config.
func (d *Daemon) findAgentEntry(name string) (config.AgentEntry, bool) {
	cfg := d.configSnapshot()
	for _, agent := range cfg.Agents {
		if agent.Worktree == name {
			return agent, true
		}
	}
	return config.AgentEntry{}, false
}

// isAgentRunning returns true if the named agent has a running superviseAgent goroutine.
func (d *Daemon) isAgentRunning(name string) bool {
	d.agentsMu.RLock()
	defer d.agentsMu.RUnlock()
	for _, ap := range d.agents {
		if ap.entry.Worktree == name {
			return true
		}
	}
	return false
}

// resolveDaemonSocketPath returns the control socket path adjacent to the PID file.
func resolveDaemonSocketPath(projectDir, pidFile string) string {
	pidFilePath := ResolveDaemonPath(projectDir, pidFile)
	return filepath.Join(filepath.Dir(pidFilePath), "daemon.sock")
}

// writeControlResponse writes a JSON response line to the connection.
func writeControlResponse(conn net.Conn, resp DaemonControlResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}
