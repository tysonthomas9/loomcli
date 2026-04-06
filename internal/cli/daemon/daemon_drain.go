package daemon

import (
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// DefaultYieldTimeout is the default number of seconds to wait for an agent
// to exit after a yield file is written, before escalating to SIGTERM.
const DefaultYieldTimeout = 60 // seconds

// DrainWithGrace implements a four-phase graceful shutdown sequence:
// yield file → wait for voluntary exit → SIGTERM → SIGKILL.
// Returns true if the agent exited from yield alone (SIGTERM was not needed).
func (d *Daemon) DrainWithGrace(ap *AgentProcess, reason string, yieldTimeout, sigtermTimeout time.Duration) bool {
	slog.Info("requesting yield", "worktree", ap.entry.Worktree, "reason", reason, "timeout", yieldTimeout)

	// Phase 1: Write yield file
	if err := d.RequestYield(ap, reason); err != nil {
		slog.Warn("yield file write failed, falling back to SIGTERM", "worktree", ap.entry.Worktree, "err", err)
		d.stopAgent(ap, sigtermTimeout)
		return false
	}

	// Phase 2: Poll for voluntary exit
	deadline := time.Now().Add(yieldTimeout)
	for time.Now().Before(deadline) {
		ap.mu.Lock()
		pid := ap.pid
		ap.mu.Unlock()

		if pid == 0 {
			slog.Info("agent yielded gracefully", "worktree", ap.entry.Worktree, "elapsed", time.Since(deadline.Add(-yieldTimeout)).Truncate(time.Millisecond))
			return true
		}

		if !lockfile.IsProcessRunning(pid) {
			slog.Info("agent yielded gracefully", "worktree", ap.entry.Worktree, "elapsed", time.Since(deadline.Add(-yieldTimeout)).Truncate(time.Millisecond))
			return true
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Phase 3: Escalate to SIGTERM → SIGKILL
	slog.Info("yield timeout expired, escalating to SIGTERM", "worktree", ap.entry.Worktree, "timeout", yieldTimeout)
	d.stopAgent(ap, sigtermTimeout)
	return false
}

// getYieldTimeout returns the configured yield timeout duration.
// Falls back to DefaultYieldTimeout if not set or <= 0.
func (d *Daemon) getYieldTimeout() time.Duration {
	cfg := d.configSnapshot()
	if cfg.Daemon.RestartPolicy.YieldTimeout != nil && *cfg.Daemon.RestartPolicy.YieldTimeout > 0 {
		return time.Duration(*cfg.Daemon.RestartPolicy.YieldTimeout) * time.Second
	}
	return DefaultYieldTimeout * time.Second
}

// drainAllWithGrace yields all agents in parallel, used by Daemon.Stop().
// Both yield and SIGTERM timeouts are capped at 30s to keep daemon shutdown prompt.
func (d *Daemon) drainAllWithGrace(agents []*AgentProcess) {
	yieldTimeout := d.getYieldTimeout()
	if yieldTimeout > 30*time.Second {
		slog.Info("capping yield timeout for daemon shutdown", "configured", yieldTimeout, "capped", 30*time.Second)
		yieldTimeout = 30 * time.Second
	}
	sigtermTimeout := d.getSigtermTimeout()
	if sigtermTimeout > 30*time.Second {
		slog.Info("capping SIGTERM timeout for daemon shutdown", "configured", sigtermTimeout, "capped", 30*time.Second)
		sigtermTimeout = 30 * time.Second
	}

	var stopWg sync.WaitGroup
	for _, ap := range agents {
		stopWg.Add(1)
		go func(agent *AgentProcess) {
			defer stopWg.Done()
			d.DrainWithGrace(agent, "shutdown", yieldTimeout, sigtermTimeout)
		}(ap)
	}
	stopWg.Wait()
}
