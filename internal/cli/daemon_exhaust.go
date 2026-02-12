package cli

import (
	"log"
)

// handleEpicTransition checks if the current epic has remaining ready tasks.
// If the epic is exhausted, it releases the assignment and tries to assign a
// new epic. If no epics are available, it falls back to non-epic mode.
// Called from superviseAgent() after post-mortem recovery and before the restart decision.
func (d *Daemon) handleEpicTransition(ap *AgentProcess) error {
	// Get current epic assignment (read under lock for thread safety)
	ap.mu.Lock()
	currentEpicID := ap.assignedEpicID
	ap.mu.Unlock()
	if currentEpicID == "" {
		// Not in epic mode — nothing to transition
		return nil
	}

	// Check if current epic still has ready tasks
	hasReady, err := epicHasReadyTasks(currentEpicID)
	if err != nil {
		log.Printf("[daemon] Agent %s: failed to check epic %s for ready tasks: %v (staying on current epic)",
			ap.entry.Worktree, currentEpicID, err)
		return nil
	}
	log.Printf("[daemon] Agent %s: epic %s hasReadyTasks=%v", ap.entry.Worktree, currentEpicID, hasReady)
	if hasReady {
		// Epic still has work — no transition needed
		return nil
	}

	// Epic is exhausted
	log.Printf("[daemon] Agent %s: epic %s exhausted (no ready tasks)", ap.entry.Worktree, currentEpicID)

	// Release the current assignment
	d.epicAssigner.ReleaseWorktree(ap.entry.Worktree)

	// Try to assign a new epic
	newEpicID, err := d.epicAssigner.AssignWorktree(ap.entry.Worktree)
	if err != nil {
		log.Printf("[daemon] Agent %s: failed to assign new epic: %v (falling back to non-epic mode)",
			ap.entry.Worktree, err)
		return d.switchToNonEpicMode(ap)
	}

	if newEpicID == "" {
		// No more epics available — fall back to non-epic mode
		log.Printf("[daemon] Agent %s: no more epics with ready tasks, switching to non-epic mode",
			ap.entry.Worktree)
		return d.switchToNonEpicMode(ap)
	}

	// Switch branch to new epic before updating state
	log.Printf("[daemon] Agent %s: transitioning from epic %s to epic %s",
		ap.entry.Worktree, currentEpicID, newEpicID)

	targetBranch := epicBranchName(newEpicID)
	if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, "origin/main"); err != nil {
		log.Printf("[daemon] Agent %s: branch switch to %s failed: %v",
			ap.entry.Worktree, targetBranch, err)
		// Roll back the assignment since branch switch failed
		d.epicAssigner.ReleaseWorktree(ap.entry.Worktree)
		return err
	}

	// Update state only after successful branch switch
	ap.mu.Lock()
	ap.assignedEpicID = newEpicID
	ap.restartCount = 0 // reset restart counter on epic switch
	ap.mu.Unlock()

	return nil
}

// switchToNonEpicMode clears the epic assignment and switches the worktree
// to the agent-name branch for non-epic batch mode.
func (d *Daemon) switchToNonEpicMode(ap *AgentProcess) error {
	// Clear epic assignment first (so the next spawn uses non-epic mode)
	ap.mu.Lock()
	ap.assignedEpicID = ""
	ap.mu.Unlock()

	// Switch to agent-name branch
	targetBranch := ap.entry.Worktree
	if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, "origin/main"); err != nil {
		log.Printf("[daemon] Agent %s: branch switch to %s failed: %v",
			ap.entry.Worktree, targetBranch, err)
		return err
	}

	// Reset restart counter only after successful branch switch
	ap.mu.Lock()
	ap.restartCount = 0
	ap.mu.Unlock()

	return nil
}
