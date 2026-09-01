//go:build e2e
// +build e2e

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// runLoomWorkspace executes `loom workspace <subArgs...>` with an isolated LOOM_CONFIG_DIR.
func runLoomWorkspace(t *testing.T, configDir string, subArgs ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	args := append([]string{"workspace"}, subArgs...)
	cmd := exec.Command(loom, args...)

	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_CONFIG_DIR=") {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered, "LOOM_CONFIG_DIR="+configDir)
	filtered = append(filtered, "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = testutil.SandboxLoomRuntimeDir(filtered)

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom workspace: %v", err)
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// createTestGitRepo creates a minimal git repo at the given path.
func createTestGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}
	runGit(t, path, "init")
	runGit(t, path, "config", "user.email", "e2e@test.local")
	runGit(t, path, "config", "user.name", "E2E Test")
	runGit(t, path, "commit", "--allow-empty", "-m", "initial")
}

// readConfigYAML reads and returns the config.yaml contents from the config dir.
func readConfigYAML(t *testing.T, configDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("failed to read config.yaml: %v", err)
	}
	return string(data)
}

// --- workspace create tests ---

func TestE2E_WorkspaceCreate_SingleRepo(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "create", "myws", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Created worktree") {
		t.Errorf("expected stdout to contain 'Created worktree', got: %s", stdout)
	}
	if !strings.Contains(stdout, "created at") {
		t.Errorf("expected stdout to contain 'created at', got: %s", stdout)
	}

	cfg := readConfigYAML(t, configDir)
	if !strings.Contains(cfg, "default_workspace: myws") {
		t.Errorf("expected config to have 'default_workspace: myws', got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "myrepo") {
		t.Errorf("expected config to contain repo name 'myrepo', got:\n%s", cfg)
	}

	// Verify worktree directory exists
	if _, err := os.Stat(filepath.Join(wsDir, "myrepo")); err != nil {
		t.Errorf("worktree directory not created: %v", err)
	}
}

func TestE2E_WorkspaceCreate_MultipleRepos(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	frontend := filepath.Join(base, "frontend")
	backend := filepath.Join(base, "backend")
	createTestGitRepo(t, frontend)
	createTestGitRepo(t, backend)
	wsDir := filepath.Join(base, "ws")

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "create", "multi", "--repos", frontend+","+backend, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	// Should have two "Created worktree" lines
	if strings.Count(stdout, "Created worktree") != 2 {
		t.Errorf("expected 2 'Created worktree' lines, got: %s", stdout)
	}

	cfg := readConfigYAML(t, configDir)
	if !strings.Contains(cfg, "frontend") || !strings.Contains(cfg, "backend") {
		t.Errorf("expected config to list both repos, got:\n%s", cfg)
	}

	// Verify both worktree directories exist
	for _, name := range []string{"frontend", "backend"} {
		if _, err := os.Stat(filepath.Join(wsDir, name)); err != nil {
			t.Errorf("worktree directory for %s not created: %v", name, err)
		}
	}
}

func TestE2E_WorkspaceCreate_CustomBranch(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "branchws", "--repos", repo, "--path", wsDir, "--branch", "feat-x")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	// Verify the worktree is on the correct branch
	worktreePath := filepath.Join(wsDir, "myrepo")
	cmd := exec.Command("git", "-C", worktreePath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current failed: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch != "feat-x" {
		t.Errorf("expected branch 'feat-x', got %q", branch)
	}
}

func TestE2E_WorkspaceCreate_DefaultBranchIsName(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "devws", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	worktreePath := filepath.Join(wsDir, "myrepo")
	cmd := exec.Command("git", "-C", worktreePath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current failed: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch != "devws" {
		t.Errorf("expected branch 'devws' (workspace name), got %q", branch)
	}
}

func TestE2E_WorkspaceCreate_SetAsDefault(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()

	repo1 := filepath.Join(base, "repo1")
	repo2 := filepath.Join(base, "repo2")
	createTestGitRepo(t, repo1)
	createTestGitRepo(t, repo2)

	// First workspace (auto-default since it's the only one)
	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "alpha", "--repos", repo1, "--path", filepath.Join(base, "ws1"))
	if exitCode != 0 {
		t.Fatalf("first create failed with exit %d", exitCode)
	}

	// Second workspace with --default
	_, _, exitCode = runLoomWorkspace(t, configDir, "create", "beta", "--repos", repo2, "--path", filepath.Join(base, "ws2"), "--default")
	if exitCode != 0 {
		t.Fatalf("second create failed with exit %d", exitCode)
	}

	cfg := readConfigYAML(t, configDir)
	if !strings.Contains(cfg, "default_workspace: beta") {
		t.Errorf("expected default_workspace to be 'beta', got:\n%s", cfg)
	}
}

