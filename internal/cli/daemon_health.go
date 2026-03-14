package cli

import (
	"log"
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
			log.Printf("[daemon] Agent %s (PID %d) is not running", worktreeName, pid)
		} else {
			healthyAgents++
		}

		// Check lock file for stale state
		lockInfo, isRunning, err := CheckLock(worktreePath)
		if err == nil && lockInfo != nil && !isRunning {
			log.Printf("[daemon] Stale lock detected for agent %s", worktreeName)
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
					log.Printf("[daemon] Agent %s: no output for %v (threshold %ds), killing hung process",
						worktreeName, silent.Truncate(time.Second), outputTimeout)
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
