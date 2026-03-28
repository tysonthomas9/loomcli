//go:build e2e
// +build e2e

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// setupDaemonTestDir creates a fully isolated test environment with a git repo,
// worktree, and minimal loom.yaml configuration.
func setupDaemonTestDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	// Initialize git repo with an initial commit
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@e2e.local"},
		{"git", "config", "user.name", "E2E Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %v\n%s", args, err, out)
		}
	}

	// Create a worktree for the stub agent
	cmd := exec.Command("git", "worktree", "add", "./worktrees/stub", "-b", "stub")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add failed: %v\n%s", err, out)
	}

	// Write minimal loom.yaml
	loomYAML := `version: 1
backend: claude
daemon:
  restart_policy:
    max_retries: 0
    backoff_initial: 1
    backoff_max: 1
agents:
  - worktree: stub
    role: task
    auto: true
`
	if err := os.WriteFile(filepath.Join(dir, "loom.yaml"), []byte(loomYAML), 0644); err != nil {
		t.Fatalf("writing loom.yaml: %v", err)
	}

	// Create .loom/logs directory
	if err := os.MkdirAll(filepath.Join(dir, ".loom", "logs"), 0755); err != nil {
		t.Fatalf("creating .loom/logs: %v", err)
	}

	return dir
}

// startDaemon launches `loom daemon` as a subprocess in the given directory.
// It returns the stdout buffer, stderr buffer, and a channel that closes when
// cmd.Wait() completes (stdout/stderr are fully drained).
// The process is automatically killed via t.Cleanup.
// A background goroutine reaps the process to prevent zombies (which would
// cause `loom daemon stop` to see the process as still running via kill -0).
func startDaemon(t *testing.T, dir string, extraArgs ...string) (*bytes.Buffer, *bytes.Buffer, <-chan struct{}) {
	t.Helper()

	args := append([]string{"daemon"}, extraArgs...)
	cmd := exec.Command("loom", args...)
	cmd.Dir = dir

	// Set up PATH to include stubs directory
	stubsDir, err := filepath.Abs("../../e2e/stubs")
	if err != nil {
		t.Fatalf("resolving stubs dir: %v", err)
	}
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PATH=%s:%s", stubsDir, os.Getenv("PATH")),
		"STUB_CLAUDE_DELAY=30",
		"STUB_CLAUDE_EXIT_CODE=0",
	)

	// Create a new process group so we can kill the daemon + children
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting daemon: %v", err)
	}

	// Reap the process in the background to prevent zombie state.
	// Without this, `loom daemon stop` polls kill(pid, 0) which succeeds
	// for zombies, causing a 30-second timeout.
	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	t.Cleanup(func() {
		// Kill entire process group if still running
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-waitDone
	})

	return &stdout, &stderr, waitDone
}

// waitForPIDFile polls for the PID file to appear and contain a valid PID.
func waitForPIDFile(t *testing.T, pidFilePath string, timeout time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFilePath)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("PID file %s did not appear within %v", pidFilePath, timeout)
	return 0
}

// runLoomDaemon runs `loom daemon <args>` synchronously and returns output.
func runLoomDaemon(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmdArgs := append([]string{"daemon"}, args...)
	cmd := exec.Command("loom", cmdArgs...)
	cmd.Dir = dir

	// Set up PATH for stubs
	stubsDir, err := filepath.Abs("../../e2e/stubs")
	if err != nil {
		t.Fatalf("resolving stubs dir: %v", err)
	}
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PATH=%s:%s", stubsDir, os.Getenv("PATH")),
		"STUB_CLAUDE_DELAY=30",
		"STUB_CLAUDE_EXIT_CODE=0",
	)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	exitCode = 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("running loom daemon %v: %v", args, runErr)
		}
	}

	return outBuf.String(), errBuf.String(), exitCode
}

// waitForFileRemoved polls until the file no longer exists.
func waitForFileRemoved(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("file %s was not removed within %v", path, timeout)
}

func TestE2E_DaemonDryRun(t *testing.T) {
	loomBinaryPath(t)
	dir := setupDaemonTestDir(t)

	stdout, _, exitCode := runLoomDaemon(t, dir, "--dry-run")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	for _, want := range []string{
		"DRY RUN - No daemon will be started",
		"stub (role: task, auto: true)",
		"PID file:",
		"State file:",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nGot: %s", want, stdout)
		}
	}

	// PID file must NOT be created during dry-run
	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	if _, err := os.Stat(pidFile); err == nil {
		t.Error("PID file should not exist after dry-run")
	}
}

