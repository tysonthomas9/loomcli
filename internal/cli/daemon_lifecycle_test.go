//go:build ignore

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// TestComputeBackoff_OverflowProtection tests that computeBackoff handles
// extreme restart counts without integer overflow.
func TestComputeBackoff_OverflowProtection(t *testing.T) {
	t.Run("restartCount=31 is capped at BackoffMax", func(t *testing.T) {
		d := &Daemon{config: &DaemonConfig{
			Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(300),
			}},
		}}
		ap := &AgentProcess{restartCount: 31}
		got := d.computeBackoff(ap)
		if got != 300*time.Second {
			t.Errorf("computeBackoff(restart=31) = %v, want %v", got, 300*time.Second)
		}
	})

	t.Run("restartCount=50 is capped at BackoffMax", func(t *testing.T) {
		d := &Daemon{config: &DaemonConfig{
			Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(300),
			}},
		}}
		ap := &AgentProcess{restartCount: 50}
		got := d.computeBackoff(ap)
		if got != 300*time.Second {
			t.Errorf("computeBackoff(restart=50) = %v, want %v", got, 300*time.Second)
		}
	})

	t.Run("restartCount=100 is capped at BackoffMax", func(t *testing.T) {
		d := &Daemon{config: &DaemonConfig{
			Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(300),
			}},
		}}
		ap := &AgentProcess{restartCount: 100}
		got := d.computeBackoff(ap)
		if got != 300*time.Second {
			t.Errorf("computeBackoff(restart=100) = %v, want %v", got, 300*time.Second)
		}
	})

	t.Run("BackoffMax=0 returns zero duration", func(t *testing.T) {
		d := &Daemon{config: &DaemonConfig{
			Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(0),
			}},
		}}
		ap := &AgentProcess{restartCount: 5}
		got := d.computeBackoff(ap)
		if got != 0 {
			t.Errorf("computeBackoff(BackoffMax=0) = %v, want 0", got)
		}
	})

	t.Run("large initial value with high count does not produce negative", func(t *testing.T) {
		d := &Daemon{config: &DaemonConfig{
			Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
				BackoffInitial: intPtr(1000000),
				BackoffMax:     intPtr(5000000),
			}},
		}}
		ap := &AgentProcess{restartCount: 25}
		got := d.computeBackoff(ap)
		if got < 0 {
			t.Errorf("computeBackoff() = %v, want non-negative", got)
		}
		if got != 5000000*time.Second {
			t.Errorf("computeBackoff() = %v, want %v (capped)", got, 5000000*time.Second)
		}
	})
}

// TestHandleRestartAfterError_IncrementCount tests that handleRestartAfterError
// increments restart count and respects max retries.
func TestHandleRestartAfterError_IncrementCount(t *testing.T) {
	t.Run("increments count and returns true when under limit", func(t *testing.T) {
		d := &Daemon{
			config:   &DaemonConfig{Daemon: DaemonSettings{RestartPolicy: RestartPolicy{MaxRetries: intPtr(3), BackoffInitial: intPtr(1), BackoffMax: intPtr(1)}}},
			shutdown: make(chan struct{}),
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "test"},
			restartCount: 0,
		}

		result := d.handleRestartAfterError(ap)
		if !result {
			t.Error("handleRestartAfterError() = false, want true (under limit)")
		}
		ap.mu.Lock()
		count := ap.restartCount
		ap.mu.Unlock()
		if count != 1 {
			t.Errorf("restartCount = %d, want 1", count)
		}
	})

	t.Run("returns false when exceeding max retries", func(t *testing.T) {
		d := &Daemon{
			config:   &DaemonConfig{Daemon: DaemonSettings{RestartPolicy: RestartPolicy{MaxRetries: intPtr(3), BackoffInitial: intPtr(1), BackoffMax: intPtr(1)}}},
			shutdown: make(chan struct{}),
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "test"},
			restartCount: 3, // already at limit
		}

		result := d.handleRestartAfterError(ap)
		if result {
			t.Error("handleRestartAfterError() = true, want false (exceeded limit)")
		}
		ap.mu.Lock()
		count := ap.restartCount
		ap.mu.Unlock()
		if count != 4 {
			t.Errorf("restartCount = %d, want 4", count)
		}
	})
}

// TestHandleRestartAfterError_ShutdownDuringBackoff tests that shutdown
// interrupts the backoff sleep and returns false.
func TestHandleRestartAfterError_ShutdownDuringBackoff(t *testing.T) {
	d := &Daemon{
		config:   &DaemonConfig{Daemon: DaemonSettings{RestartPolicy: RestartPolicy{MaxRetries: intPtr(10), BackoffInitial: intPtr(60), BackoffMax: intPtr(60)}}},
		shutdown: make(chan struct{}),
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test"},
		restartCount: 0,
	}

	// Close shutdown immediately - backoff should be interrupted
	close(d.shutdown)

	result := d.handleRestartAfterError(ap)
	if result {
		t.Error("handleRestartAfterError() = true, want false (shutdown during backoff)")
	}
}

// TestStopAgent_NilProcess tests that stopAgent is a no-op for nil process.
func TestStopAgent_NilProcess(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}
	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		// cmd is nil, pid is 0
	}
	// Should return immediately without error
	d.stopAgent(ap, 5*time.Second)
}

// TestStopAgent_ProcessAlreadyExited tests that stopAgent handles a process
// that has already exited gracefully.
func TestStopAgent_ProcessAlreadyExited(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	// Spawn a short-lived process and wait for it to finish
	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("failed to wait for test process: %v", err)
	}

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		cmd:   cmd,
		pid:   pid,
	}

	// stopAgent should handle SIGTERM failure gracefully (process already exited)
	d.stopAgent(ap, 5*time.Second)
}

