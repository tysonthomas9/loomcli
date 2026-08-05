package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

// FleetDBServerConfig holds configuration for FleetDBServer.
type FleetDBServerConfig struct {
	RedisURL  string // Redis connection URL. Empty = use miniredis if AutoStart.
	Workspace string // Workspace/project identifier.
	AutoStart bool   // If true and RedisURL empty, auto-start miniredis.
}

// ResolveFleetDBConfig produces FleetDB server bootstrap config from env vars.
func ResolveFleetDBConfig() FleetDBServerConfig {
	var autoStart bool
	var redisURL, workspace string

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

	workspace = normalizeExplicitFleetWorkspace(workspace)

	return FleetDBServerConfig{
		RedisURL:  redisURL,
		Workspace: workspace,
		AutoStart: autoStart,
	}
}

func normalizeExplicitFleetWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if strings.EqualFold(workspace, "default") {
		return "DEFAULT"
	}
	return workspace
}
