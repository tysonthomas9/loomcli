package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
)

// AgentGitHubRead is the host GitHub reader for agents' github_read (R-B),
// built before the Agent API so no agent opens or resumes without it: a PR
// review module of its own (no reviewer services) on serve's store and
// connector vault. Each read re-resolves the host credential, so a settings
// change applies at once. nil without a store or vault key; github_read
// agents then fail closed at launch.
func AgentGitHubRead(cfg webui.ServerConfig) func(ctx context.Context, ws, agentID, repoPath, op string, args map[string]any) (map[string]any, error) {
	disp := (&Server{config: cfg}).buildConnectorDispatcher()
	if disp == nil {
		return nil
	}
	m := modbuilder.NewPRReviewModule(cfg.Store, disp, nil, nil, cfg.LocalSettingsDir)
	return func(ctx context.Context, ws, agentID, repoPath, op string, args map[string]any) (map[string]any, error) {
		m.InvalidateCredentialSeeds()
		return m.GitHubRead(ctx, ws, agentID, repoPath, op, args)
	}
}
