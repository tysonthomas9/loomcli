package workspacemgr

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// testManagedAgentsCommands keeps workspace tests focused on orchestration.
// Production composition must always inject the real Agents capability.
type testManagedAgentsCommands struct {
	roles store.RoleStore
}

func managedAgentsForTest(s store.Store) ManagedAgentsCommands {
	return testManagedAgentsCommands{roles: s.Roles()}
}

func (commands testManagedAgentsCommands) EnsureRole(
	ctx context.Context,
	command agents.EnsureRoleCommand,
) (*agents.Role, error) {
	role, err := commands.roles.Get(ctx, command.WorkspaceKey, command.Role.Name)
	if err == nil {
		return testAgentsRole(role), nil
	}
	created, err := commands.roles.Create(ctx, store.RoleCreate{
		WorkspaceKey: command.WorkspaceKey, Name: command.Role.Name,
		Kind: command.Role.Kind, Description: command.Role.Description,
		Prompt: command.Role.Prompt, PromptFile: command.Role.PromptFile,
		Model: command.Role.Model, TaskFilter: command.Role.TaskFilter,
		Backend: command.Role.Backend, Effort: command.Role.Effort,
		PathPatterns: command.Role.PathPatterns, Skills: command.Role.Skills,
		MaxPriority: command.Role.MaxPriority, MaxConcurrency: command.Role.MaxConcurrency,
		ReadOnly: command.Role.ReadOnly, AllowedTools: command.Role.AllowedTools,
		DeniedTools: command.Role.DeniedTools, MaxBudgetUSD: command.Role.MaxBudgetUSD,
	})
	return testAgentsRole(created), err
}

func (commands testManagedAgentsCommands) RepairRolePromptFile(
	ctx context.Context,
	command agents.RepairManagedRolePromptFileCommand,
) (*agents.Role, bool, error) {
	current, err := commands.roles.Get(ctx, command.WorkspaceKey, command.RoleName)
	if err != nil {
		return nil, false, err
	}
	if current.PromptFile == command.PromptFile {
		return testAgentsRole(current), false, nil
	}
	promptFile := command.PromptFile
	updated, err := commands.roles.Update(
		ctx, command.WorkspaceKey, command.RoleName,
		store.RoleUpdate{PromptFile: &promptFile},
	)
	return testAgentsRole(updated), err == nil, err
}

func testAgentsRole(role *domain.Role) *agents.Role {
	if role == nil {
		return nil
	}
	return &agents.Role{
		WorkspaceKey: role.WorkspaceKey, Name: role.Name, Kind: string(role.Kind),
		Description: role.Description, Prompt: role.Prompt, PromptFile: role.PromptFile,
		Model: role.Model, TaskFilter: role.TaskFilter, Backend: role.Backend,
		Effort: role.Effort, PathPatterns: append([]string(nil), role.PathPatterns...),
		Skills: append([]string(nil), role.Skills...), MaxPriority: role.MaxPriority,
		MaxConcurrency: role.MaxConcurrency, ReadOnly: role.ReadOnly,
		AllowedTools: append([]string(nil), role.AllowedTools...),
		DeniedTools:  append([]string(nil), role.DeniedTools...),
		MaxBudgetUSD: role.MaxBudgetUSD, CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}
