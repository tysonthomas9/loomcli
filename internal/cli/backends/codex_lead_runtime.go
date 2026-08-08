package backends

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	leadcontrol "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
)

// RunCodexLeadRuntime starts a controlled Codex app-server runtime for an interactive lead session.
func RunCodexLeadRuntime(
	ctx context.Context,
	st leadcontrol.RuntimeStore,
	sessionRuntime leadcontrol.SessionRuntime,
	workspace string,
	leadName string,
	sessionID string,
	workDir string,
	prompt string,
) error {
	return leadcontrol.RunCodexLeadRuntime(ctx, leadcontrol.CodexLeadRuntimeConfig{
		Store:     st,
		Runtime:   sessionRuntime,
		Workspace: workspace,
		ConfigDir: bootstrap.LoomDir(),
		LeadName:  leadName,
		SessionID: sessionID,
		WorkDir:   workDir,
		Prompt:    prompt,
	})
}
