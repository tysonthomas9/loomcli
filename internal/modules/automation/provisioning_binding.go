package automation

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// EnsureManagedBinding converges the exact managed binding recorded by an
// AgentProvisioning intent. Exact existing state succeeds without consulting
// the mutable active catalog version; a new binding is admitted only when the
// currently effective version still equals the intent's pinned version.
//
//nolint:funlen // Convergence keeps authority, idempotency, immutable-definition checks, and exact results together.
func (s *Service) EnsureManagedBinding(
	ctx context.Context,
	auth authority.SystemAuthority,
	command EnsureManagedBindingCommand,
) (*Binding, error) {
	normalized, err := normalizeEnsureManagedBindingCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.RequireSystem(ActionEnsureManagedBinding, normalized.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.bindings == nil || s.managedBindings == nil {
		return nil, ErrUnavailable
	}

	existing, err := s.loadBinding(ctx, normalized.WorkspaceKey, normalized.Definition.BindingID)
	switch {
	case err == nil:
		if err := validateExactManagedBinding(existing, normalized); err != nil {
			return nil, err
		}
		return cloneBinding(existing), nil
	case !errors.Is(err, ErrNotFound):
		return nil, err
	}

	binding, err := s.bindingFromDefinition(ctx, normalized.WorkspaceKey, normalized.Definition, true)
	if err != nil {
		return nil, err
	}
	if binding.DriverVersionID != normalized.Definition.DriverVersionID {
		return nil, fmt.Errorf(
			"effective driver version %q differs from provisioning intent %q: %w",
			binding.DriverVersionID,
			normalized.Definition.DriverVersionID,
			ErrConflict,
		)
	}
	if err := requireManagedBindingOwner(binding, normalized.AgentServiceID); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	binding.CreatedAt = now
	binding.UpdatedAt = now
	persisted, err := s.managedBindings.CreateManagedBinding(ctx, cloneBinding(binding))
	if err == nil {
		if err := validateExactManagedBinding(persisted, normalized); err != nil {
			return nil, err
		}
		return cloneBinding(persisted), nil
	}
	if !errors.Is(err, ErrConflict) {
		return nil, fmt.Errorf("create provisioned managed binding %q: %w", binding.BindingID, err)
	}

	existing, getErr := s.loadBinding(ctx, normalized.WorkspaceKey, normalized.Definition.BindingID)
	if getErr != nil {
		return nil, errors.Join(
			fmt.Errorf("create provisioned managed binding %q: %w", binding.BindingID, err),
			fmt.Errorf("read create winner: %w", getErr),
		)
	}
	if err := validateExactManagedBinding(existing, normalized); err != nil {
		return nil, err
	}
	return cloneBinding(existing), nil
}

func normalizeEnsureManagedBindingCommand(
	command EnsureManagedBindingCommand,
) (EnsureManagedBindingCommand, error) {
	requestID, err := requireCanonical("request id", command.RequestID)
	if err != nil {
		return EnsureManagedBindingCommand{}, err
	}
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return EnsureManagedBindingCommand{}, err
	}
	agentServiceID, err := requireCanonical("agent service id", command.AgentServiceID)
	if err != nil {
		return EnsureManagedBindingCommand{}, err
	}
	if command.Definition.TargetAgentServiceID != agentServiceID {
		return EnsureManagedBindingCommand{}, ErrManagedBinding
	}
	definition, err := prepareBindingDefinition(command.Definition)
	if err != nil {
		return EnsureManagedBindingCommand{}, err
	}
	if definition.DriverVersionID == "" {
		return EnsureManagedBindingCommand{}, fmt.Errorf(
			"provisioning binding requires a pinned driver version: %w",
			ErrInvalid,
		)
	}
	command.RequestID = requestID
	command.WorkspaceKey = workspace
	command.AgentServiceID = agentServiceID
	command.Definition = definition
	return command, nil
}

func validateExactManagedBinding(
	binding *Binding,
	command EnsureManagedBindingCommand,
) error {
	if err := validatePersistedBinding(
		binding,
		command.WorkspaceKey,
		command.Definition.BindingID,
	); err != nil {
		return err
	}
	if err := requireManagedBindingOwner(binding, command.AgentServiceID); err != nil {
		return fmt.Errorf("provisioned binding owner differs from intent: %w", ErrConflict)
	}
	actual := definitionFromBinding(binding)
	if !bindingDefinitionsEqual(actual, command.Definition) {
		return fmt.Errorf(
			"binding %q already exists with a different definition: %w",
			command.Definition.BindingID,
			ErrConflict,
		)
	}
	return nil
}

//nolint:cyclop // Equality intentionally enumerates the complete immutable binding definition without reflection.
func bindingDefinitionsEqual(left, right BindingDefinition) bool {
	return left.BindingID == right.BindingID &&
		left.Name == right.Name &&
		left.SourceKind == right.SourceKind &&
		left.SourceRef == right.SourceRef &&
		left.SourceConfigRef == right.SourceConfigRef &&
		left.RouteKey == right.RouteKey &&
		left.Method == right.Method &&
		left.PathTemplate == right.PathTemplate &&
		left.Topic == right.Topic &&
		slices.Equal(left.EventTypePatterns, right.EventTypePatterns) &&
		left.FilterRef == right.FilterRef &&
		left.DriverID == right.DriverID &&
		left.DriverVersionID == right.DriverVersionID &&
		left.TargetEntrypoint == right.TargetEntrypoint &&
		left.TargetAgentServiceID == right.TargetAgentServiceID &&
		left.ConcurrencyPolicy == right.ConcurrencyPolicy &&
		left.IdempotencyPolicy == right.IdempotencyPolicy &&
		left.AuthPolicy == right.AuthPolicy &&
		left.SubjectKeyTemplate == right.SubjectKeyTemplate &&
		actorFiltersEqual(left.ActorFilter, right.ActorFilter) &&
		left.RetryMaxAttempts == right.RetryMaxAttempts &&
		left.RetryBackoffSeconds == right.RetryBackoffSeconds &&
		left.Schedule == right.Schedule &&
		left.ScheduleTimezone == right.ScheduleTimezone &&
		slices.Equal(left.Permissions, right.Permissions) &&
		left.Enabled == right.Enabled
}

func actorFiltersEqual(left, right *ActorFilter) bool {
	if left == nil || left.IsZero() {
		return right == nil || right.IsZero()
	}
	if right == nil || right.IsZero() {
		return false
	}
	return slices.Equal(left.ExcludeActorKinds, right.ExcludeActorKinds) &&
		slices.Equal(left.AllowActors, right.AllowActors)
}
