package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// superviseAgent is the main loop for a single agent (runs in goroutine).
func (d *Daemon) superviseAgent(ap *AgentProcess) {
	defer d.epicAssigner.ReleaseWorktree(ap.entry.Worktree)
	log.Printf("[daemon] Starting supervisor for agent %s (role: %s)", ap.entry.Worktree, ap.entry.Role)

	for {
		// Check shutdown before each cycle
		select {
		case <-d.shutdown:
			log.Printf("[daemon] Agent %s: shutdown signal received", ap.entry.Worktree)
			return
		default:
		}

		// 1. Pre-flight recovery
		if err := d.recoverAgent(ap, 0); err != nil {
			log.Printf("[daemon] Agent %s: pre-flight recovery failed: %v", ap.entry.Worktree, err)
			// Continue with caution - spawn may still work
		}

		// 1.5. Assign epic to worktree (if available)
		epicID, err := d.epicAssigner.AssignWorktree(ap.entry.Worktree)
		if err != nil {
			log.Printf("[daemon] Agent %s: epic assignment failed (falling back to non-epic mode): %v", ap.entry.Worktree, err)
			epicID = ""
		}
		ap.mu.Lock()
		ap.assignedEpicID = epicID
		ap.mu.Unlock()

		// 2. Ensure correct branch for epic assignment
		targetBranch := ap.entry.Worktree // default: agent-name branch
		if epicID != "" {
			targetBranch = epicBranchName(epicID)
		}
		log.Printf("[daemon] Agent %s: ensuring branch %s", ap.entry.Worktree, targetBranch)
		if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, "origin/main"); err != nil {
			log.Printf("[daemon] Agent %s: branch setup failed: %v", ap.entry.Worktree, err)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 2.5. Acquire concurrency slot for role
		if !d.concurrency.Acquire(ap.entry.Role) {
			log.Printf("[daemon] Agent %s: shutdown during concurrency wait", ap.entry.Worktree)
			return
		}

		// 3. Spawn subprocess
		if err := d.spawnAgent(ap); err != nil {
			d.concurrency.Release(ap.entry.Role)
			log.Printf("[daemon] Agent %s: spawn failed: %v", ap.entry.Worktree, err)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 4. Wait for exit
		exitCode := d.waitForAgent(ap)

		// 4.5. Release concurrency slot now that the process has exited
		d.concurrency.Release(ap.entry.Role)

		d.logAgentExit(ap, exitCode)

		// 5. Post-mortem recovery (exit-code-aware)
		if err := d.recoverAgent(ap, exitCode); err != nil {
			log.Printf("[daemon] Agent %s: post-mortem recovery failed: %v", ap.entry.Worktree, err)
			// Non-fatal, continue with restart logic
		}

		// 5.5. Ensure PR exists for epic branch (non-fatal)
		ap.mu.Lock()
		currentEpicID := ap.assignedEpicID
		ap.mu.Unlock()
		if currentEpicID != "" {
			if err := EnsureEpicPR(ap.worktreePath, currentEpicID); err != nil {
				log.Printf("[daemon] Agent %s: PR creation failed: %v", ap.entry.Worktree, err)
				// Non-fatal — don't block restart
			}
		}

		// 5.6. Release epic assignment so next iteration re-evaluates
		d.epicAssigner.ReleaseWorktree(ap.entry.Worktree)

		// 6. Epic exhaustion check and reassignment
		if err := d.handleEpicTransition(ap); err != nil {
			log.Printf("[daemon] Agent %s: epic transition failed: %v", ap.entry.Worktree, err)
			// Non-fatal: agent will respawn in current mode
		}

		// 7. Check shutdown after subprocess exit
		select {
		case <-d.shutdown:
			log.Printf("[daemon] Agent %s: shutdown signal received after exit", ap.entry.Worktree)
			return
		default:
		}

		// 8. Restart decision
		if !d.shouldRestart(ap) {
			log.Printf("[daemon] Agent %s: max restarts exceeded, stopping supervisor", ap.entry.Worktree)
			return
		}

		// 9. Backoff sleep (interruptible)
		backoff := d.computeBackoff(ap)
		log.Printf("[daemon] Agent %s: waiting %v before restart (attempt %d)", ap.entry.Worktree, backoff, ap.restartCount)

		select {
		case <-time.After(backoff):
			// Backoff complete, continue to next iteration
		case <-d.shutdown:
			log.Printf("[daemon] Agent %s: shutdown during backoff", ap.entry.Worktree)
			return
		}
	}
}

// handleRestartAfterError handles restart logic after spawn failure.
// Returns true if the supervisor should continue, false if it should exit.
func (d *Daemon) handleRestartAfterError(ap *AgentProcess) bool {
	ap.mu.Lock()
	ap.restartCount++
	count := ap.restartCount
	ap.mu.Unlock()

	maxRetries := d.getMaxRetries()
	if count > maxRetries {
		log.Printf("[daemon] Agent %s: max retries exceeded after spawn error", ap.entry.Worktree)
		return false
	}

	backoff := d.computeBackoff(ap)
	log.Printf("[daemon] Agent %s: spawn failed, waiting %v before retry (attempt %d/%d)",
		ap.entry.Worktree, backoff, count, maxRetries)

	select {
	case <-time.After(backoff):
		return true
	case <-d.shutdown:
		return false
	}
}

// logAgentExit logs the agent exit with task details from the lock file.
func (d *Daemon) logAgentExit(ap *AgentProcess, exitCode int) {
	if lockInfo, _, _ := CheckLock(ap.worktreePath); lockInfo != nil && lockInfo.TaskID != "" {
		log.Printf("[daemon] Agent %s: exited with code %d (task %s: %s)",
			ap.entry.Worktree, exitCode, lockInfo.TaskID, lockInfo.TaskTitle)
	} else {
		log.Printf("[daemon] Agent %s: exited with code %d", ap.entry.Worktree, exitCode)
	}
}

// buildCommand constructs the exec.Cmd for spawning an agent subprocess.
// Extracted from spawnAgent for testability. Does not start the process.
// Caller must hold ap.mu (reads ap.assignedEpicID).
func (d *Daemon) buildCommand(ap *AgentProcess) *exec.Cmd {
	var cmd *exec.Cmd

	// Resolve backend for this agent: per-agent > per-role > project > global > default
	agentBackend := ap.entry.Backend
	if agentBackend == "" {
		agentBackend = ap.roleConfig.Backend
	}
	if agentBackend == "" {
		agentBackend = d.config.Backend
	}

	if builtInRoles[ap.entry.Role] {
		// Built-in role: loom <role> <worktree> --auto --daemon-mode
		args := []string{ap.entry.Role, ap.worktreePath, "--auto", "--daemon-mode"}
		if agentBackend != "" {
			args = append(args, "--backend", agentBackend)
		}
		if ap.assignedEpicID != "" {
			args = append(args, "--parent", ap.assignedEpicID)
		}
		cmd = exec.Command("loom", args...)
	} else {
		// Custom role: loom agent <worktree> --prompt <path> --task-filter <filter> --auto --daemon-mode
		args := []string{"agent", ap.worktreePath, "--prompt", ap.roleConfig.PromptFile, "--auto", "--daemon-mode"}
		if ap.roleConfig.TaskFilter != "" {
			args = append(args, "--task-filter", ap.roleConfig.TaskFilter)
		}
		if agentBackend != "" {
			args = append(args, "--backend", agentBackend)
		}
		if ap.assignedEpicID != "" {
			args = append(args, "--parent", ap.assignedEpicID)
		}
		cmd = exec.Command("loom", args...)
	}

	// Set working directory to worktree
	cmd.Dir = ap.worktreePath

	// Create a new process group so we can kill the entire tree on stop
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Set environment
	cmd.Env = append(FilteredEnv(),
		fmt.Sprintf("BD_ACTOR=%s", ap.entry.Worktree),
		fmt.Sprintf("LOOM_WORKTREE_PATH=%s", ap.worktreePath),
	)

	// Propagate role constraints as env vars for the subprocess
	if len(ap.roleConfig.AllowedTools) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ALLOWED_TOOLS=%s", strings.Join(ap.roleConfig.AllowedTools, ",")))
	}
	if len(ap.roleConfig.DeniedTools) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_DENIED_TOOLS=%s", strings.Join(ap.roleConfig.DeniedTools, ",")))
	}
	if ap.roleConfig.ReadOnly {
		cmd.Env = append(cmd.Env, "LOOM_READ_ONLY=1")
	}

	return cmd
}

