package repositoryadmission

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

// ManagedAgentsCommands is the consumer-owned interface needed by workspace
// creation and exact startup repair.
type ManagedAgentsCommands interface {
	EnsureRole(context.Context, agents.EnsureRoleCommand) (*agents.Role, error)
	GetRole(context.Context, string, string) (*agents.Role, error)
	RepairRolePromptFile(context.Context, agents.RepairManagedRolePromptFileCommand) (*agents.Role, bool, error)
}
