package config

import (
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

// FleetClientConfig is the resolved runtime config for connecting to a fleet server.
type FleetClientConfig struct {
	URL       string // Fleet server base URL (required in fleet mode)
	Workspace string // Fleet workspace identifier; empty when unset.
	APIKey    string //nolint:gosec // Pre-shared API key for fleet worker registration
	Actor     string // X-Actor header (used by fleet-db --auth-dev-mode)
}

// resolveFleetConfig produces the final resolved FleetClientConfig from
// environment. Fleet server connection config is deployment bootstrap state,
// not workspace configuration.
func ResolveFleetConfig(daemon *DaemonSettings) FleetClientConfig {
	_ = daemon
	var url, workspace, apiKey, actor string

	if v, ok := os.LookupEnv(bootstrap.EnvFleetDBURL); ok {
		url = v
	}
	if v, ok := os.LookupEnv("LOOM_WORKSPACE"); ok {
		workspace = v
	}
	if v, ok := os.LookupEnv(bootstrap.EnvFleetDBAPIKey); ok {
		apiKey = v
	}
	if v, ok := os.LookupEnv(bootstrap.EnvFleetDBActor); ok {
		actor = v
	}
	// Reuse the current agent name if no explicit actor is configured.
	if actor == "" {
		if v, ok := os.LookupEnv("LOOM_AGENT_NAME"); ok {
			actor = v
		}
	}

	workspace = normalizeExplicitFleetWorkspace(workspace)

	// Trim trailing slashes from URL
	url = strings.TrimRight(url, "/")

	return FleetClientConfig{
		URL:       url,
		Workspace: workspace,
		APIKey:    apiKey,
		Actor:     actor,
	}
}
