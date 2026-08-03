// Package agentsbootstrapstore adapts canonical Role and Agent identity stores
// to the exact Agents bootstrap commands used during workspace creation and
// startup repair.
package agentsbootstrapstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Adapter struct {
	roles  store.RoleStore
	agents store.AgentServiceStore
}

func New(
	roles store.RoleStore,
	agentServices store.AgentServiceStore,
) (*Adapter, error) {
	if roles == nil || agentServices == nil {
		return nil, fmt.Errorf("compose Agents bootstrap store adapter: %w", agents.ErrUnavailable)
	}
	return &Adapter{
		roles: roles, agents: agentServices,
	}, nil
}

func (adapter *Adapter) EnsureRole(
	ctx context.Context,
	command agents.EnsureRoleCommand,
) (*agents.Role, bool, error) {
	if err := validateReplayIdentity(command.RequestID, command.WorkspaceKey, command.Role.Name); err != nil {
		return nil, false, err
	}
	current, err := adapter.roles.Get(ctx, command.WorkspaceKey, command.Role.Name)
	if err == nil {
		if !reflect.DeepEqual(roleDefinition(current), command.Role) {
			return nil, false, agents.ErrConflict
		}
		return roleFromDomain(current), false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, err
	}
	created, err := adapter.roles.Create(ctx, roleCreate(command.WorkspaceKey, command.Role))
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			winner, getErr := adapter.roles.Get(ctx, command.WorkspaceKey, command.Role.Name)
			if getErr != nil {
				return nil, false, getErr
			}
			if !reflect.DeepEqual(roleDefinition(winner), command.Role) {
				return nil, false, agents.ErrConflict
			}
			return roleFromDomain(winner), false, nil
		}
		return nil, false, err
	}
	return roleFromDomain(created), true, nil
}

func (adapter *Adapter) RepairManagedRolePromptFile(
	ctx context.Context,
	command agents.RepairManagedRolePromptFileCommand,
) (*agents.Role, bool, error) {
	if err := validateReplayIdentity(command.RequestID, command.WorkspaceKey, command.RoleName); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(command.PromptFile) == "" ||
		command.PromptFile != strings.TrimSpace(command.PromptFile) {
		return nil, false, fmt.Errorf("prompt file must be canonical: %w", agents.ErrInvalid)
	}
	promptStore, ok := adapter.roles.(agents.RolePromptRepairStore)
	if !ok {
		return nil, false, fmt.Errorf(
			"role store cannot atomically fill an empty prompt file: %w",
			agents.ErrUnavailable,
		)
	}
	updated, changed, err := promptStore.SetPromptFileIfEmpty(
		ctx,
		command.WorkspaceKey,
		command.RoleName,
		command.PromptFile,
	)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, false, agents.ErrConflict
		}
		return nil, false, err
	}
	if updated == nil || updated.WorkspaceKey != command.WorkspaceKey ||
		updated.Name != command.RoleName || updated.PromptFile != command.PromptFile {
		return nil, false, agents.ErrInvalidPersistedState
	}
	return updated, changed, nil
}

func (adapter *Adapter) EnsureAgent(
	ctx context.Context,
	command agents.EnsureAgentCommand,
) (*agents.Agent, bool, error) {
	if err := validateReplayIdentity(command.RequestID, command.WorkspaceKey, command.AgentID); err != nil {
		return nil, false, err
	}
	current, err := adapter.agents.Get(
		ctx,
		command.WorkspaceKey,
		command.AgentID,
	)
	if err == nil {
		if !agentMatches(current, command.CreateAgentCommand) {
			return nil, false, agents.ErrConflict
		}
		return agentFromDomain(current), false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, err
	}
	created, err := adapter.agents.Create(ctx, agentCreate(command.CreateAgentCommand))
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			winner, getErr := adapter.agents.Get(
				ctx,
				command.WorkspaceKey,
				command.AgentID,
			)
			if getErr != nil {
				return nil, false, getErr
			}
			if !agentMatches(winner, command.CreateAgentCommand) {
				return nil, false, agents.ErrConflict
			}
			return agentFromDomain(winner), false, nil
		}
		return nil, false, err
	}
	return agentFromDomain(created), true, nil
}

func roleCreate(workspace string, role agents.RoleDefinition) store.RoleCreate {
	return store.RoleCreate{
		WorkspaceKey: workspace, Name: role.Name, Kind: role.Kind,
		Description: role.Description, Prompt: role.Prompt, PromptFile: role.PromptFile,
		Model: role.Model, TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: append([]string(nil), role.PathPatterns...),
		Skills:       append([]string(nil), role.Skills...),
		MaxPriority:  cloneInt(role.MaxPriority), MaxConcurrency: cloneInt(role.MaxConcurrency),
		ReadOnly: role.ReadOnly, AllowedTools: append([]string(nil), role.AllowedTools...),
		DeniedTools:  append([]string(nil), role.DeniedTools...),
		MaxBudgetUSD: cloneFloat(role.MaxBudgetUSD),
	}
}

