package cli

import (
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

// createFleetIssueBackend resolves fleet config from daemon settings and env
// vars, then constructs a FleetBackend. Returns an error if the fleet URL is
// not configured.
func createFleetIssueBackend() (backend.IssueBackend, error) {
	dc, err := LoadDaemonConfig(".")
	var daemon *DaemonSettings
	if err == nil && dc != nil {
		daemon = &dc.Daemon
	}

	cfg := resolveFleetConfig(daemon)
	return createFleetIssueBackendFromConfig(cfg)
}

// createFleetIssueBackendFromConfig constructs a FleetBackend from pre-resolved
// config. Used when the caller already has the config (e.g., serve.go).
func createFleetIssueBackendFromConfig(cfg FleetClientConfig) (backend.IssueBackend, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("fleet URL is required")
	}

	fb, err := fleet.New(fleet.Config{
		BaseURL:     cfg.URL,
		WorkspaceID: cfg.Workspace,
		APIKey:      cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create fleet backend: %w", err)
	}

	slog.Info("fleet issue backend created", "url", cfg.URL, "workspace", cfg.Workspace)
	return fb, nil
}
