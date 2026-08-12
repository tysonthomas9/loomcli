//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

func TestDriverRemoteProfileFiltersBroadCredentials(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/loom",
		"LANG=C.UTF-8",
		"LC_ALL=C",
		"TMPDIR=/tmp",
		"LOOM_CONFIG_DIR=/tmp/loom-config",
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

	got := envMap(platformruntime.FilterSubprocessEnv(platformruntime.SubprocessEnvDriverRemote, env))
	for _, key := range []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "LOOM_CONFIG_DIR", "LOOM_HOST_BRIDGE_HELPER", "LOOM_DRIVER_TASK_RUNNER_CMD_JSON", "LOOM_DRIVER_TASK_RUNNER_CMD"} {
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

func TestFlueRuntimeEnvCarriesNoFleetDBCredentials(t *testing.T) {
	t.Setenv("LOOM_FLEET_DB_URL", "https://fleet.invalid")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "broad-secret")
	t.Setenv("LOOM_FLEET_DB_ACTOR", "broad-actor")
	t.Setenv("LOOM_DRIVER_FLEET_DB_URL", "https://driver-fleet.invalid")
	t.Setenv("LOOM_DRIVER_FLEET_DB_API_KEY", "scoped-secret")
	t.Setenv("LOOM_DRIVER_FLEET_DB_ACTOR", "driver-run:run-1")

	env, err := flueRuntimeEnv(RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey: "TEST",
			RunID:        "run-1",
			NodeID:       "node-1",
			LeaseID:      "lease-1",
			FencingToken: 42,
		},
		BundleRoot: "/tmp/bundle",
		ServerPath: "/tmp/bundle/dist/server.mjs",
		Manifest:   map[string]string{"workflow_name": "epic-runner"},
		RunToken:   testWorkflowRunToken,
	}, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("flueRuntimeEnv: %v", err)
	}
	got := envMap(env)
	for key := range got {
		if strings.HasPrefix(key, "LOOM_DRIVER_FLEET_DB_") || strings.HasPrefix(key, "LOOM_FLEET_DB_") {
			t.Fatalf("fleet-db credential %s leaked into workflow runtime env: %+v", key, got)
		}
	}
	if got["LOOM_DRIVER_RUN_ID"] != "run-1" {
		t.Fatalf("LOOM_DRIVER_RUN_ID = %q, want run-1", got["LOOM_DRIVER_RUN_ID"])
	}
}

// TestNodeRunnerRuntimeEnvAuthSurface pins the token-only workflow env:
// exactly {LOOM_RUN_TOKEN, LOOM_DRIVER_API_URL, workspace/run/node id} on top
// of the allowlisted base — no static bearer or lease/fencing credential.
func TestNodeRunnerRuntimeEnvAuthSurface(t *testing.T) {
	runner := NodeRunner{APIBaseURL: "http://127.0.0.1:1", ExecTaskCommand: []string{"/bin/loom"}}
	env, err := runner.runtimeEnv(runtimeEnvAuthRequest("minted.jwt.token"), []byte(`{}`))
	if err != nil {
		t.Fatalf("runtimeEnv: %v", err)
	}
	assertRuntimeEnvAuthSurface(t, envMap(env), "minted.jwt.token")
	if _, err := runner.runtimeEnv(runtimeEnvAuthRequest(""), []byte(`{}`)); err == nil {
		t.Fatal("runtimeEnv accepted a workflow request without a run token")
	}
}

func runtimeEnvAuthRequest(runToken string) RunRequest {
	return RunRequest{
		Run: &domain.DriverRun{
			WorkspaceKey: "TEST",
			RunID:        "run-1",
			NodeID:       "node-1",
			LeaseID:      "lease-1",
			FencingToken: 42,
		},
		BundleRoot: "/tmp/bundle",
		ServerPath: "/tmp/bundle/dist/server.mjs",
		Manifest:   map[string]string{"workflow_name": "epic-runner"},
		RunToken:   runToken,
	}
}

func assertRuntimeEnvAuthSurface(t *testing.T, got map[string]string, runToken string) {
	t.Helper()
	if got["LOOM_RUN_TOKEN"] != runToken {
		t.Fatalf("LOOM_RUN_TOKEN = %q, want %q", got["LOOM_RUN_TOKEN"], runToken)
	}
	// Non-secret identity and the API endpoint always flow.
	for key, want := range map[string]string{
		"LOOM_DRIVER_WORKSPACE": "TEST",
		"LOOM_DRIVER_RUN_ID":    "run-1",
		"LOOM_DRIVER_NODE_ID":   "node-1",
		"LOOM_DRIVER_API_URL":   "http://127.0.0.1:1",
	} {
		if got[key] != want {
			t.Fatalf("%s = %q, want %q (env: %+v)", key, got[key], want, got)
		}
	}
	for _, key := range []string{"LOOM_DRIVER_API_TOKEN", "LOOM_DRIVER_LEASE_ID", "LOOM_DRIVER_FENCING_TOKEN"} {
		if _, present := got[key]; present {
			t.Fatalf("%s leaked into token-only workflow env: %+v", key, got)
		}
	}
	assertExactLockedDownDriverEnv(t, got)
}

// assertExactLockedDownDriverEnv asserts the locked-down env's LOOM_* surface
// is exactly the S3 target shape: run token + API URL + non-secret identity +
// the LOOM_FLUE_* invoke contract + exec-task command, plus base-allowlisted
// LOOM keys that may flow in from the parent process.
func assertExactLockedDownDriverEnv(t *testing.T, got map[string]string) {
	t.Helper()
	allowed := map[string]struct{}{
		"LOOM_RUN_TOKEN":                 {},
		"LOOM_DRIVER_API_URL":            {},
		"LOOM_DRIVER_WORKSPACE":          {},
		"LOOM_DRIVER_RUN_ID":             {},
		"LOOM_DRIVER_NODE_ID":            {},
		"LOOM_DRIVER_EXEC_TASK_CMD_JSON": {},
		"LOOM_FLUE_SERVER_PATH":          {},
		"LOOM_FLUE_BUNDLE_ROOT":          {},
		"LOOM_FLUE_WORKFLOW_NAME":        {},
		"LOOM_FLUE_INVOKE_PAYLOAD":       {},
		// Base allowlist (env.go) entries that may legitimately flow in from
		// the parent process env.
		"LOOM_CONFIG_DIR":         {},
		"LOOM_FLUE_AGENT_MODEL":   {},
		"LOOM_HOST_BRIDGE_HELPER": {},
		TaskRunnerCommandJSONEnv:  {},
		TaskRunnerCommandEnv:      {},
	}
	for key := range got {
		if !strings.HasPrefix(key, "LOOM_") {
			continue
		}
		if _, ok := allowed[key]; !ok {
			t.Fatalf("unexpected %s in locked-down workflow env: %+v", key, got)
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
