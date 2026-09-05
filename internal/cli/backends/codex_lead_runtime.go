package backends

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

// RunCodexLeadRuntime starts a controlled Codex app-server runtime for an interactive lead session.
func RunCodexLeadRuntime(ctx context.Context, opts ControlledLeadOptions) error {
	return leadcontrol.RunCodexLeadRuntime(ctx, leadcontrol.CodexLeadRuntimeConfig{
		Store:          opts.Store,
		Workspace:      opts.Workspace,
		LeadName:       opts.LeadName,
		SessionID:      opts.SessionID,
		WorkDir:        opts.WorkDir,
		Prompt:         opts.Prompt,
		ResumeThreadID: opts.ResumeCodexThreadID,
		ResumeLast:     opts.ResumeLast,
		// leadcontrol must not import internal/cli, so the workspace runtime
		// root is resolved here and passed in explicitly.
		RuntimeDir: cli.GetWorkspaceRuntimeDir(),
	})
}
