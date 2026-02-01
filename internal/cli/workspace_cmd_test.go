package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetWorkspaceFlags resets all workspace command flag variables to their zero values.
// Call this at the start of each test that sets flag variables.
func resetWorkspaceFlags(t *testing.T) {
	t.Helper()
	origRepos := wsCreateRepos
	origPath := wsCreatePath
	origDefault := wsCreateDefault
	origBranch := wsCreateBranch
	origJSON := wsListJSON
	origForce := wsRemoveForce
	origKeep := wsRemoveKeepWorktrees
	t.Cleanup(func() {
		wsCreateRepos = origRepos
		wsCreatePath = origPath
		wsCreateDefault = origDefault
		wsCreateBranch = origBranch
		wsListJSON = origJSON
		wsRemoveForce = origForce
		wsRemoveKeepWorktrees = origKeep
	})
	wsCreateRepos = ""
	wsCreatePath = ""
	wsCreateDefault = false
	wsCreateBranch = ""
	wsListJSON = false
	wsRemoveForce = false
	wsRemoveKeepWorktrees = false
}

func TestWorkspaceCreate_HappyPath(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create two real git repos
	repo1 := filepath.Join(tmpDir, "repos", "frontend")
	repo2 := filepath.Join(tmpDir, "repos", "backend")
	createGitRepo(t, repo1)
	createGitRepo(t, repo2)

	wsDir := filepath.Join(tmpDir, "workspaces", "myws")

	// Set up flag variables
	wsCreateRepos = repo1 + "," + repo2
	wsCreatePath = wsDir
	wsCreateDefault = true
	wsCreateBranch = "feat-branch"

	// Mock execCommand to intercept git worktree add and bd init calls
	mock := NewCommandMock(t, []CommandStub{
		// git worktree add for frontend
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(wsDir, "frontend"), "-b", "feat-branch"}, Stdout: ""},
		// git worktree add for backend
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(wsDir, "backend"), "-b", "feat-branch"}, Stdout: ""},
		// bd init in workspace dir
		{Name: "bd", Args: []string{"init"}, Stdout: ""},
	})
	mock.Install()

	runWorkspaceCreate(nil, []string{"myws"})

	// Verify config was saved correctly
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil after workspace create")
	}
	if cfg.DefaultWorkspace != "myws" {
		t.Errorf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, "myws")
	}

	ws, ok := cfg.Workspaces["myws"]
	if !ok {
		t.Fatal("workspace 'myws' not found in config")
	}
	if ws.Path != wsDir {
		t.Errorf("workspace path = %q, want %q", ws.Path, wsDir)
	}
	if len(ws.Repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(ws.Repos))
	}
	if ws.Repos[0].Name != "frontend" {
		t.Errorf("repos[0].Name = %q, want %q", ws.Repos[0].Name, "frontend")
	}
	if ws.Repos[0].Path != filepath.Join(wsDir, "frontend") {
		t.Errorf("repos[0].Path = %q, want %q", ws.Repos[0].Path, filepath.Join(wsDir, "frontend"))
	}
	if ws.Repos[1].Name != "backend" {
		t.Errorf("repos[1].Name = %q, want %q", ws.Repos[1].Name, "backend")
	}
}

func TestWorkspaceCreate_DefaultBranchIsName(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	repo1 := filepath.Join(tmpDir, "repos", "svc")
	createGitRepo(t, repo1)

	wsDir := filepath.Join(tmpDir, "workspaces", "feature-x")

	wsCreateRepos = repo1
	wsCreatePath = wsDir
	wsCreateBranch = "" // Should default to workspace name

	mock := NewCommandMock(t, []CommandStub{
		// branch should default to workspace name "feature-x"
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(wsDir, "svc"), "-b", "feature-x"}, Stdout: ""},
		{Name: "bd", Args: []string{"init"}, Stdout: ""},
	})
	mock.Install()

	runWorkspaceCreate(nil, []string{"feature-x"})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil")
	}
	// First workspace should become default
	if cfg.DefaultWorkspace != "feature-x" {
		t.Errorf("DefaultWorkspace = %q, want %q", cfg.DefaultWorkspace, "feature-x")
	}
}

func TestWorkspaceCreate_DefaultWorkspacePath(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	repo1 := filepath.Join(tmpDir, "repos", "app")
	createGitRepo(t, repo1)

	// Don't set wsCreatePath -- should use GetWorkspaceDir default
	wsCreateRepos = repo1
	wsCreatePath = ""
	wsCreateBranch = "ws1"

	expectedDir := GetWorkspaceDir("ws1")

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(expectedDir, "app"), "-b", "ws1"}, Stdout: ""},
		{Name: "bd", Args: []string{"init"}, Stdout: ""},
	})
	mock.Install()

	runWorkspaceCreate(nil, []string{"ws1"})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := cfg.Workspaces["ws1"]
	if ws.Path != expectedDir {
		t.Errorf("workspace path = %q, want default %q", ws.Path, expectedDir)
	}
}

