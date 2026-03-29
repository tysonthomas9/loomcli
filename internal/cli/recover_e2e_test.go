//go:build e2e
// +build e2e

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Recover E2E helpers ---

// setupRecoverTestWorkspace creates a temp directory with git init + bd init
// and a worktrees/test-agent/ subdirectory (also git-initialized).
// Returns (projectDir, worktreeDir).
func setupRecoverTestWorkspace(t *testing.T) (string, string) {
	t.Helper()

	projectDir := initTempGitRepo(t) // reuses helper from doctor_e2e_test.go

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd binary not found on PATH")
	}

	// bd init in project root
	cmd := exec.Command("bd", "init")
	cmd.Dir = projectDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd init failed: %v\n%s", err, out)
	}

	// Create worktrees/test-agent/ with git init
	worktreeDir := filepath.Join(projectDir, "worktrees", "test-agent")
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		t.Fatalf("mkdir worktrees/test-agent: %v", err)
	}
	runGit(t, worktreeDir, "init")
	runGit(t, worktreeDir, "config", "user.email", "e2e@test.local")
	runGit(t, worktreeDir, "config", "user.name", "E2E Test")
	runGit(t, worktreeDir, "commit", "--allow-empty", "-m", "initial")

	return projectDir, worktreeDir
}

// createStaleLock writes a .agent.lock file in worktreeDir with a dead PID.
func createStaleLock(t *testing.T, worktreeDir, agentName, taskID string) {
	t.Helper()
	lock := LockInfo{
		PID:       2147483647, // Max int32, virtually guaranteed dead
		Command:   "task",
		StartedAt: time.Now().Add(-1 * time.Hour),
		AgentName: agentName,
		TaskID:    taskID,
		State:     "active",
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	lockPath := filepath.Join(worktreeDir, LockFileName)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
}

// createRecoverTask creates a bd task and returns its ID.
func createRecoverTask(t *testing.T, dir, title string) string {
	t.Helper()
	return createBdTask(t, dir, title)
}

// updateTaskStatus updates a task's status and assignee via bd.
func updateTaskStatus(t *testing.T, dir, taskID, status, assignee string) {
	t.Helper()
	args := []string{"update", taskID, "--status", status}
	if assignee != "" {
		args = append(args, "--assignee", assignee)
	}
	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd update %s failed: %v\n%s", taskID, err, out)
	}
}

// getTaskField retrieves a field from bd show --json output.
func getTaskField(t *testing.T, dir, taskID, field string) string {
	t.Helper()
	cmd := exec.Command("bd", "show", taskID, "--json")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd show %s --json failed: %v\n%s", taskID, err, out)
	}
	// bd show --json returns an array with one element
	var arr []map[string]interface{}
	if err := json.Unmarshal(out, &arr); err != nil {
		t.Fatalf("parse bd show output: %v\n%s", err, out)
	}
	if len(arr) == 0 {
		t.Fatalf("bd show returned empty array for %s", taskID)
	}
	val, ok := arr[0][field]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%v", val)
}

// runRecover runs `loom recover <args>` as a subprocess.
// Returns combined output and exit code.
func execRecover(t *testing.T, projectDir string, extraEnv []string, args ...string) (string, int) {
	t.Helper()

	loom := loomBinaryPath(t)

	fullArgs := append([]string{"recover"}, args...)
	cmd := exec.Command(loom, fullArgs...)
	cmd.Dir = projectDir

	// Isolated environment - use project dir as HOME so config doesn't leak
	env := []string{
		"HOME=" + projectDir,
		"PATH=" + filepath.Dir(loom) + ":" + os.Getenv("PATH"),
		"LOOM_CONFIG_DIR=" + filepath.Join(projectDir, ".loom-config"),
		"LOOM_BACKEND=claude",
		"GIT_CONFIG_NOSYSTEM=1",
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	env = append(env, extraEnv...)
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return string(out), exitCode
}

// --- Test cases ---

func TestE2E_RecoverNoArgs(t *testing.T) {
	loom := loomBinaryPath(t)
	cmd := exec.Command(loom, "recover")
	cmd.Dir = t.TempDir()

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit code, got 0\noutput:\n%s", out)
	}
	outStr := string(out)
	// Cobra's ExactArgs(1) should produce an argument validation error
	if !strings.Contains(outStr, "accepts") && !strings.Contains(outStr, "requires") && !strings.Contains(outStr, "arg") {
		t.Errorf("expected argument error message, got:\n%s", outStr)
	}
}

