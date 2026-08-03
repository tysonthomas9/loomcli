// Package localnodeconfig owns machine-local runtime preferences that must not
// be persisted as FleetDB workspace state. Phase 6 replaces the retired
// mixed global/local configuration with this narrow local-node boundary.
package localnodeconfig

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

// RuntimeProvider returns the node-local provider override for a workspace.
// An empty result means the caller should apply its product default.
func RuntimeProvider(workspaceKey string) (string, error) {
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return "", fmt.Errorf("local node config: workspace key is required")
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		return "", err
	}
	if cache == nil {
		return "", nil
	}
	return strings.TrimSpace(cache.Workspaces[workspaceKey].DefaultRuntimeProvider), nil
}

// SetRuntimeProvider atomically changes the node-local provider override for a
// workspace without replacing unrelated local checkout or worktree state.
func SetRuntimeProvider(workspaceKey, provider string) error {
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return fmt.Errorf("local node config: workspace key is required")
	}
	provider = strings.TrimSpace(provider)
	return bootstrap.MutateWorkspaceLocalState(workspaceKey, func(local *bootstrap.WorkspaceLocalState) error {
		local.DefaultRuntimeProvider = provider
		return nil
	})
}
