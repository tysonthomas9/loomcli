package config

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

func clearFleetEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_FLEET_URL", "")
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_FLEET_API_KEY", "")
	t.Setenv("LOOM_FLEET_ACTOR", "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")
	t.Setenv("LOOM_AGENT_NAME", "")
	t.Setenv("USER", "")
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
	t.Setenv("LOOM_FLEET_URL", "https://fleet.example.com///")
	t.Setenv("LOOM_WORKSPACE", "prod")
	t.Setenv("LOOM_FLEET_API_KEY", "secret")
	t.Setenv("LOOM_FLEET_ACTOR", "operator")

	cfg := ResolveFleetConfig(nil)

	if cfg.URL != "https://fleet.example.com" {
		t.Errorf("URL = %q, want trimmed fleet URL", cfg.URL)
	}
	if cfg.Workspace != "prod" || cfg.APIKey != "secret" || cfg.Actor != "operator" {
		t.Errorf("env fleet config = %+v", cfg)
	}
}

func TestResolveFleetConfig_AgentNameOverridesConfiguredActor(t *testing.T) {
	clearFleetEnv(t)
	t.Setenv("LOOM_FLEET_ACTOR", "config-actor")
	t.Setenv("LOOM_AGENT_NAME", "nova")

	cfg := ResolveFleetConfig(nil)

	if cfg.Actor != "nova" {
		t.Errorf("Actor = %q, want LOOM_AGENT_NAME override", cfg.Actor)
	}
}

func TestResolveFleetConfig_FleetDBActorOverridesConfiguredActor(t *testing.T) {
	clearFleetEnv(t)
	t.Setenv("LOOM_FLEET_ACTOR", "config-actor")
	t.Setenv(bootstrap.EnvFleetDBActor, "agent-x")

	cfg := ResolveFleetConfig(nil)

	if cfg.Actor != "agent-x" {
		t.Errorf("Actor = %q, want daemon-stamped actor %q", cfg.Actor, "agent-x")
	}
}

func TestResolveFleetConfig_ConfiguredActorFallbackWithoutWorkerEnv(t *testing.T) {
	clearFleetEnv(t)
	t.Setenv("LOOM_FLEET_ACTOR", "config-actor")
	t.Setenv("USER", "os-user")

	cfg := ResolveFleetConfig(nil)

	if cfg.Actor != "config-actor" {
		t.Errorf("Actor = %q, want configured actor %q", cfg.Actor, "config-actor")
	}
}
