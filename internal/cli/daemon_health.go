package cli

import (
	"log"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

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

	for _, ap := range d.agents {
		ap.mu.Lock()
		pid := ap.pid
		worktreePath := ap.worktreePath
		worktreeName := ap.entry.Worktree
		logPath := ap.logFilePath
		lastStart := ap.lastStart
		ap.mu.Unlock()

		if pid == 0 {
			continue // Not running
		}

		// Check if PID is alive
		if !lockfile.IsProcessRunning(pid) {
			// Process died unexpectedly - superviseAgent will detect via cmd.Wait()
			log.Printf("[daemon] Agent %s (PID %d) is not running", worktreeName, pid)
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
}
