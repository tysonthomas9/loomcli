package cli

import (
	"log/slog"
	"os"
	"strconv"
)

// Issue backend type constants.
const (
	IssueBackendBeads   = "beads"
	IssueBackendFleetDB = "fleetdb"
	IssueBackendFleet   = "fleet"
)

// validIssueBackends is the set of accepted values for daemon.issue_backend.
var validIssueBackends = map[string]bool{
	IssueBackendBeads:   true,
	IssueBackendFleetDB: true,
	IssueBackendFleet:   true,
}

// resolveIssueBackendType returns the active issue backend type based on config precedence:
//  1. LOOM_ISSUE_BACKEND env var (highest — if set and valid)
//  2. LOOM_FLEETDB_ENABLED env var (existing — returns "fleetdb" if true)
//  3. Project config daemon.issue_backend (loom.yaml)
//  4. Global config daemon.issue_backend (~/.loom/config.yaml)
//  5. Project config daemon.fleetdb.enabled (existing)
//  6. Global config daemon.fleetdb.enabled (existing)
//  7. Default: "beads"
func resolveIssueBackendType() string {
	if v := resolveIssueBackendFromEnv(); v != "" {
		return v
	}
	if v := resolveIssueBackendFromConfig(); v != "" {
		return v
	}
	return IssueBackendBeads
}

// resolveIssueBackendFromEnv checks environment variables for issue backend selection.
// Returns "" if no env var determines the backend.
func resolveIssueBackendFromEnv() string {
	// 1. LOOM_ISSUE_BACKEND env var (highest precedence)
	if v, ok := os.LookupEnv("LOOM_ISSUE_BACKEND"); ok && v != "" {
		if validIssueBackends[v] {
			return v
		}
		slog.Warn("invalid LOOM_ISSUE_BACKEND value; ignoring", "value", v)
	}

	// 2. LOOM_FLEETDB_ENABLED env var (backward compat)
	if v, ok := os.LookupEnv("LOOM_FLEETDB_ENABLED"); ok && v != "" {
		parsed, err := strconv.ParseBool(v)
		if err == nil && parsed {
			return IssueBackendFleetDB
		}
	}

	return ""
}

// resolveIssueBackendFromConfig checks project and global config files for issue backend selection.
// Returns "" if no config determines the backend.
func resolveIssueBackendFromConfig() string {
	// 3. Project config daemon.issue_backend
	pf, pfErr := LoadProjectFile(".")

	if pfErr == nil && pf != nil && pf.Daemon != nil {
		if pf.Daemon.IssueBackend != "" && validIssueBackends[pf.Daemon.IssueBackend] {
			return pf.Daemon.IssueBackend
		}
	}

	// 4. Global config daemon.issue_backend
	cfg, cfgErr := LoadConfig()

	if cfgErr == nil && cfg != nil && cfg.Daemon != nil {
		if cfg.Daemon.IssueBackend != "" && validIssueBackends[cfg.Daemon.IssueBackend] {
			return cfg.Daemon.IssueBackend
		}
	}

	// 5. Project config daemon.fleetdb.enabled (existing)
	if pfErr == nil && fleetDBEnabledInDaemon(projectDaemon(pf)) {
		return IssueBackendFleetDB
	}

	// 6. Global config daemon.fleetdb.enabled (existing)
	if cfgErr == nil && fleetDBEnabledInDaemon(globalDaemon(cfg)) {
		return IssueBackendFleetDB
	}

	return ""
}

// fleetDBEnabledInDaemon returns true if ds has FleetDB.Enabled set to true.
func fleetDBEnabledInDaemon(ds *DaemonSettings) bool {
	return ds != nil && ds.FleetDB != nil && ds.FleetDB.Enabled != nil && *ds.FleetDB.Enabled
}

// projectDaemon extracts the DaemonSettings from a ProjectFile, or nil.
func projectDaemon(pf *ProjectFile) *DaemonSettings {
	if pf == nil {
		return nil
	}
	return pf.Daemon
}

// globalDaemon extracts the DaemonSettings from a LoomConfig, or nil.
func globalDaemon(cfg *LoomConfig) *DaemonSettings {
	if cfg == nil {
		return nil
	}
	return cfg.Daemon
}

// isFleetActive returns true if the fleet backend (remote fleet server) is active.
func isFleetActive() bool {
	return resolveIssueBackendType() == IssueBackendFleet
}

// isFleetDBActive returns true if the fleet-db backend is active.
func isFleetDBActive() bool {
	return resolveIssueBackendType() == IssueBackendFleetDB
}
