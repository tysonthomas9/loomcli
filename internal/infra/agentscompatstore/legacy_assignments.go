package agentscompatstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

//nolint:funlen // Keep legacy input normalization, canonical create, and compatibility result validation in one atomic adapter operation.
func (adapter *Adapter) CreateSupervisedAssignment(
	ctx context.Context,
	command agents.CreateSupervisedAssignmentCommand,
) (*agents.SupervisedAssignment, error) {
	if err := validateSupervisedAssignmentCoordinates(command.WorkspaceKey, command.AgentName); err != nil {
		return nil, err
	}
	if err := validateCanonicalRequired("role name", command.RoleName); err != nil {
		return nil, err
	}
	if err := validateCanonicalOptional("backend", command.Backend); err != nil {
		return nil, err
	}
	if err := validateAgentMode(command.Mode); err != nil {
		return nil, err
	}
	if err := validateDesiredState(command.DesiredState, true); err != nil {
		return nil, err
	}
	persistence, err := adapter.supervisedAssignments()
	if err != nil {
		return nil, err
	}
	created, err := persistence.Create(ctx, store.AgentCreate{
		WorkspaceKey:     command.WorkspaceKey,
		Name:             command.AgentName,
		RoleName:         command.RoleName,
		Auto:             command.Auto,
		Backend:          command.Backend,
		FallbackBackends: slices.Clone(command.FallbackBackends),
		Repos:            slices.Clone(command.Repos),
		RepoGroups:       slices.Clone(command.RepoGroups),
		CrossRepo:        command.CrossRepo,
		Mode:             domain.AgentMode(command.Mode),
		TaskFilter:       command.TaskFilter,
		MaxConcurrency:   command.MaxConcurrency,
		BudgetPolicy:     command.BudgetPolicy,
		DesiredState:     domain.AgentDesiredState(command.DesiredState),
	})
	if err != nil {
		return nil, err
	}
	if err := validateSupervisedAssignmentResult(
		created,
		command.WorkspaceKey,
		command.AgentName,
	); err != nil {
		return nil, err
	}
	if created.RoleName != command.RoleName {
		return nil, agents.ErrInvalidPersistedState
	}
	return supervisedAssignmentFromDomain(created), nil
}

func (adapter *Adapter) UpdateSupervisedAssignmentIntent(
	ctx context.Context,
	command agents.UpdateSupervisedAssignmentIntentCommand,
) (*agents.SupervisedAssignment, error) {
	if err := validateSupervisedAssignmentCoordinates(command.WorkspaceKey, command.AgentName); err != nil {
		return nil, err
	}
	if err := validateSupervisedAssignmentIntent(command.Patch, true); err != nil {
		return nil, err
	}
	return adapter.updateSupervisedAssignment(
		ctx,
		command.WorkspaceKey,
		command.AgentName,
		supervisedAssignmentIntentStorePatch(command.Patch),
	)
}

func (adapter *Adapter) BindSupervisedAssignmentParent(
	ctx context.Context,
	command agents.BindSupervisedAssignmentParentCommand,
) (*agents.SupervisedAssignment, error) {
	if err := validateSupervisedAssignmentCoordinates(command.WorkspaceKey, command.AgentName); err != nil {
		return nil, err
	}
	if err := validateCanonicalOptional("parent", command.Parent); err != nil {
		return nil, err
	}
	if command.ExpectedParent != nil {
		if err := validateCanonicalOptional("expected parent", *command.ExpectedParent); err != nil {
			return nil, err
		}
		persistence, err := adapter.supervisedAssignments()
		if err != nil {
			return nil, err
		}
		current, err := persistence.Get(ctx, command.WorkspaceKey, command.AgentName)
		if err != nil {
			return nil, fmt.Errorf("get agent: %w", err)
		}
		if err := validateSupervisedAssignmentResult(
			current,
			command.WorkspaceKey,
			command.AgentName,
		); err != nil {
			return nil, err
		}
		if current.Parent != *command.ExpectedParent {
			return nil, fmt.Errorf(
				"agent %q parent changed from %q to %q: %w",
				command.AgentName,
				*command.ExpectedParent,
				current.Parent,
				domain.ErrConflict,
			)
		}
	}
	updated, err := adapter.updateSupervisedAssignment(
		ctx,
		command.WorkspaceKey,
		command.AgentName,
		store.AgentUpdate{Parent: clonePointer(&command.Parent)},
	)
	if err != nil && command.ExpectedParent != nil {
		return nil, fmt.Errorf("update agent parent: %w", err)
	}
	return updated, err
}