func TestE2E_DaemonStartCreatesPIDFile(t *testing.T) {
	loomBinaryPath(t)
	dir := setupDaemonTestDir(t)

	_, _, _ = startDaemon(t, dir)

	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	pid := waitForPIDFile(t, pidFile, 15*time.Second)

	if pid <= 0 {
		t.Fatalf("expected positive PID, got %d", pid)
	}

	// Verify process is actually running
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("daemon process (PID %d) is not running: %v", pid, err)
	}
}

func TestE2E_DaemonStatusShowsRunning(t *testing.T) {
	loomBinaryPath(t)
	dir := setupDaemonTestDir(t)

	_, _, _ = startDaemon(t, dir)

	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	pid := waitForPIDFile(t, pidFile, 15*time.Second)

	stdout, _, exitCode := runLoomDaemon(t, dir, "status")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "Daemon: running (PID") {
		t.Errorf("stdout missing running status\nGot: %s", stdout)
	}

	pidStr := strconv.Itoa(pid)
	if !strings.Contains(stdout, pidStr) {
		t.Errorf("stdout missing PID %s\nGot: %s", pidStr, stdout)
	}
}

func TestE2E_DaemonConcurrentStartBlocked(t *testing.T) {
	loomBinaryPath(t)
	dir := setupDaemonTestDir(t)

	_, _, _ = startDaemon(t, dir)

	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	waitForPIDFile(t, pidFile, 15*time.Second)

	// Attempt to start a second daemon — should fail
	_, stderr, exitCode := runLoomDaemon(t, dir)

	if exitCode == 0 {
		t.Fatal("expected non-zero exit code for concurrent start")
	}

	if !strings.Contains(stderr, "daemon already running (lock held on") {
		t.Errorf("stderr missing lock error\nGot: %s", stderr)
	}
}

func TestE2E_DaemonStop(t *testing.T) {
	loomBinaryPath(t)
	dir := setupDaemonTestDir(t)

	_, _, _ = startDaemon(t, dir)

	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	waitForPIDFile(t, pidFile, 15*time.Second)

	stdout, _, exitCode := runLoomDaemon(t, dir, "stop")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "Stopping daemon (PID") {
		t.Errorf("stdout missing stopping message\nGot: %s", stdout)
	}

	if !strings.Contains(stdout, "Daemon stopped.") {
		t.Errorf("stdout missing 'Daemon stopped.'\nGot: %s", stdout)
	}

	// Wait for PID file to be removed
	waitForFileRemoved(t, pidFile, 5*time.Second)

	// Verify status now shows not running
	statusOut, _, _ := runLoomDaemon(t, dir, "status")
	if !strings.Contains(statusOut, "Daemon: not running") {
		t.Errorf("expected 'Daemon: not running' after stop\nGot: %s", statusOut)
	}
}

func TestE2E_DaemonSIGTERMCleanShutdown(t *testing.T) {
	loomBinaryPath(t)
	dir := setupDaemonTestDir(t)

	stdout, _, waitDone := startDaemon(t, dir)

	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	pid := waitForPIDFile(t, pidFile, 15*time.Second)

	// Send SIGTERM directly to daemon
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}

	// Wait for cmd.Wait() to complete — this ensures stdout is fully drained
	select {
	case <-waitDone:
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not exit within 15s after SIGTERM")
	}

	// PID file should be cleaned up
	waitForFileRemoved(t, pidFile, 2*time.Second)

	// Verify clean shutdown message in stdout
	if !strings.Contains(stdout.String(), "Daemon stopped.") {
		t.Errorf("stdout missing 'Daemon stopped.' after SIGTERM\nGot: %s", stdout.String())
	}
}

func TestE2E_DaemonStateFileUpdated(t *testing.T) {
	loomBinaryPath(t)
	dir := setupDaemonTestDir(t)

	_, _, _ = startDaemon(t, dir)

	pidFile := filepath.Join(dir, ".loom", "daemon.pid")
	pid := waitForPIDFile(t, pidFile, 15*time.Second)

	// Wait for state file to appear
	stateFile := filepath.Join(dir, ".loom", "daemon-agents.json")
	deadline := time.Now().Add(20 * time.Second)
	var stateData []byte
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(stateFile)
		if err == nil && len(data) > 0 {
			stateData = data
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if stateData == nil {
		t.Fatal("state file did not appear within 20s")
	}

	var state DaemonState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("parsing state file: %v\nContent: %s", err, stateData)
	}

	if state.PID != pid {
		t.Errorf("state PID = %d, want %d", state.PID, pid)
	}

	if state.StartedAt.IsZero() {
		t.Error("state StartedAt is zero")
	}

	if len(state.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(state.Agents))
	}

	agent := state.Agents[0]
	if agent.Worktree != "stub" {
		t.Errorf("agent worktree = %q, want %q", agent.Worktree, "stub")
	}
	if agent.Role != "task" {
		t.Errorf("agent role = %q, want %q", agent.Role, "task")
	}
}
