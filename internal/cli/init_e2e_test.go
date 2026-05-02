//go:build e2e
// +build e2e

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupInitTestRepo creates an isolated temp dir with a real git repo.
func setupInitTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks (macOS /tmp -> /private/tmp)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	runGit(t, resolved, "init")
	runGit(t, resolved, "config", "user.email", "e2e@test.local")
	runGit(t, resolved, "config", "user.name", "E2E Test")
	runGit(t, resolved, "commit", "--allow-empty", "-m", "init")
	return resolved
}

// runLoomInit runs `loom init` as a subprocess in dir with the given args.
// Returns stdout, stderr, and exit code.
func runLoomInit(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	fullArgs := append([]string{"init"}, args...)
	cmd := exec.Command(loom, fullArgs...)
	cmd.Dir = dir

	// Build clean environment: unset LOOM_BACKEND and LOOM_WORKTREES_DIR to avoid interference
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_BACKEND=") ||
			strings.HasPrefix(e, "LOOM_WORKTREES_DIR=") ||
			strings.HasPrefix(e, "LOOM_FLEETDB_ENABLED=") {
			continue
		}
		filtered = append(filtered, e)
	}
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
			t.Fatalf("failed to run loom init: %v", err)
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func TestE2E_InitLegacyNonInteractive(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := setupInitTestRepo(t)

	stdout, _, exitCode := runLoomInit(t, dir, "--yes")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}

	for _, want := range []string{
		"🔧 Loom Setup Wizard",
		"Step 1: Prerequisites",
		"✓ git repository detected",
		"✓ fleet-db issue backend active",
		"Step 2: Issue storage",
		"Step 3: Create worktrees directory",
		"Step 4: Create agent worktrees",
		"Setup complete!",
		"Directory structure:",
		"Next steps:",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, stdout)
		}
	}
}

func TestE2E_InitCreatesWorktrees(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := setupInitTestRepo(t)

	stdout, _, exitCode := runLoomInit(t, dir, "--yes")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}

	// Verify output mentions creating worktrees
	if !strings.Contains(stdout, "Creating worktree falcon on branch falcon") {
		t.Errorf("stdout missing falcon creation message\nfull output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Creating worktree nova on branch nova") {
		t.Errorf("stdout missing nova creation message\nfull output:\n%s", stdout)
	}

	// Count "✓ Created" occurrences (at least 2 for two worktrees)
	if strings.Count(stdout, "✓ Created") < 2 {
		t.Errorf("expected at least 2 '✓ Created' in output, got %d\nfull output:\n%s",
			strings.Count(stdout, "✓ Created"), stdout)
	}

	// Verify directories exist
	for _, name := range []string{"falcon", "nova"} {
		wtDir := filepath.Join(dir, "worktrees", name)
		if info, err := os.Stat(wtDir); err != nil || !info.IsDir() {
			t.Errorf("expected worktree directory %s to exist", wtDir)
		}
		gitFile := filepath.Join(wtDir, ".git")
		if _, err := os.Stat(gitFile); err != nil {
			t.Errorf("expected .git in worktree %s (confirms real git worktree)", name)
		}
	}
}

func TestE2E_InitCustomNames(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := setupInitTestRepo(t)

	stdout, _, exitCode := runLoomInit(t, dir, "--yes", "--names", "spark,ember,flux")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}

	for _, name := range []string{"spark", "ember", "flux"} {
		if !strings.Contains(stdout, "Creating worktree "+name+" on branch "+name) {
			t.Errorf("stdout missing creation message for %s\nfull output:\n%s", name, stdout)
		}
		wtDir := filepath.Join(dir, "worktrees", name)
		if info, err := os.Stat(wtDir); err != nil || !info.IsDir() {
			t.Errorf("expected worktree directory %s to exist", wtDir)
		}
	}

	// Default names should NOT exist
	if _, err := os.Stat(filepath.Join(dir, "worktrees", "falcon")); err == nil {
		t.Error("default worktree 'falcon' should NOT exist when --names is specified")
	}
}

