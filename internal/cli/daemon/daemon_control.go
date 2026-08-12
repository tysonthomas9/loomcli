package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

	// Write timeout: 30 seconds for simple operations. Graceful lifecycle
	// operations may consume both the configured yield and SIGTERM windows, so
	// their deadlines cover the complete escalation budget below.
	writeDeadline := 30 * time.Second
	_ = conn.SetWriteDeadline(time.Now().Add(writeDeadline))

	var resp DaemonControlResponse
	switch req.Operation {
	case ctrlOpAgentStop:
		if req.Force {
			_ = conn.SetWriteDeadline(time.Now().Add(d.sup.GetSigtermTimeout() + 10*time.Second))
		} else {
			_ = conn.SetWriteDeadline(time.Now().Add(
				d.sup.GetYieldTimeout() + d.sup.GetSigtermTimeout() + 10*time.Second,
			))
		}
		resp = d.handleAgentControlStop(req.AgentName, req.Force)
	case ctrlOpAgentStart:
		resp = d.handleAgentControlStart(req.AgentName, "")
	case ctrlOpAgentRestart:
		_ = conn.SetWriteDeadline(time.Now().Add(
			d.sup.GetYieldTimeout() + d.sup.GetSigtermTimeout() + 10*time.Second,
		))
		resp = d.handleAgentControlRestart(req.AgentName)
	case ctrlOpAgentList:
		resp = d.handleAgentControlList()
	case ctrlOpAgentYield:
		resp = d.handleAgentControlYield(req.AgentName)
	case ctrlOpInputGet:
		resp = d.handleAgentInputGet(req.AgentName)
	case ctrlOpInputAnswer:
		resp = d.handleAgentInputAnswer(req.AgentName, req.Args)
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
	// A reconciler may have already drained desired=draining before the queued
	// command runs; treat that state as an idempotent successful stop.
	d.drainAddMu.Lock()
	var err error
	if !d.isAgentStopped(name) && d.isAgentRunning(name) {
		if force {
			err = d.sup.DrainAgentForceful(name, supervisor.StopReasonManualStop)
		} else {
			err = d.sup.DrainAgentWithReason(name, supervisor.StopReasonManualStop)
		}
	}
	if err == nil {
		d.sup.AgentsMu.Lock()
		d.sup.StoppedAgents[name] = struct{}{}
		d.sup.AgentsMu.Unlock()
	}
	if err != nil {
		d.drainAddMu.Unlock()
		return DaemonControlResponse{Error: fmt.Sprintf("failed to stop agent %q: %v", name, err)}
	}
	if err := d.markAgentStopAccepted(name); err != nil {
		// Runtime stop is already authoritative. The durable command worker
		// retries terminal projection after completion, so do not misreport a
		// completed stop as a control failure or replay its side effect.
		slog.Warn("agent stopped but lifecycle projection is pending retry",
			"worktree", name,
			"err", err)
	}
	d.drainAddMu.Unlock()

	slog.Info("agent stopped via control socket", "worktree", name, "force", force)
	return DaemonControlResponse{Success: true}
}

// handleAgentControlStart starts a previously stopped agent.
func (d *Daemon) handleAgentControlStart(name string, taskIDs ...string) DaemonControlResponse {
	resp, _ := d.handleAgentControlStartInternal(name, false, taskIDs...)
	return resp
}

func (d *Daemon) handleAgentControlStartCommand(
	name string,
	taskIDs ...string,
) (DaemonControlResponse, *supervisor.AgentProcess) {
	return d.handleAgentControlStartInternal(name, true, taskIDs...)
}

func (d *Daemon) handleAgentControlStartInternal(
	name string,
	reserveOwnership bool,
	taskIDs ...string,
) (DaemonControlResponse, *supervisor.AgentProcess) {
	taskID, parentSessionID := agentControlStartIDs(taskIDs)
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}, nil
	}

	entry, err := d.agentEntryForControlStart(name, taskID)
	if err != nil {
		return DaemonControlResponse{Error: err.Error()}, nil
	}
	replacement, err := d.startAgentProcess(entry, taskID, parentSessionID, reserveOwnership)
	if err != nil {
		return DaemonControlResponse{Error: err.Error()}, nil
	}

	slog.Info("agent started via control socket", "worktree", name)
	return DaemonControlResponse{Success: true}, replacement
}

