package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ---------- ResolveWorkspaceByName ----------

func TestResolveWorkspaceByName_Match(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: filepath.Join(tmpDir, "repo1")}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	path, ok := r.ResolveWorkspaceByName("myws")
	if !ok {
		t.Fatal("expected ok=true for matching workspace name")
	}
	if path != tmpDir {
		t.Errorf("expected path %s, got %s", tmpDir, path)
	}
}

func TestResolveWorkspaceByName_LegacyMode(t *testing.T) {
	// No config -> legacy mode
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Mode() != ModeLegacy {
		t.Fatalf("expected ModeLegacy, got %d", r.Mode())
	}

	path, ok := r.ResolveWorkspaceByName("anything")
	if ok {
		t.Error("expected ok=false in legacy mode")
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

func TestResolveWorkspaceByName_EmptyName(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {Path: "/tmp/ws"},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	path, ok := r.ResolveWorkspaceByName("")
	if ok {
		t.Error("expected ok=false for empty name")
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

func TestResolveWorkspaceByName_NoMatch(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "ws",
		Workspaces: map[string]WorkspaceConfig{
			"ws": {Path: "/tmp/ws"},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	r, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	path, ok := r.ResolveWorkspaceByName("nonexistent")
	if ok {
		t.Error("expected ok=false for non-matching name")
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

// ---------- ResolveAgentTarget ----------

func TestResolveAgentTarget_WorkspaceMode_WorkspaceName(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	target, err := ResolveAgentTarget("myws")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	if target.WorkDir != tmpDir {
		t.Errorf("expected WorkDir=%s, got %s", tmpDir, target.WorkDir)
	}
	if target.AgentName != "myws" {
		t.Errorf("expected AgentName='myws', got %q", target.AgentName)
	}
}

func TestResolveAgentTarget_WorkspaceMode_RepoName(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	target, err := ResolveAgentTarget("repo1")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	// In workspace mode, always returns workspace root, not repo path
	if target.WorkDir != tmpDir {
		t.Errorf("expected WorkDir=%s (workspace root), got %s", tmpDir, target.WorkDir)
	}
	// Agent name should be the workspace name, not the repo name
	if target.AgentName != "myws" {
		t.Errorf("expected AgentName='myws', got %q", target.AgentName)
	}
}

func TestResolveAgentTarget_WorkspaceMode_NoArg(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	target, err := ResolveAgentTarget("")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	if target.WorkDir != tmpDir {
		t.Errorf("expected WorkDir=%s, got %s", tmpDir, target.WorkDir)
	}
	if target.AgentName != "myws" {
		t.Errorf("expected AgentName='myws', got %q", target.AgentName)
	}
}

func TestResolveAgentTarget_WorkspaceMode_InvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	_, err := ResolveAgentTarget("nonexistent")
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention 'nonexistent', got: %v", err)
	}
}

func TestResolveAgentTarget_LegacyMode_WorktreeName(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	// Create worktrees/falcon directory
	wtPath := filepath.Join(tmpDir, "worktrees", "falcon")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	target, err := ResolveAgentTarget("falcon")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	if target.WorkDir != wtPath {
		t.Errorf("expected WorkDir=%s, got %s", wtPath, target.WorkDir)
	}
	if target.AgentName != "falcon" {
		t.Errorf("expected AgentName='falcon', got %q", target.AgentName)
	}
}

func TestResolveAgentTarget_LegacyMode_NoArg(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	target, err := ResolveAgentTarget("")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	if target.WorkDir != tmpDir {
		t.Errorf("expected WorkDir=%s (cwd), got %s", tmpDir, target.WorkDir)
	}
	// AgentName should be basename of cwd
	expectedName := filepath.Base(tmpDir)
	if target.AgentName != expectedName {
		t.Errorf("expected AgentName=%q, got %q", expectedName, target.AgentName)
	}
}

// ---------- worktreeCompletion with workspaces ----------

func TestWorktreeCompletion_WorkspaceMode(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "repo1", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Mock git branch --show-current for DiscoverWorktrees
	mock := NewCommandMock(t, []CommandStub{{
		Name:   "git",
		Args:   []string{"branch", "--show-current"},
		Stdout: "main\n",
	}})
	mock.Install()

	completions, directive := worktreeCompletion(nil, []string{}, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}

	// Should have workspace name "myws" and repo name "repo1"
	foundWorkspace := false
	foundRepo := false
	for _, c := range completions {
		if strings.HasPrefix(c, "myws\t") {
			foundWorkspace = true
		}
		if strings.HasPrefix(c, "repo1\t") {
			foundRepo = true
		}
	}
	if !foundWorkspace {
		t.Errorf("expected workspace name 'myws' in completions, got %v", completions)
	}
	if !foundRepo {
		t.Errorf("expected repo name 'repo1' in completions, got %v", completions)
	}
}

func TestWorktreeCompletion_WorkspaceMode_DeduplicatesNames(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Workspace name same as repo name - should not duplicate
	repoPath := filepath.Join(tmpDir, "myws")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "myws", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Mock git branch --show-current for DiscoverWorktrees
	mock := NewCommandMock(t, []CommandStub{{
		Name:   "git",
		Args:   []string{"branch", "--show-current"},
		Stdout: "main\n",
	}})
	mock.Install()

	completions, directive := worktreeCompletion(nil, []string{}, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}

	// "myws" should appear exactly once (as workspace, repo skipped due to seen map)
	count := 0
	for _, c := range completions {
		if strings.HasPrefix(c, "myws\t") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'myws' to appear once in completions, appeared %d times; completions=%v", count, completions)
	}
}

func TestWorktreeCompletion_LegacyMode_Unchanged(t *testing.T) {
	// No config -> legacy mode, should behave same as before
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "worktrees", "falcon", ".git"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	mock := NewCommandMock(t, []CommandStub{{
		Name:   "git",
		Args:   []string{"branch", "--show-current"},
		Stdout: "falcon-branch\n",
	}})
	mock.Install()

	completions, directive := worktreeCompletion(nil, []string{}, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(completions) != 1 {
		t.Fatalf("expected 1 completion, got %d: %v", len(completions), completions)
	}
	if completions[0] != "falcon\tfalcon-branch" {
		t.Errorf("completion = %q, want %q", completions[0], "falcon\tfalcon-branch")
	}
}