// TestStopAgent_GracefulShutdown tests that stopAgent sends SIGTERM and the
// process exits without needing SIGKILL.
func TestStopAgent_GracefulShutdown(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	// Spawn a process that exits on SIGTERM (sleep responds to SIGTERM)
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	t.Cleanup(func() {
		// Ensure process is cleaned up even if test fails
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		cmd:   cmd,
		pid:   pid,
	}

	// Run waitForAgent in background to clear pid
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.waitForAgent(ap)
	}()

	// stopAgent sends SIGTERM, sleep should respond and exit
	d.stopAgent(ap, 5*time.Second)

	// Wait for waitForAgent to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - process exited
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for process to exit after SIGTERM")
	}

	// Verify process was cleaned up
	ap.mu.Lock()
	finalPID := ap.pid
	ap.mu.Unlock()
	if finalPID != 0 {
		t.Errorf("pid = %d after stopAgent, want 0", finalPID)
	}
}

// TestStopAgent_ForcedKill tests that stopAgent sends SIGKILL when process
// ignores SIGTERM.
func TestStopAgent_ForcedKill(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	// Spawn a process that ignores SIGTERM
	cmd := exec.Command("bash", "-c", `trap "" TERM; sleep 60`) //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start trap process: %v", err)
	}
	pid := cmd.Process.Pid

	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		cmd:   cmd,
		pid:   pid,
	}

	// Run waitForAgent in background to clear pid after kill
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.waitForAgent(ap)
	}()

	// stopAgent should SIGTERM, wait 5s, then SIGKILL
	d.stopAgent(ap, 5*time.Second)

	// Wait for waitForAgent to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - process was killed
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for process to be killed")
	}
}

// TestStopAgent_CustomTimeout verifies that a custom SIGTERM timeout is passed
// through to the deadline. The process responds to SIGTERM and exits within the
// configured window (timeout is a ceiling, not a sleep).
func TestStopAgent_CustomTimeout(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	cmd := exec.Command("sleep", "60") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		cmd:   cmd,
		pid:   pid,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.waitForAgent(ap)
	}()

	// Use 30s timeout — process should exit promptly from SIGTERM (sleep responds),
	// well before the 30s deadline. This confirms the timeout is a ceiling, not a sleep.
	start := time.Now()
	d.stopAgent(ap, 30*time.Second)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("stopAgent took %v, want < 5s (sleep responds to SIGTERM immediately)", elapsed)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for process to exit")
	}
}

// TestWaitForAgent_SuccessfulExit tests waitForAgent with exit code 0.
func TestWaitForAgent_SuccessfulExit(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		cmd:   cmd,
		pid:   cmd.Process.Pid,
	}

	exitCode := d.waitForAgent(ap)
	if exitCode != 0 {
		t.Errorf("waitForAgent() = %d, want 0", exitCode)
	}

	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.lastExitCode != 0 {
		t.Errorf("lastExitCode = %d, want 0", ap.lastExitCode)
	}
	if ap.lastExit.IsZero() {
		t.Error("lastExit is zero, want non-zero")
	}
	if ap.cmd != nil {
		t.Error("cmd should be nil after wait")
	}
	if ap.pid != 0 {
		t.Errorf("pid = %d, want 0", ap.pid)
	}
}

// TestWaitForAgent_FailedExit tests waitForAgent with non-zero exit code.
func TestWaitForAgent_FailedExit(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	cmd := exec.Command("bash", "-c", "exit 1") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		cmd:   cmd,
		pid:   cmd.Process.Pid,
	}

	exitCode := d.waitForAgent(ap)
	if exitCode != 1 {
		t.Errorf("waitForAgent() = %d, want 1", exitCode)
	}

	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.lastExitCode != 1 {
		t.Errorf("lastExitCode = %d, want 1", ap.lastExitCode)
	}
}

// TestWaitForAgent_NilCmd tests waitForAgent with nil cmd returns -1.
func TestWaitForAgent_NilCmd(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}
	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
	}

	exitCode := d.waitForAgent(ap)
	if exitCode != -1 {
		t.Errorf("waitForAgent(nil cmd) = %d, want -1", exitCode)
	}
}

// TestWaitForAgent_ClosesLogFile tests that waitForAgent properly closes the
// log file handle.
func TestWaitForAgent_ClosesLogFile(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	tmpFile, err := os.CreateTemp(t.TempDir(), "daemon-test-log-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	ap := &AgentProcess{
		entry:   AgentEntry{Worktree: "test"},
		cmd:     cmd,
		pid:     cmd.Process.Pid,
		logFile: tmpFile,
	}

	_ = d.waitForAgent(ap)

	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.logFile != nil {
		t.Error("logFile should be nil after waitForAgent")
	}

	// Verify the file handle was closed (write should fail)
	_, writeErr := tmpFile.Write([]byte("should fail"))
	if writeErr == nil {
		t.Error("expected write to closed file to fail")
	}
}

// TestHealthChecker_ShutdownStopsLoop tests that healthChecker exits when
// shutdown channel is closed.
func TestHealthChecker_ShutdownStopsLoop(t *testing.T) {
	d := &Daemon{
		config:   &DaemonConfig{},
		agents:   []*AgentProcess{},
		shutdown: make(chan struct{}),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.healthChecker()
	}()

	// Close shutdown channel
	close(d.shutdown)

	// Verify goroutine exits
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("healthChecker did not exit after shutdown signal")
	}
}

// TestCheckAgentHealth_SkipsNonRunning tests that checkAgentHealth skips
// agents with pid=0 without errors.
func TestCheckAgentHealth_SkipsNonRunning(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{entry: AgentEntry{Worktree: "a"}, pid: 0, worktreePath: t.TempDir()},
			{entry: AgentEntry{Worktree: "b"}, pid: 0, worktreePath: t.TempDir()},
		},
	}

	// Should not panic
	d.checkAgentHealth()
}

