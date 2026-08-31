package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// BackendFleet is the issue backend identifier for fleet mode (external fleet
// server manages agent orchestration). Distinct from "fleetdb" which is the
// embedded SQLite fleet store.
const BackendFleet = "fleet"

// fleetModeEnvVar is the environment variable for overriding the issue backend.
// Separate from LOOM_BACKEND (which selects the AI backend: claude, codex, etc.).
const fleetModeEnvVar = "LOOM_ISSUE_BACKEND"

// isFleetMode returns true when the effective issue backend is "fleet",
// indicating that a remote fleet server manages agent orchestration and the
// local daemon should suppress local issue-daemon subsystems.
//
// Detection precedence:
//  1. LOOM_ISSUE_BACKEND=fleet env var
//  2. cfg.Backend == "fleet" from the active FleetDB daemon profile
func IsFleetMode(cfg *config.DaemonConfig) bool {
	if os.Getenv(fleetModeEnvVar) == BackendFleet {
		return true
	}
	if cfg != nil && cfg.Backend == BackendFleet {
		return true
	}
	return false
}

// printDaemonBanner prints the startup banner for the daemon, varying the
// output for fleet mode (no agent list) vs normal mode (shows agents).
// PrintDaemonBanner prints the supervisor's startup banner.
//
// preflightLines carries the fleet-db capability report's rendering — the
// Degraded block, or the unreachable/unverified warning — already formatted by
// internal/runtimepreflight. It is passed as lines rather than as the report
// itself so this package does not take a dependency on the preflight package;
// the report is the caller's to hold. Empty for a clean boot, which prints
// exactly what it printed before this existed. A fatal report never reaches
// here: the daemon exits upstream of this call.
func PrintDaemonBanner(config *config.DaemonConfig, projectDir string, preflightLines []string) {
	fmt.Println("═══════════════════════════════════════════════════════════════")
	if IsFleetMode(config) {
		fmt.Println("Loom Agent Supervisor — Fleet Mode")
		fmt.Printf("PID: %d\n", os.Getpid())
		fmt.Printf("Workspace: %s\n", projectDir)
		fmt.Println("Agent supervision disabled — agents managed by fleet server")
	} else {
		fmt.Println("Loom Agent Supervisor")
		fmt.Printf("PID: %d\n", os.Getpid())
		fmt.Printf("Workspace: %s\n", projectDir)
		fmt.Printf("Agents: %d\n", len(config.Agents))
		for _, a := range config.Agents {
			fmt.Printf("  - %s (%s)\n", a.Worktree, a.Role)
		}
	}
	for _, line := range preflightLines {
		fmt.Println(line)
	}
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════════")
}

// isFleetModeFromEnv checks fleet mode without a loaded config.
func IsFleetModeFromEnv() bool {
	return os.Getenv(fleetModeEnvVar) == BackendFleet
}

// --- Fleet backend adapter (merged from cli_fleet_adapter.go) ---

// createFleetIssueBackend resolves fleet config from daemon settings and env
// vars, then constructs a FleetBackend. Returns an error if the fleet URL is
// not configured.
func createFleetIssueBackend() (backend.IssueBackend, error) {
	cfg := config.ResolveFleetConfig(nil)
	return createFleetIssueBackendFromConfig(cfg)
}

// createFleetIssueBackendFromConfig constructs a FleetBackend from pre-resolved
// config. Used when the caller already has the config (e.g., serve.go).
func createFleetIssueBackendFromConfig(cfg config.FleetClientConfig) (backend.IssueBackend, error) {
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

	slog.Info("fleet issue backend created", "url", cfg.URL, "workspace", cfg.Workspace, "actor", cfg.Actor)
	return fb, nil
}
