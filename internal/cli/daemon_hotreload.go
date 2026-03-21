package cli

import (
	"fmt"
	"log/slog"
)

// drainAgent gracefully stops a single agent by name and removes it from the agents slice.
// It signals the agent's superviseAgent goroutine to exit via stopCh, stops the subprocess
// via SIGTERM/SIGKILL, waits for the goroutine to finish, then removes the agent.
func (d *Daemon) drainAgent(name string) error {
	// Find the agent under lock
	d.agentsMu.Lock()
	var target *AgentProcess
	for _, ap := range d.agents {
		if ap.entry.Worktree == name {
			target = ap
			break
		}
	}
	if target == nil {
		d.agentsMu.Unlock()
		return fmt.Errorf("agent %q not found", name)
	}
	d.agentsMu.Unlock()

	// Signal the agent to stop (safe against double-close)
	target.stopOnce.Do(func() {
		close(target.stopCh)
	})

	// Stop the subprocess (SIGTERM → SIGKILL)
	d.stopAgent(target)

	// Wait for the superviseAgent goroutine to exit
	<-target.done

	// Remove from the agents slice under write lock
	d.agentsMu.Lock()
	for i, ap := range d.agents {
		if ap == target {
			d.agents = append(d.agents[:i], d.agents[i+1:]...)
			break
		}
	}
	d.agentsMu.Unlock()

	slog.Info("agent drained and removed", "worktree", name)
	return nil
}

// addAgent creates and starts a new agent at runtime.
// The agent begins its superviseAgent loop immediately.
func (d *Daemon) addAgent(entry AgentEntry) error {
	// Early duplicate check (avoids unnecessary I/O; authoritative check is below under Lock)
	d.agentsMu.RLock()
	for _, ap := range d.agents {
		if ap.entry.Worktree == entry.Worktree {
			d.agentsMu.RUnlock()
			return fmt.Errorf("agent %q already exists", entry.Worktree)
		}
	}
	d.agentsMu.RUnlock()

	// Resolve worktree path (outside lock — may do I/O)
	target, err := ResolveAgentTarget(entry.Worktree, entry.Repo)
	if err != nil {
		return fmt.Errorf("agent %q worktree: %w", entry.Worktree, err)
	}

	// Resolve role config (outside lock — may do I/O)
	d.agentsMu.RLock()
	agentCount := len(d.agents)
	d.agentsMu.RUnlock()

	roleConfig, err := d.resolveRoleConfig(entry.Role, agentCount)
	if err != nil {
		return err
	}

	ap := &AgentProcess{
		entry:        entry,
		roleConfig:   roleConfig,
		worktreePath: target.WorkDir,
		repoConfig:   d.findRepoConfig(entry.Repo),
		stopCh:       make(chan struct{}),
		done:         make(chan struct{}),
	}

	// Check for duplicate, add to slice, and increment WaitGroup atomically
	// under a single write lock to prevent a race between wg.Add(1) and Stop()'s wg.Wait().
	d.agentsMu.Lock()
	for _, existing := range d.agents {
		if existing.entry.Worktree == entry.Worktree {
			d.agentsMu.Unlock()
			return fmt.Errorf("agent %q already exists", entry.Worktree)
		}
	}
	d.agents = append(d.agents, ap)
	d.wg.Add(1)
	d.agentsMu.Unlock()
	go func() {
		defer d.wg.Done()
		defer close(ap.done)
		d.superviseAgent(ap)
	}()

	slog.Info("agent added and started", "worktree", entry.Worktree, "role", entry.Role)
	return nil
}