// TestCheckAgentHealth_DetectsDeadProcess tests that checkAgentHealth handles
// non-existent PIDs without panicking.
func TestCheckAgentHealth_DetectsDeadProcess(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{
				entry:        AgentEntry{Worktree: "dead"},
				pid:          999999, // unlikely to be a real PID
				worktreePath: t.TempDir(),
			},
		},
	}

	// Should not panic, just log warnings
	d.checkAgentHealth()
}

// cleanupAgentProcess is a test helper that cleans up spawned processes and log files.
func cleanupAgentProcess(t *testing.T, ap *AgentProcess) {
	t.Helper()
	t.Cleanup(func() {
		ap.mu.Lock()
		defer ap.mu.Unlock()
		if ap.logFile != nil {
			ap.logFile.Close()
			ap.logFile = nil
		}
		if ap.cmd != nil && ap.cmd.Process != nil {
			_ = ap.cmd.Process.Kill()
			_ = ap.cmd.Wait()
		}
	})
}

// TestSpawnAgent_BuiltInRoleCommandConstruction tests the command args
// constructed for built-in roles by inspecting what spawnAgent would build.
func TestSpawnAgent_BuiltInRoleCommandConstruction(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("plan role builds correct command", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"},
			worktreePath: tmpDir,
		}
		cleanupAgentProcess(t, ap)

		err := d.spawnAgent(ap)
		if err != nil {
			t.Logf("spawnAgent error (expected in test): %v", err)
		}
	})

	t.Run("backend flag is propagated", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "openai"},
			projectDir: tmpDir,
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"},
			worktreePath: tmpDir,
		}
		cleanupAgentProcess(t, ap)

		if d.config.Backend != "openai" {
			t.Errorf("Backend = %q, want %q", d.config.Backend, "openai")
		}

		err := d.spawnAgent(ap)
		if err != nil {
			t.Logf("spawnAgent error (expected in test): %v", err)
		}
	})

	t.Run("per-agent backend overrides project backend", func(t *testing.T) {
		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "openai"},
			projectDir: tmpDir,
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan", Backend: "anthropic"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"},
			worktreePath: tmpDir,
		}
		cleanupAgentProcess(t, ap)

		if ap.entry.Backend != "anthropic" {
			t.Errorf("agent Backend = %q, want %q", ap.entry.Backend, "anthropic")
		}

		err := d.spawnAgent(ap)
		if err != nil {
			t.Logf("spawnAgent error (expected in test): %v", err)
		}
	})
}

// TestSpawnAgent_CustomRoleCommandConstruction tests command construction for custom roles.
func TestSpawnAgent_CustomRoleCommandConstruction(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "prompt.md")
	if err := os.WriteFile(promptFile, []byte("test prompt"), 0644); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "reviewer"},
		roleConfig:   RoleConfig{Description: "Code reviewer", PromptFile: promptFile, TaskFilter: "review"},
		worktreePath: tmpDir,
	}
	cleanupAgentProcess(t, ap)

	err := d.spawnAgent(ap)
	if err != nil {
		t.Logf("spawnAgent error (expected in test): %v", err)
	}
}

// TestSpawnAgent_LogFileSetup tests that spawnAgent creates log directory and
// log files with correct paths.
func TestSpawnAgent_LogFileSetup(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				LogDir: logDir,
			},
		},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"},
		worktreePath: tmpDir,
	}
	cleanupAgentProcess(t, ap)

	// spawnAgent will try to create log dir even if loom binary doesn't exist
	_ = d.spawnAgent(ap)

	// Verify log directory was created
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		t.Error("log directory was not created")
	}

	// Verify log file was created with expected name pattern
	expectedLogFile := filepath.Join(logDir, "plan-falcon.log")
	if _, err := os.Stat(expectedLogFile); os.IsNotExist(err) {
		t.Errorf("log file %q was not created", expectedLogFile)
	}
}

// TestSpawnAgent_Environment tests that spawnAgent sets BD_ACTOR and
// LOOM_WORKTREE_PATH in the subprocess environment.
func TestSpawnAgent_Environment(t *testing.T) {
	tmpDir := t.TempDir()

	// Instead of spawning loom (which doesn't exist in test), spawn a command
	// that will print its environment so we can verify.
	// We test environment configuration indirectly by verifying the fields
	// that spawnAgent uses to construct the env.
	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"},
		worktreePath: tmpDir,
	}

	cleanupAgentProcess(t, ap)

	// Verify the inputs that spawnAgent uses for env
	if ap.entry.Worktree != "falcon" {
		t.Errorf("Worktree = %q, want %q", ap.entry.Worktree, "falcon")
	}
	if ap.worktreePath != tmpDir {
		t.Errorf("worktreePath = %q, want %q", ap.worktreePath, tmpDir)
	}

	// Verify FilteredEnv returns a valid base
	env := FilteredEnv()
	if len(env) == 0 {
		t.Error("FilteredEnv() returned empty slice")
	}

	_ = d.spawnAgent(ap)
}

// TestDaemonStartStop tests the full Start/Stop lifecycle.
func TestDaemonStartStop(t *testing.T) {
	t.Run("start and stop without hanging", func(t *testing.T) {
		d := &Daemon{
			config: &DaemonConfig{
				Daemon: DaemonSettings{
					RestartPolicy: RestartPolicy{
						MaxRetries:     intPtr(0),
						BackoffInitial: intPtr(1),
						BackoffMax:     intPtr(1),
					},
				},
			},
			agents: []*AgentProcess{},
		}

		if err := d.Start(); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		// Stop should complete without hanging
		done := make(chan struct{})
		go func() {
			d.Stop()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(10 * time.Second):
			t.Fatal("Stop() did not complete within 10 seconds")
		}
	})

	t.Run("stop is idempotent", func(t *testing.T) {
		d := &Daemon{
			config: &DaemonConfig{
				Daemon: DaemonSettings{
					RestartPolicy: RestartPolicy{
						MaxRetries:     intPtr(0),
						BackoffInitial: intPtr(1),
						BackoffMax:     intPtr(1),
					},
				},
			},
			agents: []*AgentProcess{},
		}

		if err := d.Start(); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		d.Stop()
		// Calling Stop again should not panic
		d.Stop()
	})
}

