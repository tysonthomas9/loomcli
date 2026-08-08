package backends

import (
	"strings"
	"testing"
)

func TestCloudTaskRunEnvelopeReachesEveryLegacyBackendWithoutControlPlaneCredentials(t *testing.T) {
	t.Setenv("LOOM_TASK_RUN_API_URL", "https://loom.example.test")
	t.Setenv("LOOM_WORKSPACE", "CLOUD")
	t.Setenv("LOOM_DRIVER_WORKSPACE", "CLOUD")
	t.Setenv("LOOM_TASK_RUN_ID", "task-run-1")
	t.Setenv("LOOM_TASK_ID", "CLOUD-1")
	t.Setenv("LOOM_TASK_RUN_NODE_ID", "node-1")
	t.Setenv("LOOM_TASK_RUN_LEASE_ID", "lease-1")
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", strings.Repeat("a", 64))
	t.Setenv("LOOM_RUNNER_LEASE_TOKEN", "")
	t.Setenv("LOOM_TASK_RUN_FENCING_TOKEN", "42")

	// These values are present on the cloud host but are service bootstrap
	// authority, not agent authority. The child must receive only the TaskRun
	// facade above. Request JSON is also excluded because it duplicates the
	// scoped lease credential and carries more input than `loom data` needs.
	t.Setenv("LOOM_FLEET_DB_URL", "https://fleet.example.test")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "fleet-secret")
	t.Setenv("LOOM_FLEET_DB_ACTOR", "loom-serve")
	t.Setenv("LOOM_DRIVER_FLEET_DB_API_KEY", "driver-fleet-secret")
	t.Setenv("LOOM_FLEET_API_KEY", "legacy-fleet-secret")
	t.Setenv("FLEET_TOKEN", "fleet-token")
	t.Setenv("LOOM_TASK_RUN_REQUEST_JSON", `{"lease_token":"duplicate-secret","input":{"private":"payload"}}`)

	environments := map[string][]string{
		"standard": buildBackendEnv("/worktree", "cloud-agent"),
		"claude":   buildClaudeEnv("/worktree", "cloud-agent"),
		"gemini":   buildGeminiEnv("/worktree", "cloud-agent"),
		"codex":    buildCodexInteractiveCmd("/worktree", "prompt", "cloud-agent").Env,
		"opencode": buildOpenCodeInteractiveCmd("/worktree", "prompt", "cloud-agent").Env,
		"cursor":   buildCursorInteractiveCmd("/worktree", "prompt", "cloud-agent").Env,
	}
	for _, backend := range []string{"claude", "gemini", "opencode", "cursor"} {
		launch, ok := harnessLeadInvocation(backend, "/worktree")
		if !ok {
			t.Fatalf("harness lead backend %s is unavailable", backend)
		}
		environments["harness-lead-"+backend] = launch.env
	}

	want := map[string]string{
		"LOOM_TASK_RUN_API_URL":       "https://loom.example.test",
		"LOOM_WORKSPACE":              "CLOUD",
		"LOOM_DRIVER_WORKSPACE":       "CLOUD",
		"LOOM_TASK_RUN_ID":            "task-run-1",
		"LOOM_TASK_ID":                "CLOUD-1",
		"LOOM_TASK_RUN_NODE_ID":       "node-1",
		"LOOM_TASK_RUN_LEASE_ID":      "lease-1",
		"LOOM_TASK_RUN_LEASE_TOKEN":   strings.Repeat("a", 64),
		"LOOM_TASK_RUN_FENCING_TOKEN": "42",
	}
	for name, env := range environments {
		t.Run(name, func(t *testing.T) {
			for key, value := range want {
				if got, ok := envValue(env, key); !ok || got != value {
					t.Errorf("%s = %q, present=%v; want %q", key, got, ok, value)
				}
			}
			for _, forbidden := range []string{
				"LOOM_FLEET_DB_URL",
				"LOOM_FLEET_DB_API_KEY",
				"LOOM_FLEET_DB_ACTOR",
				"LOOM_DRIVER_FLEET_DB_API_KEY",
				"LOOM_FLEET_API_KEY",
				"FLEET_TOKEN",
				"LOOM_TASK_RUN_REQUEST_JSON",
			} {
				if envHasKey(env, forbidden) {
					t.Errorf("forbidden control-plane or duplicate credential %s reached backend", forbidden)
				}
			}
		})
	}
}

func TestPartialTaskRunEnvelopeRemainsFailClosedInBackendChild(t *testing.T) {
	for _, name := range taskRunDataEnvelopeNames {
		t.Setenv(name, "")
	}
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", "partial-scoped-token")

	env := buildBackendEnv("/worktree", "cloud-agent")
	if got, ok := envValue(env, "LOOM_TASK_RUN_LEASE_TOKEN"); !ok || got != "partial-scoped-token" {
		t.Fatalf("partial TaskRun marker = %q, present=%v; want it retained so loom data fails closed", got, ok)
	}
}
