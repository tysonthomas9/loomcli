//go:build e2e
// +build e2e

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runLoomConfig executes `loom config <subArgs...>` with an isolated LOOM_CONFIG_DIR.
// Returns stdout, stderr, and the exit code.
func runLoomConfig(t *testing.T, configDir string, subArgs ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	args := append([]string{"config"}, subArgs...)
	cmd := exec.Command(loom, args...)

	// Use configDir's parent as working directory so cwd-based defaults work
	cmd.Dir = filepath.Dir(configDir)

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
	cmd.Env = filtered

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom config: %v", err)
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// configTestDir creates an isolated temp dir suitable for LOOM_CONFIG_DIR.
func configTestDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// --- config init tests ---

func TestE2E_ConfigInit_CreatesDefaultConfig(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	stdout, _, exitCode := runLoomConfig(t, dir, "init", "--workspace", "testws")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Config created") {
		t.Errorf("expected stdout to contain 'Config created', got: %s", stdout)
	}

	// Verify config.yaml exists and has correct content
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "default_workspace: testws") {
		t.Errorf("expected config to contain 'default_workspace: testws', got:\n%s", content)
	}
}

func TestE2E_ConfigInit_DefaultWorkspaceName(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	stdout, _, exitCode := runLoomConfig(t, dir, "init")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Config created") {
		t.Errorf("expected stdout to contain 'Config created', got: %s", stdout)
	}

	// Workspace name should default to the basename of cwd (which is the parent of configDir)
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "default_workspace:") {
		t.Errorf("expected config to contain 'default_workspace:', got:\n%s", content)
	}
}

func TestE2E_ConfigInit_FailsIfExists(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	// Create config first
	_, _, exitCode := runLoomConfig(t, dir, "init", "--workspace", "ws1")
	if exitCode != 0 {
		t.Fatalf("first init failed with exit %d", exitCode)
	}

	// Second init without --force should fail
	_, stderr, exitCode := runLoomConfig(t, dir, "init")
	if exitCode != 1 {
		t.Errorf("expected exit 1, got %d", exitCode)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("expected stderr to contain 'already exists', got: %s", stderr)
	}
}

func TestE2E_ConfigInit_ForceOverwrites(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	// Create config first
	_, _, exitCode := runLoomConfig(t, dir, "init", "--workspace", "oldname")
	if exitCode != 0 {
		t.Fatalf("first init failed with exit %d", exitCode)
	}

	// Force overwrite
	_, _, exitCode = runLoomConfig(t, dir, "init", "--force", "--workspace", "newname")
	if exitCode != 0 {
		t.Errorf("expected exit 0 with --force, got %d", exitCode)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml missing after force init: %v", err)
	}
	if !strings.Contains(string(data), "default_workspace: newname") {
		t.Errorf("expected config to have 'default_workspace: newname', got:\n%s", string(data))
	}
}

func TestE2E_ConfigInit_CreatesNestedDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	nestedDir := filepath.Join(base, "deeply", "nested", "config")

	_, _, exitCode := runLoomConfig(t, nestedDir, "init", "--workspace", "ws1")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	if _, err := os.Stat(filepath.Join(nestedDir, "config.yaml")); err != nil {
		t.Errorf("config.yaml not created in nested dir: %v", err)
	}
}

func TestE2E_ConfigInit_SetsVersionField(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	_, _, exitCode := runLoomConfig(t, dir, "init", "--workspace", "ws1")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml not created: %v", err)
	}
	if !strings.Contains(string(data), "version:") {
		t.Errorf("expected config to contain 'version:', got:\n%s", string(data))
	}
}

// --- config show tests ---

func TestE2E_ConfigShow_DisplaysConfig(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	// Create config first
	_, _, exitCode := runLoomConfig(t, dir, "init", "--workspace", "showtest")
	if exitCode != 0 {
		t.Fatalf("init failed with exit %d", exitCode)
	}

	stdout, _, exitCode := runLoomConfig(t, dir, "show")
	if exitCode != 0 {
		t.Errorf("expected exit 0 for show, got %d", exitCode)
	}
	if !strings.Contains(stdout, "default_workspace") {
		t.Errorf("expected show output to contain 'default_workspace', got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "workspaces") {
		t.Errorf("expected show output to contain 'workspaces', got:\n%s", stdout)
	}
}

func TestE2E_ConfigShow_NoConfig(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	stdout, _, exitCode := runLoomConfig(t, dir, "show")
	if exitCode != 0 {
		t.Errorf("expected exit 0 for show with no config, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No config file found") {
		t.Errorf("expected 'No config file found' in stdout, got: %s", stdout)
	}
}

func TestE2E_ConfigShow_PrintsRepos(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	// Init then add a repo
	runLoomConfig(t, dir, "init", "--workspace", "ws1")
	runLoomConfig(t, dir, "add-repo", "ws1", "myrepo", "--path", "/tmp/myrepo")

	stdout, _, exitCode := runLoomConfig(t, dir, "show")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "myrepo") {
		t.Errorf("expected show output to contain 'myrepo', got:\n%s", stdout)
	}
}

// --- config add-repo tests ---

func TestE2E_ConfigAddRepo_HappyPath(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "testws")

	stdout, _, exitCode := runLoomConfig(t, dir, "add-repo", "testws", "myrepo",
		"--path", "/tmp/myrepo", "--branch", "main", "--remote", "origin")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Added repo") {
		t.Errorf("expected stdout to contain 'Added repo', got: %s", stdout)
	}

	// Verify via show
	showOut, _, _ := runLoomConfig(t, dir, "show")
	if !strings.Contains(showOut, "myrepo") {
		t.Errorf("expected show output to contain 'myrepo', got:\n%s", showOut)
	}
}

