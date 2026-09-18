package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/testutil"
)

func TestGetWorkspaceRuntimeDir_NoWorkspaceConfig(t *testing.T) {
	testutil.ClearLoomEnv(t)
	// No config file → no workspace config → returns "."
	ResetWorkspaceRuntimeDirCache()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	got := GetWorkspaceRuntimeDir()
	if got != "." {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, ".")
	}
}

func TestGetWorkspaceRuntimeDir_HonorsRuntimeDirEnv(t *testing.T) {
	testutil.ClearLoomEnv(t)
	ResetWorkspaceRuntimeDirCache()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "/tmp/loom-runtime")

	got := GetWorkspaceRuntimeDir()
	if got != "/tmp/loom-runtime" {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, "/tmp/loom-runtime")
	}
}

func TestGetWorkspaceRuntimeDir_Workspace(t *testing.T) {
	testutil.ClearLoomEnv(t)
	// Config with workspace and default_workspace set → returns workspace path
	ResetWorkspaceRuntimeDirCache()

	cfg := &LoomConfig{
		DefaultWorkspace: "myproject",
		Workspaces: map[string]WorkspaceConfig{
			"myproject": {
				Path:  "/home/user/workspaces/myproject",
				Repos: []RepoConfig{{Name: "backend", Path: "/home/user/repos/backend"}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	got := GetWorkspaceRuntimeDir()
	if got != "/home/user/workspaces/myproject" {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, "/home/user/workspaces/myproject")
	}
}

func TestGetWorkspaceRuntimeDir_NoExplicitWorkspace(t *testing.T) {
	testutil.ClearLoomEnv(t)
	// Config with workspaces but no explicit runtime workspace → returns "."
	ResetWorkspaceRuntimeDirCache()

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"zebra": {Path: "/tmp/zebra", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
			"alpha": {Path: "/tmp/alpha", Repos: []RepoConfig{{Name: "r2", Path: "/tmp/r2"}}},
		},
	}
	setupWorkspaceConfig(t, cfg)

	got := GetWorkspaceRuntimeDir()
	if got != "." {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, ".")
	}
}

func TestGetWorkspaceRuntimeDir_InfersWorkspaceFromCWD(t *testing.T) {
	testutil.ClearLoomEnv(t)
	ResetWorkspaceRuntimeDirCache()

	alphaDir := t.TempDir()
	betaDir := t.TempDir()
	childDir := filepath.Join(betaDir, "repo")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {Path: alphaDir, Repos: []RepoConfig{{Name: "r", Path: filepath.Join(alphaDir, "r")}}},
			"beta":  {Path: betaDir, Repos: []RepoConfig{{Name: "r2", Path: filepath.Join(betaDir, "r2")}}},
		},
	}
	setupWorkspaceConfig(t, cfg)
	t.Chdir(childDir)

	got := GetWorkspaceRuntimeDir()
	if got != betaDir {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, betaDir)
	}
}

func TestGetWorkspaceRuntimeDir_HonorsEnvWorkspace(t *testing.T) {
	testutil.ClearLoomEnv(t)
	ResetWorkspaceRuntimeDirCache()

	cfg := &LoomConfig{
		DefaultWorkspace: "alpha",
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {Path: "/tmp/alpha", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
			"beta":  {Path: "/tmp/beta", Repos: []RepoConfig{{Name: "r2", Path: "/tmp/r2"}}},
		},
	}
	setupWorkspaceConfig(t, cfg)
	t.Setenv("LOOM_WORKSPACE", "BETA")

	got := GetWorkspaceRuntimeDir()
	if got != "/tmp/beta" {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, "/tmp/beta")
	}
}

func TestGetWorkspaceRuntimeDir_ConfigError(t *testing.T) {
	testutil.ClearLoomEnv(t)
	// Invalid config file → returns "."
	ResetWorkspaceRuntimeDirCache()

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	got := GetWorkspaceRuntimeDir()
	if got != "." {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, ".")
	}
}

func TestGetWorkspaceRuntimeDir_Cache(t *testing.T) {
	testutil.ClearLoomEnv(t)
	// Call twice → same result (sync.Once caches)
	ResetWorkspaceRuntimeDirCache()

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {Path: "/tmp/cached", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg)

	first := GetWorkspaceRuntimeDir()
	if first != "/tmp/cached" {
		t.Fatalf("first call: GetWorkspaceRuntimeDir() = %q, want %q", first, "/tmp/cached")
	}

	// Point config to a different directory; cached value should persist
	cfg2 := &LoomConfig{
		DefaultWorkspace: "ws2",
		Workspaces: map[string]WorkspaceConfig{
			"ws2": {Path: "/tmp/different", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg2)

	second := GetWorkspaceRuntimeDir()
	if second != first {
		t.Errorf("second call: GetWorkspaceRuntimeDir() = %q, want %q (cached value)", second, first)
	}
}

func TestResetWorkspaceRuntimeDirCache(t *testing.T) {
	testutil.ClearLoomEnv(t)
	// First call with workspace config
	ResetWorkspaceRuntimeDirCache()

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {Path: "/tmp/original", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg)

	first := GetWorkspaceRuntimeDir()
	if first != "/tmp/original" {
		t.Fatalf("first call: GetWorkspaceRuntimeDir() = %q, want %q", first, "/tmp/original")
	}

	// Reset, then change config → should pick up new value
	ResetWorkspaceRuntimeDirCache()

	cfg2 := &LoomConfig{
		DefaultWorkspace: "ws2",
		Workspaces: map[string]WorkspaceConfig{
			"ws2": {Path: "/tmp/updated", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg2)

	second := GetWorkspaceRuntimeDir()
	if second != "/tmp/updated" {
		t.Errorf("after reset: GetWorkspaceRuntimeDir() = %q, want %q", second, "/tmp/updated")
	}
}

// TestGetWorkspaceRuntimeDir_IgnoresInheritedRuntimeDir covers the PUPPET-332
// guard: a LOOM_WORKSPACE_RUNTIME_DIR that was inherited from the launching
// agent shell must NOT be used under `go test`, because it points at the live
// fleet workspace whose session and usage ledgers are production data.
func TestGetWorkspaceRuntimeDir_IgnoresInheritedRuntimeDir(t *testing.T) {
	testutil.ClearLoomEnv(t)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	canary := t.TempDir()
	restore := SetInheritedRuntimeDirForTest(canary)
	t.Cleanup(restore)

	// The env holds exactly the inherited value — the agent-shell situation.
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", canary)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	if got := GetWorkspaceRuntimeDir(); got == canary {
		t.Fatalf("GetWorkspaceRuntimeDir() = %q, want anything but the inherited value", got)
	}
}

// TestGetWorkspaceRuntimeDir_DeliberateValueBeatsInherited pins the other half
// of the guard: a value a test sets itself differs from the inherited one and
// is honored, so the guard stays invisible to ordinary tests.
func TestGetWorkspaceRuntimeDir_DeliberateValueBeatsInherited(t *testing.T) {
	testutil.ClearLoomEnv(t)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	restore := SetInheritedRuntimeDirForTest("/tmp/loom-inherited-canary")
	t.Cleanup(restore)

	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "/tmp/loom-deliberate")

	if got := GetWorkspaceRuntimeDir(); got != "/tmp/loom-deliberate" {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want %q", got, "/tmp/loom-deliberate")
	}
}
