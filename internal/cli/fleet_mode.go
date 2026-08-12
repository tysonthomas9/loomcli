package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	fleet "github.com/tysonthomas9/loomcli/internal/modules/workitems/fleetdb"
)

// BackendFleet is the Work Items adapter identifier for fleet mode (external fleet
// server manages agent orchestration). Distinct from "fleetdb" which is the
// embedded SQLite fleet store.
const BackendFleet = "fleet"

// fleetModeEnvVar is the environment variable for overriding the Work Items adapter.
// Separate from LOOM_BACKEND (which selects the AI backend: claude, codex, etc.).
const fleetModeEnvVar = "LOOM_ISSUE_BACKEND"

// isFleetMode returns true when the effective Work Items adapter is "fleet",
// indicating that a remote fleet server manages agent orchestration and the
// local runtime should route issue operations to the remote fleet server.
//
// Detection precedence:
//  1. LOOM_ISSUE_BACKEND=fleet env var
//  2. cfg.Backend == "fleet" from the active runtime projection
func IsFleetMode(cfg *config.RuntimeConfig) bool {
	if os.Getenv(fleetModeEnvVar) == BackendFleet {
		return true
	}
	if cfg != nil && cfg.Backend == BackendFleet {
		return true
	}
	return false
}

// isFleetModeFromEnv checks fleet mode without a loaded config.
func IsFleetModeFromEnv() bool {
	return os.Getenv(fleetModeEnvVar) == BackendFleet
}

// --- Fleet backend adapter (merged from cli_fleet_adapter.go) ---

// createFleetWorkItemStore resolves Fleet config from the environment, then
// constructs a Work Items FleetDB adapter. Returns an error if the fleet URL is
// not configured.
func createFleetWorkItemStore() (*fleet.Adapter, error) {
	cfg := config.ResolveFleetConfig()
	return createFleetWorkItemStoreFromConfig(cfg)
}

// createFleetWorkItemStoreFromConfig constructs a Fleet adapter from pre-resolved
// config. Used when the caller already has the config (e.g., serve.go).
func createFleetWorkItemStoreFromConfig(cfg config.FleetClientConfig) (*fleet.Adapter, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("fleet URL is required")
	}

	fb, err := fleet.New(fleet.Config{
		BaseURL:     cfg.URL,
		WorkspaceID: cfg.Workspace,
		APIKey:      cfg.APIKey,
		Actor:       cfg.Actor,
	})
	if err != nil {
		return nil, fmt.Errorf("create fleet backend: %w", err)
	}

	slog.Info("FleetDB Work Items adapter created", "url", cfg.URL, "workspace", cfg.Workspace, "actor", cfg.Actor)
	return fb, nil
}
