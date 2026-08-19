package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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
	ctrlOpInputGet     = "agent_input_get"
	ctrlOpInputAnswer  = "agent_input_answer"
	ctrlOpClaimHoldSet = "claims_hold_set"
	ctrlOpClaimHoldGet = "claims_hold_get"
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

	writeControlResponse(conn, d.dispatchControlOp(conn, req))
}

// dispatchControlOp routes one control request to its handler. Split out of
// handleControlConnection so the connection lifecycle and the operation table
// stay separately readable as the table grows. Operations that can outlast the
// default 30s write deadline extend it on conn themselves.
func (d *Daemon) dispatchControlOp(conn net.Conn, req DaemonControlRequest) DaemonControlResponse {
	switch req.Operation {
	case ctrlOpAgentStop:
		if req.Force {
			_ = conn.SetWriteDeadline(time.Now().Add(20 * time.Second)) // SIGTERM(5s) + SIGKILL + done drain
		} else {
			_ = conn.SetWriteDeadline(time.Now().Add(d.sup.GetYieldTimeout() + 10*time.Second))
		}
		return d.handleAgentControlStop(req.AgentName, req.Force)
	case ctrlOpAgentStart:
		return d.handleAgentControlStart(req.AgentName, "")
	case ctrlOpAgentRestart:
		_ = conn.SetWriteDeadline(time.Now().Add(d.sup.GetYieldTimeout() + 10*time.Second))
		return d.handleAgentControlRestart(req.AgentName)
	case ctrlOpAgentList:
		return d.handleAgentControlList()
	case ctrlOpAgentYield:
		return d.handleAgentControlYield(req.AgentName, yieldTTLFromArgs(req.Args))
	case ctrlOpInputGet:
		return d.handleAgentInputGet(req.AgentName)
	case ctrlOpInputAnswer:
		return d.handleAgentInputAnswer(req.AgentName, req.Args)
	case ctrlOpClaimHoldSet:
		return d.handleClaimHoldSet(req.Args)
	case ctrlOpClaimHoldGet:
		return d.handleClaimHoldGet()
	case ctrlOpGetMutations:
		return d.handleControlGetMutations(req.Args)
	case ctrlOpWaitForMutations:
		// Extend write deadline for long-poll
		_ = conn.SetWriteDeadline(time.Now().Add(70 * time.Second))
		return d.handleControlWaitForMutations(req.Args)
	default:
		return DaemonControlResponse{Error: fmt.Sprintf("unknown operation: %q", req.Operation)}
	}
}

// handleAgentControlStop stops a single agent and records it in StoppedAgents.
// When force is true, it skips DrainWithGrace and sends SIGTERM directly.
func (d *Daemon) handleAgentControlStop(name string, force bool) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

	// Validate agent exists in config
	if !d.agentExistsInConfig(name) {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in daemon config", name)}
	}

	// Drain the agent and record in StoppedAgents atomically under drainAddMu
	// to prevent the reconciler from re-adding the agent in the gap.
	// The isAgentStopped check is inside the lock to avoid TOCTOU races.
	d.drainAddMu.Lock()
	if d.isAgentStopped(name) {
		d.drainAddMu.Unlock()
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is already stopped", name)}
	}
	var err error
	if force {
		err = d.sup.DrainAgentForceful(name, supervisor.StopReasonManualStop)
	} else {
		err = d.sup.DrainAgentWithReason(name, supervisor.StopReasonManualStop)
	}
	if err == nil {
		d.sup.AgentsMu.Lock()
		d.sup.StoppedAgents[name] = struct{}{}
		d.sup.AgentsMu.Unlock()
	}
	d.drainAddMu.Unlock()
	if err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to stop agent %q: %v", name, err)}
	}

	slog.Info("agent stopped via control socket", "worktree", name, "force", force)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlStart starts a previously stopped agent.
func (d *Daemon) handleAgentControlStart(name string, taskIDs ...string) DaemonControlResponse {
	taskID := ""
	if len(taskIDs) > 0 {
		taskID = taskIDs[0]
	}
	parentSessionID := ""
	if len(taskIDs) > 1 {
		parentSessionID = taskIDs[1]
	}
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

	// A held workspace refuses to START anything — loudly and immediately, so
	// a control-plane or lead start is marked failed rather than retried. This
	// precedes the config reload below: a held workspace does no reload either.
	if h := d.sup.ClaimHoldSnapshot(); h.Active(time.Now()) {
		return DaemonControlResponse{Error: fmt.Sprintf("claims held by %s since %s (%s)",
			h.Actor, h.Since.Format(time.RFC3339), h.Reason)}
	}

	// Command-created agents may have been written to fleet-db after the last
	// config poll. Pull config once synchronously so a queued start command can
	// materialize the new local worker immediately.
	entry, ok := d.findAgentEntry(name)
	if !ok {
		d.reloadAndReconcile()
		entry, ok = d.findAgentEntry(name)
	}
	if !ok {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in daemon config", name)}
	}
	if err := d.validateEphemeralStart(entry, taskID); err != nil {
		return DaemonControlResponse{Error: err.Error()}
	}

	if d.isAgentRunning(name) {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is already running (use restart to reset a running agent)", name)}
	}

	// Remove from StoppedAgents and add agent
	d.sup.AgentsMu.Lock()
	delete(d.sup.StoppedAgents, name)
	d.sup.AgentsMu.Unlock()

	d.drainAddMu.Lock()
	err := d.sup.AddAgentForTask(entry, taskID, parentSessionID)
	d.drainAddMu.Unlock()
	if err != nil {
		// Re-add to StoppedAgents on failure
		d.sup.AgentsMu.Lock()
		d.sup.StoppedAgents[name] = struct{}{}
		d.sup.AgentsMu.Unlock()
		return DaemonControlResponse{Error: fmt.Sprintf("failed to start agent %q: %v", name, err)}
	}
	d.markAgentStartAccepted(name)

	slog.Info("agent started via control socket", "worktree", name)
	return DaemonControlResponse{Success: true}
}

