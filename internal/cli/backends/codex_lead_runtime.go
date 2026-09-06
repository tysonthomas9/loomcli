package backends

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// RunCodexLeadRuntime starts a controlled Codex app-server runtime for an interactive lead session.
//
// The model pin is resolved HERE rather than inside leadcontrol so there is one
// resolver per harness rather than one per runtime package; leadcontrol takes
// the already-resolved value. See model_pin.go.
func RunCodexLeadRuntime(
	ctx context.Context,
	st store.Store,
	workspace string,
	leadName string,
	sessionID string,
	workDir string,
	prompt string,
) error {
	return leadcontrol.RunCodexLeadRuntime(ctx, leadcontrol.CodexLeadRuntimeConfig{
		Store:     st,
		Workspace: workspace,
		LeadName:  leadName,
		SessionID: sessionID,
		WorkDir:   workDir,
		Prompt:    prompt,
		// leadcontrol must not import internal/cli, so the workspace runtime
		// root is resolved here and passed in explicitly.
		RuntimeDir: cli.GetWorkspaceRuntimeDir(),
		ModelPin:   pinnedCodexModel(),
	})
}