func TestE2E_WorkspaceCreate_AlreadyExists(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "dup", "--repos", repo, "--path", filepath.Join(base, "ws1"))
	if exitCode != 0 {
		t.Fatalf("first create failed with exit %d", exitCode)
	}

	_, stderr, exitCode := runLoomWorkspace(t, configDir, "create", "dup", "--repos", repo, "--path", filepath.Join(base, "ws2"))
	if exitCode != 1 {
		t.Errorf("expected exit 1, got %d", exitCode)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("expected stderr to contain 'already exists', got: %s", stderr)
	}
}

func TestE2E_WorkspaceCreate_InvalidName(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)

	_, stderr, exitCode := runLoomWorkspace(t, configDir, "create", "bad name!", "--repos", repo)
	if exitCode != 1 {
		t.Errorf("expected exit 1, got %d", exitCode)
	}
	if !strings.Contains(stderr, "contains invalid characters") {
		t.Errorf("expected stderr to contain 'contains invalid characters', got: %s", stderr)
	}
}

func TestE2E_WorkspaceCreate_NonExistentRepo(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	wsDir := t.TempDir()

	_, stderr, exitCode := runLoomWorkspace(t, configDir, "create", "ws", "--repos", "/nonexistent/path", "--path", wsDir)
	if exitCode != 1 {
		t.Errorf("expected exit 1, got %d", exitCode)
	}
	if !strings.Contains(stderr, "does not exist") && !strings.Contains(stderr, "not found") {
		t.Errorf("expected stderr to mention path not found, got: %s", stderr)
	}
}

func TestE2E_WorkspaceCreate_MissingReposFlag(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, stderr, exitCode := runLoomWorkspace(t, configDir, "create", "ws")
	if exitCode != 1 {
		t.Errorf("expected exit 1, got %d", exitCode)
	}
	if !strings.Contains(stderr, "required flag") && !strings.Contains(stderr, "repos") {
		t.Errorf("expected stderr to mention required flag, got: %s", stderr)
	}
}

// --- workspace list tests ---

func TestE2E_WorkspaceList_NoWorkspaces(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "list")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No workspaces configured") {
		t.Errorf("expected stdout to contain 'No workspaces configured', got: %s", stdout)
	}
}

func TestE2E_WorkspaceList_ShowsWorkspaces(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "testws", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("create failed with exit %d", exitCode)
	}

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "list")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "testws") {
		t.Errorf("expected list output to contain 'testws', got: %s", stdout)
	}
	if !strings.Contains(stdout, "1 repos") {
		t.Errorf("expected list output to contain '1 repos', got: %s", stdout)
	}
	if !strings.Contains(stdout, "ok") {
		t.Errorf("expected list output to contain 'ok' status, got: %s", stdout)
	}
}

func TestE2E_WorkspaceList_DefaultMarker(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()

	repo1 := filepath.Join(base, "repo1")
	repo2 := filepath.Join(base, "repo2")
	createTestGitRepo(t, repo1)
	createTestGitRepo(t, repo2)

	runLoomWorkspace(t, configDir, "create", "alpha", "--repos", repo1, "--path", filepath.Join(base, "ws1"))
	runLoomWorkspace(t, configDir, "create", "beta", "--repos", repo2, "--path", filepath.Join(base, "ws2"), "--default")

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "list")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	// The default workspace (beta) should be marked with " *"
	if !strings.Contains(stdout, "*") {
		t.Errorf("expected list output to contain '*' marker for default workspace, got: %s", stdout)
	}
	// Verify the marker is on the beta line
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "beta") && !strings.Contains(line, "*") {
			t.Errorf("expected 'beta' line to have '*' marker, got: %s", line)
		}
		if strings.Contains(line, "alpha") && strings.Contains(line, "*") {
			t.Errorf("expected 'alpha' line NOT to have '*' marker, got: %s", line)
		}
	}
}

func TestE2E_WorkspaceList_MissingDirStatus(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "testws", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("create failed with exit %d", exitCode)
	}

	// Delete the workspace directory to simulate missing dir
	if err := os.RemoveAll(wsDir); err != nil {
		t.Fatalf("failed to remove wsDir: %v", err)
	}

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "list")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "missing") {
		t.Errorf("expected list output to show 'missing' status, got: %s", stdout)
	}
}

