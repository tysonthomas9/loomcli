package cli

import (
	"context"
	"fmt"
	"log"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/events"
)

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
// With config-driven epic assignment (parent field), the agent stays on its
// assigned epic. When exhausted, it logs and stops — no automatic reassignment.
// Called from superviseAgent() after post-mortem recovery and before the restart decision.
func (d *Daemon) handleEpicTransition(ap *AgentProcess) {
	ap.mu.Lock()
	currentEpicID := ap.assignedEpicID
	ap.mu.Unlock()
	if currentEpicID == "" {
		return
	}

	// ap.entry is immutable for the lifetime of this AgentProcess: the reconciler
	// always drains (waits for superviseAgent to exit) before creating a replacement
	// AgentProcess with the new entry. No lock needed here.
	if ap.entry.Parent == "" {
		log.Printf("[daemon] Agent %s: unexpected assignedEpicID=%q with no parent config, clearing",
			ap.entry.Worktree, currentEpicID)
		ap.mu.Lock()
		ap.assignedEpicID = ""
		ap.mu.Unlock()
		return
	}

	hasReady, err := epicHasReadyTasks(currentEpicID)
	if err != nil {
		log.Printf("[daemon] Agent %s: failed to check epic %s for ready tasks: %v",
			ap.entry.Worktree, currentEpicID, err)
		return
	}
	if !hasReady {
		log.Printf("[daemon] Agent %s: configured epic %s exhausted (no ready tasks), agent will idle",
			ap.entry.Worktree, currentEpicID)
		if evt, err := events.NewEvent(events.EpicExhausted, ap.entry.Worktree, ap.entry.Role, currentEpicID, events.EpicExhaustedData{EpicID: currentEpicID}); err == nil {
			d.emitEvent(evt)
		}

		// Resolve backend for error classification (matching classifyAgentExit pattern)
		backend := ap.entry.Backend
		if backend == "" {
			backend = d.configSnapshot().Backend
		}

		ap.mu.Lock()
		// Only set NoWork if lastError isn't already a real crash error —
		// don't mask a legitimate failure with an exhaustion signal.
		if ap.lastError == nil || ap.lastError.Class == agenterr.NoWork {
			ap.lastError = &agenterr.AgentError{
				Class:   agenterr.NoWork,
				Message: "configured epic exhausted",
				Backend: backend,
			}
		}
		ap.lastNoWork = true
		ap.mu.Unlock()
	}
}