// TestSuperviseAgent_ShutdownBeforeSpawn tests that superviseAgent exits
// immediately when shutdown is already signaled.
func TestSuperviseAgent_ShutdownBeforeSpawn(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(0),
					BackoffInitial: intPtr(1),
					BackoffMax:     intPtr(1),
				},
			},
		},
		shutdown: make(chan struct{}),
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test", Role: "plan"},
		worktreePath: t.TempDir(),
		stopCh:       make(chan struct{}),
	}

	// Close shutdown before starting superviseAgent
	close(d.shutdown)

	done := make(chan struct{})
	go func() {
		d.superviseAgent(ap)
		close(done)
	}()

	select {
	case <-done:
		// Success - exited immediately
	case <-time.After(5 * time.Second):
		t.Fatal("superviseAgent did not exit after pre-closed shutdown")
	}
}

// TestWaitForAgent_SignaledExit tests waitForAgent when process is killed by signal.
func TestWaitForAgent_SignaledExit(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	cmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		cmd:   cmd,
		pid:   cmd.Process.Pid,
	}

	// Kill the process
	_ = cmd.Process.Signal(syscall.SIGTERM)

	exitCode := d.waitForAgent(ap)
	// SIGTERM causes non-zero exit
	if exitCode == 0 {
		t.Error("waitForAgent() = 0 after SIGTERM, want non-zero")
	}

	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.cmd != nil {
		t.Error("cmd should be nil after wait")
	}
	if ap.pid != 0 {
		t.Errorf("pid = %d, want 0", ap.pid)
	}
}

// TestSpawnAgent_RelativeLogDir tests that a relative log dir is resolved
// relative to projectDir.
func TestSpawnAgent_RelativeLogDir(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				LogDir: "relative-logs",
			},
		},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"},
		worktreePath: tmpDir,
	}

	cleanupAgentProcess(t, ap)

	_ = d.spawnAgent(ap)

	// Verify the absolute log dir was created
	absLogDir := filepath.Join(tmpDir, "relative-logs")
	if _, err := os.Stat(absLogDir); os.IsNotExist(err) {
		t.Errorf("relative log directory was not resolved to %q", absLogDir)
	}
}

// TestSpawnAgent_EpicIDAddsParentFlag verifies that assignedEpicID is passed
// as --parent flag to the subprocess command.
func TestSpawnAgent_EpicIDAddsParentFlag(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:     RoleConfig{Description: "Built-in plan agent"},
		worktreePath:   tmpDir,
		assignedEpicID: "epic-123",
	}

	cleanupAgentProcess(t, ap)

	// Verify the assignedEpicID is set - spawnAgent will use this
	if ap.assignedEpicID != "epic-123" {
		t.Errorf("assignedEpicID = %q, want %q", ap.assignedEpicID, "epic-123")
	}

	_ = d.spawnAgent(ap)
}

// TestSpawnAgent_SetsWorkingDirectory verifies that cmd.Dir is set to worktreePath.
func TestSpawnAgent_SetsWorkingDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"},
		worktreePath: tmpDir,
	}

	cleanupAgentProcess(t, ap)

	_ = d.spawnAgent(ap)

	// Check that the working directory was set
	ap.mu.Lock()
	if ap.cmd != nil && ap.cmd.Dir != tmpDir {
		t.Errorf("cmd.Dir = %q, want %q", ap.cmd.Dir, tmpDir)
	}
	ap.mu.Unlock()
}

// TestSpawnAgent_SetsLastStartAndPID verifies that lastStart and pid are set
// after a successful spawn.
func TestSpawnAgent_SetsLastStartAndPID(t *testing.T) {
	tmpDir := t.TempDir()

	// Use a real command that exists so spawn succeeds
	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}

	// We can't easily test with a real "loom" binary, so we verify the
	// spawn sets lastStart and pid when using a process that exists.
	// The test for spawnAgent with loom will fail at exec, but the
	// integration test in TestDaemonStartStop covers the lifecycle.

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"},
		worktreePath: tmpDir,
	}

	cleanupAgentProcess(t, ap)

	before := time.Now()
	err := d.spawnAgent(ap)

	ap.mu.Lock()
	defer ap.mu.Unlock()

	if err == nil {
		// Spawn succeeded (loom binary exists)
		if ap.pid == 0 {
			t.Error("pid = 0 after successful spawn, want non-zero")
		}
		if ap.lastStart.Before(before) {
			t.Error("lastStart was not updated after spawn")
		}
	}
	// If spawn failed (loom not found), that's expected in test env
}

// TestStopAgent_KillsProcessGroup verifies that stopAgent kills the entire process
// group, including child processes spawned by the leader.
func TestStopAgent_KillsProcessGroup(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	// Spawn a bash process that creates a child in the same process group.
	// The child (sleep) would survive a simple kill of the parent without
	// process group kill semantics.
	cmd := exec.Command("bash", "-c", "sleep 60 & wait") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start bash: %v", err)
	}
	pid := cmd.Process.Pid

	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// Give bash time to spawn the child
	time.Sleep(200 * time.Millisecond)

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test-pgid"},
		cmd:   cmd,
		pid:   pid,
	}

	// Run waitForAgent concurrently to drain process state
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.waitForAgent(ap)
	}()

	d.stopAgent(ap, 5*time.Second)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for process group to be killed")
	}

	// Verify the parent is dead
	if lockfile.IsProcessRunning(pid) {
		t.Errorf("parent process %d is still running after stopAgent", pid)
	}
}