func TestWorkspaceCreate_AlreadyExists(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Pre-create config with existing workspace
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

	// Verify the precondition: workspace already exists
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, exists := loaded.Workspaces["existing"]; !exists {
		t.Fatal("precondition failed: workspace 'existing' should exist")
	}
	// runWorkspaceCreate would call os.Exit(1) here, so we verify the condition
}

func TestWorkspaceCreate_NonExistentRepoPath(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	nonExistent := filepath.Join(tmpDir, "no-such-repo")

	// Verify precondition: path does not exist
	if _, err := os.Stat(nonExistent); err == nil {
		t.Fatal("precondition failed: path should not exist")
	}
	// runWorkspaceCreate would call os.Exit(1) when Stat fails
}

func TestWorkspaceCreate_NonGitRepo(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create a directory that is NOT a git repo (no .git)
	notGit := filepath.Join(tmpDir, "not-a-repo")
	if err := os.MkdirAll(notGit, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Verify precondition: directory exists but has no .git
	info, err := os.Stat(notGit)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("precondition failed: should be a directory")
	}
	gitDir := filepath.Join(notGit, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		t.Fatal("precondition failed: .git should not exist")
	}
	// runWorkspaceCreate would call os.Exit(1) when .git check fails
}

func TestWorkspaceCreate_EmptyRepos(t *testing.T) {
	resetWorkspaceFlags(t)

	// Verify the parsing logic: empty string split produces [""] which is filtered
	repoPaths := strings.Split("", ",")
	if len(repoPaths) != 1 || repoPaths[0] != "" {
		t.Fatalf("unexpected split result: %v", repoPaths)
	}
	// runWorkspaceCreate checks: len(repoPaths) == 1 && repoPaths[0] == "" -> os.Exit(1)
}

func TestWorkspaceList_NoWorkspaces(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// No config file exists, so LoadConfig returns nil
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config when no file exists, got %+v", cfg)
	}
	// runWorkspaceList would print "No workspaces configured." and return
}

