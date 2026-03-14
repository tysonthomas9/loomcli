package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSaveConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: "/tmp/myws",
				Repos: []RepoConfig{
					{Name: "api", Path: "/tmp/myws/api", DefaultBranch: "main", Remote: "origin"},
					{Name: "web", Path: "/tmp/myws/web"},
				},
			},
		},
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Verify file was written
	configPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Roundtrip: load it back and verify
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadConfig() returned nil after SaveConfig")
	}
	if loaded.DefaultWorkspace != "myws" {
		t.Errorf("DefaultWorkspace = %q, want %q", loaded.DefaultWorkspace, "myws")
	}
	ws, ok := loaded.Workspaces["myws"]
	if !ok {
		t.Fatal("workspace 'myws' not found after roundtrip")
	}
	if ws.Path != "/tmp/myws" {
		t.Errorf("workspace path = %q, want %q", ws.Path, "/tmp/myws")
	}
	if len(ws.Repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(ws.Repos))
	}
	if ws.Repos[0].Name != "api" {
		t.Errorf("repos[0].Name = %q, want %q", ws.Repos[0].Name, "api")
	}
	if ws.Repos[0].DefaultBranch != "main" {
		t.Errorf("repos[0].DefaultBranch = %q, want %q", ws.Repos[0].DefaultBranch, "main")
	}
	if ws.Repos[1].Name != "web" {
		t.Errorf("repos[1].Name = %q, want %q", ws.Repos[1].Name, "web")
	}
}

func TestSaveConfigCreatesDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "deep")
	t.Setenv("LOOM_CONFIG_DIR", nested)

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws": {Path: "/tmp", Repos: []RepoConfig{}},
		},
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(nested, "config.yaml")); err != nil {
		t.Fatalf("config file not created in nested dir: %v", err)
	}
}

func TestConfigInit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// runConfigInit uses os.Getwd() for workspace name when --workspace is empty.
	// We set the flag directly to control the name.
	configInitForce = false
	configInitWorkspace = "testws"

	// Call runConfigInit - it will use os.Getwd() for the workspace path
	runConfigInit(nil, nil)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil after config init")
	}
	if cfg.DefaultWorkspace != "testws" {
		t.Errorf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, "testws")
	}
	ws, ok := cfg.Workspaces["testws"]
	if !ok {
		t.Fatal("workspace 'testws' not found")
	}
	// The path should be cwd
	cwd, _ := os.Getwd()
	if ws.Path != cwd {
		t.Errorf("workspace path = %q, want %q", ws.Path, cwd)
	}
	if len(ws.Repos) != 0 {
		t.Errorf("len(repos) = %d, want 0", len(ws.Repos))
	}
	if cfg.Version != CurrentConfigVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, CurrentConfigVersion)
	}
}

func TestConfigInitDefaultWorkspaceName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	configInitForce = false
	configInitWorkspace = "" // Should use basename of cwd

	runConfigInit(nil, nil)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil")
	}

	cwd, _ := os.Getwd()
	expectedName := filepath.Base(cwd)
	if cfg.DefaultWorkspace != expectedName {
		t.Errorf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, expectedName)
	}
	if _, ok := cfg.Workspaces[expectedName]; !ok {
		t.Errorf("workspace %q not found in config", expectedName)
	}
}

func TestConfigInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Create initial config
	configInitForce = false
	configInitWorkspace = "first"
	runConfigInit(nil, nil)

	// Verify it was created
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil || cfg.DefaultWorkspace != "first" {
		t.Fatal("initial config not created properly")
	}

	// Now try with --force, which should overwrite
	configInitForce = true
	configInitWorkspace = "second"
	runConfigInit(nil, nil)

	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil after force init")
	}
	if cfg.DefaultWorkspace != "second" {
		t.Errorf("DefaultWorkspace = %q, want %q after force overwrite", cfg.DefaultWorkspace, "second")
	}
}

func TestConfigShow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Create a config to show
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

	// Verify the config can be loaded and marshaled (what runConfigShow does)
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadConfig() returned nil")
	}

	data, err := yaml.Marshal(loaded)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	output := string(data)

	if !strings.Contains(output, "default_workspace: demo") {
		t.Errorf("show output missing default_workspace, got:\n%s", output)
	}
	if !strings.Contains(output, "name: svc") {
		t.Errorf("show output missing repo name, got:\n%s", output)
	}
}

func TestConfigShowNoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// No config file exists. runConfigShow would print a message.
	// We test the logic path: LoadConfig returns nil, nil.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config when no file exists, got %+v", cfg)
	}
	// The command would print: "No config file found at ..."
}

func TestConfigAddRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Create initial config with a workspace
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

	// Set the flags that runConfigAddRepo reads from package vars
	configAddRepoPath = "/tmp/myws/backend"
	configAddRepoBranch = "main"
	configAddRepoRemote = "origin"

	runConfigAddRepo(nil, []string{"myws", "backend"})

	// Verify the repo was added
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["myws"]
	if len(ws.Repos) != 1 {
		t.Fatalf("len(repos) = %d, want 1", len(ws.Repos))
	}
	repo := ws.Repos[0]
	if repo.Name != "backend" {
		t.Errorf("repo.Name = %q, want %q", repo.Name, "backend")
	}
	if repo.Path != "/tmp/myws/backend" {
		t.Errorf("repo.Path = %q, want %q", repo.Path, "/tmp/myws/backend")
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("repo.DefaultBranch = %q, want %q", repo.DefaultBranch, "main")
	}
	if repo.Remote != "origin" {
		t.Errorf("repo.Remote = %q, want %q", repo.Remote, "origin")
	}
}

func TestConfigAddRepoMultiple(t *testing.T) {
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

	// Add first repo
	configAddRepoPath = "/tmp/ws/api"
	configAddRepoBranch = ""
	configAddRepoRemote = ""
	runConfigAddRepo(nil, []string{"ws", "api"})

	// Add second repo
	configAddRepoPath = "/tmp/ws/web"
	configAddRepoBranch = "develop"
	configAddRepoRemote = "upstream"
	runConfigAddRepo(nil, []string{"ws", "web"})

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["ws"]
	if len(ws.Repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(ws.Repos))
	}
	if ws.Repos[0].Name != "api" {
		t.Errorf("repos[0].Name = %q, want %q", ws.Repos[0].Name, "api")
	}
	if ws.Repos[1].Name != "web" {
		t.Errorf("repos[1].Name = %q, want %q", ws.Repos[1].Name, "web")
	}
	if ws.Repos[1].DefaultBranch != "develop" {
		t.Errorf("repos[1].DefaultBranch = %q, want %q", ws.Repos[1].DefaultBranch, "develop")
	}
}

func TestConfigAddRepoDuplicate(t *testing.T) {
	// Since runConfigAddRepo calls os.Exit(1) on duplicate, we test the logic directly.
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "/tmp/ws",
				Repos: []RepoConfig{
					{Name: "api", Path: "/tmp/ws/api"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Simulate the duplicate check that runConfigAddRepo performs
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["ws"]
	found := false
	for _, r := range ws.Repos {
		if r.Name == "api" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find duplicate repo 'api' but didn't")
	}
}

func TestConfigAddRepoNoConfig(t *testing.T) {
	// When no config exists, LoadConfig returns nil, nil
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg != nil {
		t.Error("expected nil config when no file exists")
	}
	// runConfigAddRepo would print "No config found. Run 'loom config init' first." and exit
}

func TestConfigAddRepoNoWorkspace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "existing",
		Workspaces: map[string]WorkspaceConfig{
			"existing": {
				Path:  "/tmp/existing",
				Repos: []RepoConfig{},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Check that workspace "nonexistent" is not found
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, ok := loaded.Workspaces["nonexistent"]; ok {
		t.Error("expected workspace 'nonexistent' to not exist")
	}
	// runConfigAddRepo would print workspace not found message and exit
}

func TestConfigRemoveRepo(t *testing.T) {
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
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	runConfigRemoveRepo(nil, []string{"ws", "api"})

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["ws"]
	if len(ws.Repos) != 1 {
		t.Fatalf("len(repos) = %d, want 1 after removal", len(ws.Repos))
	}
	if ws.Repos[0].Name != "web" {
		t.Errorf("remaining repo = %q, want %q", ws.Repos[0].Name, "web")
	}
}

func TestConfigRemoveRepoLast(t *testing.T) {
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
}

func TestConfigRemoveRepoNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: "/tmp/ws",
				Repos: []RepoConfig{
					{Name: "api", Path: "/tmp/ws/api"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Check that repo "nonexistent" is not in the workspace
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := loaded.Workspaces["ws"]
	found := false
	for _, r := range ws.Repos {
		if r.Name == "nonexistent" {
			found = true
			break
		}
	}
	if found {
		t.Error("expected repo 'nonexistent' to not exist")
	}
	// runConfigRemoveRepo would print "Repo not found" and exit
}

func TestConfigAddRepoInvalidRemote(t *testing.T) {
	// Since runConfigAddRepo calls os.Exit on validation failure, test the
	// validation logic directly rather than calling the command.
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

	// Simulate what runConfigAddRepo would do: load config, check workspace exists
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, ok := loaded.Workspaces["ws"]; !ok {
		t.Fatal("workspace 'ws' not found")
	}

	// Set the flag value that runConfigAddRepo reads
	configAddRepoRemote = "--evil"

	// Validate the remote name — this is the check runConfigAddRepo performs
	if err := ValidateRemoteName(configAddRepoRemote); err == nil {
		t.Error("ValidateRemoteName(\"--evil\") = nil, want error")
	}
}
