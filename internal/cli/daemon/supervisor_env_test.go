package daemon

import (
	"os"
	"strings"
	"testing"
)

func TestCheckSupervisorEnvCleanEnvironment(t *testing.T) {
	// The six variables a correctly-registered supervisor actually carries.
	env := map[string]string{
		"LOOM_SERVER_URL":                    "http://127.0.0.1:3010",
		"LOOM_FLEET_DB_URL":                  "http://127.0.0.1:3012",
		"LOOM_FLEET_DB_API_KEY":              "key",
		"LOOM_FLEET_DB_ACTOR":                "loom",
		"LOOM_WORKSPACE":                     "PUPPET",
		"LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS": "1800",
	}
	if err := checkSupervisorEnv(lookupFrom(env)); err != nil {
		t.Fatalf("clean supervisor env rejected: %v", err)
	}
}

func TestCheckSupervisorEnvRejectsAgentIdentity(t *testing.T) {
	for _, name := range agentIdentityEnvNames {
		t.Run(name, func(t *testing.T) {
			err := checkSupervisorEnv(lookupFrom(map[string]string{name: "worker-3"}))
			if err == nil {
				t.Fatalf("%s in supervisor env accepted", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error does not name the offending variable: %v", err)
			}
			if !strings.Contains(err.Error(), "pm2 delete") {
				t.Errorf("error lacks remediation: %v", err)
			}
		})
	}
}

func TestCheckSupervisorEnvIgnoresEmptyValues(t *testing.T) {
	// pm2 and shell exports can leave a name set to "". That is not an
	// inherited identity, and failing on it would strand a healthy daemon.
	env := map[string]string{"LOOM_AGENT_NAME": "", "LOOM_ASSIGNED_TASK_ID": "   "}
	if err := checkSupervisorEnv(lookupFrom(env)); err != nil {
		t.Fatalf("blank identity vars rejected: %v", err)
	}
}

func TestScrubSupervisorEnvRemovesSessionState(t *testing.T) {
	poisoned := map[string]string{
		"LOOM_ROLE":              "coder",
		"LOOM_ROLE_LABELS":       "ready-to-implement",
		"LOOM_SESSION_ID":        "20260827-154517-worker-3--ec5e7685",
		"LOOM_AGENT_LEASE_ID":    "20260827-154517-worker-3--ec5e7685-lease",
		"LOOM_WORKTREE_PATH":     "/tmp/worktrees/worker-3",
		"LOOM_YIELD_FILE":        "/tmp/worktrees/worker-3/.yield",
		"CLAUDE_CONFIG_DIR":      "/tmp/agent-profiles/worker-3/claude",
		"CLAUDE_CODE_ENTRYPOINT": "cli",
		"CLAUDE_CODE_SESSION_ID": "35fb3983-45c4-47e5-8f58-8177f3e5e45e",
		"CLAUDE_PID":             "46426",
		"CLAUDECODE":             "1",
	}
	kept := map[string]string{
		"LOOM_WORKSPACE":                     "PUPPET",
		"LOOM_SERVER_URL":                    "http://127.0.0.1:3010",
		"LOOM_FLEET_DB_ACTOR":                "loom",
		"LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS": "1800",
		// Machine-level credentials the supervisor must still pass on.
		"CLAUDE_CODE_OAUTH_TOKEN": "token",
		"ANTHROPIC_API_KEY":       "key",
	}
	for k, v := range poisoned {
		t.Setenv(k, v)
	}
	for k, v := range kept {
		t.Setenv(k, v)
	}

	removed := scrubSupervisorEnv()

	for k := range poisoned {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s survived the scrub with value %q", k, v)
		}
		if !contains(removed, k) {
			t.Errorf("%s missing from the reported scrub list %v", k, removed)
		}
	}
	for k, want := range kept {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q (must not be scrubbed)", k, got, want)
		}
	}
}

func TestScrubSupervisorEnvCleanEnvironmentIsNoop(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "PUPPET")
	for _, name := range append(append([]string{}, agentSessionEnvNames...), "CLAUDE_CODE_SESSION_FOO") {
		os.Unsetenv(name)
	}
	if removed := scrubSupervisorEnv(); len(removed) != 0 {
		t.Fatalf("clean env reported scrubbed vars: %v", removed)
	}
	if os.Getenv("LOOM_WORKSPACE") != "PUPPET" {
		t.Fatal("LOOM_WORKSPACE was scrubbed")
	}
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
