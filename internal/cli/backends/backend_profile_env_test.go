package backends

import (
	"strings"
	"testing"
)

// The supervisor injects CLAUDE_CONFIG_DIR / CODEX_HOME into the agent loom
// process (see supervisor.appendProfileEnv). The backends layer rebuilds the
// harness environment from cli.FilteredEnv(), so both variables must survive
// that filter for per-agent profile isolation to reach the harness child.
func TestProfileEnvPropagatesThroughHarnessEnvBuilders(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/profiles/worker/claude")
	t.Setenv("CODEX_HOME", "/profiles/worker/codex")

	find := func(env []string, key string) string {
		for _, kv := range env {
			if strings.HasPrefix(kv, key+"=") {
				return strings.TrimPrefix(kv, key+"=")
			}
		}
		return ""
	}

	claudeEnv := buildClaudeEnv(t.TempDir(), "worker")
	if got := find(claudeEnv, "CLAUDE_CONFIG_DIR"); got != "/profiles/worker/claude" {
		t.Errorf("buildClaudeEnv dropped CLAUDE_CONFIG_DIR, got %q", got)
	}

	codexEnv := buildBackendEnv(t.TempDir(), "worker")
	if got := find(codexEnv, "CODEX_HOME"); got != "/profiles/worker/codex" {
		t.Errorf("buildBackendEnv dropped CODEX_HOME, got %q", got)
	}
}
