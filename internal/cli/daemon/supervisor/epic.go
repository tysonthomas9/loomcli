package supervisor

import (
	"log"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// handleEpicTransition checks if the current epic has remaining ready tasks.
// With config-driven epic assignment (parent field), the agent stays on its
// assigned epic. When exhausted, it logs and stops — no automatic reassignment.
// Called from superviseAgent() after post-mortem recovery and before the restart decision.
func (s *Supervisor) handleEpicTransition(ap *AgentProcess) {
	ap.Mu.Lock()
	currentEpicID := ap.AssignedEpicID
	ap.Mu.Unlock()
	if currentEpicID == "" {
		return
	}

	// ap.Entry is immutable for the lifetime of this AgentProcess: the reconciler
	// always drains (waits for superviseAgent to exit) before creating a replacement
	// AgentProcess with the new entry. No lock needed here.
	if ap.Entry.Parent == "" {
		log.Printf("[daemon] Agent %s: unexpected assignedEpicID=%q with no parent config, clearing",
			ap.Entry.Worktree, currentEpicID)
		ap.Mu.Lock()
		ap.AssignedEpicID = ""
		ap.Mu.Unlock()
		return
	}

	hasReady, err := s.IssueBackendReady(currentEpicID)
	if err != nil {
		log.Printf("[daemon] Agent %s: failed to check epic %s for ready tasks: %v",
			ap.Entry.Worktree, currentEpicID, err)
		return
	}
	if !hasReady {
		log.Printf("[daemon] Agent %s: configured epic %s exhausted (no ready tasks), agent will idle",
			ap.Entry.Worktree, currentEpicID)
		if evt, err := events.NewEvent(events.EpicExhausted, ap.Entry.Worktree, ap.Entry.Role, currentEpicID, events.EpicExhaustedData{EpicID: currentEpicID}); err == nil {
			s.EmitEvent(evt)
		}

		// Resolve backend for error classification (matching classifyAgentExit pattern)
		backend := ap.Entry.Backend
		if backend == "" {
			backend = s.ConfigSnapshot().Backend
		}

		ap.Mu.Lock()
		// Only set NoWork if LastError isn't already a real crash error —
		// don't mask a legitimate failure with an exhaustion signal.
		if ap.LastError == nil || ap.LastError.Class == agenterr.NoWork {
			ap.LastError = &agenterr.AgentError{
				Class:   agenterr.NoWork,
				Message: "configured epic exhausted",
				Backend: backend,
			}
		}
		ap.LastNoWork = true
		ap.Mu.Unlock()
	}
}