func (adapter *Adapter) RetireSupervisedAssignment(
	ctx context.Context,
	command agents.RetireSupervisedAssignmentCommand,
) error {
	if err := validateSupervisedAssignmentCoordinates(command.WorkspaceKey, command.AgentName); err != nil {
		return err
	}
	persistence, err := adapter.supervisedAssignments()
	if err != nil {
		return err
	}
	if err := persistence.Delete(ctx, command.WorkspaceKey, command.AgentName); err != nil &&
		!errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return nil
}

func (adapter *Adapter) supervisedAssignments() (store.AgentStore, error) {
	if adapter == nil || adapter.assignments == nil {
		return nil, fmt.Errorf("compose supervised assignment commands: %w", agents.ErrUnavailable)
	}
	return adapter.assignments, nil
}

func (adapter *Adapter) updateSupervisedAssignment(
	ctx context.Context,
	workspace, name string,
	patch store.AgentUpdate,
) (*agents.SupervisedAssignment, error) {
	persistence, err := adapter.supervisedAssignments()
	if err != nil {
		return nil, err
	}
	updated, err := persistence.Update(ctx, workspace, name, patch)
	if err != nil {
		return nil, err
	}
	if err := validateSupervisedAssignmentResult(updated, workspace, name); err != nil {
		return nil, err
	}
	if err := validateSupervisedAssignmentPatchResult(updated, patch); err != nil {
		return nil, err
	}
	return supervisedAssignmentFromDomain(updated), nil
}

func supervisedAssignmentIntentStorePatch(
	patch agents.SupervisedAssignmentIntentPatch,
) store.AgentUpdate {
	return store.AgentUpdate{
		RoleName:         clonePointer(patch.RoleName),
		Auto:             clonePointer(patch.Auto),
		Backend:          clonePointer(patch.Backend),
		FallbackBackends: cloneSlicePointer(patch.FallbackBackends),
		Repos:            cloneSlicePointer(patch.Repos),
		RepoGroups:       cloneSlicePointer(patch.RepoGroups),
		CrossRepo:        clonePointer(patch.CrossRepo),
		Mode:             convertedPointer(patch.Mode, func(value agents.SupervisedAssignmentMode) domain.AgentMode { return domain.AgentMode(value) }),
		TaskFilter:       clonePointer(patch.TaskFilter),
		MaxConcurrency:   clonePointer(patch.MaxConcurrency),
		BudgetPolicy:     clonePointer(patch.BudgetPolicy),
		DesiredState: convertedPointer(
			patch.DesiredState,
			func(value agents.SupervisedAssignmentDesiredState) domain.AgentDesiredState {
				return domain.AgentDesiredState(value)
			},
		),
	}
}

func validateSupervisedAssignmentCoordinates(workspace, name string) error {
	if err := validateCanonicalRequired("workspace", workspace); err != nil {
		return err
	}
	return validateCanonicalRequired("agent name", name)
}

func validateSupervisedAssignmentIntent(
	patch agents.SupervisedAssignmentIntentPatch,
	requireFields bool,
) error {
	if requireFields && !supervisedAssignmentIntentHasFields(patch) {
		return fmt.Errorf("assignment intent patch must change at least one field: %w", agents.ErrInvalid)
	}
	if patch.RoleName != nil {
		if err := validateCanonicalRequired("role name", *patch.RoleName); err != nil {
			return err
		}
	}
	if patch.Backend != nil {
		if err := validateCanonicalOptional("backend", *patch.Backend); err != nil {
			return err
		}
	}
	if patch.Mode != nil {
		if err := validateAgentMode(*patch.Mode); err != nil {
			return err
		}
	}
	if patch.DesiredState != nil {
		if err := validateDesiredState(*patch.DesiredState, false); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentMode(mode agents.SupervisedAssignmentMode) error {
	switch mode {
	case "", agents.SupervisedAssignmentModeEphemeral, agents.SupervisedAssignmentModeService:
		return nil
	default:
		return fmt.Errorf("invalid assignment mode %q: %w", mode, agents.ErrInvalid)
	}
}

func validateDesiredState(state agents.SupervisedAssignmentDesiredState, allowEmpty bool) error {
	switch state {
	case agents.SupervisedAssignmentDesiredStopped, agents.SupervisedAssignmentDesiredIdle,
		agents.SupervisedAssignmentDesiredRunning, agents.SupervisedAssignmentDesiredDraining:
		return nil
	case "":
		if allowEmpty {
			return nil
		}
	}
	return fmt.Errorf("invalid assignment desired state %q: %w", state, agents.ErrInvalid)
}

func validateCanonicalRequired(label, value string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be canonical: %w", label, agents.ErrInvalid)
	}
	return nil
}

func validateCanonicalOptional(label, value string) error {
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be canonical: %w", label, agents.ErrInvalid)
	}
	return nil
}

