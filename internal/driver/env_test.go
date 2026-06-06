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
		"LOOM_DRIVER_RUN_ID=stale-run",
		"LOOM_FLEET_DB_URL=https://fleet.invalid",
		"LOOM_FLEET_DB_API_KEY=broad-secret",
		"LOOM_TASK_RUN_LEASE_TOKEN=task-token",
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
	for _, key := range []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "LOOM_HOST_BRIDGE_HELPER"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s missing from filtered env: %+v", key, got)
		}
	}
	for _, key := range []string{
		"LOOM_DRIVER_RUN_ID",
		"LOOM_FLEET_DB_URL",
		"LOOM_FLEET_DB_API_KEY",
		"LOOM_TASK_RUN_LEASE_TOKEN",
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