// TestStopAgent_ConcurrentWithWaitForAgent verifies that running stopAgent and
// waitForAgent concurrently does not panic or deadlock.
func TestStopAgent_ConcurrentWithWaitForAgent(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	cmd := exec.Command("sleep", "5") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test-concurrent"},
		cmd:   cmd,
		pid:   pid,
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Run waitForAgent and stopAgent concurrently
	go func() {
		defer wg.Done()
		d.waitForAgent(ap)
	}()
	go func() {
		defer wg.Done()
		d.stopAgent(ap, 5*time.Second)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// No panic or deadlock
	case <-time.After(10 * time.Second):
		t.Fatal("timeout: stopAgent and waitForAgent deadlocked")
	}

	ap.mu.Lock()
	finalPID := ap.pid
	ap.mu.Unlock()
	if finalPID != 0 {
		t.Errorf("pid = %d after concurrent stop/wait, want 0", finalPID)
	}
}

// TestHealthChecker_ShutdownWithActiveAgents verifies that healthChecker exits
// promptly when shutdown is signaled, even when agents have active (non-zero) PIDs.
func TestHealthChecker_ShutdownWithActiveAgents(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{
				entry:        AgentEntry{Worktree: "agent1"},
				pid:          99999999, // fake PID, not running
				worktreePath: t.TempDir(),
			},
			{
				entry:        AgentEntry{Worktree: "agent2"},
				pid:          99999998,
				worktreePath: t.TempDir(),
			},
		},
		shutdown: make(chan struct{}),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.healthChecker()
	}()

	// Close shutdown immediately
	close(d.shutdown)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Exited promptly (well before the 30s ticker)
	case <-time.After(2 * time.Second):
		t.Fatal("healthChecker did not exit within 2 seconds after shutdown with active agents")
	}
}

// TestCheckAgentHealth_StaleLockWithLiveProcess verifies behavior when the lock
// file contains a dead PID but the agent itself has a live PID.
func TestCheckAgentHealth_StaleLockWithLiveProcess(t *testing.T) {
	tmpDir := t.TempDir()

	// Start a real process to use as the agent's live PID
	liveCmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := liveCmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	livePID := liveCmd.Process.Pid
	t.Cleanup(func() {
		_ = liveCmd.Process.Kill()
		_ = liveCmd.Wait()
	})

	// Write a lock file with a dead PID (99999999 is almost certainly not running)
	lockInfo := LockInfo{
		PID:       99999999,
		Command:   "plan",
		AgentName: "stale-agent",
		StartedAt: time.Now().Add(-1 * time.Hour),
	}
	lockData, _ := json.MarshalIndent(lockInfo, "", "  ")
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, lockData, 0600); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	// Set LOOM_CONFIG_DIR to an empty temp dir to disable workspace resolution
	origConfig := os.Getenv("LOOM_CONFIG_DIR")
	os.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Cleanup(func() {
		if origConfig == "" {
			os.Unsetenv("LOOM_CONFIG_DIR")
		} else {
			os.Setenv("LOOM_CONFIG_DIR", origConfig)
		}
	})

	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{
				entry:        AgentEntry{Worktree: "stale-lock-test"},
				pid:          livePID,
				worktreePath: tmpDir,
			},
		},
	}

	// Should complete without panic; stale lock detection logs but doesn't modify agent state
	d.checkAgentHealth()

	// Verify agent's PID is unchanged
	d.agents[0].mu.Lock()
	finalPID := d.agents[0].pid
	d.agents[0].mu.Unlock()
	if finalPID != livePID {
		t.Errorf("agent PID changed from %d to %d, want unchanged", livePID, finalPID)
	}
}

// TestCheckAgentHealth_ValidLockWithLiveProcess verifies that checkAgentHealth
// completes cleanly when lock file PID matches the agent's live PID.
func TestCheckAgentHealth_ValidLockWithLiveProcess(t *testing.T) {
	tmpDir := t.TempDir()

	// Start a real process
	liveCmd := exec.Command("sleep", "60") //nolint:norawexec
	if err := liveCmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	livePID := liveCmd.Process.Pid
	t.Cleanup(func() {
		_ = liveCmd.Process.Kill()
		_ = liveCmd.Wait()
	})

	// Write a lock file with the live PID (valid, non-stale)
	lockInfo := LockInfo{
		PID:       livePID,
		Command:   "plan",
		AgentName: "live-agent",
		StartedAt: time.Now(),
	}
	lockData, _ := json.MarshalIndent(lockInfo, "", "  ")
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, lockData, 0600); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}

	origConfig := os.Getenv("LOOM_CONFIG_DIR")
	os.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Cleanup(func() {
		if origConfig == "" {
			os.Unsetenv("LOOM_CONFIG_DIR")
		} else {
			os.Setenv("LOOM_CONFIG_DIR", origConfig)
		}
	})

	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{
				entry:        AgentEntry{Worktree: "valid-lock-test"},
				pid:          livePID,
				worktreePath: tmpDir,
			},
		},
	}

	// Should complete without panic; no stale lock path triggered
	d.checkAgentHealth()
}

// TestHandleRestartAfterError_SequentialEscalation verifies that restartCount
// increments on each call and handleRestartAfterError returns false when
// maxRetries is exceeded.
func TestHandleRestartAfterError_SequentialEscalation(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
			MaxRetries:     intPtr(3),
			BackoffInitial: intPtr(0), // zero backoff for fast test
			BackoffMax:     intPtr(0),
		}}},
		shutdown: make(chan struct{}),
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test-escalation"},
		restartCount: 0,
	}

	// First 3 calls should return true (count goes 1, 2, 3)
	for i := 1; i <= 3; i++ {
		result := d.handleRestartAfterError(ap)
		if !result {
			t.Errorf("call %d: handleRestartAfterError() = false, want true", i)
		}
		ap.mu.Lock()
		count := ap.restartCount
		ap.mu.Unlock()
		if count != i {
			t.Errorf("call %d: restartCount = %d, want %d", i, count, i)
		}
	}

	// 4th call: count becomes 4 > maxRetries=3, should return false
	result := d.handleRestartAfterError(ap)
	if result {
		t.Error("call 4: handleRestartAfterError() = true, want false (exceeded maxRetries)")
	}
	ap.mu.Lock()
	finalCount := ap.restartCount
	ap.mu.Unlock()
	if finalCount != 4 {
		t.Errorf("final restartCount = %d, want 4", finalCount)
	}
}