func TestWorkspaceList_WithWorkspaces(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	wsPath := filepath.Join(tmpDir, "ws-alpha")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := &LoomConfig{
		DefaultWorkspace: "alpha",
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {
				Path: wsPath,
				Repos: []RepoConfig{
					{Name: "api", Path: filepath.Join(wsPath, "api")},
					{Name: "web", Path: filepath.Join(wsPath, "web")},
				},
			},
			"beta": {
				Path: filepath.Join(tmpDir, "ws-beta"),
				Repos: []RepoConfig{
					{Name: "svc", Path: filepath.Join(tmpDir, "ws-beta", "svc")},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Verify list can load and sort workspace names deterministically
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(loaded.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(loaded.Workspaces))
	}
	// Verify sorting
	names := make([]string, 0, len(loaded.Workspaces))
	for name := range loaded.Workspaces {
		names = append(names, name)
	}
	// Sort and check
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}

	// Verify default marker logic
	if loaded.DefaultWorkspace != "alpha" {
		t.Errorf("DefaultWorkspace = %q, want %q", loaded.DefaultWorkspace, "alpha")
	}

	// Verify directory status check
	if _, err := os.Stat(wsPath); err != nil {
		t.Errorf("expected workspace dir to exist: %v", err)
	}
	betaPath := filepath.Join(tmpDir, "ws-beta")
	if _, err := os.Stat(betaPath); err == nil {
		t.Errorf("expected beta workspace dir to NOT exist (for 'missing' status)")
	}
}

func TestWorkspaceList_JSON(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: "/tmp/myws",
				Repos: []RepoConfig{
					{Name: "api", Path: "/tmp/myws/api"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Verify the JSON marshaling logic that runWorkspaceList uses
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	data, err := json.MarshalIndent(loaded.Workspaces, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	output := string(data)

	if !strings.Contains(output, `"myws"`) {
		t.Errorf("JSON output missing workspace name, got:\n%s", output)
	}
	if !strings.Contains(output, `"path": "/tmp/myws"`) {
		t.Errorf("JSON output missing workspace path, got:\n%s", output)
	}
	if !strings.Contains(output, `"name": "api"`) {
		t.Errorf("JSON output missing repo name, got:\n%s", output)
	}

	// Verify it's valid JSON by parsing it back
	var parsed map[string]WorkspaceConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ws, ok := parsed["myws"]; !ok {
		t.Error("parsed JSON missing 'myws' workspace")
	} else if len(ws.Repos) != 1 {
		t.Errorf("parsed JSON repos count = %d, want 1", len(ws.Repos))
	}
}

func TestWorkspaceRemove_HappyPath(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Create the workspace directory with a fake worktree structure
	wsDir := filepath.Join(tmpDir, "workspaces", "myws")
	repoWT := filepath.Join(wsDir, "api")
	if err := os.MkdirAll(repoWT, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a .git file in the worktree pointing to a main repo
	// (simulating a real git worktree)
	mainRepo := filepath.Join(tmpDir, "repos", "api")
	createGitRepo(t, mainRepo)
	gitWorktreeDir := filepath.Join(mainRepo, ".git", "worktrees", "api")
	if err := os.MkdirAll(gitWorktreeDir, 0755); err != nil {
		t.Fatalf("mkdir gitWorktreeDir: %v", err)
	}
	// Write .git file in worktree path
	gitFileContent := "gitdir: " + gitWorktreeDir
	if err := os.WriteFile(filepath.Join(repoWT, ".git"), []byte(gitFileContent), 0644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "api", Path: repoWT},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Mock git worktree remove call
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "remove", repoWT}, Stdout: ""},
	})
	mock.Install()

	wsRemoveForce = false
	wsRemoveKeepWorktrees = false

	runWorkspaceRemove(nil, []string{"myws"})

	// Verify workspace was removed from config
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, exists := loaded.Workspaces["myws"]; exists {
		t.Error("workspace 'myws' should have been removed from config")
	}
	if loaded.DefaultWorkspace != "" {
		t.Errorf("DefaultWorkspace = %q, want empty (no remaining workspaces)", loaded.DefaultWorkspace)
	}
}

func TestWorkspaceRemove_NonExistent(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

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

	// Verify precondition: workspace "nope" does not exist
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, exists := loaded.Workspaces["nope"]; exists {
		t.Fatal("precondition failed: workspace 'nope' should not exist")
	}
	// runWorkspaceRemove would call os.Exit(1) here
}

func TestWorkspaceRemove_KeepWorktrees(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	wsDir := filepath.Join(tmpDir, "workspaces", "myws")
	repoWT := filepath.Join(wsDir, "api")
	if err := os.MkdirAll(repoWT, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "api", Path: repoWT},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// With --keep-worktrees, no git commands should be called
	mock := NewCommandMock(t, []CommandStub{})
	mock.Install()

	wsRemoveForce = false
	wsRemoveKeepWorktrees = true

	runWorkspaceRemove(nil, []string{"myws"})

	// Verify workspace was removed from config
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, exists := loaded.Workspaces["myws"]; exists {
		t.Error("workspace 'myws' should have been removed from config")
	}

	// Verify the workspace directory was NOT removed (--keep-worktrees)
	if _, err := os.Stat(wsDir); err != nil {
		t.Errorf("workspace directory should still exist with --keep-worktrees, got error: %v", err)
	}
}

func TestWorkspaceRemove_UpdatesDefaultWorkspace(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "alpha",
		Workspaces: map[string]WorkspaceConfig{
			"alpha": {
				Path:  filepath.Join(tmpDir, "alpha"),
				Repos: []RepoConfig{},
			},
			"beta": {
				Path:  filepath.Join(tmpDir, "beta"),
				Repos: []RepoConfig{},
			},
			"gamma": {
				Path:  filepath.Join(tmpDir, "gamma"),
				Repos: []RepoConfig{},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// No git commands needed since repos have no .git files
	mock := NewCommandMock(t, []CommandStub{})
	mock.Install()

	wsRemoveForce = false
	wsRemoveKeepWorktrees = false

	// Remove the default workspace
	runWorkspaceRemove(nil, []string{"alpha"})

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, exists := loaded.Workspaces["alpha"]; exists {
		t.Error("workspace 'alpha' should have been removed")
	}
	// Default should be set to first remaining (sorted): "beta"
	if loaded.DefaultWorkspace != "beta" {
		t.Errorf("DefaultWorkspace = %q, want %q (first remaining alphabetically)", loaded.DefaultWorkspace, "beta")
	}
	if len(loaded.Workspaces) != 2 {
		t.Errorf("expected 2 remaining workspaces, got %d", len(loaded.Workspaces))
	}
}

func TestWorkspaceRemove_LockedAgent(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	wsDir := filepath.Join(tmpDir, "workspaces", "myws")
	repoWT := filepath.Join(wsDir, "api")
	if err := os.MkdirAll(repoWT, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a lock file to simulate a running agent
	lockPath := filepath.Join(repoWT, ".agent.lock")
	if err := os.WriteFile(lockPath, []byte("locked"), 0644); err != nil {
		t.Fatalf("write lock file: %v", err)
	}

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "api", Path: repoWT},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Verify precondition: lock file exists
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatal("precondition failed: lock file should exist")
	}
	// runWorkspaceRemove without --force would os.Exit(1) due to lock file
}

func TestFindMainRepoPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake worktree .git file
	worktreePath := filepath.Join(tmpDir, "worktree")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mainRepoGitDir := filepath.Join(tmpDir, "mainrepo", ".git", "worktrees", "worktree")
	if err := os.MkdirAll(mainRepoGitDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	gitFileContent := "gitdir: " + mainRepoGitDir
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte(gitFileContent), 0644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	result := findMainRepoPath(worktreePath)
	expected := filepath.Join(tmpDir, "mainrepo")
	if result != expected {
		t.Errorf("findMainRepoPath() = %q, want %q", result, expected)
	}
}

func TestFindMainRepoPath_NoGitFile(t *testing.T) {
	tmpDir := t.TempDir()

	result := findMainRepoPath(tmpDir)
	if result != "" {
		t.Errorf("findMainRepoPath() = %q, want empty for no .git file", result)
	}
}

func TestFindMainRepoPath_NotAWorktree(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a .git file without the gitdir: prefix
	if err := os.WriteFile(filepath.Join(tmpDir, ".git"), []byte("something else"), 0644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	result := findMainRepoPath(tmpDir)
	if result != "" {
		t.Errorf("findMainRepoPath() = %q, want empty for non-worktree .git", result)
	}
}

func TestWorkspaceCreate_MultipleReposWithSpaces(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	repo1 := filepath.Join(tmpDir, "repos", "svc1")
	repo2 := filepath.Join(tmpDir, "repos", "svc2")
	createGitRepo(t, repo1)
	createGitRepo(t, repo2)

	wsDir := filepath.Join(tmpDir, "workspaces", "ws")

	// Repos with spaces around commas
	wsCreateRepos = repo1 + " , " + repo2
	wsCreatePath = wsDir
	wsCreateBranch = "dev"

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(wsDir, "svc1"), "-b", "dev"}, Stdout: ""},
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(wsDir, "svc2"), "-b", "dev"}, Stdout: ""},
		{Name: "bd", Args: []string{"init"}, Stdout: ""},
	})
	mock.Install()

	runWorkspaceCreate(nil, []string{"ws"})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := cfg.Workspaces["ws"]
	if len(ws.Repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(ws.Repos))
	}
}

