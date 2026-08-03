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

	cfg := ResolveFleetDBConfig()
	if cfg.Workspace != "" {
		t.Errorf("Workspace = %q, want empty", cfg.Workspace)
	}
	if cfg.RedisURL != "" || cfg.AutoStart {
		t.Errorf("unexpected FleetDB defaults: %+v", cfg)
	}
}

func TestResolveFleetDBConfig_NormalizesDefaultWorkspace(t *testing.T) {
	clearFleetDBEnv(t)
	t.Setenv("LOOM_FLEETDB_WORKSPACE", " default ")

	cfg := ResolveFleetDBConfig()

	if cfg.Workspace != "DEFAULT" {
		t.Errorf("Workspace = %q, want DEFAULT", cfg.Workspace)
	}
}

func TestResolveFleetDBConfig_Env(t *testing.T) {
	clearFleetDBEnv(t)
	t.Setenv("LOOM_FLEETDB_REDIS_URL", "redis://env:6379")
	t.Setenv("LOOM_FLEETDB_WORKSPACE", "prod")
	t.Setenv("LOOM_FLEETDB_AUTO_START", "true")

	cfg := ResolveFleetDBConfig()
	if cfg.RedisURL != "redis://env:6379" || cfg.Workspace != "prod" || !cfg.AutoStart {
		t.Errorf("env FleetDB config = %+v", cfg)
	}
}
