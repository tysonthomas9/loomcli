package config

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

func clearFleetEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_FLEET_URL", "")
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_FLEET_API_KEY", "")
	t.Setenv(bootstrap.EnvFleetDBAPIKey, "")
	t.Setenv("LOOM_FLEET_ACTOR", "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")
	t.Setenv("LOOM_AGENT_NAME", "")
}

func TestResolveFleetConfig_Defaults(t *testing.T) {
	clearFleetEnv(t)

	cfg := ResolveFleetConfig(&DaemonSettings{})

	if cfg.Workspace != "" {
		t.Errorf("Workspace = %q, want empty", cfg.Workspace)
	}
	if cfg.URL != "" || cfg.APIKey != "" || cfg.Actor != "" {
		t.Errorf("unexpected fleet config defaults: %+v", cfg)
	}
}

func TestResolveFleetConfig_NormalizesDefaultWorkspace(t *testing.T) {
	clearFleetEnv(t)
	t.Setenv("LOOM_WORKSPACE", " default ")

	cfg := ResolveFleetConfig(nil)

	if cfg.Workspace != "DEFAULT" {
		t.Errorf("Workspace = %q, want DEFAULT", cfg.Workspace)
	}
}

func TestResolveFleetConfig_Env(t *testing.T) {
	clearFleetEnv(t)
	t.Setenv(bootstrap.EnvFleetDBURL, "https://fleet.example.com///")
	t.Setenv("LOOM_WORKSPACE", "prod")
	t.Setenv(bootstrap.EnvFleetDBAPIKey, "secret")
	t.Setenv(bootstrap.EnvFleetDBActor, "operator")

	cfg := ResolveFleetConfig(nil)

	if cfg.URL != "https://fleet.example.com" {
		t.Errorf("URL = %q, want trimmed fleet URL", cfg.URL)
	}
	if cfg.Workspace != "prod" || cfg.APIKey != "secret" || cfg.Actor != "operator" {
		t.Errorf("env fleet config = %+v", cfg)
	}
}

func TestResolveFleetConfig_IgnoresLegacyFleetURL(t *testing.T) {
	clearFleetEnv(t)
	t.Setenv("LOOM_FLEET_URL", "https://legacy.example.com")
	t.Setenv("LOOM_FLEET_API_KEY", "legacy-secret")
	t.Setenv("LOOM_FLEET_ACTOR", "legacy-actor")

	cfg := ResolveFleetConfig(nil)

	if cfg.URL != "" || cfg.APIKey != "" || cfg.Actor != "" {
		t.Fatalf("ResolveFleetConfig read legacy fleet env: %+v", cfg)
	}
}

func TestResolveFleetConfig_AgentNameActorFallback(t *testing.T) {
	clearFleetEnv(t)
	t.Setenv("LOOM_AGENT_NAME", "nova")

	cfg := ResolveFleetConfig(nil)

	if cfg.Actor != "nova" {
		t.Errorf("Actor = %q, want LOOM_AGENT_NAME fallback", cfg.Actor)
	}
}
