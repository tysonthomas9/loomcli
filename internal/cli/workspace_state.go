package cli

import (
	"fmt"
	"log/slog"
)

// wsState returns "ready" for empty state (backwards compat with old configs).
func wsState(s WorkspaceState) string {
	if s == "" {
		return "ready"
	}
	return string(s)
}

// setWorkspaceState atomically loads config, updates the workspace's state
// and error message, and saves. Load-modify-save pattern ensures concurrent
// config writes (e.g., from another workspace creation) don't conflict.
func setWorkspaceState(wsName string, state WorkspaceState, errMsg string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config for state transition: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		return fmt.Errorf("workspace %q not found in config", wsName)
	}

	ws.State = state
	ws.ErrorMessage = errMsg
	cfg.Workspaces[wsName] = ws

	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config for state transition: %w", err)
	}

	slog.Info("workspace state transition", "workspace", wsName, "state", state)
	return nil
}

// recoverIncompleteWorkspaces marks any workspace in a non-terminal state
// (creating, cloning, initializing) as error. Called at startup to handle
// workspaces that were interrupted by a crash.
func recoverIncompleteWorkspaces() {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return
	}

	for name, ws := range cfg.Workspaces {
		switch ws.State {
		case WorkspaceStateCreating, WorkspaceStateCloning, WorkspaceStateInitializing:
			slog.Warn("recovering interrupted workspace", "workspace", name, "state", ws.State)
			if err := setWorkspaceState(name, WorkspaceStateError, "creation interrupted — click retry"); err != nil {
				slog.Error("failed to recover workspace state", "workspace", name, "err", err)
			}
		}
	}
}
