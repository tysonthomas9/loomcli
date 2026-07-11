package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemonregistry"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func (s *Supervisor) startControlPlaneNode() error {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return nil
	}
	nodeID := s.resolveNodeID()
	ttl := s.NodeTTL
	if ttl <= 0 {
		ttl = defaultNodeTTL
	}
	interval := s.NodeInterval
	if interval <= 0 {
		interval = defaultNodeInterval
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    s.WorkspaceID,
		NodeID:          nodeID,
		OwnerActor:      resolveNodeOwnerActor(),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          s.daemonRuntimeLabels(),
		Capabilities:    []string{"local-supervisor", "agent-process"},
		Capacity:        len(s.Agents),
		DrainState:      domain.NodeDrainActive,
		TTL:             ttl,
	}); err != nil {
		return fmt.Errorf("register supervisor node %q: %w", nodeID, err)
	}
	s.NodeID = nodeID

	s.RegisterTick(GoroutineNodeHeartbeat)
	s.RunCritical(GoroutineNodeHeartbeat, func() {
		s.runNodeHeartbeat(nodeID, ttl, interval)
	})
	return nil
}

func (s *Supervisor) runNodeHeartbeat(nodeID string, ttl, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.Shutdown:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
			_, err := s.ControlStore.Nodes().Heartbeat(ctx, s.WorkspaceID, nodeID, ttl)
			cancel()
			if err != nil {
				slog.Warn("supervisor node heartbeat failed", "workspace", s.WorkspaceID, "node_id", nodeID, "err", err)
			}
			s.RecordTick(GoroutineNodeHeartbeat)
		}
	}
}

// daemonRuntimeLabels composes the loom.daemon.* Node labels that
// daemonregistry.Detect parses to report cwd-independent daemon
// liveness. The labels are the LOOM-3 fix: they let
// `loom workspace ops diagnose` recognize a supervisor regardless of
// the cwd it was launched from.
func (s *Supervisor) daemonRuntimeLabels() []string {
	labels := []string{
		daemonregistry.LabelPID + strconv.Itoa(os.Getpid()),
	}
	if s.ProjectDir != "" {
		labels = append(labels, daemonregistry.LabelCwd+s.ProjectDir)
	}
	if s.IpcSocketPath != "" {
		labels = append(labels, daemonregistry.LabelSocket+s.IpcSocketPath)
	}
	return labels
}

// RefreshNodeLabels re-publishes the supervisor's Node labels using
// the current Supervisor state. Call from startDaemonSockets after
// the IPC socket has actually bound — without the refresh, callers
// that mutate IpcSocketPath after Start (e.g., flipping it to "" on
// bind failure in a future code path) would not be reflected in the
// fleet-db Node row.
//
// Errors are logged at Warn level and not returned: the Socket label
// is informational, not load-bearing for the LOOM-3 liveness rule.
func (s *Supervisor) RefreshNodeLabels() {
	if s.ControlStore == nil || s.WorkspaceID == "" || s.NodeID == "" {
		return
	}
	labels := s.daemonRuntimeLabels()
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.Nodes().Update(ctx, s.WorkspaceID, s.NodeID, store.NodeUpdate{
		Labels: &labels,
	}); err != nil {
		slog.Warn("supervisor node label refresh failed", "workspace", s.WorkspaceID, "node_id", s.NodeID, "err", err)
	}
}

func (s *Supervisor) resolveNodeID() string {
	if s.NodeID != "" {
		return s.NodeID
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("loom-supervisor-%s-%d", host, os.Getpid())
}

func resolveNodeOwnerActor() string {
	for _, key := range []string{"LOOM_FLEET_DB_ACTOR", "LOOM_AGENT_NAME", "USER"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return "local"
}

func (s *Supervisor) markAgentActive(ap *AgentProcess) {
	s.updateAgentRuntimeState(ap, domain.AgentStateActive, nil)
}

func (s *Supervisor) markAgentStoppedOnExit(ap *AgentProcess) {
	var desired *domain.AgentDesiredState
	ap.Mu.Lock()
	stopReason := ap.StopReason
	ap.Mu.Unlock()
	if ap.Entry.Mode == domain.AgentModeEphemeral && stopReason == StopReasonEphemeralDone {
		stopped := domain.AgentDesiredStopped
		desired = &stopped
	}
	s.updateAgentRuntimeState(ap, domain.AgentStateStopped, desired)
}

func (s *Supervisor) updateAgentRuntimeState(ap *AgentProcess, state domain.AgentState, desired *domain.AgentDesiredState) {
	if s.ControlStore == nil || s.WorkspaceID == "" || ap == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	patch := store.AgentUpdate{State: &state}
	if desired != nil {
		patch.DesiredState = desired
	}
	if _, err := s.ControlStore.Agents().Update(ctx, s.WorkspaceID, ap.Entry.Worktree, patch); err != nil {
		slog.Warn("agent runtime state update failed", "worktree", ap.Entry.Worktree, "state", state, "err", err)
	}
}
