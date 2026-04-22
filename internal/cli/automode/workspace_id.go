package automode

import (
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// DefaultWorkspaceID resolves the workspace UUID for CLI entry points that
// don't thread a WorkspaceID explicitly: env LOOM_WORKSPACE_ID first, then
// the config's DefaultWorkspaceID, else empty (central stores then land in
// the "_default" fallback bucket).
func DefaultWorkspaceID() string {
	if id := workspace.ResolveWorkspaceID(""); id != "" {
		return id
	}
	if cfg, err := config.LoadConfig(); err == nil && cfg != nil {
		return cfg.DefaultWorkspaceID
	}
	return ""
}
