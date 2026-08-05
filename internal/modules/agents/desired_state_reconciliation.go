package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (s *Service) ReconcileDesiredState(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ReconcileDesiredStateCommand,
) (ReconcileDesiredStateResult, error) {
	command = normalizeReconcileDesiredStateCommand(command)
	result := ReconcileDesiredStateResult{WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID}
	if err := validateReconcileDesiredStateCommand(command); err != nil {
		return result, fmt.Errorf("reconciliation coordinates and revision are required: %w", ErrInvalid)
	}
	if err := s.requireSystem(ActionReconcileDesiredState, command.WorkspaceKey, auth); err != nil {
		return result, err
	}
	if s == nil || s.reader == nil || s.bindings == nil || s.lifecycle == nil {
		return result, ErrUnavailable
	}
	agent, err := s.loadDesiredStateReconciliationAgent(ctx, command)
	if err != nil {
		return result, err
	}
	bindingStates, err := s.bindings.ListAgentBindingStates(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return result, fmt.Errorf("list Agent binding states: %w", err)
	}
	wantEnabled := agent.DesiredState == DesiredRunning
	if bindingStatesConverged(bindingStates, wantEnabled) {
		result.Converged = true
		return result, nil
	}
	reconciled, err := s.lifecycle.ApplyLifecycle(ctx, ApplyLifecycleMutation{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		Action: LifecycleReconcile, ExpectedUpdatedAt: command.ExpectedUpdatedAt,
		ExpectedGenerationID: command.GenerationID,
		IdempotencyKey:       desiredStateReconciliationKey(command), ChangedBy: auth.Subject(),
	})
	if err != nil {
		return result, fmt.Errorf("repair Agent desired-state projection: %w", err)
	}
	if !validDesiredStateReconciliationResult(reconciled, agent, command) {
		return result, ErrInvalidPersistedState
	}
	result.Converged = true
	result.Repaired = true
	return result, nil
}

func normalizeReconcileDesiredStateCommand(command ReconcileDesiredStateCommand) ReconcileDesiredStateCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.AgentID = strings.TrimSpace(command.AgentID)
	return command
}

func validateReconcileDesiredStateCommand(command ReconcileDesiredStateCommand) error {
	if command.WorkspaceKey == "" || command.AgentID == "" || command.ExpectedUpdatedAt.IsZero() ||
		!ValidGenerationID(command.GenerationID) {
		return ErrInvalid
	}
	return nil
}

func (s *Service) loadDesiredStateReconciliationAgent(
	ctx context.Context,
	command ReconcileDesiredStateCommand,
) (*Agent, error) {
	agent, err := s.reader.GetAgent(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, fmt.Errorf("load Agent desired state: %w", err)
	}
	if err := validatePersistedAgent(agent, command.WorkspaceKey, command.AgentID); err != nil {
		return nil, err
	}
	if agent.GenerationID != command.GenerationID || !agent.UpdatedAt.Equal(command.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("Agent changed before desired-state reconciliation: %w", ErrConflict)
	}
	return agent, nil
}

func validDesiredStateReconciliationResult(
	result *LifecycleResult,
	agent *Agent,
	command ReconcileDesiredStateCommand,
) bool {
	return result != nil && result.Agent != nil && result.Action == LifecycleReconcile &&
		result.WorkspaceKey == command.WorkspaceKey && result.AgentID == command.AgentID &&
		result.Agent.DesiredState == agent.DesiredState && result.Agent.GenerationID == command.GenerationID &&
		result.Agent.UpdatedAt.Equal(result.CommittedAt)
}

func bindingStatesConverged(states []bool, wantEnabled bool) bool {
	for _, enabled := range states {
		if enabled != wantEnabled {
			return false
		}
	}
	return true
}

func desiredStateReconciliationKey(command ReconcileDesiredStateCommand) string {
	return fmt.Sprintf("desired-state-%s-%d", command.GenerationID, command.ExpectedUpdatedAt.UnixNano())
}
