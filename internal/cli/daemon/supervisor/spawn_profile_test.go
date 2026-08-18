package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendProfileEnv_AbsentDirsLeaveEnvUntouched(t *testing.T) {
	projectDir := t.TempDir()
	env := appendProfileEnv([]string{"A=1"}, projectDir, "worker")
	if len(env) != 1 {
		t.Fatalf("expected env untouched, got %v", env)
	}
}

func TestAppendProfileEnv_InjectsExistingProfileRoots(t *testing.T) {
	projectDir := t.TempDir()
	claudeDir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, "worker", "claude")
	codexDir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, "worker", "codex")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}

	env := appendProfileEnv(nil, projectDir, "worker")
	want := map[string]bool{
		"CLAUDE_CONFIG_DIR=" + claudeDir: false,
		"CODEX_HOME=" + codexDir:         false,
	}
	for _, kv := range env {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
	}
	for kv, seen := range want {
		if !seen {
			t.Errorf("missing %s in %v", kv, env)
		}
	}

	// A different agent with no profile dir stays legacy.
	if extra := appendProfileEnv(nil, projectDir, "critic"); len(extra) != 0 {
		t.Errorf("critic should have no profile env, got %v", extra)
	}
}
