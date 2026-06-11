package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// dispatchHookEvent captures the backend's native transcript (and any
// completed subagent transcripts) into the loom session directory, and on
// every hook event patches the session metadata with token usage (throttled;
// SessionEnd always captures).
//
// Designed for use inside hook subprocesses: errors are logged to stderr and
// the function always returns nil so the hook process exits 0. Returns nil
// immediately (no-op) when event is nil, or when runtimeDir / sessionID are
// missing (non-loom agent session).
func dispatchHookEvent(event *HookEvent, runtimeDir, sessionID string) error { //nolint:unparam // always nil by design: hooks must exit 0
	if event == nil {
		return nil
	}
	if runtimeDir == "" || sessionID == "" {
		return nil
	}

	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to create session store: %v\n", err)
		return nil
	}

	captureTokens := false
	if event.SessionRef != "" {
		captureTokens = event.Type == HookSessionEnd || shouldCaptureTokenUsage(store, sessionID, tokenUsageThrottle)
	}

	// Mirror the backend's native JSONL transcript into the session directory.
	// The capture is idempotent (full snapshot copy, skip-if-unchanged), so
	// calling on every hook event is safe and keeps the UI's view close to
	// the agent's live progress.
	if event.SessionRef != "" {
		if err := store.SyncNativeTranscript(sessionID, event.SessionRef, sessions.TranscriptFormatRaw); err != nil {
			fmt.Fprintf(os.Stderr, "loom hook: failed to sync native transcript: %v\n", err)
		}
	}

	// When a Task subagent completes, Claude Code has written its transcript
	// at <parent_dir>/subagents/agent-<subagentID>.jsonl. Mirror that into
	// sessions/<sid>/subagents/ so the UI can render nested subagent work.
	if event.Type == HookSubagentEnd && event.SubagentID != "" && event.SessionRef != "" {
		subPath := deriveSubagentPath(event.SessionRef, event.SubagentID)
		if err := store.SyncSubagentTranscript(sessionID, event.SubagentID, subPath); err != nil {
			fmt.Fprintf(os.Stderr, "loom hook: failed to sync subagent transcript: %v\n", err)
		}
	}

	// Capture token usage from Claude's transcript on every event that
	// carries a transcript reference, so metadata token/cost fields tick up
	// live during a run. Throttled by metadata.json mtime to bound full
	// transcript rescans; SessionEnd is always captured (final authoritative
	// sum).
	if captureTokens {
		captureTokenUsage(store, sessionID, event.SessionRef, event.Backend)
	}

	return nil
}

// deriveSubagentPath returns the on-disk path to a spawned subagent's JSONL
// transcript. Claude Code writes them to
//
//	<parent_transcript_dir>/subagents/agent-<subagentID>.jsonl
func deriveSubagentPath(parentTranscriptPath, subagentID string) string {
	if parentTranscriptPath == "" || subagentID == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(parentTranscriptPath), "subagents", "agent-"+subagentID+".jsonl")
}

// tokenUsageThrottle is the minimum interval between live (non-SessionEnd)
// token-usage captures for a session. Each capture rescans the full
// transcript, so the throttle bounds the rescan rate.
const tokenUsageThrottle = 10 * time.Second

// shouldCaptureTokenUsage reports whether enough time has passed since the
// session's metadata.json was last written to justify another transcript
// rescan. A missing or unreadable metadata file counts as stale (capture
// proceeds and surfaces any real error there; LoadMetadata also validates
// the session ID, so a malformed ID is rejected on that path).
//
// Best-effort throttle: the mtime reflects any metadata write (not only
// token captures), and two concurrent hook subprocesses can both pass the
// check — both then compute identical sums from the same transcript, so
// this only costs an extra rescan, never incorrect data.
func shouldCaptureTokenUsage(store *sessions.Store, sessionID string, throttle time.Duration) bool {
	if strings.ContainsAny(sessionID, "/\\") {
		return false
	}
	info, err := os.Stat(filepath.Join(store.Dir(), sessionID, "metadata.json"))
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) >= throttle
}

// captureTokenUsage reads the Claude transcript, sums token usage, and
// patches session metadata. Errors are logged to stderr and never propagated.
func captureTokenUsage(store *sessions.Store, sessionID, transcriptPath, backend string) {
	tok, err := sessions.SumTranscriptUsage(transcriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to sum transcript usage: %v\n", err)
		return
	}
	if tok.InputTokens == 0 && tok.OutputTokens == 0 &&
		tok.CacheReadTokens == 0 && tok.CacheWriteTokens == 0 {
		return
	}

	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to load metadata for token capture: %v\n", err)
		return
	}

	meta.InputTokens = tok.InputTokens
	meta.OutputTokens = tok.OutputTokens
	meta.CacheReadTokens = tok.CacheReadTokens
	meta.CacheWriteTokens = tok.CacheWriteTokens

	tier := usage.ResolvePricing(backend)
	meta.EstimatedCostUSD = usage.EstimateCost(tier, usage.SessionUsage{
		InputTokens:      tok.InputTokens,
		OutputTokens:     tok.OutputTokens,
		CacheReadTokens:  tok.CacheReadTokens,
		CacheWriteTokens: tok.CacheWriteTokens,
	})

	if err := store.SaveMetadata(sessionID, meta); err != nil {
		fmt.Fprintf(os.Stderr, "loom hook: failed to save metadata with token usage: %v\n", err)
	}
}