func (d *Daemon) markAgentStartAccepted(name string) {
	if d.store == nil || d.sup == nil || d.sup.WorkspaceID == "" {
		return
	}
	desired := domain.AgentDesiredRunning
	state := domain.AgentStateActive
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	d.reconcileMu.Lock()
	defer d.reconcileMu.Unlock()
	if _, err := d.store.Agents().Update(ctx, d.sup.WorkspaceID, name, store.AgentUpdate{
		DesiredState: &desired,
		State:        &state,
	}); err != nil {
		slog.Warn("failed to mark agent start accepted", "worktree", name, "err", err)
		return
	}
	d.setConfigAgentDesiredStateLocked(name, desired)
}

func (d *Daemon) validateEphemeralStart(entry config.AgentEntry, taskID string) error {
	if entry.Mode != domain.AgentModeEphemeral {
		return nil
	}
	if taskID == "" {
		return fmt.Errorf("ephemeral agent %q requires a task_id; rerun the task to create a new worker attempt", entry.Worktree)
	}
	if d.hasTerminalEphemeralTaskSession(entry.Worktree) {
		return fmt.Errorf("ephemeral agent %q already has a terminal task attempt; rerun the task to create a new worker attempt", entry.Worktree)
	}
	return nil
}

func (d *Daemon) hasTerminalEphemeralTaskSession(agentName string) bool {
	if d.store == nil || d.sup == nil || d.sup.WorkspaceID == "" || d.store.AgentSessions() == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sessions, err := d.store.AgentSessions().List(ctx, d.sup.WorkspaceID, store.AgentSessionFilter{
		AgentID: agentName,
		Limit:   100,
	})
	if err != nil {
		slog.Warn("failed to inspect ephemeral task sessions", "agent", agentName, "err", err)
		return true
	}
	for _, session := range sessions {
		if session == nil || session.Kind != domain.AgentSessionKindTask || session.TaskID == "" {
			continue
		}
		switch session.Status {
		case domain.AgentSessionCompleted, domain.AgentSessionFailed, domain.AgentSessionCancelled, domain.AgentSessionExpired:
			return true
		}
	}
	return false
}

func (d *Daemon) setConfigAgentDesiredState(name string, desired domain.AgentDesiredState) {
	d.reconcileMu.Lock()
	defer d.reconcileMu.Unlock()
	d.setConfigAgentDesiredStateLocked(name, desired)
}

func (d *Daemon) setConfigAgentDesiredStateLocked(name string, desired domain.AgentDesiredState) {
	if d.config == nil {
		return
	}
	for i := range d.config.Agents {
		if d.config.Agents[i].Worktree == name {
			d.config.Agents[i].DesiredState = desired
			// Mirror fleet-db's derived clear: drain metadata is meaningless
			// outside "draining", so leaving it behind would let a stale stamp
			// re-park the agent until the next 30s config poll overwrote it.
			if desired != domain.AgentDesiredDraining {
				d.config.Agents[i].DrainNodeID = ""
				d.config.Agents[i].DrainExpiresAt = nil
			}
			d.configHash = computeConfigHash(d.config)
			return
		}
	}
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
	entry, ok := d.findAgentEntry(name)
	if !ok {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found in current config", name)}
	}
	if entry.Mode == domain.AgentModeEphemeral {
		return DaemonControlResponse{Error: fmt.Sprintf("ephemeral agent %q cannot be restarted; rerun the task to create a new worker attempt", name)}
	}

	isStopped := d.isAgentStopped(name)

	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()

	// If running, drain it first
	if !isStopped {
		if err := d.sup.DrainAgentWithReason(name, supervisor.StopReasonManualStop); err != nil {
			return DaemonControlResponse{Error: fmt.Sprintf("failed to stop agent %q for restart: %v", name, err)}
		}
	}

	// Remove from StoppedAgents if present
	d.sup.AgentsMu.Lock()
	delete(d.sup.StoppedAgents, name)
	d.sup.AgentsMu.Unlock()

	// Add the agent back with fresh state
	if err := d.sup.AddAgent(entry); err != nil {
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

// handleAgentControlYield handles the agent_yield control socket operation.
//
// The drain is stamped BEFORE the "agent not found" and "not running" early
// returns below. A yield aimed at an agent with no live process is still a
// statement of intent, and dropping it there is what let a yield report
// success while recording nothing.
func (d *Daemon) handleAgentControlYield(name string, ttl time.Duration) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

	d.markAgentYieldAccepted(name, ttl)

	d.sup.AgentsMu.RLock()
	var target *supervisor.AgentProcess
	for _, ap := range d.sup.Agents {
		if ap.Entry.Worktree == name {
			target = ap
			break
		}
	}
	d.sup.AgentsMu.RUnlock()

	if target == nil {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found", name)}
	}

	target.Mu.Lock()
	pid := target.Pid
	target.Mu.Unlock()

	if pid == 0 {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is not running", name)}
	}

	if err := d.sup.RequestYield(target, "manual_stop"); err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to yield agent %q: %v", name, err)}
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
