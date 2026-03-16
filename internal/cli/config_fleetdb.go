package cli

import (
	"os"
	"strconv"
)

// FleetDBSettings holds fleet-db configuration from YAML config files.
type FleetDBSettings struct {
	Enabled   bool   `yaml:"enabled,omitempty"`
	RedisURL  string `yaml:"redis_url,omitempty"`
	Workspace string `yaml:"workspace,omitempty"`
	AutoStart bool   `yaml:"auto_start,omitempty"`
}

// FleetDBServerConfig is the resolved configuration passed to NewFleetDBServer.
type FleetDBServerConfig struct {
	RedisURL   string
	Workspace  string
	AutoStart  bool
	FleetDBBin string // path to fleet-db binary (default: "fleet-db" via PATH)
	Actor      string // identity for fleet-db client (default: "loom")
}

// overlayFleetDBSettings applies non-zero fields from src onto dst.
// Follows the same pattern as overlayOTelConfig.
func overlayFleetDBSettings(dst, src *FleetDBSettings) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.RedisURL != "" {
		dst.RedisURL = src.RedisURL
	}
	if src.Workspace != "" {
		dst.Workspace = src.Workspace
	}
	if src.AutoStart {
		dst.AutoStart = true
	}
}

// resolveFleetDBEnabled checks LOOM_FLEETDB_ENABLED env var, falling back to settings value.
func resolveFleetDBEnabled(settings *FleetDBSettings) bool {
	if v := os.Getenv("LOOM_FLEETDB_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	if settings != nil {
		return settings.Enabled
	}
	return false
}

// resolveFleetDBConfig merges config from env vars and YAML settings.
// Priority: env vars > YAML (already merged from loom.yaml > ~/.loom/config.yaml) > defaults.
func resolveFleetDBConfig(daemon *DaemonSettings) FleetDBServerConfig {
	cfg := FleetDBServerConfig{
		Workspace: "default",
	}

	// Apply YAML settings (already merged by overlayDaemonSettings)
	if daemon.FleetDB != nil {
		if daemon.FleetDB.RedisURL != "" {
			cfg.RedisURL = daemon.FleetDB.RedisURL
		}
		if daemon.FleetDB.Workspace != "" {
			cfg.Workspace = daemon.FleetDB.Workspace
		}
		cfg.AutoStart = daemon.FleetDB.AutoStart
	}

	// Env vars override (highest priority)
	if v := os.Getenv("LOOM_FLEETDB_REDIS_URL"); v != "" {
		cfg.RedisURL = v
	}
	if v := os.Getenv("LOOM_FLEETDB_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}

	return cfg
}
