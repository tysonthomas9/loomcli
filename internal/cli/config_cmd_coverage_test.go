package cli

import (
	"os"
	"testing"
)

func resetConfigAddRepoFlags(t *testing.T) {
	t.Helper()
	origPath := configAddRepoPath
	origBranch := configAddRepoBranch
	origRemote := configAddRepoRemote
	t.Cleanup(func() {
		configAddRepoPath = origPath
		configAddRepoBranch = origBranch
		configAddRepoRemote = origRemote
	})
}

func resetConfigInitFlags(t *testing.T) {
	t.Helper()
	origForce := configInitForce
	origWorkspace := configInitWorkspace
	t.Cleanup(func() {
		configInitForce = origForce
		configInitWorkspace = origWorkspace
	})
}

func TestRunConfigShow_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "demo",
		Workspaces: map[string]WorkspaceConfig{
			"demo": {
				Path: "/tmp/demo",
				Repos: []RepoConfig{
					{Name: "svc", Path: "/tmp/demo/svc"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Should not panic - exercises the full code path of runConfigShow
	runConfigShow(nil, nil)
}

func TestRunConfigShow_NoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// No config file exists - should print helpful message and return
	runConfigShow(nil, nil)
}

func TestRunConfigAddRepo_WithAllFlags(t *testing.T) {
	resetConfigAddRepoFlags(t)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  "/tmp/myws",
				Repos: []RepoConfig{},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Set all flags
	configAddRepoPath = "/tmp/myws/frontend"
	configAddRepoBranch = "develop"
	configAddRepoRemote = "upstream"

	runConfigAddRepo(nil, []string{"myws", "frontend"})

	// Verify the repo was added with all flags
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["myws"]
	if len(ws.Repos) != 1 {
		t.Fatalf("len(repos) = %d, want 1", len(ws.Repos))
	}
	repo := ws.Repos[0]
	if repo.Name != "frontend" {
		t.Errorf("repo.Name = %q, want %q", repo.Name, "frontend")
	}
	if repo.Path != "/tmp/myws/frontend" {
		t.Errorf("repo.Path = %q, want %q", repo.Path, "/tmp/myws/frontend")
	}
	if repo.DefaultBranch != "develop" {
		t.Errorf("repo.DefaultBranch = %q, want %q", repo.DefaultBranch, "develop")
	}
	if repo.Remote != "upstream" {
		t.Errorf("repo.Remote = %q, want %q", repo.Remote, "upstream")
	}
}

func TestRunConfigAddRepo_MinimalFlags(t *testing.T) {
	resetConfigAddRepoFlags(t)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path:  "/tmp/ws",
				Repos: []RepoConfig{},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Only path is set (branch and remote are empty)
	configAddRepoPath = "/tmp/ws/minimal"
	configAddRepoBranch = ""
	configAddRepoRemote = ""

	runConfigAddRepo(nil, []string{"ws", "minimal"})

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["ws"]
	if len(ws.Repos) != 1 {
		t.Fatalf("len(repos) = %d, want 1", len(ws.Repos))
	}
	if ws.Repos[0].DefaultBranch != "" {
		t.Errorf("DefaultBranch should be empty, got %q", ws.Repos[0].DefaultBranch)
	}
	if ws.Repos[0].Remote != "" {
		t.Errorf("Remote should be empty, got %q", ws.Repos[0].Remote)
	}
}

func TestRunConfigRemoveRepo_HappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "/tmp/ws",
				Repos: []RepoConfig{
					{Name: "api", Path: "/tmp/ws/api"},
					{Name: "web", Path: "/tmp/ws/web"},
					{Name: "svc", Path: "/tmp/ws/svc"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Remove middle repo
	runConfigRemoveRepo(nil, []string{"ws", "web"})

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["ws"]
	if len(ws.Repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2 after removing web", len(ws.Repos))
	}
	for _, r := range ws.Repos {
		if r.Name == "web" {
			t.Error("repo 'web' should have been removed")
		}
	}
}

func TestRunConfigRemoveRepo_LastRepoLeavesEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "/tmp/ws",
				Repos: []RepoConfig{
					{Name: "only", Path: "/tmp/ws/only"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	runConfigRemoveRepo(nil, []string{"ws", "only"})

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["ws"]
	if len(ws.Repos) != 0 {
		t.Fatalf("len(repos) = %d, want 0 after removing last repo", len(ws.Repos))
	}

	// Verify workspace still exists in config even though it has no repos
	if _, ok := loaded.Workspaces["ws"]; !ok {
		t.Error("workspace 'ws' should still exist in config")
	}
}

func TestRunConfigInit_ForceOverwrite(t *testing.T) {
	resetConfigInitFlags(t)

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Create initial config
	configInitForce = false
	configInitWorkspace = "original"
	runConfigInit(nil, nil)

	// Verify original config
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DefaultWorkspace != "original" {
		t.Fatalf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, "original")
	}

	// Force overwrite with new name
	configInitForce = true
	configInitWorkspace = "replaced"
	runConfigInit(nil, nil)

	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DefaultWorkspace != "replaced" {
		t.Errorf("DefaultWorkspace = %q, want %q after force overwrite", cfg.DefaultWorkspace, "replaced")
	}

	// Verify old workspace is gone
	if _, ok := cfg.Workspaces["original"]; ok {
		t.Error("original workspace should have been replaced")
	}
}

func TestRunConfigInit_CreatesDirIfNeeded(t *testing.T) {
	resetConfigInitFlags(t)

	dir := t.TempDir()
	nested := dir + "/deep/nested/dir"
	t.Setenv("LOOM_CONFIG_DIR", nested)

	configInitForce = false
	configInitWorkspace = "ws"

	runConfigInit(nil, nil)

	// Verify the config was created in the nested directory
	if _, err := os.Stat(nested + "/config.yaml"); err != nil {
		t.Errorf("config file should exist in nested dir: %v", err)
	}
}
