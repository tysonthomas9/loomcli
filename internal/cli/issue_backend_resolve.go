package cli

import (
	"os"
	"strconv"
)

// resolveIssueBackendType returns "beads" or "fleetdb" based on config precedence:
// 1. LOOM_FLEETDB_ENABLED env var
// 2. Project config (loom.yaml)
// 3. Global config (~/.loom/config.yaml)
// 4. Default: "beads"
func resolveIssueBackendType() string {
	// Highest precedence: env var (only if non-empty)
	if v, ok := os.LookupEnv("LOOM_FLEETDB_ENABLED"); ok && v != "" {
		parsed, err := strconv.ParseBool(v)
		if err == nil && parsed {
			return "fleetdb"
		}
		return "beads"
	}

	// Project config
	pf, err := LoadProjectFile(".")
	if err == nil && pf != nil && pf.Daemon != nil && pf.Daemon.FleetDB != nil && pf.Daemon.FleetDB.Enabled != nil && *pf.Daemon.FleetDB.Enabled {
		return "fleetdb"
	}

	// Global config
	cfg, err := LoadConfig()
	if err == nil && cfg != nil && cfg.Daemon != nil && cfg.Daemon.FleetDB != nil && cfg.Daemon.FleetDB.Enabled != nil && *cfg.Daemon.FleetDB.Enabled {
		return "fleetdb"
	}

	return "beads"
}

// isFleetDBActive returns true if the fleet-db backend is active.
func isFleetDBActive() bool {
	return resolveIssueBackendType() == "fleetdb"
}