func roleDefinition(role *domain.Role) agents.RoleDefinition {
	if role == nil {
		return agents.RoleDefinition{}
	}
	return agents.RoleDefinition{
		Name: role.Name, Kind: string(role.Kind), Description: role.Description,
		Prompt: role.Prompt, PromptFile: role.PromptFile, Model: role.Model,
		TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: append([]string(nil), role.PathPatterns...),
		Skills:       append([]string(nil), role.Skills...),
		MaxPriority:  cloneInt(role.MaxPriority), MaxConcurrency: cloneInt(role.MaxConcurrency),
		ReadOnly: role.ReadOnly, AllowedTools: append([]string(nil), role.AllowedTools...),
		DeniedTools:  append([]string(nil), role.DeniedTools...),
		MaxBudgetUSD: cloneFloat(role.MaxBudgetUSD),
	}
}

func roleFromDomain(role *domain.Role) *agents.Role {
	if role == nil {
		return nil
	}
	definition := roleDefinition(role)
	return &agents.Role{
		WorkspaceKey: role.WorkspaceKey, Name: definition.Name, Kind: definition.Kind,
		Description: definition.Description, Prompt: definition.Prompt,
		PromptFile: definition.PromptFile, Model: definition.Model,
		TaskFilter: definition.TaskFilter, Backend: definition.Backend, Effort: definition.Effort,
		PathPatterns: definition.PathPatterns, Skills: definition.Skills,
		MaxPriority: definition.MaxPriority, MaxConcurrency: definition.MaxConcurrency,
		ReadOnly: definition.ReadOnly, AllowedTools: definition.AllowedTools,
		DeniedTools: definition.DeniedTools, MaxBudgetUSD: definition.MaxBudgetUSD,
		CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

func agentCreate(command agents.CreateAgentCommand) store.AgentServiceCreate {
	return store.AgentServiceCreate{
		WorkspaceKey: command.WorkspaceKey, ServiceID: command.AgentID, Name: command.Name,
		Kind: domain.AgentServiceKind(command.Kind), DesiredState: domain.AgentServiceDesiredState(command.DesiredState),
		RoleName: command.Behavior.RoleName, DriverID: command.Behavior.DriverID,
		DriverVersionID: command.Behavior.DriverVersionID,
		ProfileName:     command.ProfileName, ScheduleID: command.ScheduleID,
		EventSources:    append([]string(nil), command.EventSources...),
		TriggerRefs:     append([]string(nil), command.TriggerRefs...),
		PlacementPolicy: command.PlacementPolicy, MaxInstances: command.MaxInstances,
		LeaseID: command.LeaseID, RestartPolicy: command.RestartPolicy,
		Permissions:  append([]string(nil), command.Permissions...),
		BudgetPolicy: command.BudgetPolicy, StateRef: command.StateRef,
		Metadata: cloneMap(command.Metadata),
	}
}

func agentMatches(current *domain.AgentService, command agents.CreateAgentCommand) bool {
	if current == nil || current.DeletedAt != nil {
		return false
	}
	return reflect.DeepEqual(agentCreate(command), store.AgentServiceCreate{
		WorkspaceKey: current.WorkspaceKey, ServiceID: current.ServiceID, Name: current.Name,
		Kind: current.Kind, DesiredState: current.DesiredState, RoleName: current.RoleName,
		DriverID: current.DriverID, DriverVersionID: current.DriverVersionID,
		ProfileName: current.ProfileName, ScheduleID: current.ScheduleID,
		EventSources: current.EventSources, TriggerRefs: current.TriggerRefs,
		PlacementPolicy: current.PlacementPolicy, MaxInstances: current.MaxInstances,
		LeaseID: current.LeaseID, RestartPolicy: current.RestartPolicy,
		Permissions: current.Permissions, BudgetPolicy: current.BudgetPolicy,
		StateRef: current.StateRef, Metadata: current.Metadata,
	})
}

func agentFromDomain(current *domain.AgentService) *agents.Agent {
	if current == nil {
		return nil
	}
	return &agents.Agent{
		WorkspaceKey: current.WorkspaceKey, AgentID: current.ServiceID, Name: current.Name,
		Kind: agents.AgentKind(current.Kind),
		Behavior: agents.BehaviorReference{
			RoleName: current.RoleName, DriverID: current.DriverID, DriverVersionID: current.DriverVersionID,
		},
		DesiredState: agents.DesiredState(current.DesiredState),
		ProfileName:  current.ProfileName, ScheduleID: current.ScheduleID,
		EventSources:    append([]string(nil), current.EventSources...),
		TriggerRefs:     append([]string(nil), current.TriggerRefs...),
		PlacementPolicy: current.PlacementPolicy, MaxInstances: current.MaxInstances,
		LeaseID: current.LeaseID, RestartPolicy: current.RestartPolicy,
		Permissions:  append([]string(nil), current.Permissions...),
		BudgetPolicy: current.BudgetPolicy, StateRef: current.StateRef,
		Metadata: cloneMap(current.Metadata), CreatedBy: current.CreatedBy,
		DeletedAt: current.DeletedAt, CreatedAt: current.CreatedAt, UpdatedAt: current.UpdatedAt,
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

// RequestID is diagnostic replay identity for these natural-key ensure
// commands. The durable replay decision is the exact aggregate definition,
// matching the public Agents provisioning service; this adapter therefore
// validates but does not persist a second receipt.
func validateReplayIdentity(requestID, workspace, name string) error {
	for label, value := range map[string]string{
		"request id": requestID,
		"workspace":  workspace,
		"name":       name,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s must be canonical: %w", label, agents.ErrInvalid)
		}
	}
	return nil
}