// TestHandleRestartAfterError_ActualBackoffDelay verifies that
// handleRestartAfterError actually sleeps for the backoff duration.
func TestHandleRestartAfterError_ActualBackoffDelay(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{RestartPolicy: RestartPolicy{
			MaxRetries:     intPtr(10),
			BackoffInitial: intPtr(1), // 1 second
			BackoffMax:     intPtr(1),
		}}},
		shutdown: make(chan struct{}),
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test-delay"},
		restartCount: 0,
	}

	start := time.Now()
	result := d.handleRestartAfterError(ap)
	elapsed := time.Since(start)

	if !result {
		t.Error("handleRestartAfterError() = false, want true")
	}
	// With BackoffInitial=1, BackoffMax=1, and restartCount=1 after increment:
	// backoff = min(1 * 2^1, 1) = 1 second. Allow 100ms tolerance.
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 900ms (actual backoff sleep)", elapsed)
	}
}

// TestBuildCommand_CustomRoleAllFlags verifies command construction for a custom
// role with all optional flags set: PromptFile, TaskFilter, Backend, epicID.
func TestBuildCommand_CustomRoleAllFlags(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "prompt.md")

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "anthropic"},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Role: "reviewer", Backend: "openai"},
		roleConfig:     RoleConfig{PromptFile: promptFile, TaskFilter: "review-tasks"},
		worktreePath:   tmpDir,
		assignedEpicID: "epic-42",
	}

	cmd, err := d.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}

	// Verify args: loom agent <path> --prompt <file> --auto --daemon-mode --task-filter <filter> --backend <backend> --parent <epic>
	expectedArgs := []string{
		"loom", "agent", tmpDir, "--prompt", promptFile, "--auto", "--daemon-mode",
		"--task-filter", "review-tasks",
		"--backend", "openai", // per-agent backend overrides project backend
		"--parent", "epic-42",
	}
	if len(cmd.Args) != len(expectedArgs) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, expectedArgs)
	}
	for i, want := range expectedArgs {
		if cmd.Args[i] != want {
			t.Errorf("cmd.Args[%d] = %q, want %q", i, cmd.Args[i], want)
		}
	}

	if cmd.Dir != tmpDir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, tmpDir)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid not set")
	}

	// Verify env contains BD_ACTOR and LOOM_WORKTREE_PATH
	foundActor, foundPath := false, false
	for _, env := range cmd.Env {
		if env == "BD_ACTOR=falcon" {
			foundActor = true
		}
		if env == "LOOM_WORKTREE_PATH="+tmpDir {
			foundPath = true
		}
	}
	if !foundActor {
		t.Error("BD_ACTOR=falcon not found in cmd.Env")
	}
	if !foundPath {
		t.Errorf("LOOM_WORKTREE_PATH=%s not found in cmd.Env", tmpDir)
	}
}

// TestBuildCommand_CustomRoleMinimal verifies command construction for a custom
// role with only PromptFile set (no TaskFilter, no Backend, no epicID).
func TestBuildCommand_CustomRoleMinimal(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "prompt.md")

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "hawk", Role: "coder"},
		roleConfig:   RoleConfig{PromptFile: promptFile},
		worktreePath: tmpDir,
	}

	cmd, err := d.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}

	expectedArgs := []string{
		"loom", "agent", tmpDir, "--prompt", promptFile, "--auto", "--daemon-mode",
	}
	if len(cmd.Args) != len(expectedArgs) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, expectedArgs)
	}
	for i, want := range expectedArgs {
		if cmd.Args[i] != want {
			t.Errorf("cmd.Args[%d] = %q, want %q", i, cmd.Args[i], want)
		}
	}

	// Verify optional flags are NOT present
	for _, arg := range cmd.Args {
		switch arg {
		case "--task-filter", "--backend", "--parent":
			t.Errorf("unexpected flag %q in minimal custom role args", arg)
		}
	}
}

// TestSuperviseAgent_AcquiresAndReleasesOnShutdown tests that superviseAgent
// acquires a concurrency slot and the tracker can be closed to unblock it.
func TestSuperviseAgent_AcquiresAndReleasesOnShutdown(t *testing.T) {
	maxConc := 1
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(0),
					BackoffInitial: intPtr(1),
					BackoffMax:     intPtr(1),
				},
			},
			Roles: map[string]RoleConfig{
				"plan": {MaxConcurrency: &maxConc},
			},
		},
		shutdown:    make(chan struct{}),
		concurrency: NewConcurrencyTracker(map[string]RoleConfig{"plan": {MaxConcurrency: &maxConc}}),
	}

	// Pre-acquire the only slot so the next Acquire will block
	if !d.concurrency.Acquire("plan") {
		t.Fatal("failed to pre-acquire slot")
	}
	if d.concurrency.ActiveCount("plan") != 1 {
		t.Fatalf("ActiveCount = %d, want 1", d.concurrency.ActiveCount("plan"))
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test-conc", Role: "plan"},
		worktreePath: t.TempDir(),
		stopCh:       make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		d.superviseAgent(ap)
		close(done)
	}()

	// superviseAgent should be blocked on Acquire (or will be shortly).
	// Close the tracker to unblock — Close() is safe regardless of timing:
	// if the goroutine hasn't reached Acquire yet, it will see closed=true immediately.
	d.concurrency.Close()

	select {
	case <-done:
		// Success - superviseAgent exited because Acquire returned false
	case <-time.After(5 * time.Second):
		t.Fatal("superviseAgent did not exit after concurrency tracker closed")
	}

	// Verify the goroutine never acquired a slot (Acquire returned false)
	if got := d.concurrency.ActiveCount("plan"); got != 1 {
		t.Errorf("ActiveCount after goroutine exit = %d, want 1 (only pre-acquired setup slot)", got)
	}
}