func (d *Daemon) agentEntryForControlStart(name, taskID string) (config.AgentEntry, error) {
	// Command-created agents may have been written to fleet-db after the last
	// config poll. Pull config once synchronously so a queued start command can
	// materialize the new local worker immediately.
	entry, ok := d.findAgentEntry(name)
	if !ok {
		d.reloadAndReconcile()
		entry, ok = d.findAgentEntry(name)
	}
	if !ok {
		return config.AgentEntry{}, fmt.Errorf("agent %q not found in daemon config", name)
	}
	if err := d.validateEphemeralStart(entry, taskID); err != nil {
		return config.AgentEntry{}, err
	}
	return entry, nil
}

func (d *Daemon) startAgentProcess(
	entry config.AgentEntry,
	taskID,
	parentSessionID string,
	reserveOwnership bool,
) (*supervisor.AgentProcess, error) {
	name := entry.Worktree
	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()
	if daemonShutdownRequested(d.sup.Shutdown) {
		return nil, errors.New("daemon is shutting down")
	}
	if d.isAgentRunning(name) {
		return nil, fmt.Errorf("agent %q is already running (use restart to reset a running agent)", name)
	}

	// StoppedAgents and the runtime slice form one lifecycle decision. Keep the
	// marker removal, add, rollback, and durable acknowledgement under
	// drainAddMu so a concurrent stop cannot leave a live agent marked stopped.
	d.sup.AgentsMu.Lock()
	delete(d.sup.StoppedAgents, name)
	d.sup.AgentsMu.Unlock()

	var replacement *supervisor.AgentProcess
	var err error
	if reserveOwnership {
		replacement, err = d.sup.AddAgentForTaskWithOwnershipReservation(entry, taskID, parentSessionID)
	} else {
		err = d.sup.AddAgentForTask(entry, taskID, parentSessionID)
	}
	if err != nil {
		// Re-add to StoppedAgents on failure
		d.sup.AgentsMu.Lock()
		d.sup.StoppedAgents[name] = struct{}{}
		d.sup.AgentsMu.Unlock()
		return nil, fmt.Errorf("failed to start agent %q: %v", name, err)
	}
	d.markAgentStartAccepted(name)
	return replacement, nil
}

func agentControlStartIDs(taskIDs []string) (string, string) {
	var taskID, parentSessionID string
	if len(taskIDs) > 0 {
		taskID = taskIDs[0]
	}
	if len(taskIDs) > 1 {
		parentSessionID = taskIDs[1]
	}
	return taskID, parentSessionID
}

func (d *Daemon) markAgentStartAccepted(name string) {
	desired := domain.AgentDesiredRunning
	state := domain.AgentStateActive
	if err := d.markAgentLifecycleAccepted(name, state, desired); err != nil {
		slog.Warn("failed to mark agent start accepted", "worktree", name, "err", err)
	}
}

func (d *Daemon) markAgentStopAccepted(name string) error {
	return d.markAgentLifecycleAccepted(
		name,
		domain.AgentStateStopped,
		domain.AgentDesiredStopped,
	)
}

func (d *Daemon) markAgentLifecycleAccepted(
	name string,
	state domain.AgentState,
	desired domain.AgentDesiredState,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	d.reconcileMu.Lock()
	defer d.reconcileMu.Unlock()
	if d.store != nil && d.sup != nil && d.sup.WorkspaceID != "" {
		if _, err := d.store.Agents().Update(ctx, d.sup.WorkspaceID, name, store.AgentUpdate{
			DesiredState: &desired,
			State:        &state,
		}); err != nil {
			return err
		}
	}
	d.setConfigAgentDesiredStateLocked(name, desired)
	return nil
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

	// configSnapshot returns its pointer after releasing reconcileMu. Keep those
	// published snapshots immutable by replacing the top-level config and agent
	// slice instead of mutating an AgentEntry in place.
	updated := *d.config
	updated.Agents = append([]config.AgentEntry(nil), d.config.Agents...)
	for i := range updated.Agents {
		if updated.Agents[i].Worktree == name {
			updated.Agents[i].DesiredState = desired
			d.config = &updated
			d.configHash = computeConfigHash(d.config)
			return
		}
	}
}