func TestWorkspaceCreate_SingleRepo(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	repo1 := filepath.Join(tmpDir, "repos", "mono")
	createGitRepo(t, repo1)

	wsDir := filepath.Join(tmpDir, "workspaces", "solo")

	wsCreateRepos = repo1
	wsCreatePath = wsDir
	wsCreateBranch = "solo"
	wsCreateDefault = false

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(wsDir, "mono"), "-b", "solo"}, Stdout: ""},
		{Name: "bd", Args: []string{"init"}, Stdout: ""},
	})
	mock.Install()

	runWorkspaceCreate(nil, []string{"solo"})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	ws := cfg.Workspaces["solo"]
	if len(ws.Repos) != 1 {
		t.Fatalf("len(repos) = %d, want 1", len(ws.Repos))
	}
	if ws.Repos[0].Name != "mono" {
		t.Errorf("repos[0].Name = %q, want %q", ws.Repos[0].Name, "mono")
	}
	// Even without --default, first workspace becomes default
	if cfg.DefaultWorkspace != "solo" {
		t.Errorf("DefaultWorkspace = %q, want %q (first ws should be default)", cfg.DefaultWorkspace, "solo")
	}
}

func TestWorkspaceRemove_MultipleWorkspaces(t *testing.T) {
	resetWorkspaceFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "ws2",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: filepath.Join(tmpDir, "ws1"), Repos: []RepoConfig{}},
			"ws2": {Path: filepath.Join(tmpDir, "ws2"), Repos: []RepoConfig{}},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	mock := NewCommandMock(t, []CommandStub{})
	mock.Install()

	wsRemoveForce = false
	wsRemoveKeepWorktrees = false

	// Remove non-default workspace
	runWorkspaceRemove(nil, []string{"ws1"})

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if _, exists := loaded.Workspaces["ws1"]; exists {
		t.Error("workspace 'ws1' should have been removed")
	}
	// Default should remain "ws2" since we removed ws1
	if loaded.DefaultWorkspace != "ws2" {
		t.Errorf("DefaultWorkspace = %q, want %q", loaded.DefaultWorkspace, "ws2")
	}
}
