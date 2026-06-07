package driver

import "testing"

func TestScopedSubprocessBaseEnvFiltersBroadCredentials(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/loom",
		"LANG=C.UTF-8",
		"LC_ALL=C",
		"TMPDIR=/tmp",
		"LOOM_HOST_BRIDGE_HELPER=1",
		"LOOM_DRIVER_TASK_RUNNER_CMD_JSON=[\"/tmp/runner\"]",
		"LOOM_DRIVER_TASK_RUNNER_CMD=/tmp/runner",
		"LOOM_DRIVER_RUN_ID=stale-run",
		"LOOM_FLEET_DB_URL=https://fleet.invalid",
		"LOOM_FLEET_DB_API_KEY=broad-secret",
		"LOOM_TASK_RUN_LEASE_TOKEN=task-token",
		"LOOM_DRIVER_FLEET_DB_URL=https://driver-fleet.invalid",
		"LOOM_DRIVER_FLEET_DB_API_KEY=driver-scoped-secret",
		"LOOM_DRIVER_FLEET_DB_ACTOR=driver-run:run-1",
		"OPENAI_API_KEY=model-secret",
		"ANTHROPIC_API_KEY=model-secret",
		"GITHUB_TOKEN=github-secret",
		"AWS_ACCESS_KEY_ID=aws-key",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"GOOGLE_APPLICATION_CREDENTIALS=/secrets/google.json",
		"GIT_CONFIG_KEY_0=core.sshCommand",
		"CUSTOM_VAR=value",
		"malformed",
	}

	got := envMap(scopedSubprocessBaseEnv(env))
	for _, key := range []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "LOOM_HOST_BRIDGE_HELPER", "LOOM_DRIVER_TASK_RUNNER_CMD_JSON", "LOOM_DRIVER_TASK_RUNNER_CMD"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s missing from filtered env: %+v", key, got)
		}
	}
	for _, key := range []string{
		"LOOM_DRIVER_RUN_ID",
		"LOOM_FLEET_DB_URL",
		"LOOM_FLEET_DB_API_KEY",
		"LOOM_TASK_RUN_LEASE_TOKEN",
		"LOOM_DRIVER_FLEET_DB_URL",
		"LOOM_DRIVER_FLEET_DB_API_KEY",
		"LOOM_DRIVER_FLEET_DB_ACTOR",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"GITHUB_TOKEN",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"GIT_CONFIG_KEY_0",
		"CUSTOM_VAR",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("%s leaked into filtered env: %+v", key, got)
		}
	}
}

func TestDriverRuntimeFleetDBHandoffEnvUsesExplicitScopedCredential(t *testing.T) {
	env := []string{
		"LOOM_FLEET_DB_URL=https://fleet.invalid",
		"LOOM_FLEET_DB_API_KEY=broad-secret",
		"LOOM_FLEET_DB_ACTOR=broad-actor",
		"LOOM_DRIVER_FLEET_DB_API_KEY=scoped-secret",
		"LOOM_DRIVER_FLEET_DB_ACTOR=driver-run:run-1",
	}

	got := envMap(driverRuntimeFleetDBHandoffEnv(env))
	if got["LOOM_DRIVER_FLEET_DB_URL"] != "https://fleet.invalid" {
		t.Fatalf("driver fleet URL = %q, want inherited FleetDB URL", got["LOOM_DRIVER_FLEET_DB_URL"])
	}
	if got["LOOM_DRIVER_FLEET_DB_API_KEY"] != "scoped-secret" {
		t.Fatalf("driver fleet API key = %q, want scoped-secret", got["LOOM_DRIVER_FLEET_DB_API_KEY"])
	}
	if got["LOOM_DRIVER_FLEET_DB_ACTOR"] != "driver-run:run-1" {
		t.Fatalf("driver fleet actor = %q, want driver-run:run-1", got["LOOM_DRIVER_FLEET_DB_ACTOR"])
	}
}

func TestDriverRuntimeFleetDBHandoffEnvDoesNotForwardBroadAPIKey(t *testing.T) {
	got := envMap(driverRuntimeFleetDBHandoffEnv([]string{
		"LOOM_FLEET_DB_URL=https://fleet.invalid",
		"LOOM_FLEET_DB_API_KEY=broad-secret",
	}))
	if got["LOOM_DRIVER_FLEET_DB_URL"] != "https://fleet.invalid" {
		t.Fatalf("driver fleet URL = %q, want inherited FleetDB URL", got["LOOM_DRIVER_FLEET_DB_URL"])
	}
	if _, ok := got["LOOM_DRIVER_FLEET_DB_API_KEY"]; ok {
		t.Fatalf("broad FleetDB API key was forwarded into driver handoff: %+v", got)
	}
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		for i := 0; i < len(entry); i++ {
			if entry[i] == '=' {
				out[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	return out
}
