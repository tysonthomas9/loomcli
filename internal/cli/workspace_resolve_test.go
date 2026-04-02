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
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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
	// not parallel: uses t.Setenv, defaultResolver global
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
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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

	target, err := ResolveAgentTarget("myws", "")
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
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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

	target, err := ResolveAgentTarget("repo1", "")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	// In workspace mode, repo name resolves to the repo's worktree path
	// so each agent gets its own lock file and working directory.
	if target.WorkDir != repoPath {
		t.Errorf("expected WorkDir=%s (repo path), got %s", repoPath, target.WorkDir)
	}
	if target.AgentName != "repo1" {
		t.Errorf("expected AgentName='repo1', got %q", target.AgentName)
	}
}

func TestResolveAgentTarget_WorkspaceMode_NoArg(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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

	target, err := ResolveAgentTarget("", "")
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
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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

	_, err := ResolveAgentTarget("nonexistent", "")
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention 'nonexistent', got: %v", err)
	}
}

func TestResolveAgentTarget_LegacyMode_WorktreeName(t *testing.T) {
	// not parallel: uses t.Setenv, defaultResolver global, os.Chdir
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

	target, err := ResolveAgentTarget("falcon", "")
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
	// not parallel: uses t.Setenv, defaultResolver global, os.Chdir
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	target, err := ResolveAgentTarget("", "")
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

// ---------- ResolveAgentTarget per-repo routing ----------

func TestResolveAgentTarget_WorkspaceMode_WithRepo(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global, defaultDeps (ensureRepoWorktree)
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "myrepo")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "myrepo", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	target, err := ResolveAgentTarget("myagent", "myrepo")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	expectedDir := filepath.Join(tmpDir, "worktrees", "myrepo", "myagent")
	if target.WorkDir != expectedDir {
		t.Errorf("expected WorkDir=%s, got %s", expectedDir, target.WorkDir)
	}
	if target.AgentName != "myagent" {
		t.Errorf("expected AgentName='myagent', got %q", target.AgentName)
	}
	if target.Repo != "myrepo" {
		t.Errorf("expected Repo='myrepo', got %q", target.Repo)
	}
	// Verify worktree was created on disk
	gitFile := filepath.Join(expectedDir, ".git")
	if _, err := os.Stat(gitFile); os.IsNotExist(err) {
		t.Errorf("expected worktree .git file at %s", gitFile)
	}
}

func TestResolveAgentTarget_WorkspaceMode_WithRepo_AlreadyExists(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global, defaultDeps (ensureRepoWorktree)
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "myrepo")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "myrepo", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// First call creates
	_, err := ResolveAgentTarget("myagent", "myrepo")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call should succeed (idempotent)
	target, err := ResolveAgentTarget("myagent", "myrepo")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	expectedDir := filepath.Join(tmpDir, "worktrees", "myrepo", "myagent")
	if target.WorkDir != expectedDir {
		t.Errorf("expected WorkDir=%s, got %s", expectedDir, target.WorkDir)
	}
}

func TestResolveAgentTarget_WorkspaceMode_EmptyRepo(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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

	target, err := ResolveAgentTarget("", "")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	// With empty repo, should fall through to workspace root
	if target.WorkDir != tmpDir {
		t.Errorf("expected WorkDir=%s (workspace root), got %s", tmpDir, target.WorkDir)
	}
	if target.Repo != "" {
		t.Errorf("expected Repo='', got %q", target.Repo)
	}
}

func TestResolveAgentTarget_WorkspaceMode_UnknownRepo(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
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

	_, err := ResolveAgentTarget("myagent", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown repo")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("expected error to mention 'nonexistent', got: %v", err)
	}
}

func TestResolveAgentTarget_WorkspaceMode_AbsPathIgnoresRepo(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repoPath)

	absTarget := filepath.Join(tmpDir, "custom")
	os.MkdirAll(absTarget, 0755)

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

	// Absolute path takes priority, repo is ignored
	target, err := ResolveAgentTarget(absTarget, "repo1")
	if err != nil {
		t.Fatalf("ResolveAgentTarget: %v", err)
	}
	if target.WorkDir != absTarget {
		t.Errorf("expected WorkDir=%s, got %s", absTarget, target.WorkDir)
	}
}

