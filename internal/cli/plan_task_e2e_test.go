//go:build e2e
// +build e2e

package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Workspace helpers ---

// setupPlanTaskWorkspace creates a temp directory with git init + bd init.
func setupPlanTaskWorkspace(t *testing.T) string {
	t.Helper()

	dir := initTempGitRepo(t) // reuses helper from doctor_e2e_test.go

	// Check bd is available
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd binary not found on PATH")
	}

	// bd init
	cmd := exec.Command("bd", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd init failed: %v\n%s", err, out)
	}

	return dir
}

// execLoom executes the loom binary with args in workDir.
// Returns stdout, stderr, and exit code.
func execLoom(t *testing.T, workDir string, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	loom := loomBinaryPath(t) // reuses helper from doctor_e2e_test.go

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, loom, args...)
	cmd.Dir = workDir

	// Isolated environment
	env := []string{
		"HOME=" + workDir,
		"PATH=" + filepath.Dir(loom) + ":" + os.Getenv("PATH"),
		"LOOM_CONFIG_DIR=" + filepath.Join(workDir, ".loom-config"),
		"LOOM_BACKEND=claude",
		"GIT_CONFIG_NOSYSTEM=1",
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	env = append(env, extraEnv...)
	cmd.Env = env

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// execLoomWithStubClaude runs loom with a stub claude binary on PATH.
func execLoomWithStubClaude(t *testing.T, workDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	// Create stub claude in a separate bin dir
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	stubPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(stubPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("create stub claude: %v", err)
	}

	loom := loomBinaryPath(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, loom, args...)
	cmd.Dir = workDir

	// Stub claude goes first in PATH
	cmd.Env = []string{
		"HOME=" + workDir,
		"PATH=" + binDir + ":" + filepath.Dir(loom) + ":" + os.Getenv("PATH"),
		"LOOM_CONFIG_DIR=" + filepath.Join(workDir, ".loom-config"),
		"LOOM_BACKEND=claude",
		"GIT_CONFIG_NOSYSTEM=1",
		"GOPATH=" + os.Getenv("GOPATH"),
	}

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// createBdTask creates a bd task and returns its ID.
func createBdTask(t *testing.T, dir, title string, opts ...string) string {
	t.Helper()
	args := []string{"create", title}
	args = append(args, opts...)

	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd create %q failed: %v\n%s", title, err, out)
	}

	// bd create output format: "Created issue: <id>" or similar
	outStr := string(out)
	for _, line := range strings.Split(outStr, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Created") {
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				return fields[len(fields)-1]
			}
		}
	}
	// Fallback: last non-empty line
	lines := strings.Split(strings.TrimSpace(outStr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			fields := strings.Fields(l)
			return fields[len(fields)-1]
		}
	}
	t.Fatalf("could not parse task ID from bd create output: %s", outStr)
	return ""
}

// =============================================
// Plan command tests
// =============================================

func TestE2E_PlanHelp(t *testing.T) {
	dir := t.TempDir()

	stdout, _, exitCode := execLoom(t, dir, nil, "plan", "--help")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	stdoutLower := strings.ToLower(stdout)
	if !strings.Contains(stdoutLower, "planning") {
		t.Errorf("help output should mention 'planning', got:\n%s", stdout)
	}
	for _, flag := range []string{"--auto", "--interval", "--max-tasks", "--idle-timeout", "--parent"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("help output should mention %q", flag)
		}
	}
}

func TestE2E_PlanNoTasks(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	stdout, _, exitCode := execLoom(t, dir, nil, "plan")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning', got:\n%s", stdout)
	}

	// No lock file should be left behind
	lockPath := filepath.Join(dir, LockFileName)
	if _, err := os.Stat(lockPath); err == nil {
		t.Error("lock file should not exist after no-task exit")
	}
}

func TestE2E_PlanSkipsTasksWithDesign(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create a task WITH a design field — should NOT be picked for planning
	createBdTask(t, dir, "Task with design", "--design", "Already planned")

	stdout, _, exitCode := execLoom(t, dir, nil, "plan")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available for planning") {
		t.Errorf("should report no tasks for planning when all have designs, got:\n%s", stdout)
	}
}

func TestE2E_PlanSkipsEpics(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create an epic (no design) — should NOT be picked for planning
	createBdTask(t, dir, "An epic", "--type", "epic")

	stdout, _, exitCode := execLoom(t, dir, nil, "plan")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available for planning") {
		t.Errorf("should report no tasks when only epics exist, got:\n%s", stdout)
	}
}

func TestE2E_PlanPicksTaskWithoutDesign(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create a task WITHOUT design — should be picked for planning
	createBdTask(t, dir, "Task needs planning")

	stdout, _, _ := execLoomWithStubClaude(t, dir, "plan")

	if !strings.Contains(stdout, "Running PLANNING agent") {
		t.Errorf("expected 'Running PLANNING agent' banner, got:\n%s", stdout)
	}

	// Lock file should be cleaned up after single-task mode
	lockPath := filepath.Join(dir, LockFileName)
	if _, err := os.Stat(lockPath); err == nil {
		t.Error("lock file should be cleaned up after single-task exit")
	}
}

func TestE2E_PlanPicksNeedsRevisionTask(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create a task WITH design AND needs-revision label — should be re-planned
	createBdTask(t, dir, "Task needs revision", "--design", "Old plan", "--labels", "needs-revision")

	stdout, _, _ := execLoomWithStubClaude(t, dir, "plan")

	if !strings.Contains(stdout, "Running PLANNING agent") {
		t.Errorf("expected needs-revision task to be picked for planning, got:\n%s", stdout)
	}
}

// =============================================
// Task command tests
// =============================================

