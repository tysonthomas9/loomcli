package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// resetAddRepoFlags zeros the workspace add-repo flag variables.
func resetAddRepoFlags(t *testing.T) {
	t.Helper()
	origName := wsAddRepoName
	origBranch := wsAddRepoBranch
	origRemote := wsAddRepoRemote
	origGroups := wsAddRepoGroups
	t.Cleanup(func() {
		wsAddRepoName = origName
		wsAddRepoBranch = origBranch
		wsAddRepoRemote = origRemote
		wsAddRepoGroups = origGroups
	})
	wsAddRepoName = ""
	wsAddRepoBranch = ""
	wsAddRepoRemote = ""
	wsAddRepoGroups = ""
}

func TestWorkspaceAddRepo_HappyPath(t *testing.T) {
	resetAddRepoFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	// Pre-create workspace
	wsDir := filepath.Join(tmpDir, "workspaces", "myws")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {ID: "ws-1", Path: wsDir, Repos: []RepoConfig{}},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Real git repo to add
	repoPath := filepath.Join(tmpDir, "repos", "newrepo")
	createGitRepo(t, repoPath)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	runWorkspaceAddRepo(cmd, []string{"myws", repoPath})

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	ws := got.Workspaces["myws"]
	if len(ws.Repos) != 1 {
		t.Fatalf("repos count = %d, want 1", len(ws.Repos))
	}
	added := ws.Repos[0]
	if added.Name != "newrepo" {
		t.Errorf("name = %q, want newrepo", added.Name)
	}
	abs, _ := filepath.Abs(repoPath)
	if added.Path != abs {
		t.Errorf("path = %q, want %q", added.Path, abs)
	}
	if added.SourceRepoID != "newrepo" {
		t.Errorf("source_repo_id = %q, want newrepo", added.SourceRepoID)
	}
}

func TestWorkspaceAddRepo_WithFlags(t *testing.T) {
	resetAddRepoFlags(t)

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {ID: "ws-1", Path: tmpDir, Repos: []RepoConfig{}},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	repoPath := filepath.Join(tmpDir, "raw")
	createGitRepo(t, repoPath)

	wsAddRepoName = "custom"
	wsAddRepoBranch = "develop"
	wsAddRepoRemote = "upstream"
	wsAddRepoGroups = "backend, api"

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	runWorkspaceAddRepo(cmd, []string{"myws", repoPath})

	got, _ := LoadConfig()
	added := got.Workspaces["myws"].Repos[0]
	if added.Name != "custom" {
		t.Errorf("name = %q, want custom", added.Name)
	}
	if added.DefaultBranch != "develop" {
		t.Errorf("default_branch = %q", added.DefaultBranch)
	}
	if added.Remote != "upstream" {
		t.Errorf("remote = %q", added.Remote)
	}
	if len(added.Groups) != 2 || added.Groups[0] != "backend" || added.Groups[1] != "api" {
		t.Errorf("groups = %v", added.Groups)
	}
}

func TestWorkspaceRemoveRepo_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	repoA := filepath.Join(tmpDir, "a")
	repoB := filepath.Join(tmpDir, "b")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				ID:   "ws-1",
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "a", Path: repoA},
					{Name: "b", Path: repoB},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	runWorkspaceRemoveRepo(cmd, []string{"myws", "a"})

	got, _ := LoadConfig()
	repos := got.Workspaces["myws"].Repos
	if len(repos) != 1 || repos[0].Name != "b" {
		t.Errorf("after remove: %+v", repos)
	}

	// Verify directory not deleted
	if _, err := os.Stat(repoA); err != nil {
		t.Errorf("repo dir was deleted (must not happen): %v", err)
	}
}

func TestIsValidRepoNameCLI(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"valid", true},
		{"with-hyphen", true},
		{"with_underscore", true},
		{"with.dots", true},
		{"123abc", true},
		{"", false},
		{"has space", false},
		{"has/slash", false},
		{"has!bang", false},
	}
	for _, c := range cases {
		if got := isValidRepoNameCLI(c.name); got != c.ok {
			t.Errorf("isValidRepoNameCLI(%q) = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestValidateRepoPathErr(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing path.
	if err := validateRepoPathErr(filepath.Join(tmpDir, "nonexistent")); err == nil {
		t.Error("want error for missing path, got nil")
	}

	// Not a directory.
	file := filepath.Join(tmpDir, "f")
	if err := os.WriteFile(file, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRepoPathErr(file); err == nil {
		t.Error("want error for non-directory, got nil")
	}

	// Directory without .git.
	plainDir := filepath.Join(tmpDir, "plain")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateRepoPathErr(plainDir); err == nil {
		t.Error("want error for dir without .git, got nil")
	}

	// Real git repo.
	repo := filepath.Join(tmpDir, "good")
	createGitRepo(t, repo)
	if err := validateRepoPathErr(repo); err != nil {
		t.Errorf("want nil for valid git repo, got %v", err)
	}
}
