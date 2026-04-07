package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnsureDaemonForWorkspace_ContextCancelled(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	// Mock: daemon start succeeds but status never reports running.
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		return CommandResult{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := EnsureDaemonForWorkspace(deps, ctx, t.TempDir(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention 'cancelled', got: %v", err)
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("error should wrap context.Canceled, got: %v", err)
	}
}

func TestEnsureDaemonForWorkspace_ContextDeadlineExceeded(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	// Mock: daemon start succeeds but status never reports running.
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		return CommandResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// Give the context time to expire before calling.
	time.Sleep(5 * time.Millisecond)

	err := EnsureDaemonForWorkspace(deps, ctx, t.TempDir(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error from expired context deadline")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention 'cancelled', got: %v", err)
	}
}

func TestEnsureDaemonForWorkspace_TimeoutFallback(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	// Mock: daemon start succeeds but status never reports running.
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		return CommandResult{}
	}

	err := EnsureDaemonForWorkspace(deps, context.Background(), t.TempDir(), 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("error should mention 'did not become ready', got: %v", err)
	}
}

func TestDefaultDaemonStartupTimeout(t *testing.T) {
	t.Parallel()
	if DefaultDaemonStartupTimeout != 30*time.Second {
		t.Errorf("DefaultDaemonStartupTimeout = %v, want 30s", DefaultDaemonStartupTimeout)
	}
}

func TestEnsureCurrentProjectRegistered_GeneratesUUID(t *testing.T) {
	// Not parallel: uses t.Setenv and os.Chdir.
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	// Create a temp directory to serve as cwd so the workspace name is predictable.
	wsDir := filepath.Join(t.TempDir(), "myproject")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// Change to the workspace directory so ensureCurrentProjectRegistered uses it.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(wsDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	EnsureCurrentProjectRegistered()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil after ensureCurrentProjectRegistered")
	}

	ws, ok := cfg.Workspaces["myproject"]
	if !ok {
		t.Fatal("workspace 'myproject' not found in config")
	}
	if ws.ID == "" {
		t.Fatal("workspace ID is empty; auto-init should generate a UUID")
	}
	if _, err := uuid.Parse(ws.ID); err != nil {
		t.Errorf("workspace ID %q is not a valid UUID: %v", ws.ID, err)
	}
}

func TestEnsureCurrentProjectRegistered_SkipsExistingByPath(t *testing.T) {
	// Not parallel: uses t.Setenv and os.Chdir.
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	wsDir := filepath.Join(t.TempDir(), "existing")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// Resolve symlinks (macOS /var -> /private/var) so the path matches os.Getwd().
	wsDir, err := filepath.EvalSymlinks(wsDir)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}

	// Pre-create a config with this path already registered under a different name.
	existingID := "pre-existing-uuid-789"
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"other-name": {ID: existingID, Path: wsDir, Repos: []RepoConfig{}},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(wsDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	EnsureCurrentProjectRegistered()

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Should not have added a duplicate entry.
	if len(loaded.Workspaces) != 1 {
		t.Errorf("len(Workspaces) = %d, want 1 (should not duplicate)", len(loaded.Workspaces))
	}
	ws := loaded.Workspaces["other-name"]
	if ws.ID != existingID {
		t.Errorf("ID = %q, want %q (should preserve existing)", ws.ID, existingID)
	}
}

func TestEnsureCurrentProjectRegistered_RefusesToSaveOnParseError(t *testing.T) {
	// Not parallel: uses t.Setenv and os.Chdir.
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	// Write invalid YAML to the config path.
	configPath := filepath.Join(configDir, "config.yaml")
	invalidContent := []byte("{{{broken yaml")
	if err := os.WriteFile(configPath, invalidContent, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	wsDir := filepath.Join(t.TempDir(), "testproject")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(wsDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	EnsureCurrentProjectRegistered()

	// The config file must still contain the original invalid content (not overwritten).
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(invalidContent) {
		t.Errorf("config file was overwritten; got %q, want %q", string(got), string(invalidContent))
	}
}

func TestStopDaemonForWorkspace_CallsBdDaemonStop(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	var calledDir, calledName string
	var calledArgs []string
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		calledDir = dir
		calledName = name
		calledArgs = args
		return CommandResult{}
	}

	wsDir := t.TempDir()
	StopDaemonForWorkspace(deps, wsDir)

	if calledDir != wsDir {
		t.Errorf("dir = %q, want %q", calledDir, wsDir)
	}
	if calledName != "bd" {
		t.Errorf("name = %q, want %q", calledName, "bd")
	}
	wantArgs := []string{"daemon", "stop"}
	if len(calledArgs) != len(wantArgs) || calledArgs[0] != wantArgs[0] || calledArgs[1] != wantArgs[1] {
		t.Errorf("args = %v, want %v", calledArgs, wantArgs)
	}
}

func TestStopDaemonForWorkspace_ErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		return CommandResult{Err: fmt.Errorf("daemon not running")}
	}

	// Should not panic or crash
	StopDaemonForWorkspace(deps, t.TempDir())
}

func TestStopDaemonForWorkspace_EmptyDir(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	called := false
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		called = true
		return CommandResult{}
	}

	StopDaemonForWorkspace(deps, "")

	if called {
		t.Error("execCommand should not be called with empty dir")
	}
}

func TestEnsureCurrentProjectRegistered_RefusesToSaveOnReadError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping: root can read anything")
	}
	// Not parallel: uses t.Setenv and os.Chdir.

	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	// Create a config file and make it unreadable.
	configPath := filepath.Join(configDir, "config.yaml")
	originalContent := []byte("default_workspace: myws\n")
	if err := os.WriteFile(configPath, originalContent, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(configPath, 0000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { os.Chmod(configPath, 0644) })

	wsDir := filepath.Join(t.TempDir(), "testproject2")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(wsDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	EnsureCurrentProjectRegistered()

	// Restore permissions and verify content unchanged.
	if err := os.Chmod(configPath, 0644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(originalContent) {
		t.Errorf("config file was overwritten; got %q, want %q", string(got), string(originalContent))
	}
}