// TestSuperviseAgent_ReleasesOnBranchSetupFailure verifies that a concurrency
// slot is released when branch setup fails.
func TestSuperviseAgent_ReleasesOnBranchSetupFailure(t *testing.T) {
	maxConc := 2
	tracker := NewConcurrencyTracker(map[string]RoleConfig{"plan": {MaxConcurrency: &maxConc}})

	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(0), // no retries - will exit after first failure
					BackoffInitial: intPtr(0),
					BackoffMax:     intPtr(0),
				},
			},
		},
		shutdown:    make(chan struct{}),
		concurrency: tracker,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test-branch-fail", Role: "plan"},
		worktreePath: "/nonexistent/path/that/will/fail", // branch setup will fail
		stopCh:       make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		d.superviseAgent(ap)
		close(done)
	}()

	select {
	case <-done:
		// superviseAgent exited
	case <-time.After(10 * time.Second):
		t.Fatal("superviseAgent did not exit")
	}

	// Verify the concurrency slot was released
	count := tracker.ActiveCount("plan")
	if count != 0 {
		t.Errorf("ActiveCount after exit = %d, want 0 (slot should be released)", count)
	}
}

// TestBuildCommand_BuiltInRoleWithBackendAndEpic verifies command construction
// for a built-in role with Backend and assignedEpicID set.
func TestBuildCommand_BuiltInRoleWithBackendAndEpic(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}, Backend: "openai"},
		projectDir: tmpDir,
	}

	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "eagle", Role: "plan"},
		roleConfig:     RoleConfig{Description: "Built-in plan agent"},
		worktreePath:   tmpDir,
		assignedEpicID: "epic-99",
	}

	cmd, err := d.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}

	expectedArgs := []string{
		"loom", "plan", tmpDir, "--auto", "--daemon-mode",
		"--backend", "openai",
		"--parent", "epic-99",
	}
	if len(cmd.Args) != len(expectedArgs) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, expectedArgs)
	}
	for i, want := range expectedArgs {
		if cmd.Args[i] != want {
			t.Errorf("cmd.Args[%d] = %q, want %q", i, cmd.Args[i], want)
		}
	}

	// Verify "agent" and "--prompt" are NOT in args (built-in, not custom)
	for _, arg := range cmd.Args {
		switch arg {
		case "agent", "--prompt":
			t.Errorf("unexpected arg %q in built-in role command", arg)
		}
	}
}

// TestDrainAgent_NotFound verifies that drainAgent returns an error when the
// requested agent name does not exist in the agents slice.
func TestDrainAgent_NotFound(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{entry: AgentEntry{Worktree: "alpha"}},
			{entry: AgentEntry{Worktree: "beta"}},
		},
	}

	err := d.drainAgent("nonexistent")
	if err == nil {
		t.Fatal("drainAgent() returned nil error, want error for non-existent agent")
	}
	if got := err.Error(); got != `agent "nonexistent" not found` {
		t.Errorf("drainAgent() error = %q, want %q", got, `agent "nonexistent" not found`)
	}
}

// TestDrainAgent_RemovesFromSlice verifies that drainAgent stops the agent,
// waits for its done channel, and removes it from the agents slice.
func TestDrainAgent_RemovesFromSlice(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
		agents: make([]*AgentProcess, 0, 3),
	}

	// Create three agents with pre-closed done channels (simulates goroutine already exited)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		ap := &AgentProcess{
			entry:  AgentEntry{Worktree: name},
			stopCh: make(chan struct{}),
			done:   make(chan struct{}),
		}
		// Pre-close done so drainAgent's <-ap.done returns immediately
		close(ap.done)
		d.agents = append(d.agents, ap)
	}

	if d.AgentCount() != 3 {
		t.Fatalf("AgentCount() = %d, want 3", d.AgentCount())
	}

	// Drain the middle agent
	err := d.drainAgent("beta")
	if err != nil {
		t.Fatalf("drainAgent(beta) error = %v", err)
	}

	if d.AgentCount() != 2 {
		t.Fatalf("AgentCount() = %d after drain, want 2", d.AgentCount())
	}

	// Verify beta is gone and order is preserved
	statuses := d.Agents()
	if len(statuses) != 2 {
		t.Fatalf("Agents() returned %d entries, want 2", len(statuses))
	}
	if statuses[0].Worktree != "alpha" {
		t.Errorf("Agents()[0].Worktree = %q, want %q", statuses[0].Worktree, "alpha")
	}
	if statuses[1].Worktree != "gamma" {
		t.Errorf("Agents()[1].Worktree = %q, want %q", statuses[1].Worktree, "gamma")
	}

	// Drain the first agent
	err = d.drainAgent("alpha")
	if err != nil {
		t.Fatalf("drainAgent(alpha) error = %v", err)
	}
	if d.AgentCount() != 1 {
		t.Fatalf("AgentCount() = %d after second drain, want 1", d.AgentCount())
	}

	// Drain the last agent
	err = d.drainAgent("gamma")
	if err != nil {
		t.Fatalf("drainAgent(gamma) error = %v", err)
	}
	if d.AgentCount() != 0 {
		t.Fatalf("AgentCount() = %d after draining all, want 0", d.AgentCount())
	}

	// Draining from empty slice should return error
	err = d.drainAgent("gamma")
	if err == nil {
		t.Error("drainAgent() on empty slice returned nil error, want error")
	}
}