func TestResolveAgentTarget_LegacyMode_RepoError(t *testing.T) {
	// not parallel: uses t.Setenv, defaultResolver global
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	_, err := ResolveAgentTarget("myagent", "somerepo")
	if err == nil {
		t.Fatal("expected error for repo in legacy mode")
	}
	if !strings.Contains(err.Error(), "workspace mode") {
		t.Errorf("expected error to mention workspace mode, got: %v", err)
	}
}

func TestEnsureRepoWorktree_Creates(t *testing.T) {
	// not parallel: uses ensureRepoWorktree which calls defaultDeps
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	createGitRepo(t, repoPath)

	wtPath := filepath.Join(tmpDir, "worktrees", "repo", "agent1")
	err := ensureRepoWorktree(repoPath, wtPath, "agent1")
	if err != nil {
		t.Fatalf("ensureRepoWorktree: %v", err)
	}
	gitFile := filepath.Join(wtPath, ".git")
	if _, err := os.Stat(gitFile); os.IsNotExist(err) {
		t.Errorf("expected .git file at %s", gitFile)
	}
}

func TestEnsureRepoWorktree_Idempotent(t *testing.T) {
	// not parallel: uses ensureRepoWorktree which calls defaultDeps
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	createGitRepo(t, repoPath)

	wtPath := filepath.Join(tmpDir, "worktrees", "repo", "agent1")
	// First call
	if err := ensureRepoWorktree(repoPath, wtPath, "agent1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call should succeed
	if err := ensureRepoWorktree(repoPath, wtPath, "agent1"); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestEnsureRepoWorktree_BranchExists(t *testing.T) {
	// not parallel: uses ensureRepoWorktree which calls defaultDeps
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "repo")
	createGitRepo(t, repoPath)

	// Create branch first
	RunGitCommand(repoPath, "branch", "agent1")

	wtPath := filepath.Join(tmpDir, "worktrees", "repo", "agent1")
	err := ensureRepoWorktree(repoPath, wtPath, "agent1")
	if err != nil {
		t.Fatalf("ensureRepoWorktree with existing branch: %v", err)
	}
	gitFile := filepath.Join(wtPath, ".git")
	if _, err := os.Stat(gitFile); os.IsNotExist(err) {
		t.Errorf("expected .git file at %s", gitFile)
	}
}

func TestResolveAgentTarget_WorkspaceMode_MultipleAgentsSameRepo(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global, defaultDeps (ensureRepoWorktree)
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repoPath := filepath.Join(tmpDir, "myrepo")
	createGitRepo(t, repoPath)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path:  tmpDir,
				Repos: []RepoConfig{{Name: "myrepo", Path: repoPath}},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Two different agents targeting the same repo
	t1, err := ResolveAgentTarget("agent1", "myrepo")
	if err != nil {
		t.Fatalf("agent1: %v", err)
	}
	t2, err := ResolveAgentTarget("agent2", "myrepo")
	if err != nil {
		t.Fatalf("agent2: %v", err)
	}
	// Each should get a different worktree directory
	if t1.WorkDir == t2.WorkDir {
		t.Errorf("expected different WorkDir for different agents, both got %s", t1.WorkDir)
	}
	expected1 := filepath.Join(tmpDir, "worktrees", "myrepo", "agent1")
	expected2 := filepath.Join(tmpDir, "worktrees", "myrepo", "agent2")
	if t1.WorkDir != expected1 {
		t.Errorf("agent1 WorkDir: expected %s, got %s", expected1, t1.WorkDir)
	}
	if t2.WorkDir != expected2 {
		t.Errorf("agent2 WorkDir: expected %s, got %s", expected2, t2.WorkDir)
	}
}

// ---------- worktreeCompletion with workspaces ----------

func TestWorktreeCompletion_WorkspaceMode(t *testing.T) {
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global, mock.Install()
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
	// not parallel: uses setupWorkspaceConfig (t.Setenv), defaultResolver global, mock.Install()
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
	// not parallel: uses t.Setenv, defaultResolver global, os.Chdir, mock.Install()
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
