package config

import (
	"testing"
)

func TestOverlayFleetSettings_Empty(t *testing.T) {
	dst := &FleetSettings{
		URL:       "https://fleet.example.com",
		Workspace: "prod",
		APIKey:    "secret-key",
	}
	src := &FleetSettings{} // all empty

	overlayFleetSettings(dst, src)

	if dst.URL != "https://fleet.example.com" {
		t.Errorf("URL should remain, got %q", dst.URL)
	}
	if dst.Workspace != "prod" {
		t.Errorf("Workspace should remain, got %q", dst.Workspace)
	}
	if dst.APIKey != "secret-key" {
		t.Errorf("APIKey should remain, got %q", dst.APIKey)
	}
}

func TestOverlayFleetSettings_Full(t *testing.T) {
	dst := &FleetSettings{
		URL:       "https://old.example.com",
		Workspace: "old-ws",
		APIKey:    "old-key",
	}
	src := &FleetSettings{
		URL:       "https://new.example.com",
		Workspace: "new-ws",
		APIKey:    "new-key",
	}

	overlayFleetSettings(dst, src)

	if dst.URL != "https://new.example.com" {
		t.Errorf("URL = %q, want https://new.example.com", dst.URL)
	}
	if dst.Workspace != "new-ws" {
		t.Errorf("Workspace = %q, want new-ws", dst.Workspace)
	}
	if dst.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want new-key", dst.APIKey)
	}
}

func TestOverlayFleetSettings_Partial(t *testing.T) {
	dst := &FleetSettings{
		URL:       "https://keep.example.com",
		Workspace: "keep-ws",
		APIKey:    "keep-key",
	}
	src := &FleetSettings{
		URL: "https://override.example.com",
		// Workspace and APIKey left empty
	}

	overlayFleetSettings(dst, src)

	if dst.URL != "https://override.example.com" {
		t.Errorf("URL should be overridden, got %q", dst.URL)
	}
	if dst.Workspace != "keep-ws" {
		t.Errorf("Workspace should be preserved, got %q", dst.Workspace)
	}
	if dst.APIKey != "keep-key" {
		t.Errorf("APIKey should be preserved, got %q", dst.APIKey)
	}
}

func TestOverlayFleetSettings_NilSafe(t *testing.T) {
	// Neither should panic
	overlayFleetSettings(nil, nil)
	overlayFleetSettings(nil, &FleetSettings{URL: "https://x.com"})
	overlayFleetSettings(&FleetSettings{}, nil)
}

func TestResolveFleetConfig_Defaults(t *testing.T) {
	cfg := ResolveFleetConfig(nil)

	if cfg.Workspace != "default" {
		t.Errorf("Workspace = %q, want %q", cfg.Workspace, "default")
	}
	if cfg.URL != "" {
		t.Errorf("URL = %q, want empty", cfg.URL)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", cfg.APIKey)
	}
}

func TestResolveFleetConfig_Defaults_NilFleet(t *testing.T) {
	daemon := &DaemonSettings{Fleet: nil}
	cfg := ResolveFleetConfig(daemon)

	if cfg.Workspace != "default" {
		t.Errorf("Workspace = %q, want %q", cfg.Workspace, "default")
	}
	if cfg.URL != "" {
		t.Errorf("URL = %q, want empty", cfg.URL)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", cfg.APIKey)
	}
}

func TestResolveFleetConfig_FromYAML(t *testing.T) {
	daemon := &DaemonSettings{
		Fleet: &FleetSettings{
			URL:       "https://fleet.example.com",
			Workspace: "staging",
			APIKey:    "yaml-key",
		},
	}

	cfg := ResolveFleetConfig(daemon)

	if cfg.URL != "https://fleet.example.com" {
		t.Errorf("URL = %q, want https://fleet.example.com", cfg.URL)
	}
	if cfg.Workspace != "staging" {
		t.Errorf("Workspace = %q, want staging", cfg.Workspace)
	}
	if cfg.APIKey != "yaml-key" {
		t.Errorf("APIKey = %q, want yaml-key", cfg.APIKey)
	}
}

func TestResolveFleetConfig_EnvOverrides(t *testing.T) {
	daemon := &DaemonSettings{
		Fleet: &FleetSettings{
			URL:       "https://yaml.example.com",
			Workspace: "yaml-ws",
			APIKey:    "yaml-key",
		},
	}

	t.Setenv("LOOM_FLEET_URL", "https://env.example.com")
	t.Setenv("LOOM_FLEET_WORKSPACE", "env-ws")
	t.Setenv("LOOM_FLEET_API_KEY", "env-key")

	cfg := ResolveFleetConfig(daemon)

	if cfg.URL != "https://env.example.com" {
		t.Errorf("URL = %q, want https://env.example.com", cfg.URL)
	}
	if cfg.Workspace != "env-ws" {
		t.Errorf("Workspace = %q, want env-ws", cfg.Workspace)
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want env-key", cfg.APIKey)
	}
}

func TestResolveFleetConfig_URLTrailingSlash(t *testing.T) {
	daemon := &DaemonSettings{
		Fleet: &FleetSettings{
			URL: "https://fleet.example.com/",
		},
	}

	cfg := ResolveFleetConfig(daemon)

	if cfg.URL != "https://fleet.example.com" {
		t.Errorf("URL = %q, want trailing slash trimmed to https://fleet.example.com", cfg.URL)
	}
}

func TestResolveFleetConfig_MultipleTrailingSlashes(t *testing.T) {
	daemon := &DaemonSettings{
		Fleet: &FleetSettings{
			URL: "https://fleet.example.com///",
		},
	}

	cfg := ResolveFleetConfig(daemon)

	if cfg.URL != "https://fleet.example.com" {
		t.Errorf("URL = %q, want multiple trailing slashes trimmed to https://fleet.example.com", cfg.URL)
	}
}

func TestResolveFleetConfig_EnvURLTrailingSlash(t *testing.T) {
	t.Setenv("LOOM_FLEET_URL", "https://env.example.com/")

	cfg := ResolveFleetConfig(nil)

	if cfg.URL != "https://env.example.com" {
		t.Errorf("URL = %q, want trailing slash trimmed from env override", cfg.URL)
	}
}

func TestResolveFleetConfig_EmptyWorkspaceDefaults(t *testing.T) {
	daemon := &DaemonSettings{
		Fleet: &FleetSettings{
			URL: "https://fleet.example.com",
			// Workspace left empty
		},
	}

	cfg := ResolveFleetConfig(daemon)

	if cfg.Workspace != "default" {
		t.Errorf("Workspace = %q, want %q when not set", cfg.Workspace, "default")
	}
}