// handleAgentControlRestart restarts an agent (works for both running and stopped agents).
func (d *Daemon) handleAgentControlRestart(name string) DaemonControlResponse {
	resp, _ := d.handleAgentControlRestartInternal(name, false)
	return resp
}

// handleAgentControlRestartCommand reserves the replacement AgentProcess's
// first ownership lease until the durable command is terminal. The caller must
// wait for its explicit ownership signal and release the reservation.
func (d *Daemon) handleAgentControlRestartCommand(name string) (DaemonControlResponse, *supervisor.AgentProcess) {
	return d.handleAgentControlRestartInternal(name, true)
}

func (d *Daemon) handleAgentControlRestartInternal(
	name string,
	reserveReplacementOwnership bool,
) (DaemonControlResponse, *supervisor.AgentProcess) {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}, nil
	}

	entry, err := d.agentEntryForControlRestart(name)
	if err != nil {
		return DaemonControlResponse{Error: err.Error()}, nil
	}
	replacement, err := d.restartAgentProcess(entry, reserveReplacementOwnership)
	if err != nil {
		return DaemonControlResponse{Error: err.Error()}, nil
	}

	slog.Info("agent restarted via control socket", "worktree", name)
	return DaemonControlResponse{Success: true}, replacement
}

func (d *Daemon) agentEntryForControlRestart(name string) (config.AgentEntry, error) {
	// Validate agent exists in config
	if !d.agentExistsInConfig(name) {
		return config.AgentEntry{}, fmt.Errorf("agent %q not found in daemon config", name)
	}
	entry, ok := d.findAgentEntry(name)
	if !ok {
		return config.AgentEntry{}, fmt.Errorf("agent %q not found in current config", name)
	}
	if entry.Mode == domain.AgentModeEphemeral {
		return config.AgentEntry{}, fmt.Errorf(
			"ephemeral agent %q cannot be restarted; rerun the task to create a new worker attempt",
			name,
		)
	}
	return entry, nil
}

func (d *Daemon) restartAgentProcess(
	entry config.AgentEntry,
	reserveReplacementOwnership bool,
) (*supervisor.AgentProcess, error) {
	name := entry.Worktree
	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()
	if daemonShutdownRequested(d.sup.Shutdown) {
		return nil, errors.New("daemon is shutting down")
	}
	isStopped := d.isAgentStopped(name) || !d.isAgentRunning(name)

	// If running, drain it first
	if !isStopped {
		if err := d.sup.DrainAgentWithReason(name, supervisor.StopReasonManualStop); err != nil {
			return nil, fmt.Errorf("failed to stop agent %q for restart: %v", name, err)
		}
	}

	// Remove from StoppedAgents if present
	d.sup.AgentsMu.Lock()
	delete(d.sup.StoppedAgents, name)
	d.sup.AgentsMu.Unlock()

	// Add the agent back with fresh state. Durable lifecycle commands reserve
	// the first ownership generation until FleetDB accepts terminal completion.
	var replacement *supervisor.AgentProcess
	var err error
	if reserveReplacementOwnership {
		replacement, err = d.sup.AddAgentWithOwnershipReservation(entry)
	} else {
		err = d.sup.AddAgent(entry)
	}
	if err != nil {
		d.sup.AgentsMu.Lock()
		d.sup.StoppedAgents[name] = struct{}{}
		d.sup.AgentsMu.Unlock()
		return nil, fmt.Errorf("failed to restart agent %q: %v", name, err)
	}
	d.markAgentStartAccepted(name)
	return replacement, nil
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
func (d *Daemon) handleAgentControlYield(name string) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

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
