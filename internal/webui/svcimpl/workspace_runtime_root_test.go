package svcimpl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRuntimeRootRejectsInvalidIDs(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	for _, wsID := range []string{"", ".", "..", "a/b", `a\b`, "/abs", `..\up`} {
		if got := workspaceRuntimeRoot(wsID); got != "" {
			t.Errorf("workspaceRuntimeRoot(%q) = %q, want empty", wsID, got)
		}
	}
}

func TestWorkspaceRuntimeRootNonexistentDir(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	if got := workspaceRuntimeRoot("WS-NOPE"); got != "" {
		t.Errorf("workspaceRuntimeRoot(WS-NOPE) = %q, want empty for missing dir", got)
	}
}

func TestWorkspaceRuntimeRootExistingDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	wsDir := filepath.Join(configDir, "workspaces", "WS1")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace dir: %v", err)
	}

	if got := workspaceRuntimeRoot("WS1"); got != wsDir {
		t.Errorf("workspaceRuntimeRoot(WS1) = %q, want %q", got, wsDir)
	}
}

func TestWorkspaceRuntimeRootFileNotDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	if err := os.MkdirAll(filepath.Join(configDir, "workspaces"), 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workspaces", "WSFILE"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if got := workspaceRuntimeRoot("WSFILE"); got != "" {
		t.Errorf("workspaceRuntimeRoot(WSFILE) = %q, want empty for non-directory", got)
	}
}
