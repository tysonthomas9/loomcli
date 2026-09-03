package agentprofile

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// TestDir pins the layout the supervisor injects from. supervisor.AppendProfileEnv
// resolves through Dir precisely so the two cannot disagree; if they ever did,
// agents would write transcripts where no reader looks — silently.
func TestDir(t *testing.T) {
	project := "/ws/PUPPET"
	if got, want := Dir(project, "jack"), filepath.Join(project, ".loom", "agent-profiles", "jack"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if DirName != "agent-profiles" {
		t.Errorf("DirName = %q, want agent-profiles", DirName)
	}
}

// TestDir_RejectsUnusableNames covers the inputs that must not produce a path:
// an empty agent would resolve the agent-profiles root itself (where a stray
// "claude" dir would look like a real profile), and a traversing name would
// resolve a profile outside the workspace.
func TestDir_RejectsUnusableNames(t *testing.T) {
	cases := []struct{ project, agent string }{
		{"", "jack"},
		{"/ws", ""},
		{"", ""},
		{"/ws", ".."},
		{"/ws", "."},
		{"/ws", "../escape"},
		{"/ws", "nested/agent"},
		{"/ws", "/abs"},
	}
	for _, tc := range cases {
		if got := Dir(tc.project, tc.agent); got != "" {
			t.Errorf("Dir(%q, %q) = %q, want \"\"", tc.project, tc.agent, got)
		}
	}
}

func TestBackendDirs_ExistenceIsTheContract(t *testing.T) {
	project := t.TempDir()
	agent := "jack"

	// Nothing staged: both absent.
	if got := ClaudeConfigDir(project, agent); got != "" {
		t.Errorf("ClaudeConfigDir with no profile = %q, want \"\"", got)
	}
	if got := CodexHome(project, agent); got != "" {
		t.Errorf("CodexHome with no profile = %q, want \"\"", got)
	}

	// Only claude/ staged: claude resolves, codex still absent.
	claudeDir := filepath.Join(Dir(project, agent), "claude")
	mkdirAll(t, claudeDir)
	if got := ClaudeConfigDir(project, agent); got != claudeDir {
		t.Errorf("ClaudeConfigDir = %q, want %q", got, claudeDir)
	}
	if got := CodexHome(project, agent); got != "" {
		t.Errorf("CodexHome = %q, want \"\" (codex/ not staged)", got)
	}

	// codex/ staged too.
	codexDir := filepath.Join(Dir(project, agent), "codex")
	mkdirAll(t, codexDir)
	if got := CodexHome(project, agent); got != codexDir {
		t.Errorf("CodexHome = %q, want %q", got, codexDir)
	}
}

// TestBackendDirs_FileIsNotADir keeps a regular file from being mistaken for a
// profile: pointing CLAUDE_CONFIG_DIR at a file yields a reader that finds
// nothing, which is indistinguishable from a broken run.
func TestBackendDirs_FileIsNotADir(t *testing.T) {
	project := t.TempDir()
	agent := "jack"
	mkdirAll(t, Dir(project, agent))
	if err := os.WriteFile(filepath.Join(Dir(project, agent), "claude"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if got := ClaudeConfigDir(project, agent); got != "" {
		t.Errorf("ClaudeConfigDir for a regular file = %q, want \"\"", got)
	}
}

// TestBackendDirs_OtherAgentIsolated guards against cross-agent leakage: one
// agent's profile must never resolve for another.
func TestBackendDirs_OtherAgentIsolated(t *testing.T) {
	project := t.TempDir()
	mkdirAll(t, filepath.Join(Dir(project, "jack"), "claude"))
	if got := ClaudeConfigDir(project, "jill"); got != "" {
		t.Errorf("ClaudeConfigDir for a different agent = %q, want \"\"", got)
	}
}
