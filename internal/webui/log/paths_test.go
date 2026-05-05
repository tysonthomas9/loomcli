// This file tests functions (getWorkspaceLogDir, getAgentLogPath, getTaskLogPath,
// getTaskLogDir, listTaskPhases) that have been moved to the handlers/misc
// package. These tests should be migrated to handlers/misc or adapted to use
// the misc package's exports.

package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGetWorkspaceLogDir verifies workspace-scoped log directory resolution.
func TestGetWorkspaceLogDir(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	t.Run("valid UUID returns scoped dir", func(t *testing.T) {
		dir, err := getWorkspaceLogDir("abc-123-uuid")
		if err != nil {
			t.Fatalf("getWorkspaceLogDir() error = %v", err)
		}
		expected := filepath.Join(tmpHome, ".loom", "logs", "abc-123-uuid")
		if dir != expected {
			t.Errorf("getWorkspaceLogDir() = %q, want %q", dir, expected)
		}
	})

	t.Run("empty string falls back to _default", func(t *testing.T) {
		dir, err := getWorkspaceLogDir("")
		if err != nil {
			t.Fatalf("getWorkspaceLogDir() error = %v", err)
		}
		expected := filepath.Join(tmpHome, ".loom", "logs", "_default")
		if dir != expected {
			t.Errorf("getWorkspaceLogDir() = %q, want %q", dir, expected)
		}
	})
}

