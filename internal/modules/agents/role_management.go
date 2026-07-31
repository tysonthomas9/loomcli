package agents

import (
	"context"
	"fmt"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (s *Service) GetRole(ctx context.Context, workspace, roleName string) (*Role, error) {
	workspace, roleName, err := normalizeRoleIdentity(workspace, roleName)
	if err != nil {
		return nil, err
	}
	if s == nil || s.roleStore == nil {
		return nil, ErrUnavailable
	}
	role, err := s.roleStore.GetRole(ctx, workspace, roleName)
	if err != nil {
		return nil, fmt.Errorf("get role %q: %w", roleName, err)
	}
	if err := validatePersistedRole(role, workspace, roleName); err != nil {
		return nil, err
	}
	return cloneRole(role), nil
}

func (s *Service) ListRoles(ctx context.Context, workspace string) ([]*Role, error) {
	workspace, err := normalizeWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	if s == nil || s.roleStore == nil {
		return nil, ErrUnavailable
	}
	values, err := s.roleStore.ListRoles(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	out := make([]*Role, 0, len(values))
	for _, role := range values {
		if err := validatePersistedRole(role, workspace, ""); err != nil {
			return nil, err
		}
		out = append(out, cloneRole(role))
	}
	slices.SortFunc(out, func(left, right *Role) int {
		switch {
		case left.Name < right.Name:
			return -1
		case left.Name > right.Name:
			return 1
		default:
			return 0
		}
	})
	return out, nil
}

func (s *Service) CreateRole(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command CreateRoleCommand,
) (*Role, error) {
	command, err := normalizeCreateRoleCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireOperator(ActionCreateRole, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s == nil || s.roleStore == nil {
		return nil, ErrUnavailable
	}
	role, err := s.roleStore.CreateRole(ctx, command.WorkspaceKey, cloneRoleDefinition(command.Role))
	if err != nil {
		return nil, fmt.Errorf("create role %q: %w", command.Role.Name, err)
	}
	if err := validateExactRole(role, command.WorkspaceKey, command.Role); err != nil {
		return nil, err
	}
	return cloneRole(role), nil
}

func (s *Service) UpdateRole(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command UpdateRoleCommand,
) (*Role, error) {
	command, err := normalizeUpdateRoleCommand(command)
	if err != nil {
		return nil, err
	}
	if err := s.requireOperator(ActionUpdateRole, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s == nil || s.roleStore == nil {
		return nil, ErrUnavailable
	}
	role, err := s.roleStore.UpdateRole(ctx, UpdateRoleMutation{
		WorkspaceKey: command.WorkspaceKey, RoleName: command.RoleName,
		ExpectedUpdatedAt: command.ExpectedUpdatedAt, Patch: cloneRolePatch(command.Patch),
		UpdatedBy: auth.Subject(),
	})
	if err != nil {
		return nil, fmt.Errorf("update role %q: %w", command.RoleName, err)
	}
	if err := validatePersistedRole(role, command.WorkspaceKey, command.RoleName); err != nil {
		return nil, err
	}
	if !role.UpdatedAt.After(command.ExpectedUpdatedAt) {
		return nil, ErrInvalidPersistedState
	}
	if err := validateRolePatchResult(role, command.Patch); err != nil {
		return nil, err
	}
	return cloneRole(role), nil
}

func (s *Service) DeleteRole(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command DeleteRoleCommand,
) error {
	command, err := normalizeDeleteRoleCommand(command)
	if err != nil {
		return err
	}
	if err := s.requireOperator(ActionDeleteRole, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if s == nil || s.roleStore == nil {
		return ErrUnavailable
	}
	if err := s.roleStore.DeleteRole(ctx, DeleteRoleMutation{
		WorkspaceKey: command.WorkspaceKey, RoleName: command.RoleName,
		ExpectedUpdatedAt: command.ExpectedUpdatedAt, DeletedBy: auth.Subject(),
	}); err != nil {
		return fmt.Errorf("delete role %q: %w", command.RoleName, err)
	}
	return nil
}

func normalizeRoleIdentity(workspace, roleName string) (string, string, error) {
	workspace, err := normalizeWorkspace(workspace)
	if err != nil {
		return "", "", err
	}
	roleName, err = requireCanonical("role name", roleName)
	if err != nil {
		return "", "", err
	}
	return workspace, roleName, nil
}

func normalizeCreateRoleCommand(command CreateRoleCommand) (CreateRoleCommand, error) {
	workspace, err := normalizeWorkspace(command.WorkspaceKey)
	if err != nil {
		return CreateRoleCommand{}, err
	}
	definition, err := normalizeRoleDefinition(command.Role)
	if err != nil {
		return CreateRoleCommand{}, err
	}
	command.WorkspaceKey, command.Role = workspace, definition
	return command, nil
}

func normalizeUpdateRoleCommand(command UpdateRoleCommand) (UpdateRoleCommand, error) {
	workspace, roleName, err := normalizeRoleIdentity(command.WorkspaceKey, command.RoleName)
	if err != nil {
		return UpdateRoleCommand{}, err
	}
	if command.ExpectedUpdatedAt.IsZero() {
		return UpdateRoleCommand{}, fmt.Errorf("expected role revision is required: %w", ErrInvalid)
	}
	patch, err := normalizeRolePatch(command.Patch)
	if err != nil {
		return UpdateRoleCommand{}, err
	}
	command.WorkspaceKey, command.RoleName, command.Patch = workspace, roleName, patch
	return command, nil
}

func normalizeDeleteRoleCommand(command DeleteRoleCommand) (DeleteRoleCommand, error) {
	workspace, roleName, err := normalizeRoleIdentity(command.WorkspaceKey, command.RoleName)
	if err != nil {
		return DeleteRoleCommand{}, err
	}
	if command.ExpectedUpdatedAt.IsZero() {
		return DeleteRoleCommand{}, fmt.Errorf("expected role revision is required: %w", ErrInvalid)
	}
	command.WorkspaceKey, command.RoleName = workspace, roleName
	return command, nil
}

func normalizeRolePatch(patch RolePatch) (RolePatch, error) {
	if rolePatchEmpty(patch) {
		return RolePatch{}, fmt.Errorf("role patch must change at least one field: %w", ErrInvalid)
	}
	var err error
	for label, value := range map[string]**string{
		"role kind":   &patch.Kind,
		"description": &patch.Description,
		"prompt file": &patch.PromptFile,
		"model":       &patch.Model,
		"task filter": &patch.TaskFilter,
		"backend":     &patch.Backend,
		"effort":      &patch.Effort,
	} {
		if *value == nil {
			continue
		}
		normalized, normalizeErr := normalizeOptional(label, **value)
		if normalizeErr != nil {
			return RolePatch{}, normalizeErr
		}
		*value = &normalized
	}
	if patch.MaxPriority != nil && *patch.MaxPriority != nil && **patch.MaxPriority < 0 {
		return RolePatch{}, fmt.Errorf("role max priority must not be negative: %w", ErrInvalid)
	}
	if patch.MaxConcurrency != nil && *patch.MaxConcurrency != nil && **patch.MaxConcurrency <= 0 {
		return RolePatch{}, fmt.Errorf("role max concurrency must be positive: %w", ErrInvalid)
	}
	if patch.MaxBudgetUSD != nil && *patch.MaxBudgetUSD != nil && **patch.MaxBudgetUSD < 0 {
		return RolePatch{}, fmt.Errorf("role max budget must not be negative: %w", ErrInvalid)
	}
	for label, value := range map[string]*[]string{
		"path pattern": patch.PathPatterns,
		"skill":        patch.Skills,
		"allowed tool": patch.AllowedTools,
		"denied tool":  patch.DeniedTools,
	} {
		if value == nil {
			continue
		}
		*value, err = normalizeCanonicalList(label, *value)
		if err != nil {
			return RolePatch{}, err
		}
	}
	return cloneRolePatch(patch), nil
}

func rolePatchEmpty(patch RolePatch) bool {
	return patch.Kind == nil && patch.Description == nil && patch.Prompt == nil &&
		patch.PromptFile == nil && patch.Model == nil && patch.TaskFilter == nil &&
		patch.Backend == nil && patch.Effort == nil && patch.PathPatterns == nil &&
		patch.Skills == nil && patch.MaxPriority == nil && patch.MaxConcurrency == nil &&
		patch.ReadOnly == nil && patch.AllowedTools == nil && patch.DeniedTools == nil &&
		patch.MaxBudgetUSD == nil
}

//nolint:cyclop,gocognit // Exact-result validation mirrors every optional role field to detect divergent owner state.
func validateRolePatchResult(role *Role, patch RolePatch) error {
	actual := roleDefinitionFromRole(role)
	if patch.Kind != nil && actual.Kind != *patch.Kind ||
		patch.Description != nil && actual.Description != *patch.Description ||
		patch.Prompt != nil && actual.Prompt != *patch.Prompt ||
		patch.PromptFile != nil && actual.PromptFile != *patch.PromptFile ||
		patch.Model != nil && actual.Model != *patch.Model ||
		patch.TaskFilter != nil && actual.TaskFilter != *patch.TaskFilter ||
		patch.Backend != nil && actual.Backend != *patch.Backend ||
		patch.Effort != nil && actual.Effort != *patch.Effort ||
		patch.PathPatterns != nil && !slices.Equal(actual.PathPatterns, *patch.PathPatterns) ||
		patch.Skills != nil && !slices.Equal(actual.Skills, *patch.Skills) ||
		patch.MaxPriority != nil && !equalIntPointer(actual.MaxPriority, *patch.MaxPriority) ||
		patch.MaxConcurrency != nil && !equalIntPointer(actual.MaxConcurrency, *patch.MaxConcurrency) ||
		patch.ReadOnly != nil && actual.ReadOnly != *patch.ReadOnly ||
		patch.AllowedTools != nil && !slices.Equal(actual.AllowedTools, *patch.AllowedTools) ||
		patch.DeniedTools != nil && !slices.Equal(actual.DeniedTools, *patch.DeniedTools) ||
		patch.MaxBudgetUSD != nil && !equalFloatPointer(actual.MaxBudgetUSD, *patch.MaxBudgetUSD) {
		return ErrInvalidPersistedState
	}
	return nil
}

func cloneRolePatch(patch RolePatch) RolePatch {
	out := patch
	out.PathPatterns = cloneStringSlicePointer(patch.PathPatterns)
	out.Skills = cloneStringSlicePointer(patch.Skills)
	out.MaxPriority = cloneIntPointerPointer(patch.MaxPriority)
	out.MaxConcurrency = cloneIntPointerPointer(patch.MaxConcurrency)
	out.AllowedTools = cloneStringSlicePointer(patch.AllowedTools)
	out.DeniedTools = cloneStringSlicePointer(patch.DeniedTools)
	out.MaxBudgetUSD = cloneFloatPointerPointer(patch.MaxBudgetUSD)
	return out
}

func cloneStringSlicePointer(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	out := append([]string(nil), (*value)...)
	return &out
}

func cloneIntPointerPointer(value **int) **int {
	if value == nil {
		return nil
	}
	out := cloneInt(*value)
	return &out
}

func cloneFloatPointerPointer(value **float64) **float64 {
	if value == nil {
		return nil
	}
	out := cloneFloat64(*value)
	return &out
}
