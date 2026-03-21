package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// dispatchHookEvent maps a parsed HookEvent to a sessions.TranscriptEntry
// and appends it to the session's transcript. It is designed for use inside
// hook handlers: errors are logged to stderr and the function always returns
// nil so the hook process exits 0.
//
// Returns nil immediately (no-op) when:
//   - event is nil (unhandled hook type)
//   - beadsDir or sessionID is empty (non-loom Claude session)
func dispatchHookEvent(event *HookEvent, beadsDir, sessionID string) error { //nolint:unparam // always nil by design: hooks must exit 0
	// Nil event means ParseClaudeHookInput returned nil for an unrecognized
	// hook name. This is expected and not an error.
	if event == nil {
		return nil
	}

	// If either env var is missing, this Claude session was not started by
	// loom (e.g., a developer using Claude Code directly). Exit silently.
	if beadsDir == "" || sessionID == "" {
		return nil
	}

	store, err := sessions.NewStore(beadsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to create session store: %v\n", err)
		return nil
	}

	entry, ok := mapEventToEntry(event)
	if !ok {
		fmt.Fprintf(os.Stderr, "loom hook: unhandled event type %v, skipping\n", event.Type)
		return nil
	}

	if err := store.AppendTranscript(sessionID, entry); err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to append transcript: %v\n", err)
		return nil
	}

	return nil
}

// mapEventToEntry converts a backend-agnostic HookEvent into a TranscriptEntry.
// Returns false if the event type is unrecognized (caller should skip).
func mapEventToEntry(event *HookEvent) (sessions.TranscriptEntry, bool) {
	entry := sessions.TranscriptEntry{
		Timestamp: time.Now().UTC(),
	}

	switch event.Type {
	case HookSessionStart:
		entry.Role = "system"
		entry.Type = "session_start"
		entry.Content = fmt.Sprintf("Session started (model: %s)", event.Model)

	case HookTurnStart:
		entry.Role = "user"
		entry.Type = "text"
		entry.Content = event.Prompt

	case HookTurnEnd:
		entry.Role = "assistant"
		entry.Type = "turn_end"
		entry.Content = "Turn completed"

	case HookSessionEnd:
		entry.Role = "system"
		entry.Type = "session_end"
		entry.Content = "Session ended"

	default:
		return entry, false
	}

	return entry, true
}