// spawnAgent starts the subprocess for an agent.
func (d *Daemon) spawnAgent(ap *AgentProcess) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	cmd := d.buildCommand(ap)

	// Set up log files if log directory is configured
	if d.config.Daemon.LogDir != "" {
		logDir := d.config.Daemon.LogDir
		if !filepath.IsAbs(logDir) {
			logDir = filepath.Join(d.projectDir, logDir)
		}
		if err := os.MkdirAll(logDir, 0700); err != nil {
			log.Printf("[daemon] Agent %s: failed to create log directory: %v", ap.entry.Worktree, err)
		} else {
			// Sanitize worktree name to prevent path traversal in log filename
			safeWorktree := filepath.Base(ap.entry.Worktree)
			logFilePath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", ap.entry.Role, safeWorktree))
			ap.logFilePath = logFilePath // store for watchdog stat checks
			f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				log.Printf("[daemon] Agent %s: failed to open log file: %v", ap.entry.Worktree, err)
			} else {
				cmd.Stdout = f
				cmd.Stderr = f
				ap.logFile = f // Track file handle for cleanup in waitForAgent
			}
		}
	}

	// Start the subprocess
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start subprocess: %w", err)
	}

	ap.cmd = cmd
	ap.pid = cmd.Process.Pid
	ap.lastStart = time.Now()

	log.Printf("[daemon] Agent %s: spawned subprocess PID %d", ap.entry.Worktree, ap.pid)
	return nil
}