// TestGetAgentLogPath_WithWorkspace verifies agent log path includes workspace ID.
func TestGetAgentLogPath_WithWorkspace(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	// Create the parent directory so validatePathWithinDir can resolve it
	wsAgentDir := filepath.Join(tmpHome, ".loom", "logs", "uuid-123", "agents")
	if err := os.MkdirAll(wsAgentDir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	logPath, err := getAgentLogPath("uuid-123", "falcon")
	if err != nil {
		t.Fatalf("getAgentLogPath() error = %v", err)
	}

	expected := filepath.Join(tmpHome, ".loom", "logs", "uuid-123", "agents", "falcon.log")
	if logPath != expected {
		t.Errorf("getAgentLogPath() = %q, want %q", logPath, expected)
	}

	// Verify workspace ID is in the path
	if !strings.Contains(logPath, "uuid-123") {
		t.Errorf("expected path to contain workspace ID 'uuid-123', got %q", logPath)
	}
}

// TestGetTaskLogPath_WithWorkspace verifies task log path includes workspace ID.
func TestGetTaskLogPath_WithWorkspace(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	// Create parent directory for path validation
	wsTaskDir := filepath.Join(tmpHome, ".loom", "logs", "ws-456", "tasks", "task-abc")
	if err := os.MkdirAll(wsTaskDir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	logPath, err := getTaskLogPath("ws-456", "task-abc", "planning")
	if err != nil {
		t.Fatalf("getTaskLogPath() error = %v", err)
	}

	expected := filepath.Join(tmpHome, ".loom", "logs", "ws-456", "tasks", "task-abc", "planning.log")
	if logPath != expected {
		t.Errorf("getTaskLogPath() = %q, want %q", logPath, expected)
	}

	// Verify workspace ID is in the path
	if !strings.Contains(logPath, "ws-456") {
		t.Errorf("expected path to contain workspace ID 'ws-456', got %q", logPath)
	}
}

// TestGetTaskLogDir_WithWorkspace verifies task log directory includes workspace ID.
func TestGetTaskLogDir_WithWorkspace(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	// Create parent directory for path validation
	wsTaskDir := filepath.Join(tmpHome, ".loom", "logs", "ws-789", "tasks", "task-xyz")
	if err := os.MkdirAll(wsTaskDir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	dir, err := getTaskLogDir("ws-789", "task-xyz")
	if err != nil {
		t.Fatalf("getTaskLogDir() error = %v", err)
	}

	expected := filepath.Join(tmpHome, ".loom", "logs", "ws-789", "tasks", "task-xyz")
	if dir != expected {
		t.Errorf("getTaskLogDir() = %q, want %q", dir, expected)
	}

	// Verify workspace ID is in the path
	if !strings.Contains(dir, "ws-789") {
		t.Errorf("expected path to contain workspace ID 'ws-789', got %q", dir)
	}
}

// TestListTaskPhases_WithWorkspace creates a workspace-scoped temp dir structure
// and verifies that phases are listed correctly.
func TestListTaskPhases_WithWorkspace(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	wsID := "ws-phases-test"
	taskID := "task-phases-123"
	taskDir := filepath.Join(tmpHome, ".loom", "logs", wsID, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task dir: %v", err)
	}

	// Create phase log files
	for _, phase := range []string{"planning", "implementation"} {
		logPath := filepath.Join(taskDir, phase+".log")
		if err := os.WriteFile(logPath, []byte("test content\n"), 0o644); err != nil {
			t.Fatalf("failed to write %s log: %v", phase, err)
		}
	}

	// Also create a non-.log file that should be ignored
	if err := os.WriteFile(filepath.Join(taskDir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("failed to write notes.txt: %v", err)
	}

	phases, err := listTaskPhases(wsID, taskID)
	if err != nil {
		t.Fatalf("listTaskPhases() error = %v", err)
	}

	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %v", len(phases), phases)
	}

	// Check that both phases are present (order may vary)
	phaseSet := map[string]bool{}
	for _, p := range phases {
		phaseSet[p] = true
	}
	if !phaseSet["planning"] {
		t.Error("expected 'planning' phase in results")
	}
	if !phaseSet["implementation"] {
		t.Error("expected 'implementation' phase in results")
	}
}

// TestGetAgentLogPath_EmptyWorkspace_UsesDefault verifies that an empty workspace
// ID falls back to the _default directory.
func TestGetAgentLogPath_EmptyWorkspace_UsesDefault(t *testing.T) {
	tmpHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("HOME", tmpHome)

	// Create the _default agents directory for path validation
	defaultAgentDir := filepath.Join(tmpHome, ".loom", "logs", "_default", "agents")
	if err := os.MkdirAll(defaultAgentDir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	logPath, err := getAgentLogPath("", "falcon")
	if err != nil {
		t.Fatalf("getAgentLogPath() error = %v", err)
	}

	expected := filepath.Join(tmpHome, ".loom", "logs", "_default", "agents", "falcon.log")
	if logPath != expected {
		t.Errorf("getAgentLogPath(\"\", \"falcon\") = %q, want %q", logPath, expected)
	}

	// Verify _default is in the path, not an empty segment
	if !strings.Contains(logPath, "_default") {
		t.Errorf("expected path to contain '_default', got %q", logPath)
	}
}

func TestValidatePathWithinDir_AllowsSymlinkedAllowedDir(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("failed to create real dir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	filePath := filepath.Join(linkDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("ok"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	if err := ValidatePathWithinDir(filePath, linkDir); err != nil {
		t.Fatalf("ValidatePathWithinDir() error = %v", err)
	}
}

func TestValidatePathWithinDir_AllowsMissingPathUnderSymlinkedAllowedDir(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("failed to create real dir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	filePath := filepath.Join(linkDir, "new-file.txt")
	if err := ValidatePathWithinDir(filePath, linkDir); err != nil {
		t.Fatalf("ValidatePathWithinDir() error = %v", err)
	}
}

func TestValidatePathWithinDir_DeniesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	allowedDir := filepath.Join(root, "allowed")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(allowedDir, 0o755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("failed to write outside file: %v", err)
	}
	linkPath := filepath.Join(allowedDir, "escape")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := ValidatePathWithinDir(filepath.Join(linkPath, "secret.txt"), allowedDir)
	if err == nil {
		t.Fatal("expected error for symlink escape, got nil")
	}
	if !strings.Contains(err.Error(), "outside allowed directory") {
		t.Fatalf("error = %q, want outside allowed directory", err.Error())
	}
}
