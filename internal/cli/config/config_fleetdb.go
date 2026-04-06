package config

import (
	"log"
	"os"
	"strconv"
)

// FleetDBSettings holds fleet-db backend config for use in YAML config files.
// Pointer fields (*bool) distinguish "not set" from "set to false" during overlay merging.
type FleetDBSettings struct {
	Enabled *bool `yaml:"enabled,omitempty"`
	// RedisURL is the Redis connection URL for the fleet-db subsystem.
	// This is distinct from DaemonSettings.RedisURL (daemon_config.go)
	// which is used by the stale-detector/serve subsystem (see serve.go).
	RedisURL  string `yaml:"redis_url,omitempty"`
	Workspace string `yaml:"workspace,omitempty"`
	AutoStart *bool  `yaml:"auto_start,omitempty"`
}

// overlayFleetDBSettings merges src into dst, copying only explicitly-set fields.
// For *bool fields: copy if src is non-nil. For string fields: copy if non-empty.
// Both dst and src must be non-nil (callers ensure this).
func overlayFleetDBSettings(dst, src *FleetDBSettings) {
	if dst == nil || src == nil {
		return
	}
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.RedisURL != "" {
		dst.RedisURL = src.RedisURL
	}
	if src.Workspace != "" {
		dst.Workspace = src.Workspace
	}
	if src.AutoStart != nil {
		dst.AutoStart = src.AutoStart
	}
}

// FleetDBServerConfig holds configuration for FleetDBServer.
type FleetDBServerConfig struct {
	RedisURL   string // Redis connection URL. Empty = use miniredis if AutoStart.
	Workspace  string // Workspace/project identifier.
	AutoStart  bool   // If true and RedisURL empty, auto-start miniredis.
	DBPath     string // SQLite database path. Empty = in-memory storage.
	SocketPath string // Unix socket path for RPC server.
}

// resolveFleetDBConfig produces the final resolved FleetDBServerConfig from
// merged DaemonSettings. It applies env var overrides (highest precedence)
// and defaults for unset values. Returns the config and whether fleet-db is enabled.
//
// Precedence: env vars > loom.yaml > ~/.loom/config.yaml > defaults
//
// Note: DBPath and SocketPath are NOT set here — they depend on the project
// directory which is known at daemon startup time, not config resolution time.
func ResolveFleetDBConfig(daemon *DaemonSettings) (FleetDBServerConfig, bool) {
	var enabled, autoStart bool
	var redisURL, workspace string

	// Start with values from daemon.FleetDB (already merged via overlay)
	if daemon.FleetDB != nil {
		if daemon.FleetDB.Enabled != nil {
			enabled = *daemon.FleetDB.Enabled
		}
		redisURL = daemon.FleetDB.RedisURL
		workspace = daemon.FleetDB.Workspace
		if daemon.FleetDB.AutoStart != nil {
			autoStart = *daemon.FleetDB.AutoStart
		}
	}

	// Override with env vars if set (highest precedence)
	if v, ok := os.LookupEnv("LOOM_FLEETDB_ENABLED"); ok {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			log.Printf("Warning: invalid LOOM_FLEETDB_ENABLED value %q, defaulting to false", v)
			parsed = false
		}
		enabled = parsed
	}
	if v, ok := os.LookupEnv("LOOM_FLEETDB_REDIS_URL"); ok {
		redisURL = v
	}
	if v, ok := os.LookupEnv("LOOM_FLEETDB_WORKSPACE"); ok {
		workspace = v
	}
	if v, ok := os.LookupEnv("LOOM_FLEETDB_AUTO_START"); ok {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			log.Printf("Warning: invalid LOOM_FLEETDB_AUTO_START value %q, defaulting to false", v)
			parsed = false
		}
		autoStart = parsed
	}

	// Apply defaults for unset values
	if workspace == "" {
		workspace = "default"
	}

	return FleetDBServerConfig{
		RedisURL:  redisURL,
		Workspace: workspace,
		AutoStart: autoStart,
	}, enabled
}
