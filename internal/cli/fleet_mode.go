package cli

import (
	"fmt"
	"os"

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
// local daemon should suppress beads-specific subsystems.
//
// Detection precedence:
//  1. LOOM_ISSUE_BACKEND=fleet env var (highest — useful for testing before config support lands)
//  2. cfg.Backend == "fleet" (config field — added by sibling task .3; currently unused)
//
// Returns false for nil config and empty Backend (safe default = beads mode).
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
func PrintDaemonBanner(config *config.DaemonConfig, projectDir string) {
	fmt.Println("═══════════════════════════════════════════════════════════════")
	if IsFleetMode(config) {
		fmt.Println("Loom Agent Supervisor — Fleet Mode")
		fmt.Printf("PID: %d\n", os.Getpid())
		fmt.Printf("Config: %s/loom.yaml\n", projectDir)
		fmt.Println("Agent supervision disabled — agents managed by fleet server")
	} else {
		fmt.Println("Loom Agent Supervisor")
		fmt.Printf("PID: %d\n", os.Getpid())
		fmt.Printf("Config: %s/loom.yaml\n", projectDir)
		fmt.Printf("Agents: %d\n", len(config.Agents))
		for _, a := range config.Agents {
			fmt.Printf("  - %s (%s)\n", a.Worktree, a.Role)
		}
	}
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════════")
}

// isFleetModeFromEnv checks fleet mode without a loaded config. Used in
// contexts where config.DaemonConfig is not available (e.g., loom init, hooks install).
//
// Checks LOOM_ISSUE_BACKEND env var first, then falls back to loading project config.
func IsFleetModeFromEnv() bool {
	if os.Getenv(fleetModeEnvVar) == BackendFleet {
		return true
	}
	// Fall back to project config
	pf, err := config.LoadProjectFile(".")
	if err == nil && pf != nil && pf.Backend == BackendFleet {
		return true
	}
	return false
}
