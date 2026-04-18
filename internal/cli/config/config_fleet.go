package config

import (
	"os"
	"strings"
)

// FleetSettings holds fleet client config for use in YAML config files.
// Used when loom acts as a worker connecting to an external fleet server.
type FleetSettings struct {
	URL       string `yaml:"url,omitempty"`
	Workspace string `yaml:"workspace,omitempty"`
	APIKey    string `yaml:"api_key,omitempty"` //nolint:gosec // YAML-provided pre-shared key
}

// overlayFleetSettings merges src into dst, copying only non-empty string fields.
func overlayFleetSettings(dst, src *FleetSettings) {
	if dst == nil || src == nil {
		return
	}
	if src.URL != "" {
		dst.URL = src.URL
	}
	if src.Workspace != "" {
		dst.Workspace = src.Workspace
	}
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
}

// FleetClientConfig is the resolved runtime config for connecting to a fleet server.
type FleetClientConfig struct {
	URL       string // Fleet server base URL (required in fleet mode)
	Workspace string // Workspace identifier (default: "default")
	APIKey    string //nolint:gosec // Pre-shared API key for fleet worker registration
}

// resolveFleetConfig produces the final resolved FleetClientConfig from
// merged DaemonSettings. It applies env var overrides (highest precedence)
// and defaults for unset values.
//
// Precedence: env vars > loom.yaml > ~/.loom/config.yaml > defaults
func ResolveFleetConfig(daemon *DaemonSettings) FleetClientConfig {
	var url, workspace, apiKey string

	// Start with values from daemon.Fleet (already merged via overlay)
	if daemon != nil && daemon.Fleet != nil {
		url = daemon.Fleet.URL
		workspace = daemon.Fleet.Workspace
		apiKey = daemon.Fleet.APIKey
	}

	// Override with env vars if set (highest precedence)
	if v, ok := os.LookupEnv("LOOM_FLEET_URL"); ok {
		url = v
	}
	if v, ok := os.LookupEnv("LOOM_FLEET_WORKSPACE"); ok {
		workspace = v
	}
	if v, ok := os.LookupEnv("LOOM_FLEET_API_KEY"); ok {
		apiKey = v
	}

	// Apply defaults
	if workspace == "" {
		workspace = "default"
	}

	// Trim trailing slashes from URL
	url = strings.TrimRight(url, "/")

	return FleetClientConfig{
		URL:       url,
		Workspace: workspace,
		APIKey:    apiKey,
	}
}