// waitForAgent blocks until subprocess exits, returns exit code.
func (d *Daemon) waitForAgent(ap *AgentProcess) int {
	ap.mu.Lock()
	cmd := ap.cmd
	ap.mu.Unlock()

	if cmd == nil {
		return -1
	}

	err := cmd.Wait()

	ap.mu.Lock()
	ap.lastExit = time.Now()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			ap.lastExitCode = exitErr.ExitCode()
		} else {
			ap.lastExitCode = -1
		}
	} else {
		ap.lastExitCode = 0
	}
	exitCode := ap.lastExitCode
	ap.cmd = nil
	ap.pid = 0

	// Close log file handle to prevent leaks
	if ap.logFile != nil {
		if err := ap.logFile.Close(); err != nil {
			log.Printf("[daemon] Agent %s: failed to close log file: %v", ap.entry.Worktree, err)
		}
		ap.logFile = nil
	}
	ap.mu.Unlock()

	return exitCode
}

// recoverAgent calls RecoverWorktree for cleanup.
// exitCode is passed so recovery can make smarter decisions (e.g. skip task
// reset on clean exit when the task status is already terminal).
func (d *Daemon) recoverAgent(ap *AgentProcess, exitCode int) error {
	return RecoverWorktree(ap.worktreePath, ap.entry.Worktree, exitCode)
}

// shouldRestart determines if agent should restart based on backoff policy.
func (d *Daemon) shouldRestart(ap *AgentProcess) bool {
	maxRetries := d.getMaxRetries()

	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Successful run (exit 0, ran for >1 minute) resets counter
	if ap.lastExitCode == 0 && time.Since(ap.lastStart) > time.Minute {
		ap.restartCount = 0
		return true
	}

	ap.restartCount++
	return ap.restartCount <= maxRetries
}

