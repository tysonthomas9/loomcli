package cli

import (
	"log/slog"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// stopAgent terminates a single agent using its execution strategy.
// This function is safe to call concurrently with waitForAgent.
// It delegates to the agent's ExecutionStrategy.Kill method which handles
// the SIGTERM/SIGKILL sequence and process group cleanup.
func (d *Daemon) stopAgent(ap *AgentProcess) {
	// Resolve strategy (fallback to DirectStrategy for backward compatibility with tests)
	ap.mu.Lock()
	strategy := ap.strategy
	ap.mu.Unlock()

	if strategy == nil {
		strategy = &DirectStrategy{}
	}

	strategy.Kill(ap)
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

		// Watchdog: kill agent if no log output for outputTimeout seconds
		if outputTimeout > 0 && logPath != "" {
			if info, err := os.Stat(logPath); err == nil {
				lastOutput := info.ModTime()
				// Use lastStart if log hasn't been written yet (agent just spawned)
				if lastOutput.Before(lastStart) {
					lastOutput = lastStart
				}
				silent := time.Since(lastOutput)
				threshold := time.Duration(outputTimeout) * time.Second
				if silent > threshold {
					slog.Error("killing hung process, no output detected",
						"worktree", worktreeName, "silent_duration", silent.Truncate(time.Second), "threshold_sec", outputTimeout)
					d.stopAgent(ap)
				}
			}
		}
	}

	// Emit health_check summary event
	if evt, err := events.NewEvent(events.HealthCheck, "", "", "", events.HealthCheckData{AgentCount: totalAgents, HealthyCount: healthyAgents}); err == nil {
		d.emitEvent(evt)
	}
}
