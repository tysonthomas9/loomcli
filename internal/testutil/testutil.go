// Package testutil provides shared test utilities for use across packages.
// This package should only be imported by _test.go files.
package testutil

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var loomDesktopEnvKeys = []string{
	"LOOM_WORKSPACE",
	"LOOM_WORKSPACE_RUNTIME_DIR",
	"LOOM_CONFIG_DIR",
	"LOOM_DESKTOP_DATA_DIR",
	"LOOM_FRONTEND_DIR",
	"LOOM_WEBUI_URL",
	"LOOM_LOCAL_RUNTIME",
	"LOOM_NOTIFY_TOKEN",
	"LOOM_AGENT_NAME",
	"LOOM_AGENT_ROLE",
	"LOOM_AGENT_TERMINAL_ID",
	"LOOM_SESSION_ID",
	"LOOM_ORCHESTRATOR_SESSION_ID",
}

// ClearLoomEnv removes Loom desktop/runtime environment variables that can
// make local tests resolve the real desktop workspace, frontend bundle, runtime
// directory, or notify token instead of test fixtures.
func ClearLoomEnv(t *testing.T) {
	t.Helper()
	for _, key := range loomDesktopEnvKeys {
		t.Setenv(key, "")
	}
}

// SetupTestEnv sets environment variables and registers cleanup with t.Cleanup().
func SetupTestEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	origVals := make(map[string]string)
	origSet := make(map[string]bool)

	for k, v := range vars {
		origVals[k], origSet[k] = os.LookupEnv(k)
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("failed to set env %s: %v", k, err)
		}
	}

	t.Cleanup(func() {
		for k := range vars {
			if origSet[k] {
				_ = os.Setenv(k, origVals[k])
			} else {
				_ = os.Unsetenv(k)
			}
		}
	})
}

// MockStdin replaces os.Stdin with a pipe containing the given input.
// Restores original stdin via t.Cleanup().
func MockStdin(t *testing.T, input string) {
	t.Helper()
	origStdin := os.Stdin

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer w.Close()

	_, err = io.WriteString(w, input)
	if err != nil {
		t.Fatalf("failed to write to pipe: %v", err)
	}

	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		r.Close()
	})
}

// ContainsSubstring checks if any element in the slice contains the substring.
func ContainsSubstring(slice []string, substr string) bool {
	for _, s := range slice {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// LoadFixture reads a test fixture file from a testdata directory.
// It searches for testdata/<path> relative to the current working directory
// (which go test sets to the package directory), then walks up parent
// directories toward the repository root looking for testdata/<path>.
func LoadFixture(t *testing.T, path string) string {
	t.Helper()

	// Primary: testdata/<path> relative to CWD (go test sets CWD to package dir)
	target := filepath.Join("testdata", path)
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // test fixture loading by design
		return string(data)
	}

	// Walk up directories to find testdata/<path>
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for i := 0; i < 10; i++ {
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			candidate := filepath.Join(dir, "testdata", path)
			if data, err := os.ReadFile(candidate); err == nil { //nolint:gosec // test fixture loading by design
				return string(data)
			}
			// Stop at repo root
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
		}
	}

	t.Fatalf("failed to load fixture %s: not found in any search path", path)
	return ""
}

// sandboxRuntimeDir is a per-process temp dir used as the runtime root for
// subprocesses a test spawns. One dir per test binary keeps state coherent
// across the whole run, the same way bootstrap's test config dir does.
var sandboxRuntimeDir = sync.OnceValue(func() string {
	dir, err := os.MkdirTemp("", "loom-test-runtime-*")
	if err != nil {
		return ""
	}
	return dir
})

// SandboxLoomRuntimeDir returns env with LOOM_WORKSPACE_RUNTIME_DIR replaced by
// a per-process temp dir.
//
// The in-process guard in cli.GetWorkspaceRuntimeDir does not cross an exec
// boundary, so a test that runs the loom binary with a pass-through os.Environ()
// hands the child the fleet workspace it inherited from the launching agent
// shell — and the child then appends to the production session and usage
// ledgers (PUPPET-332). Call this on the env of any command that runs loom,
// unless the test already sets an explicit LOOM_WORKSPACE_RUNTIME_DIR of its
// own; this function would override that.
func SandboxLoomRuntimeDir(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "LOOM_WORKSPACE_RUNTIME_DIR=") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "LOOM_WORKSPACE_RUNTIME_DIR="+sandboxRuntimeDir())
}