func TestE2E_WorkspaceList_JSONOutput(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "jsonws", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("create failed with exit %d", exitCode)
	}

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "list", "--json")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if !json.Valid([]byte(stdout)) {
		t.Errorf("expected valid JSON output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "jsonws") {
		t.Errorf("expected JSON output to contain 'jsonws', got: %s", stdout)
	}
}

func TestE2E_WorkspaceList_JSONParseable(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "parsews", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("create failed with exit %d", exitCode)
	}

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "list", "--json")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var result map[string]WorkspaceConfig
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v\nOutput: %s", err, stdout)
	}

	ws, ok := result["parsews"]
	if !ok {
		t.Fatalf("expected workspace 'parsews' in JSON output, got keys: %v", keysOf(result))
	}
	if len(ws.Repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(ws.Repos))
	}
	if ws.Path != wsDir {
		t.Errorf("expected path %q, got %q", wsDir, ws.Path)
	}
}

// keysOf returns the keys of a map as a slice.
func keysOf(m map[string]WorkspaceConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// --- workspace remove tests ---

func TestE2E_WorkspaceRemove_HappyPath(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "rmws", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("create failed with exit %d", exitCode)
	}

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "remove", "rmws")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "removed") {
		t.Errorf("expected stdout to contain 'removed', got: %s", stdout)
	}

	cfg := readConfigYAML(t, configDir)
	if strings.Contains(cfg, "rmws") {
		t.Errorf("expected config to no longer contain 'rmws', got:\n%s", cfg)
	}

	// Workspace directory should be deleted
	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Errorf("expected workspace directory to be deleted, but it still exists")
	}
}

func TestE2E_WorkspaceRemove_KeepWorktrees(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)
	wsDir := filepath.Join(base, "ws")

	_, _, exitCode := runLoomWorkspace(t, configDir, "create", "keepws", "--repos", repo, "--path", wsDir)
	if exitCode != 0 {
		t.Fatalf("create failed with exit %d", exitCode)
	}

	stdout, _, exitCode := runLoomWorkspace(t, configDir, "remove", "keepws", "--keep-worktrees")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "removed") {
		t.Errorf("expected stdout to contain 'removed', got: %s", stdout)
	}

	cfg := readConfigYAML(t, configDir)
	if strings.Contains(cfg, "keepws") {
		t.Errorf("expected config to no longer contain 'keepws', got:\n%s", cfg)
	}

	// Workspace directory should still exist
	if _, err := os.Stat(wsDir); err != nil {
		t.Errorf("expected workspace directory to still exist with --keep-worktrees, but got: %v", err)
	}
}

func TestE2E_WorkspaceRemove_NonExistent(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	createTestGitRepo(t, repo)

	// Create a workspace so config exists
	runLoomWorkspace(t, configDir, "create", "exists", "--repos", repo, "--path", filepath.Join(base, "ws"))

	_, stderr, exitCode := runLoomWorkspace(t, configDir, "remove", "nope")
	if exitCode != 1 {
		t.Errorf("expected exit 1, got %d", exitCode)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("expected stderr to contain 'not found', got: %s", stderr)
	}
}

func TestE2E_WorkspaceRemove_UpdatesDefault(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	base := t.TempDir()

	repo1 := filepath.Join(base, "repo1")
	repo2 := filepath.Join(base, "repo2")
	createTestGitRepo(t, repo1)
	createTestGitRepo(t, repo2)

	// Create alpha (auto-default) and beta
	runLoomWorkspace(t, configDir, "create", "alpha", "--repos", repo1, "--path", filepath.Join(base, "ws1"))
	runLoomWorkspace(t, configDir, "create", "beta", "--repos", repo2, "--path", filepath.Join(base, "ws2"))

	// Remove alpha (the default)
	_, _, exitCode := runLoomWorkspace(t, configDir, "remove", "alpha")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	cfg := readConfigYAML(t, configDir)
	if !strings.Contains(cfg, "default_workspace: beta") {
		t.Errorf("expected default_workspace to be updated to 'beta', got:\n%s", cfg)
	}
}

func TestE2E_WorkspaceRemove_NoArgs(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()

	_, _, exitCode := runLoomWorkspace(t, configDir, "remove")
	if exitCode == 0 {
		t.Errorf("expected non-zero exit for remove with no args, got 0")
	}
}