func validateSupervisedAssignmentResult(
	assignment *domain.Agent,
	workspace, name string,
) error {
	if assignment == nil ||
		assignment.WorkspaceKey != workspace ||
		assignment.Name != name ||
		strings.TrimSpace(assignment.RoleName) == "" ||
		assignment.RoleName != strings.TrimSpace(assignment.RoleName) {
		return agents.ErrInvalidPersistedState
	}
	return nil
}

//nolint:cyclop // This exhaustive field-by-field postcondition check must fail closed when any requested canonical mutation is absent.
func validateSupervisedAssignmentPatchResult(
	assignment *domain.Agent,
	patch store.AgentUpdate,
) error {
	if (patch.RoleName != nil && assignment.RoleName != *patch.RoleName) ||
		(patch.Auto != nil && assignment.Auto != *patch.Auto) ||
		(patch.Backend != nil && assignment.Backend != *patch.Backend) ||
		(patch.FallbackBackends != nil && !slices.Equal(assignment.FallbackBackends, *patch.FallbackBackends)) ||
		(patch.Repos != nil && !slices.Equal(assignment.Repos, *patch.Repos)) ||
		(patch.RepoGroups != nil && !slices.Equal(assignment.RepoGroups, *patch.RepoGroups)) ||
		(patch.CrossRepo != nil && assignment.CrossRepo != *patch.CrossRepo) ||
		(patch.Mode != nil && assignment.Mode != *patch.Mode) ||
		(patch.TaskFilter != nil && assignment.TaskFilter != *patch.TaskFilter) ||
		(patch.MaxConcurrency != nil && assignment.MaxConcurrency != *patch.MaxConcurrency) ||
		(patch.BudgetPolicy != nil && assignment.BudgetPolicy != *patch.BudgetPolicy) ||
		(patch.DesiredState != nil && assignment.DesiredState != *patch.DesiredState) {
		return agents.ErrInvalidPersistedState
	}
	return nil
}

func supervisedAssignmentIntentHasFields(patch agents.SupervisedAssignmentIntentPatch) bool {
	return patch.RoleName != nil ||
		patch.Auto != nil ||
		patch.Backend != nil ||
		patch.FallbackBackends != nil ||
		patch.Repos != nil ||
		patch.RepoGroups != nil ||
		patch.CrossRepo != nil ||
		patch.Mode != nil ||
		patch.TaskFilter != nil ||
		patch.MaxConcurrency != nil ||
		patch.BudgetPolicy != nil ||
		patch.DesiredState != nil
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSlicePointer[T any](value *[]T) *[]T {
	if value == nil {
		return nil
	}
	cloned := slices.Clone(*value)
	return &cloned
}

func convertedPointer[From, To any](value *From, convert func(From) To) *To {
	if value == nil {
		return nil
	}
	converted := convert(*value)
	return &converted
}

func supervisedAssignmentFromDomain(value *domain.Agent) *agents.SupervisedAssignment {
	if value == nil {
		return nil
	}
	return &agents.SupervisedAssignment{
		WorkspaceKey:     value.WorkspaceKey,
		Name:             value.Name,
		RoleName:         value.RoleName,
		Auto:             value.Auto,
		Backend:          value.Backend,
		FallbackBackends: slices.Clone(value.FallbackBackends),
		Repos:            slices.Clone(value.Repos),
		RepoGroups:       slices.Clone(value.RepoGroups),
		CrossRepo:        value.CrossRepo,
		Parent:           value.Parent,
		State:            agents.SupervisedAssignmentState(value.State),
		Mode:             agents.SupervisedAssignmentMode(value.Mode),
		TaskFilter:       value.TaskFilter,
		MaxConcurrency:   value.MaxConcurrency,
		BudgetPolicy:     value.BudgetPolicy,
		DesiredState:     agents.SupervisedAssignmentDesiredState(value.DesiredState),
		CreatedAt:        value.CreatedAt,
		UpdatedAt:        value.UpdatedAt,
		LiveStatus:       agents.SupervisedAssignmentLiveStatus(value.LiveStatus),
		ActiveTaskID:     value.ActiveTaskID,
		ActivePhase:      value.ActivePhase,
		LastErrorClass:   value.LastErrorClass,
	}
}