func TestE2E_InitIdempotent(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := setupInitTestRepo(t)

	// First run
	stdout1, _, exitCode1 := runLoomInit(t, dir, "--yes")
	if exitCode1 != 0 {
		t.Fatalf("first run: expected exit 0, got %d\nstdout: %s", exitCode1, stdout1)
	}

	// Second run (idempotent)
	stdout2, _, exitCode2 := runLoomInit(t, dir, "--yes")
	if exitCode2 != 0 {
		t.Fatalf("second run: expected exit 0, got %d\nstdout: %s", exitCode2, stdout2)
	}

	if !strings.Contains(stdout2, "Fleet-db issue storage is used") {
		t.Errorf("second run should report fleet-db issue storage\nfull output:\n%s", stdout2)
	}
	if !strings.Contains(stdout2, "already exists") {
		t.Errorf("second run should report existing directories/worktrees\nfull output:\n%s", stdout2)
	}
	// Either "No new worktrees to create" or "already exists, skipping"
	if !strings.Contains(stdout2, "No new worktrees to create") && !strings.Contains(stdout2, "already exists, skipping") {
		t.Errorf("second run should skip existing worktrees\nfull output:\n%s", stdout2)
	}
	if !strings.Contains(stdout2, "Setup complete!") {
		t.Errorf("second run should still complete successfully\nfull output:\n%s", stdout2)
	}

	// Verify directories still intact
	for _, name := range []string{"falcon", "nova"} {
		wtDir := filepath.Join(dir, "worktrees", name)
		if info, err := os.Stat(wtDir); err != nil || !info.IsDir() {
			t.Errorf("worktree directory %s should survive second run", wtDir)
		}
	}
}

func TestE2E_InitNotGitRepo(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := t.TempDir()

	stdout, stderr, exitCode := runLoomInit(t, dir, "--yes")
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code for non-git dir, got 0")
	}

	combined := stdout + stderr
	if !strings.Contains(combined, "✗ Not a git repository") {
		t.Errorf("expected 'Not a git repository' in output\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(combined, "Please run 'loom init' from within a git repository") {
		t.Errorf("expected guidance message in output\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

func TestE2E_InitCustomWorktreesDir(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := setupInitTestRepo(t)

	stdout, _, exitCode := runLoomInit(t, dir, "--yes", "--worktrees-dir", "agents")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}

	if !strings.Contains(stdout, "agents") {
		t.Errorf("stdout should mention custom dir 'agents'\nfull output:\n%s", stdout)
	}

	for _, name := range []string{"falcon", "nova"} {
		wtDir := filepath.Join(dir, "agents", name)
		if info, err := os.Stat(wtDir); err != nil || !info.IsDir() {
			t.Errorf("expected worktree directory %s to exist", wtDir)
		}
	}

	// Default worktrees dir should NOT exist
	if _, err := os.Stat(filepath.Join(dir, "worktrees")); err == nil {
		t.Error("default 'worktrees' directory should NOT exist when --worktrees-dir is specified")
	}
}

func TestE2E_InitUsesFleetDBStorage(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := setupInitTestRepo(t)

	stdout, _, exitCode := runLoomInit(t, dir, "--yes")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}

	beadsDir := filepath.Join(dir, ".beads")
	if _, err := os.Stat(beadsDir); !os.IsNotExist(err) {
		t.Errorf("expected no .beads directory at %s, stat err=%v", beadsDir, err)
	}

	if !strings.Contains(stdout, "Fleet-db issue storage is used") {
		t.Errorf("expected fleet-db storage message\nfull output:\n%s", stdout)
	}
}

func TestE2E_InitHooksInstalled(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := setupInitTestRepo(t)

	stdout, _, exitCode := runLoomInit(t, dir, "--yes")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s", exitCode, stdout)
	}

	if !strings.Contains(stdout, "✓ Installed Claude Code hooks") {
		t.Errorf("expected hooks installation message\nfull output:\n%s", stdout)
	}

	// Verify settings.json exists in at least one worktree
	settingsPath := filepath.Join(dir, "worktrees", "falcon", ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("expected Claude Code hooks settings at %s: %v", settingsPath, err)
	}
}

func TestE2E_InitExitCodeOnPrereqFail(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)
	dir := t.TempDir()

	_, _, exitCode := runLoomInit(t, dir, "--yes")
	if exitCode != 1 {
		t.Errorf("expected exit code 1 when prerequisites fail, got %d", exitCode)
	}
}