func TestE2E_ConfigAddRepo_MinimalFlags(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")

	_, _, exitCode := runLoomConfig(t, dir, "add-repo", "ws1", "minrepo", "--path", "/tmp/min")
	if exitCode != 0 {
		t.Errorf("expected exit 0 with only --path, got %d", exitCode)
	}
}

func TestE2E_ConfigAddRepo_DuplicateRepoFails(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")
	runLoomConfig(t, dir, "add-repo", "ws1", "dup", "--path", "/tmp/dup")

	_, stderr, exitCode := runLoomConfig(t, dir, "add-repo", "ws1", "dup", "--path", "/tmp/dup2")
	if exitCode != 1 {
		t.Errorf("expected exit 1 for duplicate repo, got %d", exitCode)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("expected stderr to contain 'already exists', got: %s", stderr)
	}
}

func TestE2E_ConfigAddRepo_NoConfigFails(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	_, stderr, exitCode := runLoomConfig(t, dir, "add-repo", "ws1", "repo1", "--path", "/tmp/r")
	if exitCode != 1 {
		t.Errorf("expected exit 1 without config, got %d", exitCode)
	}
	if !strings.Contains(stderr, "No config found") {
		t.Errorf("expected stderr to contain 'No config found', got: %s", stderr)
	}
}

func TestE2E_ConfigAddRepo_InvalidWorkspaceFails(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")

	_, stderr, exitCode := runLoomConfig(t, dir, "add-repo", "nonexistent", "repo1", "--path", "/tmp/r")
	if exitCode != 1 {
		t.Errorf("expected exit 1 for invalid workspace, got %d", exitCode)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("expected stderr to contain 'not found', got: %s", stderr)
	}
}

// --- config remove-repo tests ---

func TestE2E_ConfigRemoveRepo_HappyPath(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")
	runLoomConfig(t, dir, "add-repo", "ws1", "removeme", "--path", "/tmp/rm")

	stdout, _, exitCode := runLoomConfig(t, dir, "remove-repo", "ws1", "removeme")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Removed repo") {
		t.Errorf("expected stdout to contain 'Removed repo', got: %s", stdout)
	}

	// Verify repo is gone via show
	showOut, _, _ := runLoomConfig(t, dir, "show")
	if strings.Contains(showOut, "removeme") {
		t.Errorf("expected show output to NOT contain 'removeme' after removal, got:\n%s", showOut)
	}
}

func TestE2E_ConfigRemoveRepo_NotFoundFails(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")

	_, stderr, exitCode := runLoomConfig(t, dir, "remove-repo", "ws1", "ghost")
	if exitCode != 1 {
		t.Errorf("expected exit 1 for missing repo, got %d", exitCode)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("expected stderr to contain 'not found', got: %s", stderr)
	}
}

func TestE2E_ConfigRemoveRepo_NoConfigFails(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	_, _, exitCode := runLoomConfig(t, dir, "remove-repo", "ws1", "repo1")
	if exitCode != 1 {
		t.Errorf("expected exit 1 without config, got %d", exitCode)
	}
}

func TestE2E_ConfigRemoveRepo_LeavesWorkspaceIntact(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")
	runLoomConfig(t, dir, "add-repo", "ws1", "keep", "--path", "/tmp/keep")
	runLoomConfig(t, dir, "add-repo", "ws1", "drop", "--path", "/tmp/drop")

	_, _, exitCode := runLoomConfig(t, dir, "remove-repo", "ws1", "drop")
	if exitCode != 0 {
		t.Fatalf("remove failed with exit %d", exitCode)
	}

	showOut, _, _ := runLoomConfig(t, dir, "show")
	if !strings.Contains(showOut, "keep") {
		t.Errorf("expected 'keep' repo to remain after removing 'drop', got:\n%s", showOut)
	}
	if strings.Contains(showOut, "drop") {
		t.Errorf("expected 'drop' repo to be gone, but still found in:\n%s", showOut)
	}
}

// --- config migrate tests ---

func TestE2E_ConfigMigrate_AlreadyCurrent(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")

	stdout, _, exitCode := runLoomConfig(t, dir, "migrate")
	if exitCode != 0 {
		t.Errorf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "already at version") {
		t.Errorf("expected stdout to contain 'already at version', got: %s", stdout)
	}
}

func TestE2E_ConfigMigrate_NoConfigFails(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	_, _, exitCode := runLoomConfig(t, dir, "migrate")
	if exitCode != 1 {
		t.Errorf("expected exit 1 without config, got %d", exitCode)
	}
}

func TestE2E_ConfigMigrate_ProjectFlag(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	// --project looks for loom.yaml in cwd, which won't exist
	_, _, exitCode := runLoomConfig(t, dir, "migrate", "--project")
	if exitCode != 1 {
		t.Errorf("expected exit 1 with --project and no loom.yaml, got %d", exitCode)
	}
}

// --- argument validation tests ---

func TestE2E_ConfigAddRepo_InvalidRemoteFails(t *testing.T) {
	t.Parallel()
	dir := configTestDir(t)

	runLoomConfig(t, dir, "init", "--workspace", "ws1")

	_, stderr, exitCode := runLoomConfig(t, dir, "add-repo", "ws1", "repo1",
		"--path", "/tmp/r", "--remote", "--evil")
	if exitCode == 0 {
		t.Errorf("expected non-zero exit for invalid remote, got 0")
	}
	// Should contain either "invalid" or "must not start with"
	lower := strings.ToLower(stderr + fmt.Sprintf(" exit=%d", exitCode))
	if !strings.Contains(lower, "invalid") && !strings.Contains(lower, "must not start with") && !strings.Contains(stderr, "unknown flag") {
		t.Errorf("expected error about invalid remote, got stderr: %s", stderr)
	}
}