// computeBackoff returns the sleep duration before next restart.
func (d *Daemon) computeBackoff(ap *AgentProcess) time.Duration {
	initial := d.getBackoffInitial()
	maxBackoff := d.getBackoffMax()

	ap.mu.Lock()
	count := ap.restartCount
	ap.mu.Unlock()

	// Cap count to prevent integer overflow in bit shift (max safe shift is 30 for 32-bit result)
	if count > 30 {
		count = 30
	}

	// Exponential: initial * 2^restartCount
	backoffSec := initial * (1 << count)
	if backoffSec > maxBackoff || backoffSec < 0 { // check for overflow (negative)
		backoffSec = maxBackoff
	}

	return time.Duration(backoffSec) * time.Second
}

// Helper functions to safely access RestartPolicy fields with defaults.
func (d *Daemon) getMaxRetries() int {
	if d.config.Daemon.RestartPolicy.MaxRetries != nil {
		return *d.config.Daemon.RestartPolicy.MaxRetries
	}
	return 3 // default
}

func (d *Daemon) getBackoffInitial() int {
	if d.config.Daemon.RestartPolicy.BackoffInitial != nil {
		return *d.config.Daemon.RestartPolicy.BackoffInitial
	}
	return 2 // default seconds
}

func (d *Daemon) getBackoffMax() int {
	if d.config.Daemon.RestartPolicy.BackoffMax != nil {
		return *d.config.Daemon.RestartPolicy.BackoffMax
	}
	return 300 // default seconds
}

func (d *Daemon) getOutputTimeout() int {
	if d.config.Daemon.RestartPolicy.OutputTimeout != nil {
		return *d.config.Daemon.RestartPolicy.OutputTimeout
	}
	return 900 // default: 15 minutes
}

// stopAgent sends SIGTERM then SIGKILL to a single agent and its entire process group.
// This function is safe to call concurrently with waitForAgent.
// It uses polling instead of cmd.Wait() to avoid double-wait issues.
// The process group kill ensures child processes (e.g. codex) are not orphaned.
func (d *Daemon) stopAgent(ap *AgentProcess) {
	ap.mu.Lock()
	proc := ap.cmd
	pid := ap.pid
	ap.mu.Unlock()

	if proc == nil || proc.Process == nil || pid == 0 {
		return
	}

	log.Printf("[daemon] Agent %s: sending SIGTERM to process group %d", ap.entry.Worktree, pid)

	// Send SIGTERM to the entire process group (negative PID)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Process group may have already exited; try the process directly
		log.Printf("[daemon] Agent %s: SIGTERM to process group failed: %v (trying process directly)", ap.entry.Worktree, err)
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			log.Printf("[daemon] Agent %s: SIGTERM failed (process may have exited): %v", ap.entry.Worktree, err)
			return
		}
	}

	// Poll for process exit up to 5 seconds instead of calling Wait()
	// (Wait() is called by waitForAgent in the supervise loop)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ap.mu.Lock()
		currentPID := ap.pid
		ap.mu.Unlock()

		if currentPID == 0 {
			// Process has exited (waitForAgent cleared the pid)
			log.Printf("[daemon] Agent %s: process exited gracefully", ap.entry.Worktree)
			return
		}

		// Also check if process is still running via OS
		if !lockfile.IsProcessRunning(pid) {
			log.Printf("[daemon] Agent %s: process exited gracefully", ap.entry.Worktree)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Force kill the entire process group if still running
	ap.mu.Lock()
	stillRunning := ap.pid != 0
	ap.mu.Unlock()

	if stillRunning {
		log.Printf("[daemon] Agent %s: sending SIGKILL to process group %d", ap.entry.Worktree, pid)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}
