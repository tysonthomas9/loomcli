package storeadapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExistingDefaultWorkspaceDirRejectsInvalidNames(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "/abs", `..\up`} {
		if got := ExistingDefaultWorkspaceDir(name); got != "" {
			t.Errorf("ExistingDefaultWorkspaceDir(%q) = %q, want empty", name, got)
		}
	}
}

func TestExistingDefaultWorkspaceDirNonexistent(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	if got := ExistingDefaultWorkspaceDir("WS-NOPE"); got != "" {
		t.Errorf("ExistingDefaultWorkspaceDir(WS-NOPE) = %q, want empty for missing dir", got)
	}
}

func TestExistingDefaultWorkspaceDirExisting(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	wsDir := filepath.Join(configDir, "workspaces", "WS1")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace dir: %v", err)
	}

	if got := ExistingDefaultWorkspaceDir("WS1"); got != wsDir {
		t.Errorf("ExistingDefaultWorkspaceDir(WS1) = %q, want %q", got, wsDir)
	}
}

func TestExistingDefaultWorkspaceDirFileNotDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	if err := os.MkdirAll(filepath.Join(configDir, "workspaces"), 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workspaces", "WSFILE"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if got := ExistingDefaultWorkspaceDir("WSFILE"); got != "" {
		t.Errorf("ExistingDefaultWorkspaceDir(WSFILE) = %q, want empty for non-directory", got)
	}
}
