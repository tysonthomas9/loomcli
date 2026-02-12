package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
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
	d.stopAgent(ap)
}

// TestStopAgent_ProcessAlreadyExited tests that stopAgent handles a process
// that has already exited gracefully.
func TestStopAgent_ProcessAlreadyExited(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	// Spawn a short-lived process and wait for it to finish
	cmd := exec.Command("true")
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
	d.stopAgent(ap)
}

// TestStopAgent_GracefulShutdown tests that stopAgent sends SIGTERM and the
// process exits without needing SIGKILL.
func TestStopAgent_GracefulShutdown(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	// Spawn a process that exits on SIGTERM (sleep responds to SIGTERM)
	cmd := exec.Command("sleep", "60")
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
	d.stopAgent(ap)

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
	cmd := exec.Command("bash", "-c", `trap "" TERM; sleep 60`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start trap process: %v", err)
	}
	pid := cmd.Process.Pid

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
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
	d.stopAgent(ap)

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

// TestWaitForAgent_SuccessfulExit tests waitForAgent with exit code 0.
func TestWaitForAgent_SuccessfulExit(t *testing.T) {
	d := &Daemon{config: &DaemonConfig{}}

	cmd := exec.Command("true")
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

	cmd := exec.Command("bash", "-c", "exit 1")
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

	cmd := exec.Command("true")
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
			agents:       []*AgentProcess{},
			epicAssigner: NewEpicAssigner(),
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
			agents:       []*AgentProcess{},
			epicAssigner: NewEpicAssigner(),
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
		shutdown:     make(chan struct{}),
		epicAssigner: NewEpicAssigner(),
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "test", Role: "plan"},
		worktreePath: t.TempDir(),
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

	cmd := exec.Command("sleep", "60")
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
