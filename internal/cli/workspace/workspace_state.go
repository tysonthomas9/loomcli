package workspace

import (
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// WSState returns "ready" for empty state (backwards compat with old configs).
func WSState(s config.WorkspaceState) string {
	if s == "" {
		return "ready"
	}
	return string(s)
}

// SetWorkspaceState atomically loads config under the config lock, updates
// the workspace's state and error message, and saves. Use this from outside
// an existing locked section. Use SetWorkspaceStateUnlocked when already
// holding the config lock.
func SetWorkspaceState(wsName string, state config.WorkspaceState, errMsg string) error {
	return config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return fmt.Errorf("load config for state transition: %w", err)
		}
		if cfg == nil {
			return fmt.Errorf("config is nil")
		}
		return SetWorkspaceStateUnlocked(cfg, wsName, state, errMsg)
	})
}

// SetWorkspaceStateUnlocked updates the workspace state in-place on cfg and
// writes the config back. Caller must already hold the config lock.
func SetWorkspaceStateUnlocked(cfg *config.LoomConfig, wsName string, state config.WorkspaceState, errMsg string) error {
	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		return fmt.Errorf("workspace %q not found in config", wsName)
	}
	ws.State = state
	ws.ErrorMessage = errMsg
	cfg.Workspaces[wsName] = ws

	if err := config.SaveConfigUnlocked(cfg); err != nil {
		return fmt.Errorf("save config for state transition: %w", err)
	}
	slog.Info("workspace state transition", "workspace", wsName, "state", state)
	return nil
}

// RecoverIncompleteWorkspaces marks any workspace in a non-terminal state
// (creating, cloning, initializing) as error. Called at startup to handle
// workspaces that were interrupted by a crash before reaching the ready state.
func RecoverIncompleteWorkspaces() {
	err := config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil || cfg == nil {
			return err
		}
		for name, ws := range cfg.Workspaces {
			switch ws.State {
			case config.WorkspaceStateCreating, config.WorkspaceStateCloning, config.WorkspaceStateInitializing:
				slog.Warn("recovering interrupted workspace", "workspace", name, "state", ws.State)
				if err := SetWorkspaceStateUnlocked(cfg, name, config.WorkspaceStateError, "creation interrupted — click retry"); err != nil {
					slog.Error("failed to recover workspace state", "workspace", name, "err", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("workspace recovery failed", "err", err)
	}
}
