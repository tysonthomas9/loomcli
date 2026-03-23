package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
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

	// On SessionEnd, capture token usage from the backend's transcript file.
	if event.Type == HookSessionEnd && event.SessionRef != "" {
		captureTokenUsage(store, sessionID, event.SessionRef, event.Backend)
	}

	return nil
}

// captureTokenUsage reads the backend's transcript file, sums token usage,
// computes estimated cost, and patches the session metadata. All errors are
// logged to stderr; this function never returns an error (hooks must exit 0).
func captureTokenUsage(store *sessions.Store, sessionID, transcriptPath, backend string) {
	tokenUsage, err := sessions.SumTranscriptUsage(transcriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to sum transcript usage: %v\n", err)
		return
	}

	if tokenUsage.InputTokens == 0 && tokenUsage.OutputTokens == 0 {
		return
	}

	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to load metadata for token usage: %v\n", err)
		return
	}

	meta.InputTokens = tokenUsage.InputTokens
	meta.OutputTokens = tokenUsage.OutputTokens
	meta.CacheReadTokens = tokenUsage.CacheReadTokens
	meta.CacheWriteTokens = tokenUsage.CacheWriteTokens

	tier := usage.ResolvePricing(backend)
	meta.EstimatedCostUSD = usage.EstimateCost(tier, usage.SessionUsage{
		InputTokens:      tokenUsage.InputTokens,
		OutputTokens:     tokenUsage.OutputTokens,
		CacheReadTokens:  tokenUsage.CacheReadTokens,
		CacheWriteTokens: tokenUsage.CacheWriteTokens,
	})

	if err := store.SaveMetadata(sessionID, meta); err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to save metadata with token usage: %v\n", err)
	}
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

	case HookSubagentStart:
		entry.Role = "system"
		entry.Type = "subagent_start"
		entry.ToolName = "Task"
		entry.ToolInput = string(event.ToolInput)
		entry.Content = fmt.Sprintf("Subagent started (tool_use_id: %s)", event.ToolUseID)

	case HookSubagentEnd:
		entry.Role = "system"
		entry.Type = "subagent_end"
		entry.ToolName = "Task"
		entry.ToolInput = string(event.ToolInput)
		if event.SubagentID != "" {
			entry.Content = fmt.Sprintf("Subagent completed (tool_use_id: %s, agent_id: %s)", event.ToolUseID, event.SubagentID)
		} else {
			entry.Content = fmt.Sprintf("Subagent completed (tool_use_id: %s)", event.ToolUseID)
		}

	default:
		return entry, false
	}

	return entry, true
}
