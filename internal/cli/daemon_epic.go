package cli

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/events"
)

// EpicAssigner manages epic-to-worktree assignments for the daemon.
// It queries open epics and assigns each available worktree to the
// highest-priority epic with ready tasks.
type EpicAssigner struct {
	mu          sync.Mutex
	assignments map[string]string // worktreeName -> epicID
}

// EpicInfo holds parsed epic data for assignment ranking.
type EpicInfo struct {
	ID             string
	Priority       int
	ReadyTaskCount int
}

// NewEpicAssigner creates a new EpicAssigner.
func NewEpicAssigner() *EpicAssigner {
	return &EpicAssigner{
		assignments: make(map[string]string),
	}
}

// AssignWorktree queries open epics, skips already-assigned ones, and returns
// the highest-priority unassigned epic with ready tasks. Returns empty string
// if no epics are available (agent falls back to non-epic mode).
func (ea *EpicAssigner) AssignWorktree(worktreeName string) (string, error) {
	ea.mu.Lock()
	defer ea.mu.Unlock()

	// If already assigned, return existing assignment
	if epicID, ok := ea.assignments[worktreeName]; ok {
		return epicID, nil
	}

	// Query open epics
	epics, err := queryOpenEpics()
	if err != nil {
		return "", fmt.Errorf("failed to query open epics: %w", err)
	}

	if len(epics) == 0 {
		return "", nil
	}

	// Build set of already-assigned epic IDs
	assigned := make(map[string]bool, len(ea.assignments))
	for _, epicID := range ea.assignments {
		assigned[epicID] = true
	}

	// Filter to unassigned epics with ready tasks
	var candidates []EpicInfo
	for _, epic := range epics {
		if assigned[epic.ID] {
			continue
		}
		hasReady, err := epicHasReadyTasks(epic.ID)
		if err != nil {
			log.Printf("[daemon] Warning: failed to check ready tasks for epic %s: %v", epic.ID, err)
			continue
		}
		if !hasReady {
			continue
		}
		candidates = append(candidates, epic)
	}

	if len(candidates) == 0 {
		return "", nil
	}

	// Sort by priority (lower = higher priority), then by ID for stability
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})

	// Assign the highest-priority epic
	chosen := candidates[0].ID
	ea.assignments[worktreeName] = chosen
	log.Printf("[daemon] Assigned worktree %s to epic %s", worktreeName, chosen)
	return chosen, nil
}

// ReleaseWorktree removes the assignment for a worktree, making its epic
// available for reassignment.
func (ea *EpicAssigner) ReleaseWorktree(worktreeName string) {
	ea.mu.Lock()
	defer ea.mu.Unlock()

	if epicID, ok := ea.assignments[worktreeName]; ok {
		log.Printf("[daemon] Released worktree %s from epic %s", worktreeName, epicID)
		delete(ea.assignments, worktreeName)
	}
}

// GetAssignment returns the current epic ID for a worktree, or empty string.
func (ea *EpicAssigner) GetAssignment(worktreeName string) string {
	ea.mu.Lock()
	defer ea.mu.Unlock()
	return ea.assignments[worktreeName]
}

// Assignments returns a copy of the full assignment map (for status/logging).
func (ea *EpicAssigner) Assignments() map[string]string {
	ea.mu.Lock()
	defer ea.mu.Unlock()

	result := make(map[string]string, len(ea.assignments))
	for k, v := range ea.assignments {
		result[k] = v
	}
	return result
}

// queryOpenEpics runs `bd list --type=epic --status=open --json` and parses results.
var queryOpenEpics = defaultQueryOpenEpics

func defaultQueryOpenEpics() ([]EpicInfo, error) {
	tracker := defaultTracker()
	issues, err := tracker.List(context.Background(), ListOpts{Type: "epic", Status: "open", Limit: 0})
	if err != nil {
		return nil, fmt.Errorf("failed to list open epics: %w", err)
	}
	epics := make([]EpicInfo, 0, len(issues))
	for _, issue := range issues {
		epics = append(epics, EpicInfo{
			ID:       issue.ID,
			Priority: issue.Priority,
		})
	}
	return epics, nil
}

// epicHasReadyTasks runs `bd ready --parent <epicID> --json --limit 1` and returns
// true if there are any ready tasks under the epic.
var epicHasReadyTasks = defaultEpicHasReadyTasks

func defaultEpicHasReadyTasks(epicID string) (bool, error) {
	tracker := defaultTracker()
	issues, err := tracker.Ready(context.Background(), ReadyOpts{ParentID: epicID, Limit: 1})
	if err != nil {
		return false, fmt.Errorf("failed to check ready tasks for epic %s: %w", epicID, err)
	}
	return len(issues) > 0, nil
}

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

	// Emit epic_exhausted event
	if evt, err := events.NewEvent(events.EpicExhausted, ap.entry.Worktree, ap.entry.Role, currentEpicID, events.EpicExhaustedData{EpicID: currentEpicID}); err == nil {
		d.emitEvent(evt)
	}

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
	if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, ap.resolveRemote(), ap.resolveRemoteBranch()); err != nil {
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
	if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, ap.resolveRemote(), ap.resolveRemoteBranch()); err != nil {
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
