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

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
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
				case <-d.sup.Shutdown:
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
			_ = conn.SetWriteDeadline(time.Now().Add(d.sup.GetYieldTimeout() + 10*time.Second))
		}
		resp = d.handleAgentControlStop(req.AgentName, req.Force)
	case ctrlOpAgentStart:
		resp = d.handleAgentControlStart(req.AgentName)
	case ctrlOpAgentRestart:
		_ = conn.SetWriteDeadline(time.Now().Add(d.sup.GetYieldTimeout() + 10*time.Second))
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

// resolveAgentNotFoundError returns the standard "not found" response. Covers
// both the "no matching agent" and "ambiguous bare name" cases: the daemon
// cannot tell them apart without leaking config internals, and either way the
// user must be more specific.
func resolveAgentNotFoundError(name string) DaemonControlResponse {
	return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in daemon config", name)}
}

// handleAgentControlStop stops a single agent and records it in StoppedAgents.
// When force is true, it skips DrainWithGrace and sends SIGTERM directly.
// The input name may be either a compound key ("repo/worktree") or a bare
// worktree name when unambiguous; resolveToCompoundKey normalises it.
func (d *Daemon) handleAgentControlStop(name string, force bool) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}
	key, ok := d.resolveToCompoundKey(name)
	if !ok {
		return resolveAgentNotFoundError(name)
	}

	// Drain the agent and record in StoppedAgents atomically under drainAddMu
	// to prevent the reconciler from re-adding the agent in the gap.
	// The isAgentStopped check is inside the lock to avoid TOCTOU races.
	d.drainAddMu.Lock()
	if d.isAgentStopped(key) {
		d.drainAddMu.Unlock()
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is already stopped", key)}
	}
	var err error
	if force {
		err = d.sup.DrainAgentForceful(key, supervisor.StopReasonManualStop)
	} else {
		err = d.sup.DrainAgentWithReason(key, supervisor.StopReasonManualStop)
	}
	if err == nil {
		d.sup.AgentsMu.Lock()
		d.sup.StoppedAgents[key] = struct{}{}
		d.sup.AgentsMu.Unlock()
	}
	d.drainAddMu.Unlock()
	if err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to stop agent %q: %v", key, err)}
	}

	slog.Info("agent stopped via control socket", "agent", key, "force", force)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlStart starts a previously stopped agent.
func (d *Daemon) handleAgentControlStart(name string) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}
	key, ok := d.resolveToCompoundKey(name)
	if !ok {
		return resolveAgentNotFoundError(name)
	}

	// Must be in StoppedAgents set
	if !d.isAgentStopped(key) {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is not stopped (use restart to reset a running agent)", key)}
	}

	// Look up the config.AgentEntry from current config
	entry, ok := d.findAgentEntry(key)
	if !ok {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in current config", key)}
	}

	// Hold drainAddMu across the StoppedAgents delete and AddAgent to prevent
	// the reconciler's addNewAgents from racing in and re-adding the agent
	// between the delete and the add (matches handleAgentControlRestart).
	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()

	d.sup.AgentsMu.Lock()
	delete(d.sup.StoppedAgents, key)
	d.sup.AgentsMu.Unlock()

	if err := d.sup.AddAgent(entry); err != nil {
		// Re-add to StoppedAgents on failure
		d.sup.AgentsMu.Lock()
		d.sup.StoppedAgents[key] = struct{}{}
		d.sup.AgentsMu.Unlock()
		return DaemonControlResponse{Error: fmt.Sprintf("failed to start agent %q: %v", key, err)}
	}

	slog.Info("agent started via control socket", "agent", key)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlRestart restarts an agent (works for both running and stopped agents).
func (d *Daemon) handleAgentControlRestart(name string) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}
	key, ok := d.resolveToCompoundKey(name)
	if !ok {
		return resolveAgentNotFoundError(name)
	}

	// Hold drainAddMu across the isAgentStopped read, the drain (if running),
	// the StoppedAgents delete, and the AddAgent. Reading isAgentStopped
	// outside the lock would create a TOCTOU window where a concurrent stop
	// could flip the state between the read and the drain attempt.
	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()

	// If running, drain it first
	if !d.isAgentStopped(key) {
		if err := d.sup.DrainAgentWithReason(key, supervisor.StopReasonManualStop); err != nil {
			return DaemonControlResponse{Error: fmt.Sprintf("failed to stop agent %q for restart: %v", key, err)}
		}
	}

	// Remove from StoppedAgents if present
	d.sup.AgentsMu.Lock()
	delete(d.sup.StoppedAgents, key)
	d.sup.AgentsMu.Unlock()

	// Look up the config.AgentEntry from current config
	entry, ok := d.findAgentEntry(key)
	if !ok {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in current config", key)}
	}

	// Add the agent back with fresh state
	if err := d.sup.AddAgent(entry); err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to restart agent %q: %v", key, err)}
	}

	slog.Info("agent restarted via control socket", "agent", key)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlList returns a list of all agents with their status.
// Names returned are compound keys (repo/worktree in workspace mode, bare worktree otherwise).
func (d *Daemon) handleAgentControlList() DaemonControlResponse {
	cfg := d.configSnapshot()

	entries := make([]AgentListEntry, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		key := agent.Key()
		status := "running"
		if d.isAgentStopped(key) {
			status = "stopped"
		} else if !d.isAgentRunning(key) {
			status = "stopped"
		}
		entries = append(entries, AgentListEntry{
			Name:   key,
			Role:   agent.Role,
			Status: status,
		})
	}

	data, _ := json.Marshal(entries)
	return DaemonControlResponse{Success: true, Data: data}
}

// handleAgentControlYield handles the agent_yield control socket operation.
// Input name may be either a compound key or a bare worktree when unambiguous.
func (d *Daemon) handleAgentControlYield(name string) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}
	key, ok := d.resolveToCompoundKey(name)
	if !ok {
		return resolveAgentNotFoundError(name)
	}

	d.sup.AgentsMu.RLock()
	var target *supervisor.AgentProcess
	for _, ap := range d.sup.Agents {
		if ap.Entry.Key() == key {
			target = ap
			break
		}
	}
	d.sup.AgentsMu.RUnlock()

	if target == nil {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found", key)}
	}

	target.Mu.Lock()
	pid := target.Pid
	target.Mu.Unlock()

	if pid == 0 {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is not running", key)}
	}

	if err := d.sup.RequestYield(target, "manual_stop"); err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to yield agent %q: %v", key, err)}
	}

	return DaemonControlResponse{Success: true}
}

// resolveDaemonSocketPath returns the control socket path adjacent to the PID file.
func resolveDaemonSocketPath(projectDir, pidFile string) string {
	pidFilePath := supervisor.ResolveDaemonPath(projectDir, pidFile)
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
