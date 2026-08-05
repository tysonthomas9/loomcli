package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (s *Service) CreateManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command CreateManagedBindingCommand) (*Binding, error) {
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	agentServiceID, err := requireCanonical("agent service id", command.AgentServiceID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireOperator(ActionCreateManagedBinding, workspace, auth); err != nil {
		return nil, err
	}
	if s.bindings == nil {
		return nil, ErrUnavailable
	}
	if command.Definition.TargetAgentServiceID != agentServiceID {
		return nil, ErrManagedBinding
	}

	binding, err := s.bindingFromDefinition(ctx, workspace, command.Definition, true)
	if err != nil {
		return nil, err
	}
	if err := requireManagedBindingOwner(binding, agentServiceID); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	binding.CreatedAt = now
	binding.UpdatedAt = now
	if s.managedBindings == nil {
		return nil, ErrUnavailable
	}
	persisted, err := s.managedBindings.CreateManagedBinding(ctx, cloneBinding(binding))
	if err != nil {
		return nil, fmt.Errorf("create managed binding %q: %w", binding.BindingID, err)
	}
	if err := validatePersistedBinding(persisted, workspace, binding.BindingID); err != nil {
		return nil, err
	}
	if err := requireManagedBindingOwner(persisted, agentServiceID); err != nil {
		return nil, ErrInvalidPersistedState
	}
	return cloneBinding(persisted), nil
}

func (s *Service) UpdateManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command UpdateManagedBindingCommand) (*Binding, error) {
	normalized, err := normalizeManagedBindingCommand(ManagedBindingCommand{
		WorkspaceKey: command.WorkspaceKey, BindingID: command.BindingID, AgentServiceID: command.AgentServiceID,
	})
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireOperator(ActionUpdateManagedBinding, normalized.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.bindings == nil || s.managedBindings == nil {
		return nil, ErrUnavailable
	}
	existing, err := s.loadBinding(ctx, normalized.WorkspaceKey, normalized.BindingID)
	if err != nil {
		return nil, err
	}
	if err := requireManagedBindingOwner(existing, normalized.AgentServiceID); err != nil {
		return nil, err
	}
	if command.Patch.TargetAgentServiceID != nil && *command.Patch.TargetAgentServiceID != normalized.AgentServiceID {
		return nil, ErrManagedBinding
	}
	updated, err := s.bindingFromPatch(ctx, normalized.WorkspaceKey, existing, command.Patch)
	if err != nil {
		return nil, err
	}
	if err := requireManagedBindingOwner(updated, normalized.AgentServiceID); err != nil {
		return nil, err
	}
	persisted, err := s.managedBindings.ReplaceManagedBinding(ctx, ManagedBindingReplacement{
		Expected: managedBindingSnapshot(existing, normalized.AgentServiceID),
		Binding:  cloneBinding(updated),
	})
	if err != nil {
		return nil, fmt.Errorf("update managed binding %q: %w", updated.BindingID, err)
	}
	if err := validatePersistedBinding(persisted, normalized.WorkspaceKey, normalized.BindingID); err != nil {
		return nil, err
	}
	if err := requireManagedBindingOwner(persisted, normalized.AgentServiceID); err != nil {
		return nil, ErrInvalidPersistedState
	}
	return cloneBinding(persisted), nil
}

func (s *Service) EnableManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command ManagedBindingCommand) (*Binding, error) {
	return s.setManagedBindingEnabled(ctx, auth, command, true)
}

func (s *Service) DisableManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command ManagedBindingCommand) (*Binding, error) {
	return s.setManagedBindingEnabled(ctx, auth, command, false)
}

