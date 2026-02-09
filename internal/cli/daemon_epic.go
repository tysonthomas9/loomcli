package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
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
	result := execCommand(GetBeadsDir(), "bd", "list", "--type=epic", "--status=open", "--json", "--limit", "0")
	if result.Err != nil {
		return nil, fmt.Errorf("bd list failed: %w", result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse epic list: %w", err)
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
	result := execCommand(GetBeadsDir(), "bd", "ready", "--parent", epicID, "--json", "--limit", "1")
	if result.Err != nil {
		return false, fmt.Errorf("bd ready failed for epic %s: %w", epicID, result.Err)
	}

	var issues []BdIssue
	if err := json.Unmarshal([]byte(result.Stdout), &issues); err != nil {
		return false, fmt.Errorf("failed to parse ready tasks for epic %s: %w", epicID, err)
	}
	return len(issues) > 0, nil
}
