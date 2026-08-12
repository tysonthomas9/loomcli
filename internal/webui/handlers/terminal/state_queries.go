package terminal

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

// StateQueries is the Terminal transport's read-only view of state owned by
// Agents, Interaction, Workspace, and the machine-local workspace registry.
// It deliberately exposes exact queries instead of repository families.
type StateQueries interface {
	GetRole(context.Context, string, string) (*agents.Role, error)
	FindActiveOrchestrationSession(context.Context, string, string) (string, error)
	ResolveWorkspaceName(context.Context, string) (string, error)
	ResolveWorkspacePath(context.Context, string) string
}
