package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetBeadsDir_Legacy(t *testing.T) {
	// No config file → legacy mode → returns "."
	ResetBeadsDirCache()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	got := GetBeadsDir()
	if got != "." {
		t.Errorf("GetBeadsDir() = %q, want %q", got, ".")
	}
}

func TestGetBeadsDir_Workspace(t *testing.T) {
	// Config with workspace and default_workspace set → returns workspace path
	ResetBeadsDirCache()

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

	got := GetBeadsDir()
	if got != "/home/user/workspaces/myproject" {
		t.Errorf("GetBeadsDir() = %q, want %q", got, "/home/user/workspaces/myproject")
	}
}

func TestGetBeadsDir_NoDefaultWorkspace(t *testing.T) {
	// Config with workspaces but no default → uses first sorted key
	ResetBeadsDirCache()

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"zebra": {Path: "/tmp/zebra", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
			"alpha": {Path: "/tmp/alpha", Repos: []RepoConfig{{Name: "r2", Path: "/tmp/r2"}}},
		},
	}
	setupWorkspaceConfig(t, cfg)

	got := GetBeadsDir()
	if got != "/tmp/alpha" {
		t.Errorf("GetBeadsDir() = %q, want %q (first sorted workspace key)", got, "/tmp/alpha")
	}
}

func TestGetBeadsDir_ConfigError(t *testing.T) {
	// Invalid config file → returns "."
	ResetBeadsDirCache()

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	got := GetBeadsDir()
	if got != "." {
		t.Errorf("GetBeadsDir() = %q, want %q", got, ".")
	}
}

func TestGetBeadsDir_Cache(t *testing.T) {
	// Call twice → same result (sync.Once caches)
	ResetBeadsDirCache()

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {Path: "/tmp/cached", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg)

	first := GetBeadsDir()
	if first != "/tmp/cached" {
		t.Fatalf("first call: GetBeadsDir() = %q, want %q", first, "/tmp/cached")
	}

	// Point config to a different directory; cached value should persist
	cfg2 := &LoomConfig{
		DefaultWorkspace: "ws2",
		Workspaces: map[string]WorkspaceConfig{
			"ws2": {Path: "/tmp/different", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg2)

	second := GetBeadsDir()
	if second != first {
		t.Errorf("second call: GetBeadsDir() = %q, want %q (cached value)", second, first)
	}
}

func TestResetBeadsDirCache(t *testing.T) {
	// First call with workspace config
	ResetBeadsDirCache()

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {Path: "/tmp/original", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg)

	first := GetBeadsDir()
	if first != "/tmp/original" {
		t.Fatalf("first call: GetBeadsDir() = %q, want %q", first, "/tmp/original")
	}

	// Reset, then change config → should pick up new value
	ResetBeadsDirCache()

	cfg2 := &LoomConfig{
		DefaultWorkspace: "ws2",
		Workspaces: map[string]WorkspaceConfig{
			"ws2": {Path: "/tmp/updated", Repos: []RepoConfig{{Name: "r", Path: "/tmp/r"}}},
		},
	}
	setupWorkspaceConfig(t, cfg2)

	second := GetBeadsDir()
	if second != "/tmp/updated" {
		t.Errorf("after reset: GetBeadsDir() = %q, want %q", second, "/tmp/updated")
	}
}