func (s *Service) setManagedBindingEnabled(ctx context.Context, auth authority.OperatorAuthority, command ManagedBindingCommand, enabled bool) (*Binding, error) {
	command, err := normalizeManagedBindingCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	action := ActionDisableManagedBinding
	if enabled {
		action = ActionEnableManagedBinding
	}
	if err := s.authority.RequireOperator(action, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.bindings == nil || s.managedBindings == nil {
		return nil, ErrUnavailable
	}
	binding, err := s.loadBinding(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, err
	}
	if err := requireManagedBindingOwner(binding, command.AgentServiceID); err != nil {
		return nil, err
	}
	expected := managedBindingSnapshot(binding, command.AgentServiceID)
	binding.Enabled = enabled
	binding.UpdatedAt = nextBindingRevision(binding.UpdatedAt, s.now())
	persisted, err := s.managedBindings.ReplaceManagedBinding(ctx, ManagedBindingReplacement{
		Expected: expected,
		Binding:  cloneBinding(binding),
	})
	if err != nil {
		return nil, fmt.Errorf("set managed binding %q enabled=%t: %w", binding.BindingID, enabled, err)
	}
	if err := validatePersistedBinding(persisted, command.WorkspaceKey, command.BindingID); err != nil {
		return nil, err
	}
	if err := requireManagedBindingOwner(persisted, command.AgentServiceID); err != nil || persisted.Enabled != enabled {
		return nil, ErrInvalidPersistedState
	}
	return cloneBinding(persisted), nil
}

func (s *Service) DeleteManagedBinding(ctx context.Context, auth authority.OperatorAuthority, command ManagedBindingCommand) error {
	command, err := normalizeManagedBindingCommand(command)
	if err != nil {
		return err
	}
	if s == nil || s.authority == nil {
		return authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireOperator(ActionDeleteManagedBinding, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if s.bindings == nil || s.managedBindings == nil {
		return ErrUnavailable
	}
	binding, err := s.loadBinding(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if err := requireManagedBindingOwner(binding, command.AgentServiceID); err != nil {
		return err
	}
	if binding.Enabled {
		return ErrBindingEnabled
	}
	expected := managedBindingSnapshot(binding, command.AgentServiceID)
	if err := s.managedBindings.DeleteManagedBindingIfUnchanged(ctx, expected); err != nil {
		deleteErr := fmt.Errorf("delete managed binding %q: %w", command.BindingID, err)
		current, probeErr := s.loadBinding(ctx, command.WorkspaceKey, command.BindingID)
		switch {
		case errors.Is(probeErr, ErrNotFound):
			// The conditional delete committed but its response was lost.
			return nil
		case probeErr != nil:
			// Preserve the original transport/storage failure when the single
			// reconciliation probe cannot establish the committed outcome.
			return deleteErr
		case current.Enabled || !managedBindingMatchesSnapshot(current, expected):
			// The same ID now identifies a different revision/generation. Never
			// treat that row as the result of this stale delete command.
			return fmt.Errorf("delete managed binding %q observed a changed generation after an uncertain outcome: %w", command.BindingID, ErrManagedBinding)
		default:
			// The original row still exists unchanged, so this was a genuine
			// pre-commit failure rather than a lost successful response.
			return deleteErr
		}
	}
	return nil
}

func managedBindingSnapshot(binding *Binding, agentServiceID string) ManagedBindingSnapshot {
	if binding == nil {
		return ManagedBindingSnapshot{}
	}
	return ManagedBindingSnapshot{
		WorkspaceKey:                 binding.WorkspaceKey,
		BindingID:                    binding.BindingID,
		ExpectedTargetAgentServiceID: agentServiceID,
		ExpectedRouteKey:             binding.RouteKey,
		ExpectedCreatedAt:            binding.CreatedAt,
		ExpectedUpdatedAt:            binding.UpdatedAt,
	}
}

func managedBindingMatchesSnapshot(binding *Binding, expected ManagedBindingSnapshot) bool {
	return binding != nil && binding.WorkspaceKey == expected.WorkspaceKey && binding.BindingID == expected.BindingID &&
		binding.TargetAgentServiceID == expected.ExpectedTargetAgentServiceID && binding.RouteKey == expected.ExpectedRouteKey &&
		binding.CreatedAt.Equal(expected.ExpectedCreatedAt) && binding.UpdatedAt.Equal(expected.ExpectedUpdatedAt)
}

// PostgreSQL persists timestamps at microsecond precision. Advancing by at
// least one microsecond guarantees that any committed managed mutation changes
// the revision in both PostgreSQL and Redis, even under a fixed/coarse clock.
func nextBindingRevision(previous, now time.Time) time.Time {
	now = now.UTC()
	minimum := previous.UTC().Add(time.Microsecond)
	if now.Before(minimum) {
		return minimum
	}
	return now
}

func normalizeManagedBindingCommand(command ManagedBindingCommand) (ManagedBindingCommand, error) {
	binding, err := normalizeBindingCommand(BindingCommand{WorkspaceKey: command.WorkspaceKey, BindingID: command.BindingID})
	if err != nil {
		return ManagedBindingCommand{}, err
	}
	agentServiceID, err := requireCanonical("agent service id", command.AgentServiceID)
	if err != nil {
		return ManagedBindingCommand{}, err
	}
	return ManagedBindingCommand{
		WorkspaceKey: binding.WorkspaceKey, BindingID: binding.BindingID, AgentServiceID: agentServiceID,
	}, nil
}

func requireManagedBindingOwner(binding *Binding, agentServiceID string) error {
	if binding == nil || strings.TrimSpace(agentServiceID) == "" ||
		strings.TrimSpace(binding.TargetAgentServiceID) == "" ||
		binding.TargetAgentServiceID != strings.TrimSpace(binding.TargetAgentServiceID) ||
		binding.TargetAgentServiceID != agentServiceID {
		return ErrManagedBinding
	}
	return nil
}