func TestE2E_TaskHelp(t *testing.T) {
	dir := t.TempDir()

	stdout, _, exitCode := execLoom(t, dir, nil, "task", "--help")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	stdoutLower := strings.ToLower(stdout)
	if !strings.Contains(stdoutLower, "implementation") {
		t.Errorf("help output should mention 'implementation', got:\n%s", stdout)
	}
	for _, flag := range []string{"--auto", "--interval", "--max-tasks", "--idle-timeout", "--parent"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("help output should mention %q", flag)
		}
	}
}

func TestE2E_TaskNoTasks(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	stdout, _, exitCode := execLoom(t, dir, nil, "task")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available for implementation") {
		t.Errorf("expected 'No tasks available for implementation', got:\n%s", stdout)
	}
}

func TestE2E_TaskSkipsTasksWithoutDesign(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create a task WITHOUT design — should NOT be picked for implementation
	createBdTask(t, dir, "Unplanned task")

	stdout, _, exitCode := execLoom(t, dir, nil, "task")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available for implementation") {
		t.Errorf("should report no tasks when none have designs, got:\n%s", stdout)
	}
}

func TestE2E_TaskSkipsNeedsRevision(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create a task WITH design AND needs-revision — should NOT be picked for implementation
	createBdTask(t, dir, "Revision needed", "--design", "Some plan", "--labels", "needs-revision")

	stdout, _, exitCode := execLoom(t, dir, nil, "task")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available for implementation") {
		t.Errorf("needs-revision tasks should not be picked for implementation, got:\n%s", stdout)
	}
}

func TestE2E_TaskSkipsEpics(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create an epic with design — should NOT be picked for implementation
	createBdTask(t, dir, "Epic with design", "--type", "epic", "--design", "Epic plan")

	stdout, _, exitCode := execLoom(t, dir, nil, "task")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available for implementation") {
		t.Errorf("epics should not be picked for implementation, got:\n%s", stdout)
	}
}

func TestE2E_TaskPicksReadyTask(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create a task WITH design and no needs-revision — should be picked
	createBdTask(t, dir, "Ready for implementation", "--design", "Approved plan")

	stdout, _, _ := execLoomWithStubClaude(t, dir, "task")

	if !strings.Contains(stdout, "Running IMPLEMENTATION agent") {
		t.Errorf("expected 'Running IMPLEMENTATION agent' banner, got:\n%s", stdout)
	}

	// Lock file should be cleaned up after single-task mode
	lockPath := filepath.Join(dir, LockFileName)
	if _, err := os.Stat(lockPath); err == nil {
		t.Error("lock file should be cleaned up after single-task exit")
	}
}

// =============================================
// Claim command tests
// =============================================

func TestE2E_ClaimUpdatesLockFile(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Create a task to claim
	taskID := createBdTask(t, dir, "Task to claim", "--design", "Some design")

	// Create a lock file (claim requires an existing lock)
	lockPath := filepath.Join(dir, LockFileName)
	lockInfo := LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "test-agent",
		StartedAt: time.Now(),
	}
	lockData, _ := json.MarshalIndent(lockInfo, "", "  ")
	if err := os.WriteFile(lockPath, lockData, 0600); err != nil {
		t.Fatalf("create lock file: %v", err)
	}
	t.Cleanup(func() { os.Remove(lockPath) })

	stdout, stderr, exitCode := execLoom(t, dir, nil, "claim", taskID)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "Claimed task") {
		t.Errorf("expected 'Claimed task' in output, got:\n%s", stdout)
	}

	// Verify lock file was updated with task info
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	var updatedLock LockInfo
	if err := json.Unmarshal(data, &updatedLock); err != nil {
		t.Fatalf("parse lock file: %v", err)
	}
	if updatedLock.TaskID != taskID {
		t.Errorf("lock task_id = %q, want %q", updatedLock.TaskID, taskID)
	}
	if updatedLock.TaskTitle == "" {
		t.Error("lock task_title should be non-empty")
	}
}

// =============================================
// Complete command tests
// =============================================

func TestE2E_CompleteCreatesSignalFile(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Resolve the signal file path the same way loom complete does
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		resolved = absDir
	}
	expectedSignal := GetSignalFilePath(resolved)

	// Clean up signal file after test
	t.Cleanup(func() {
		os.Remove(expectedSignal)
		os.Remove(filepath.Dir(expectedSignal))
	})

	extraEnv := []string{"LOOM_WORKTREE_PATH=" + dir}
	stdout, stderr, exitCode := execLoom(t, dir, extraEnv, "complete")

	if exitCode != 0 {
		t.Fatalf("loom complete failed with exit code %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	if !strings.Contains(stdout, "Task completion signaled") {
		t.Errorf("expected 'Task completion signaled', got:\n%s", stdout)
	}

	// Verify signal file was created
	if _, err := os.Stat(expectedSignal); err != nil {
		t.Errorf("signal file should exist at %s: %v", expectedSignal, err)
	}
}

// =============================================
// Flag parsing tests
// =============================================

func TestE2E_PlanParentFlag(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Run with --parent flag and no matching tasks
	stdout, _, exitCode := execLoom(t, dir, nil, "plan", "--parent", "epic-123")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No tasks available") {
		t.Errorf("expected 'No tasks available' with --parent filter, got:\n%s", stdout)
	}
}

func TestE2E_TaskIntervalFlag(t *testing.T) {
	dir := setupPlanTaskWorkspace(t)

	// Run with --interval flag in single-task mode (no --auto)
	stdout, _, exitCode := execLoom(t, dir, nil, "task", "--interval", "10")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	// Should still report no tasks — interval doesn't affect single-task behavior
	if !strings.Contains(stdout, "No tasks available") {
		t.Errorf("expected 'No tasks available', got:\n%s", stdout)
	}
}
