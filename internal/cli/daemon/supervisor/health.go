package supervisor

import (
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/lockfile"

	"go.opentelemetry.io/otel/attribute"
)

// StopAgent sends SIGTERM then SIGKILL to a single agent and its entire process group.
// This function is safe to call concurrently with waitForAgent.
// It uses polling instead of cmd.Wait() to avoid double-wait issues.
// The process group kill ensures child processes (e.g. codex) are not orphaned.
//
// Wrapped in a daemon.supervisor.stop span. The span attaches loom.exit_code
// at end (as observed by waitForAgent and recorded on the AgentProcess) and
// loom.stop_reason from the AgentProcess.StopReason set by the caller. The
// span ends when StopAgent returns; the actual exec.Cmd.Wait happens on the
// supervise goroutine concurrently — see the long-lived monitoring goroutine
// note in the comment on superviseAgent.
func (s *Supervisor) StopAgent(ap *AgentProcess, sigtermTimeout time.Duration) {
	ap.Mu.Lock()
	proc := ap.Cmd
	pid := ap.Pid
	ap.Mu.Unlock()

	if proc == nil || proc.Process == nil || pid == 0 {
		return
	}

	_, span := startSpan(cmdstore.RootContext(),
		"daemon.supervisor.stop",
		attribute.String("loom.agent", ap.Entry.Worktree),
		attribute.String("loom.workspace", s.WorkspaceID),
	)
	defer func() {
		ap.Mu.Lock()
		exitCode := ap.LastExitCode
		stopReason := string(ap.StopReason)
		ap.Mu.Unlock()
		span.SetAttributes(
			attribute.Int("loom.exit_code", exitCode),
			attribute.String("loom.stop_reason", stopReason),
		)
		span.End()
	}()

	slog.Info("sending signal to process group", "worktree", ap.Entry.Worktree, "signal", "SIGTERM", "pid", pid)

	// Send SIGTERM to the entire process group (negative PID)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Process group may have already exited; try the process directly
		slog.Warn("SIGTERM to process group failed, trying process directly", "worktree", ap.Entry.Worktree, "err", err)
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			slog.Warn("SIGTERM failed, process may have exited", "worktree", ap.Entry.Worktree, "err", err)
			return
		}
	}

	// Poll for process exit instead of calling Wait()
	// (Wait() is called by waitForAgent in the supervise loop)
	deadline := time.Now().Add(sigtermTimeout)
	for time.Now().Before(deadline) {
		ap.Mu.Lock()
		currentPID := ap.Pid
		ap.Mu.Unlock()

		if currentPID == 0 {
			// Process has exited (waitForAgent cleared the pid)
			slog.Info("process exited gracefully", "worktree", ap.Entry.Worktree)
			return
		}

		// Also check if process is still running via OS
		if !lockfile.IsProcessRunning(pid) {
			slog.Info("process exited gracefully", "worktree", ap.Entry.Worktree)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Force kill the entire process group if still running
	ap.Mu.Lock()
	stillRunning := ap.Pid != 0
	ap.Mu.Unlock()

	if stillRunning {
		slog.Warn("sending SIGKILL to process group", "worktree", ap.Entry.Worktree, "pid", pid)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// checkWatchdog checks both transcript mtime (updated by hooks on every turn)
// and log file mtime (stdout output). Kills the agent if no activity signal is
// newer than outputTimeout seconds.
func (s *Supervisor) checkWatchdog(ap *AgentProcess, outputTimeout int, logPath string, lastStart time.Time, worktreeName string) {
	var lastActivity time.Time
	activitySource := "none"

	// Tier 1: Check session transcript (updated by hooks on every turn)
	ap.Mu.Lock()
	txPath := ap.TranscriptPath
	ap.Mu.Unlock()
	if txPath != "" {
		if info, err := os.Stat(txPath); err == nil {
			lastActivity = info.ModTime()
			activitySource = "transcript"
		}
	}

	// Tier 2: Check log file mtime (stdout output). Use the newest signal so a
	// stale hook transcript does not mask active stdout.
	if logPath != "" {
		if info, err := os.Stat(logPath); err == nil {
			if activitySource == "none" || info.ModTime().After(lastActivity) {
				lastActivity = info.ModTime()
				activitySource = "log"
			}
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
			s.setStopReasonDefault(ap, StopReasonWatchdog)
			s.StopAgent(ap, 10*time.Second)
		}
	}
}

// healthChecker runs periodic health checks in a goroutine.
func (s *Supervisor) healthChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.Shutdown:
			return
		case <-ticker.C:
			s.checkAgentHealth()
		}
	}
}

// checkAgentHealth performs health checks on all agents.
func (s *Supervisor) checkAgentHealth() {
	outputTimeout := s.GetOutputTimeout()
	var totalAgents, healthyAgents int

	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	for _, ap := range snapshot {
		ap.Mu.Lock()
		pid := ap.Pid
		worktreePath := ap.WorktreePath
		worktreeName := ap.Entry.Worktree
		logPath := ap.LogFilePath
		lastStart := ap.LastStart
		ap.Mu.Unlock()

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
		lockInfo, isRunning, err := cli.CheckLock(worktreePath)
		if err == nil && lockInfo != nil && !isRunning {
			slog.Warn("stale lock detected", "worktree", worktreeName)
		}

		// Watchdog: kill agent if no activity for outputTimeout seconds.
		if outputTimeout > 0 {
			s.checkWatchdog(ap, outputTimeout, logPath, lastStart, worktreeName)
		}
	}

	// Emit health_check summary event
	if evt, err := events.NewEvent(events.HealthCheck, "", "", "", events.HealthCheckData{AgentCount: totalAgents, HealthyCount: healthyAgents}); err == nil {
		s.EmitEvent(evt)
	}
}
