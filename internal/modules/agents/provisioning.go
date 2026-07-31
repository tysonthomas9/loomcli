package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// EnsureManagedRole converges one exact Role definition for the durable
// AgentProvisioning process manager. The natural workspace/name key makes a
// lost create response replayable; a same-key different definition is never
// silently adopted.
func (s *Service) EnsureManagedRole(
	ctx context.Context,
	auth authority.SystemAuthority,
	command EnsureRoleCommand,
) (*Role, error) {
	command, err := normalizeEnsureRoleCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireSystem(ActionEnsureManagedRole, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s == nil || s.roleStore == nil {
		return nil, ErrUnavailable
	}

	existing, err := s.roleStore.GetRole(ctx, command.WorkspaceKey, command.Role.Name)
	switch {
	case err == nil:
		if err := validateExactRole(existing, command.WorkspaceKey, command.Role); err != nil {
			return nil, err
		}
		return cloneRole(existing), nil
	case !errors.Is(err, ErrNotFound):
		return nil, fmt.Errorf("get managed role %q: %w", command.Role.Name, err)
	}

	created, err := s.roleStore.CreateRole(ctx, command.WorkspaceKey, cloneRoleDefinition(command.Role))
	if err == nil {
		if err := validateExactRole(created, command.WorkspaceKey, command.Role); err != nil {
			return nil, err
		}
		return cloneRole(created), nil
	}
	if !errors.Is(err, ErrAlreadyExists) && !errors.Is(err, ErrConflict) {
		return nil, fmt.Errorf("create managed role %q: %w", command.Role.Name, err)
	}

	// Resolve the create race or a lost response through the authoritative
	// aggregate read. Return the original create error if the winner cannot be
	// read so callers never mistake an ambiguous outcome for success.
	existing, getErr := s.roleStore.GetRole(ctx, command.WorkspaceKey, command.Role.Name)
	if getErr != nil {
		return nil, errors.Join(
			fmt.Errorf("create managed role %q: %w", command.Role.Name, err),
			fmt.Errorf("read create winner: %w", getErr),
		)
	}
	if err := validateExactRole(existing, command.WorkspaceKey, command.Role); err != nil {
		return nil, err
	}
	return cloneRole(existing), nil
}

// EnsureManagedAgent converges one exact Agent identity. Desired-state changes
// after provisioning remain explicit commands; this method only admits the
// immutable initial intent or an exact replay of it.
func (s *Service) EnsureManagedAgent(
	ctx context.Context,
	auth authority.SystemAuthority,
	command EnsureAgentCommand,
) (*Agent, error) {
	requestID, err := requireCanonical("request id", command.RequestID)
	if err != nil {
		return nil, err
	}
	create, err := normalizeCreateCommand(command.CreateAgentCommand)
	if err != nil {
		return nil, err
	}
	if err := s.requireSystem(ActionEnsureManagedAgent, create.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if err := s.validateRoleBackedBehavior(ctx, create.WorkspaceKey, create.Behavior); err != nil {
		return nil, err
	}
	if s == nil || s.reader == nil || s.identity == nil {
		return nil, ErrUnavailable
	}
	command.RequestID = requestID
	command.CreateAgentCommand = create

	existing, err := s.reader.GetAgent(ctx, create.WorkspaceKey, create.AgentID)
	switch {
	case err == nil:
		if err := validateExactAgent(existing, create); err != nil {
			return nil, err
		}
		return cloneAgent(existing), nil
	case !errors.Is(err, ErrNotFound):
		return nil, fmt.Errorf("get managed agent %q: %w", create.AgentID, err)
	}

	created, err := s.identity.CreateAgent(ctx, createMutation(create, auth.Subject()))
	if err == nil {
		return validateCreatedManagedAgent(created, create, auth.Subject())
	}
	if !errors.Is(err, ErrAlreadyExists) && !errors.Is(err, ErrConflict) {
		return nil, fmt.Errorf("create managed agent %q: %w", create.AgentID, err)
	}
	return s.resolveManagedAgentCreateRace(ctx, create, err)
}

func (s *Service) resolveManagedAgentCreateRace(
	ctx context.Context,
	command CreateAgentCommand,
	createErr error,
) (*Agent, error) {
	existing, getErr := s.reader.GetAgent(ctx, command.WorkspaceKey, command.AgentID)
	if getErr != nil {
		return nil, errors.Join(
			fmt.Errorf("create managed agent %q: %w", command.AgentID, createErr),
			fmt.Errorf("read create winner: %w", getErr),
		)
	}
	if err := validateExactAgent(existing, command); err != nil {
		return nil, err
	}
	return cloneAgent(existing), nil
}

func validateCreatedManagedAgent(agent *Agent, command CreateAgentCommand, subject string) (*Agent, error) {
	if err := validateExactAgent(agent, command); err != nil {
		return nil, err
	}
	if agent.CreatedBy != subject {
		return nil, ErrInvalidPersistedState
	}
	return cloneAgent(agent), nil
}

func createMutation(command CreateAgentCommand, createdBy string) CreateAgentMutation {
	return CreateAgentMutation{
		WorkspaceKey:    command.WorkspaceKey,
		AgentID:         command.AgentID,
		Name:            command.Name,
		Kind:            command.Kind,
		Behavior:        command.Behavior,
		DesiredState:    command.DesiredState,
		PlacementPolicy: command.PlacementPolicy,
		MaxInstances:    command.MaxInstances,
		RestartPolicy:   command.RestartPolicy,
		BudgetPolicy:    command.BudgetPolicy,
		Metadata:        cloneStringMap(command.Metadata),
		CreatedBy:       createdBy,
	}
}

func normalizeEnsureRoleCommand(command EnsureRoleCommand) (EnsureRoleCommand, error) {
	var err error
	command.RequestID, err = requireCanonical("request id", command.RequestID)
	if err != nil {
		return EnsureRoleCommand{}, err
	}
	command.WorkspaceKey, err = normalizeWorkspace(command.WorkspaceKey)
	if err != nil {
		return EnsureRoleCommand{}, err
	}
	command.Role, err = normalizeRoleDefinition(command.Role)
	if err != nil {
		return EnsureRoleCommand{}, err
	}
	return command, nil
}

func normalizeRoleDefinition(role RoleDefinition) (RoleDefinition, error) {
	var err error
	role.Name, err = requireCanonical("role name", role.Name)
	if err != nil {
		return RoleDefinition{}, err
	}
	for label, value := range map[string]*string{
		"role kind":   &role.Kind,
		"description": &role.Description,
		"prompt file": &role.PromptFile,
		"model":       &role.Model,
		"task filter": &role.TaskFilter,
		"backend":     &role.Backend,
		"effort":      &role.Effort,
	} {
		*value, err = normalizeOptional(label, *value)
		if err != nil {
			return RoleDefinition{}, err
		}
	}
	if role.MaxConcurrency != nil && *role.MaxConcurrency <= 0 {
		return RoleDefinition{}, fmt.Errorf("role max concurrency must be positive: %w", ErrInvalid)
	}
	if role.MaxBudgetUSD != nil && *role.MaxBudgetUSD < 0 {
		return RoleDefinition{}, fmt.Errorf("role max budget must not be negative: %w", ErrInvalid)
	}
	role.PathPatterns, err = normalizeCanonicalList("path pattern", role.PathPatterns)
	if err != nil {
		return RoleDefinition{}, err
	}
	role.Skills, err = normalizeCanonicalList("skill", role.Skills)
	if err != nil {
		return RoleDefinition{}, err
	}
	role.AllowedTools, err = normalizeCanonicalList("allowed tool", role.AllowedTools)
	if err != nil {
		return RoleDefinition{}, err
	}
	role.DeniedTools, err = normalizeCanonicalList("denied tool", role.DeniedTools)
	if err != nil {
		return RoleDefinition{}, err
	}
	role.MaxPriority = cloneInt(role.MaxPriority)
	role.MaxConcurrency = cloneInt(role.MaxConcurrency)
	role.MaxBudgetUSD = cloneFloat64(role.MaxBudgetUSD)
	return role, nil
}

func normalizeCanonicalList(label string, values []string) ([]string, error) {
	out := append([]string(nil), values...)
	for index, value := range out {
		normalized, err := requireCanonical(label, value)
		if err != nil {
			return nil, err
		}
		out[index] = normalized
	}
	return out, nil
}

func validateExactRole(role *Role, workspace string, definition RoleDefinition) error {
	if err := validatePersistedRole(role, workspace, definition.Name); err != nil {
		return err
	}
	actual := roleDefinitionFromRole(role)
	if !equalRoleDefinition(actual, definition) {
		return fmt.Errorf("role %q already exists with a different definition: %w", definition.Name, ErrConflict)
	}
	return nil
}

func validatePersistedRole(role *Role, workspace, name string) error {
	if role == nil || role.WorkspaceKey != workspace || name != "" && role.Name != name ||
		role.WorkspaceKey != strings.TrimSpace(role.WorkspaceKey) ||
		role.Name != strings.TrimSpace(role.Name) ||
		role.CreatedAt.IsZero() || role.UpdatedAt.IsZero() ||
		role.UpdatedAt.Before(role.CreatedAt) {
		return ErrInvalidPersistedState
	}
	normalized, err := normalizeRoleDefinition(roleDefinitionFromRole(role))
	if err != nil || !equalRoleDefinition(normalized, roleDefinitionFromRole(role)) {
		return ErrInvalidPersistedState
	}
	return nil
}

func validateExactAgent(agent *Agent, command CreateAgentCommand) error {
	if err := validatePersistedAgent(agent, command.WorkspaceKey, command.AgentID); err != nil {
		return err
	}
	if agent.DeletedAt != nil ||
		agent.Name != command.Name ||
		agent.Kind != command.Kind ||
		agent.Behavior != command.Behavior ||
		agent.DesiredState != command.DesiredState ||
		agent.PlacementPolicy != command.PlacementPolicy ||
		agent.MaxInstances != command.MaxInstances ||
		agent.RestartPolicy != command.RestartPolicy ||
		agent.BudgetPolicy != command.BudgetPolicy ||
		!equalStringMap(agent.Metadata, command.Metadata) {
		return fmt.Errorf("agent %q already exists with a different definition: %w", command.AgentID, ErrConflict)
	}
	return nil
}

func roleDefinitionFromRole(role *Role) RoleDefinition {
	return RoleDefinition{
		Name: role.Name, Kind: role.Kind, Description: role.Description,
		Prompt: role.Prompt, PromptFile: role.PromptFile, Model: role.Model,
		TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: append([]string(nil), role.PathPatterns...),
		Skills:       append([]string(nil), role.Skills...),
		MaxPriority:  cloneInt(role.MaxPriority), MaxConcurrency: cloneInt(role.MaxConcurrency),
		ReadOnly: role.ReadOnly, AllowedTools: append([]string(nil), role.AllowedTools...),
		DeniedTools:  append([]string(nil), role.DeniedTools...),
		MaxBudgetUSD: cloneFloat64(role.MaxBudgetUSD),
	}
}

func cloneRoleDefinition(role RoleDefinition) RoleDefinition {
	out := role
	out.PathPatterns = append([]string(nil), role.PathPatterns...)
	out.Skills = append([]string(nil), role.Skills...)
	out.AllowedTools = append([]string(nil), role.AllowedTools...)
	out.DeniedTools = append([]string(nil), role.DeniedTools...)
	out.MaxPriority = cloneInt(role.MaxPriority)
	out.MaxConcurrency = cloneInt(role.MaxConcurrency)
	out.MaxBudgetUSD = cloneFloat64(role.MaxBudgetUSD)
	return out
}

func equalRoleDefinition(left, right RoleDefinition) bool {
	return left.Name == right.Name &&
		left.Kind == right.Kind &&
		left.Description == right.Description &&
		left.Prompt == right.Prompt &&
		left.PromptFile == right.PromptFile &&
		left.Model == right.Model &&
		left.TaskFilter == right.TaskFilter &&
		left.Backend == right.Backend &&
		left.Effort == right.Effort &&
		slices.Equal(left.PathPatterns, right.PathPatterns) &&
		slices.Equal(left.Skills, right.Skills) &&
		equalIntPointer(left.MaxPriority, right.MaxPriority) &&
		equalIntPointer(left.MaxConcurrency, right.MaxConcurrency) &&
		left.ReadOnly == right.ReadOnly &&
		slices.Equal(left.AllowedTools, right.AllowedTools) &&
		slices.Equal(left.DeniedTools, right.DeniedTools) &&
		equalFloatPointer(left.MaxBudgetUSD, right.MaxBudgetUSD)
}

func equalIntPointer(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalFloatPointer(left, right *float64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
