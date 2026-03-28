package cli

import (
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

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

	slog.Info("sending signal to process group", "worktree", ap.entry.Worktree, "signal", "SIGTERM", "pid", pid)

	// Send SIGTERM to the entire process group (negative PID)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Process group may have already exited; try the process directly
		slog.Warn("SIGTERM to process group failed, trying process directly", "worktree", ap.entry.Worktree, "err", err)
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			slog.Warn("SIGTERM failed, process may have exited", "worktree", ap.entry.Worktree, "err", err)
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
			slog.Info("process exited gracefully", "worktree", ap.entry.Worktree)
			return
		}

		// Also check if process is still running via OS
		if !lockfile.IsProcessRunning(pid) {
			slog.Info("process exited gracefully", "worktree", ap.entry.Worktree)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Force kill the entire process group if still running
	ap.mu.Lock()
	stillRunning := ap.pid != 0
	ap.mu.Unlock()

	if stillRunning {
		slog.Warn("sending SIGKILL to process group", "worktree", ap.entry.Worktree, "pid", pid)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// checkWatchdog checks transcript mtime first (more reliable — updated by hooks
// on every turn), falls back to log file mtime (stdout output). Kills the agent
// if no activity signal is newer than outputTimeout seconds.
func (d *Daemon) checkWatchdog(ap *AgentProcess, outputTimeout int, logPath string, lastStart time.Time, worktreeName string) {
	var lastActivity time.Time
	activitySource := "none"

	// Tier 1: Check session transcript (updated by hooks on every turn)
	ap.mu.Lock()
	txPath := ap.transcriptPath
	ap.mu.Unlock()
	if txPath != "" {
		if info, err := os.Stat(txPath); err == nil {
			lastActivity = info.ModTime()
			activitySource = "transcript"
		}
		// If transcript doesn't exist yet (no hooks fired), fall through to log
	}

	// Tier 2: Fall back to log file mtime (stdout output)
	if activitySource == "none" && logPath != "" {
		if info, err := os.Stat(logPath); err == nil {
			lastActivity = info.ModTime()
			activitySource = "log"
		}
	}

	// Apply timeout if we found any activity signal
	if activitySource != "none" {
		// Use lastStart if activity signal predates agent spawn
		if lastActivity.Before(lastStart) {
			lastActivity = lastStart
		}
		silent := time.Since(lastActivity)
		threshold := time.Duration(outputTimeout) * time.Second
		if silent > threshold {
			slog.Error("killing hung process, no activity detected",
				"worktree", worktreeName, "silent_duration", silent.Truncate(time.Second),
				"threshold_sec", outputTimeout, "source", activitySource)
			d.stopAgent(ap)
		}
	}
}

// healthChecker runs periodic health checks in a goroutine.
func (d *Daemon) healthChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.shutdown:
			return
		case <-ticker.C:
			d.checkAgentHealth()
		}
	}
}

// checkAgentHealth performs health checks on all agents.
func (d *Daemon) checkAgentHealth() {
	outputTimeout := d.getOutputTimeout()
	var totalAgents, healthyAgents int

	d.agentsMu.RLock()
	snapshot := make([]*AgentProcess, len(d.agents))
	copy(snapshot, d.agents)
	d.agentsMu.RUnlock()

	for _, ap := range snapshot {
		ap.mu.Lock()
		pid := ap.pid
		worktreePath := ap.worktreePath
		worktreeName := ap.entry.Worktree
		logPath := ap.logFilePath
		lastStart := ap.lastStart
		ap.mu.Unlock()

		totalAgents++

		if pid == 0 {
			continue // Not running
		}

		// Check if PID is alive
		if !lockfile.IsProcessRunning(pid) {
			// Process died unexpectedly - superviseAgent will detect via cmd.Wait()
			slog.Warn("agent is not running", "worktree", worktreeName, "pid", pid)
		} else {
			healthyAgents++
		}

		// Check lock file for stale state
		lockInfo, isRunning, err := CheckLock(worktreePath)
		if err == nil && lockInfo != nil && !isRunning {
			slog.Warn("stale lock detected", "worktree", worktreeName)
		}

		// Watchdog: kill agent if no activity for outputTimeout seconds.
		if outputTimeout > 0 {
			d.checkWatchdog(ap, outputTimeout, logPath, lastStart, worktreeName)
		}
	}

	// Emit health_check summary event
	if evt, err := events.NewEvent(events.HealthCheck, "", "", "", events.HealthCheckData{AgentCount: totalAgents, HealthyCount: healthyAgents}); err == nil {
		d.emitEvent(evt)
	}
}