func TestE2E_RecoverNonExistentWorktree(t *testing.T) {
	projectDir, _ := setupRecoverTestWorkspace(t)

	output, exitCode := execRecover(t, projectDir, nil, "nonexistent-agent", "--force", "--no-analyze")

	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code for nonexistent worktree, got 0\noutput:\n%s", output)
	}
	if !strings.Contains(output, "not exist") && !strings.Contains(output, "not found") && !strings.Contains(output, "Error") {
		t.Errorf("expected error message about missing worktree, got:\n%s", output)
	}
}

func TestE2E_RecoverNoLockNoTasks(t *testing.T) {
	projectDir, _ := setupRecoverTestWorkspace(t)

	output, exitCode := execRecover(t, projectDir, nil, "test-agent", "--force", "--no-analyze")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput:\n%s", exitCode, output)
	}
	if !strings.Contains(output, "Recovering agent: test-agent") {
		t.Errorf("expected recovery banner, got:\n%s", output)
	}
	if !strings.Contains(output, "No lock file found") {
		t.Errorf("expected 'No lock file found' message, got:\n%s", output)
	}
	if !strings.Contains(output, "ready") {
		t.Errorf("expected ready message, got:\n%s", output)
	}
}

func TestE2E_RecoverStaleLockClearsLock(t *testing.T) {
	projectDir, worktreeDir := setupRecoverTestWorkspace(t)

	// Create a task and set it to in_progress assigned to test-agent
	taskID := createRecoverTask(t, projectDir, "Lock test task")
	updateTaskStatus(t, projectDir, taskID, "in_progress", "test-agent")

	// Create stale lock
	createStaleLock(t, worktreeDir, "test-agent", taskID)

	output, exitCode := execRecover(t, projectDir, nil, "test-agent", "--force", "--no-analyze")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput:\n%s", exitCode, output)
	}
	if !strings.Contains(output, "Clearing stale lock") {
		t.Errorf("expected 'Clearing stale lock' message, got:\n%s", output)
	}
	if !strings.Contains(output, "Lock cleared") {
		t.Errorf("expected 'Lock cleared' message, got:\n%s", output)
	}

	// Verify lock file is removed
	lockPath := filepath.Join(worktreeDir, LockFileName)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file should have been removed, but still exists at %s", lockPath)
	}

	// Verify task was reset to open
	status := getTaskField(t, projectDir, taskID, "status")
	if status != "open" {
		t.Errorf("expected task status 'open', got %q", status)
	}
}

func TestE2E_RecoverStaleLockResetsTaskToOpen(t *testing.T) {
	projectDir, worktreeDir := setupRecoverTestWorkspace(t)

	// Create TWO tasks, both in_progress assigned to test-agent
	taskID1 := createRecoverTask(t, projectDir, "Orphan task 1")
	updateTaskStatus(t, projectDir, taskID1, "in_progress", "test-agent")

	taskID2 := createRecoverTask(t, projectDir, "Orphan task 2")
	updateTaskStatus(t, projectDir, taskID2, "in_progress", "test-agent")

	// Create stale lock referencing FIRST task
	createStaleLock(t, worktreeDir, "test-agent", taskID1)

	output, exitCode := execRecover(t, projectDir, nil, "test-agent", "--force", "--no-analyze")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput:\n%s", exitCode, output)
	}

	// Both tasks should be reset to open
	status1 := getTaskField(t, projectDir, taskID1, "status")
	if status1 != "open" {
		t.Errorf("expected task 1 status 'open', got %q", status1)
	}
	status2 := getTaskField(t, projectDir, taskID2, "status")
	if status2 != "open" {
		t.Errorf("expected task 2 status 'open', got %q", status2)
	}

	// Verify output mentions reset
	if !strings.Contains(output, "reset to open") {
		t.Errorf("expected 'reset to open' in output, got:\n%s", output)
	}
}