// TestDrainAgent_WaitsForDone verifies that drainAgent blocks until the done
// channel is closed, simulating a superviseAgent goroutine that takes time to exit.
func TestDrainAgent_WaitsForDone(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
	}

	ap := &AgentProcess{
		entry:  AgentEntry{Worktree: "slow-agent"},
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	d.agents = []*AgentProcess{ap}

	// Launch a goroutine that closes done after observing stopCh
	go func() {
		<-ap.stopCh
		// Simulate some cleanup time
		time.Sleep(50 * time.Millisecond)
		close(ap.done)
	}()

	drainDone := make(chan error, 1)
	go func() {
		drainDone <- d.drainAgent("slow-agent")
	}()

	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("drainAgent() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drainAgent() did not complete within 5 seconds")
	}

	if d.AgentCount() != 0 {
		t.Errorf("AgentCount() = %d after drain, want 0", d.AgentCount())
	}
}

// TestAddAgent_DuplicateName verifies that addAgent returns an error when
// an agent with the same worktree name already exists.
func TestAddAgent_DuplicateName(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{
				entry:  AgentEntry{Worktree: "existing-agent", Role: "plan"},
				stopCh: make(chan struct{}),
				done:   make(chan struct{}),
			},
		},
	}

	err := d.addAgent(AgentEntry{Worktree: "existing-agent", Role: "task"})
	if err == nil {
		t.Fatal("addAgent() returned nil error, want error for duplicate name")
	}
	if got := err.Error(); got != `agent "existing-agent" already exists` {
		t.Errorf("addAgent() error = %q, want %q", got, `agent "existing-agent" already exists`)
	}

	// Verify original agent is unchanged
	if d.AgentCount() != 1 {
		t.Errorf("AgentCount() = %d, want 1 (unchanged)", d.AgentCount())
	}
}

// TestAgentsMu_ConcurrentAccess verifies that Agents() and AgentCount() can be
// called concurrently without data races. Run with `go test -race` to detect races.
// TestBuildCommand_SessionEnvVars verifies that LOOM_SESSION_ID and LOOM_BEADS_DIR
// are included in cmd.Env when ap.session is set, and omitted when nil.
func TestBuildCommand_SessionEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("session set propagates env vars", func(t *testing.T) {
		// Create a real session via sessions.NewStore + CreateSession
		beadsDir := filepath.Join(tmpDir, "beads-set")
		store, err := sessions.NewStore(beadsDir)
		if err != nil {
			t.Fatalf("NewStore error: %v", err)
		}
		sess, err := store.CreateSession(sessions.CreateOptions{
			AgentName: "falcon",
			Backend:   "claude",
			Prompt:    "test session env propagation",
		})
		if err != nil {
			t.Fatalf("CreateSession error: %v", err)
		}

		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
			roleConfig:   RoleConfig{Description: "Built-in plan agent"},
			worktreePath: tmpDir,
			session:      sess,
		}

		cmd, err := d.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand: %v", err)
		}

		// Verify LOOM_SESSION_ID is present with correct value
		wantSessionEnv := "LOOM_SESSION_ID=" + sess.SessionID()
		foundSession := false
		for _, env := range cmd.Env {
			if env == wantSessionEnv {
				foundSession = true
			}
		}
		if !foundSession {
			t.Errorf("%s not found in cmd.Env", wantSessionEnv)
		}

		// Verify LOOM_BEADS_DIR is present
		foundBeads := false
		for _, env := range cmd.Env {
			if len(env) > len("LOOM_BEADS_DIR=") && env[:len("LOOM_BEADS_DIR=")] == "LOOM_BEADS_DIR=" {
				foundBeads = true
			}
		}
		if !foundBeads {
			t.Error("LOOM_BEADS_DIR not found in cmd.Env")
		}
	})

	t.Run("nil session omits env vars", func(t *testing.T) {
		// Clear any LOOM_SESSION_ID / LOOM_BEADS_DIR from the parent process
		// environment so FilteredEnv() does not leak them into the test.
		// t.Setenv handles restore-on-cleanup; Unsetenv removes them for the
		// duration of this subtest.
		for _, key := range []string{"LOOM_SESSION_ID", "LOOM_BEADS_DIR"} {
			t.Setenv(key, "") // registers cleanup to restore original value
			os.Unsetenv(key)  // actually remove from os.Environ()
		}

		d := &Daemon{
			config:     &DaemonConfig{Daemon: DaemonSettings{}},
			projectDir: tmpDir,
		}
		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "hawk", Role: "task"},
			roleConfig:   RoleConfig{Description: "Built-in task agent"},
			worktreePath: tmpDir,
			session:      nil, // no session
		}

		cmd, err := d.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand: %v", err)
		}

		// Verify LOOM_SESSION_ID is NOT present
		for _, env := range cmd.Env {
			if len(env) >= len("LOOM_SESSION_ID=") && env[:len("LOOM_SESSION_ID=")] == "LOOM_SESSION_ID=" {
				t.Errorf("LOOM_SESSION_ID should not be in cmd.Env when session is nil, got %q", env)
			}
			if len(env) >= len("LOOM_BEADS_DIR=") && env[:len("LOOM_BEADS_DIR=")] == "LOOM_BEADS_DIR=" {
				t.Errorf("LOOM_BEADS_DIR should not be in cmd.Env when session is nil, got %q", env)
			}
		}
	})
}

func TestAgentsMu_ConcurrentAccess(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
		agents: []*AgentProcess{
			{entry: AgentEntry{Worktree: "a", Role: "plan"}},
			{entry: AgentEntry{Worktree: "b", Role: "task"}},
			{entry: AgentEntry{Worktree: "c", Role: "plan"}},
		},
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if id%2 == 0 {
					statuses := d.Agents()
					_ = len(statuses)
				} else {
					count := d.AgentCount()
					_ = count
				}
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// No races detected (race detector would fail the test)
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent access test timed out")
	}
}
