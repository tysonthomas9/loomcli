package config

import "testing"

func clearFleetDBEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_FLEETDB_REDIS_URL", "")
	t.Setenv("LOOM_FLEETDB_WORKSPACE", "")
	t.Setenv("LOOM_FLEETDB_AUTO_START", "")
}

func TestResolveFleetDBConfig_Defaults(t *testing.T) {
	clearFleetDBEnv(t)

	cfg, enabled := ResolveFleetDBConfig(&DaemonSettings{})

	if enabled {
		t.Error("enabled should default to false")
	}
	if cfg.Workspace != "default" {
		t.Errorf("Workspace = %q, want default", cfg.Workspace)
	}
	if cfg.RedisURL != "" || cfg.AutoStart {
		t.Errorf("unexpected FleetDB defaults: %+v", cfg)
	}
}

func TestResolveFleetDBConfig_Env(t *testing.T) {
	clearFleetDBEnv(t)
	t.Setenv("LOOM_FLEETDB_REDIS_URL", "redis://env:6379")
	t.Setenv("LOOM_FLEETDB_WORKSPACE", "prod")
	t.Setenv("LOOM_FLEETDB_AUTO_START", "true")

	cfg, enabled := ResolveFleetDBConfig(nil)

	if enabled {
		t.Error("enabled should remain false; FleetDB is the default store and no longer toggled by YAML")
	}
	if cfg.RedisURL != "redis://env:6379" || cfg.Workspace != "prod" || !cfg.AutoStart {
		t.Errorf("env FleetDB config = %+v", cfg)
	}
}
