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

func TestGetLogDirUsesWorkspaceRuntimeDir(t *testing.T) {
	runtimeDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)

	dir, err := GetLogDir()
	if err != nil {
		t.Fatalf("GetLogDir() error = %v", err)
	}
	expected := filepath.Join(runtimeDir, ".loom", "logs")
	if dir != expected {
		t.Errorf("GetLogDir() = %q, want %q", dir, expected)
	}
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

func TestGetLogDirEnvPriorityAndTaskPathValidation(t *testing.T) {
	runtimeDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	dir, err := GetLogDir()
	if err != nil {
		t.Fatalf("GetLogDir: %v", err)
	}
	if dir != filepath.Join(runtimeDir, ".loom", "logs") {
		t.Fatalf("GetLogDir = %q, want runtime dir", dir)
	}

	taskDir := filepath.Join(runtimeDir, ".loom", "logs", "_default", "tasks", "task-1")
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	taskPath, err := GetTaskLogPath("", "task-1", "review")
	if err != nil {
		t.Fatalf("GetTaskLogPath: %v", err)
	}
	if taskPath != filepath.Join(taskDir, "review.log") {
		t.Fatalf("task path = %q", taskPath)
	}
}

func TestListTaskPhasesErrorBranches(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_CONFIG_DIR", "")

	missing, err := ListTaskPhases("WS", "missing")
	if err != nil {
		t.Fatalf("missing ListTaskPhases: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing phases = %#v", missing)
	}

	taskBase := filepath.Join(runtimeDir, ".loom", "logs", "WS", "tasks")
	if err := os.MkdirAll(taskBase, 0755); err != nil {
		t.Fatalf("mkdir task base: %v", err)
	}
	fileTask := filepath.Join(taskBase, "file-task")
	if err := os.WriteFile(fileTask, []byte("not a dir"), 0600); err != nil {
		t.Fatalf("write task file: %v", err)
	}
	if _, err := ListTaskPhases("WS", "file-task"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file task err = %v", err)
	}

	realTask := filepath.Join(taskBase, "real-task")
	if err := os.MkdirAll(realTask, 0755); err != nil {
		t.Fatalf("mkdir real task: %v", err)
	}
	linkTask := filepath.Join(taskBase, "link-task")
	if err := os.Symlink(realTask, linkTask); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ListTaskPhases("WS", "link-task"); err == nil || !strings.Contains(err.Error(), "refusing to follow symlink") {
		t.Fatalf("symlink task err = %v", err)
	}
}

func TestPathWithinDirAndResolvePathHelpers(t *testing.T) {
	root := t.TempDir()
	if !pathWithinDir(root, root) {
		t.Fatal("root should be within itself")
	}
	if pathWithinDir(filepath.Dir(root), root) {
		t.Fatal("parent should not be within child")
	}

	missing := filepath.Join(root, "missing", "leaf")
	resolved, err := resolvePathForComparison(missing)
	if err != nil {
		t.Fatalf("resolve missing path: %v", err)
	}
	if !strings.HasSuffix(resolved, filepath.Join("missing", "leaf")) {
		t.Fatalf("resolved missing path = %q", resolved)
	}
	if FileExists(missing) {
		t.Fatal("FileExists returned true for missing file")
	}
}

func TestLogPathAdditionalErrorAndSkipBranches(t *testing.T) {
	t.Run("home lookup errors propagate", func(t *testing.T) {
		t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")
		t.Setenv("LOOM_CONFIG_DIR", "")
		t.Setenv("HOME", "")
		if _, err := GetLogDir(); err == nil {
			t.Fatal("GetLogDir HOME error = nil")
		}
		if _, err := GetWorkspaceLogDir("WS"); err == nil {
			t.Fatal("GetWorkspaceLogDir HOME error = nil")
		}
		if _, err := GetAgentLogPath("WS", "agent"); err == nil {
			t.Fatal("GetAgentLogPath HOME error = nil")
		}
		if _, err := GetTaskLogPath("WS", "task", "phase"); err == nil {
			t.Fatal("GetTaskLogPath HOME error = nil")
		}
		if _, err := GetTaskLogDir("WS", "task"); err == nil {
			t.Fatal("GetTaskLogDir HOME error = nil")
		}
		if _, err := ListTaskPhases("WS", "task"); err == nil {
			t.Fatal("ListTaskPhases HOME error = nil")
		}
	})

	t.Run("missing parent outside allowed dir", func(t *testing.T) {
		root := t.TempDir()
		allowed := filepath.Join(root, "allowed")
		outside := filepath.Join(root, "outside")
		if err := os.MkdirAll(allowed, 0755); err != nil {
			t.Fatalf("mkdir allowed: %v", err)
		}
		if err := os.MkdirAll(outside, 0755); err != nil {
			t.Fatalf("mkdir outside: %v", err)
		}
		err := ValidatePathWithinDir(filepath.Join(outside, "missing.log"), allowed)
		if err == nil || !strings.Contains(err.Error(), "outside allowed directory") {
			t.Fatalf("outside missing err = %v", err)
		}
	})

	t.Run("ListTaskPhases skips directories", func(t *testing.T) {
		runtimeDir := t.TempDir()
		t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
		taskDir := filepath.Join(runtimeDir, ".loom", "logs", "WS", "tasks", "task-with-dir")
		if err := os.MkdirAll(filepath.Join(taskDir, "nested.log"), 0755); err != nil {
			t.Fatalf("mkdir nested dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(taskDir, "run.log"), []byte("ok"), 0600); err != nil {
			t.Fatalf("write run log: %v", err)
		}
		phases, err := ListTaskPhases("WS", "task-with-dir")
		if err != nil {
			t.Fatalf("ListTaskPhases: %v", err)
		}
		if len(phases) != 1 || phases[0] != "run" {
			t.Fatalf("phases = %#v, want only run", phases)
		}
	})
}