func TestE2E_RecoverSkipsReviewTasks(t *testing.T) {
	projectDir, worktreeDir := setupRecoverTestWorkspace(t)

	// Create a task, set to review assigned to test-agent
	taskID := createRecoverTask(t, projectDir, "Review task")
	updateTaskStatus(t, projectDir, taskID, "review", "test-agent")

	// Create stale lock with that task
	createStaleLock(t, worktreeDir, "test-agent", taskID)

	output, exitCode := execRecover(t, projectDir, nil, "test-agent", "--force", "--no-analyze")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput:\n%s", exitCode, output)
	}

	// Task should still be in review
	status := getTaskField(t, projectDir, taskID, "status")
	if status != "review" {
		t.Errorf("expected task status 'review' (should not be reset), got %q", status)
	}

	// Output should indicate skipping
	if !strings.Contains(output, "skipping reset") {
		t.Errorf("expected 'skipping reset' in output, got:\n%s", output)
	}
}

func TestE2E_RecoverAnalyzeWithClaude(t *testing.T) {
	projectDir, worktreeDir := setupRecoverTestWorkspace(t)

	// Create a task, set to in_progress assigned to test-agent
	taskID := createRecoverTask(t, projectDir, "Analyze task")
	updateTaskStatus(t, projectDir, taskID, "in_progress", "test-agent")

	// Create stale lock
	createStaleLock(t, worktreeDir, "test-agent", taskID)

	// Use the e2e stubs directory for the claude stub
	stubsDir, err := filepath.Abs("../../e2e/stubs")
	if err != nil {
		t.Fatalf("resolving stubs dir: %v", err)
	}

	// Set STUB_CLAUDE_RESPONSE to return a COMPLETED analysis
	extraEnv := []string{
		"STUB_CLAUDE_RESPONSE=COMPLETED: all tests pass and code merged",
		"PATH=" + stubsDir + ":" + os.Getenv("PATH"),
	}

	output, exitCode := execRecover(t, projectDir, extraEnv, "test-agent", "--force")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput:\n%s", exitCode, output)
	}

	// Should show analysis was triggered
	if !strings.Contains(output, "Analyzing task completion with Claude") {
		t.Errorf("expected 'Analyzing task completion with Claude' in output, got:\n%s", output)
	}

	// Should show task appears COMPLETE
	if !strings.Contains(output, "COMPLETE") {
		t.Errorf("expected 'COMPLETE' in output, got:\n%s", output)
	}

	// Task should be closed
	status := getTaskField(t, projectDir, taskID, "status")
	if status != "closed" {
		t.Errorf("expected task status 'closed' after COMPLETED analysis, got %q", status)
	}
}

func TestE2E_RecoverCleansUntrackedFiles(t *testing.T) {
	projectDir, worktreeDir := setupRecoverTestWorkspace(t)

	// Create an untracked file in the worktree
	junkFile := filepath.Join(worktreeDir, "leftover.txt")
	if err := os.WriteFile(junkFile, []byte("junk content\n"), 0644); err != nil {
		t.Fatalf("write junk file: %v", err)
	}

	// We need a lock file to reach the cleanUntrackedFiles path.
	// (Without a lock, the code returns early after resetOrphanedAgentTasks.)
	createStaleLock(t, worktreeDir, "test-agent", "")

	output, exitCode := execRecover(t, projectDir, nil, "test-agent", "--force", "--no-analyze")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\noutput:\n%s", exitCode, output)
	}

	// Should indicate untracked files were found and cleaned
	if !strings.Contains(output, "Untracked files") {
		t.Errorf("expected 'Untracked files' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Untracked files removed") {
		t.Errorf("expected 'Untracked files removed' in output, got:\n%s", output)
	}

	// Verify the junk file was removed
	if _, err := os.Stat(junkFile); !os.IsNotExist(err) {
		t.Errorf("leftover.txt should have been removed by git clean, but still exists")
	}
}
