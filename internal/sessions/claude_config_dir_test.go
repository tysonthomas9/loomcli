package sessions

import (
	"path/filepath"
	"strings"
	"testing"
)

// The transcript lookup and the backend's auth check must agree on where
// Claude Code's config lives. They did not: auth honored CLAUDE_CONFIG_DIR
// while the transcript path resolved only from $HOME, so setting the override
// produced passing auth and a lookup that never found anything — which fails
// the comment completion hook and reopens the owned task on every attempt.
func TestClaudeConfigDir_HonorsOverride(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/custom-claude")
	if got := ClaudeConfigDir(); got != "/tmp/custom-claude" {
		t.Fatalf("ClaudeConfigDir() = %q, want the CLAUDE_CONFIG_DIR override", got)
	}
}

func TestClaudeConfigDir_FallsBackToHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got := ClaudeConfigDir()
	if got == "" || !strings.HasSuffix(got, string(filepath.Separator)+".claude") {
		t.Fatalf("ClaudeConfigDir() = %q, want <home>/.claude", got)
	}
}

func TestClaudeProjectDir_UsesTheOverride(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/custom-claude")
	// No agent context: the process-scoped override is the whole resolution.
	got := claudeProjectDirFor("", "", "/work/dir")
	want := filepath.Join("/tmp/custom-claude", "projects", encodeClaudeCWD("/work/dir"))
	if got != want {
		t.Fatalf("claudeProjectDirFor() = %q, want %q", got, want)
	}
	if claudeProjectDirFor("", "", "") != "" {
		t.Fatal("empty workDir must yield an empty project dir")
	}
}
