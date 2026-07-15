package supervisor

import (
	"context"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// checkAgentStopSignals checks shutdown and per-agent stop signals.
func (s *Supervisor) checkAgentStopSignals(ap *AgentProcess) bool {
	select {
	case <-s.Shutdown:
		slog.Info("shutdown signal received", "worktree", ap.Entry.Worktree)
		s.setShutdownStopReason(ap)
		return true
	case <-ap.StopCh:
		slog.Info("stop signal received", "worktree", ap.Entry.Worktree)
		s.setStopReasonDefault(ap, StopReasonConfigRemoved)
		return true
	default:
		return false
	}
}

// setShutdownStopReason unconditionally records that this agent stopped
// because of supervisor shutdown. Every caller (drain, signal handler,
// ownership transfer) uses the same reason; if a new code path ever needs
// a different reason, reintroduce the explicit parameter.
func (s *Supervisor) setShutdownStopReason(ap *AgentProcess) {
	ap.Mu.Lock()
	ap.StopReason = StopReasonShutdown
	ap.Mu.Unlock()
}

// setStopReasonDefault sets the agent's stop reason only if not already set.
func (s *Supervisor) setStopReasonDefault(ap *AgentProcess, reason StopReason) {
	ap.Mu.Lock()
	if ap.StopReason == "" {
		ap.StopReason = reason
	}
	ap.Mu.Unlock()
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
