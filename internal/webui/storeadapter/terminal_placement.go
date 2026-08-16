package storeadapter

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// NewTerminalPlacement builds Interaction's machine-local read adapter from
// existing Store and local-workspace projections. The adapter never mutates a
// capability aggregate or exposes private runtime handles to HTTP delivery.
func NewTerminalPlacement(
	orchestration interaction.OrchestrationSessionStore,
	workspacePath func(context.Context, string) string,
) interaction.AgentTerminalPlacement {
	return terminalPlacement{orchestration: orchestration, workspacePath: workspacePath}
}

type terminalPlacement struct {
	orchestration interaction.OrchestrationSessionStore
	workspacePath func(context.Context, string) string
}

func (adapter terminalPlacement) FindActiveOrchestrationSession(
	ctx context.Context,
	workspaceKey, agentID string,
) (string, error) {
	if adapter.orchestration == nil {
		return "", nil
	}
	return interaction.OrchestrationSessionIDFor(ctx, adapter.orchestration, workspaceKey, agentID)
}

func (terminalPlacement) AgentWorktree(_ context.Context, workspaceKey, agentID string) string {
	path, _ := localworkspace.RememberedAgentWorktree(workspaceKey, agentID)
	return path
}

func (adapter terminalPlacement) WorkspacePath(ctx context.Context, workspaceKey string) string {
	if adapter.workspacePath == nil {
		return ""
	}
	return adapter.workspacePath(ctx, workspaceKey)
}

func (terminalPlacement) DefaultBackend(_ context.Context, workspaceKey string) string {
	backend, _ := bootstrap.RuntimeProvider(workspaceKey)
	return backend
}

func (terminalPlacement) ConfigDir() string { return bootstrap.LoomDir() }
