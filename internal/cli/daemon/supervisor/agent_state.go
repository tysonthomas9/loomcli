package supervisor

import (
	"context"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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
