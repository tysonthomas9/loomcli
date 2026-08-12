package sessioncoord

import (
	"os"
	"strings"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// serveFromEventStoreEnabled reports F3 (env LOOM_SERVE_FROM_EVENTSTORE): whether
// the Runs tab serves transcripts from the per-run event store (with native
// fallback) instead of the native file. Default OFF ⇒ serving is unchanged.
// (Per-workspace gating is a later refinement; this is a global rollout switch.)
func serveFromEventStoreEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_SERVE_FROM_EVENTSTORE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// eventStoreHasTranscript reports whether the session's event store holds events
// (the F3 contributor to has_transcript; native disk-truth remains the fallback).
func eventStoreHasTranscript(store *sessions.Store, sessionID string) bool {
	return serveFromEventStoreEnabled() && store.HasEventTranscript(sessionID)
}

// loomEvent maps a wrapper transcript Event (the event store's canonical form) to
// loom's serving DTO. The public fields are identical; the wrapper's internal
// Source/NativeID/SchemaVersion are not part of loom's Event.
func loomEvent(e hwtranscript.Event) transcript.Event {
	return transcript.FromWrapper(e)
}

// eventStoreParentEvents returns the PARENT-conversation events (empty
// ParentSessionID) from the session's event store, or (nil, false) when F3 is
// off or the store is empty — so the caller falls back to the native reader.
func eventStoreParentEvents(store *sessions.Store, sessionID string) ([]transcript.Event, bool) {
	return eventStoreEventsMatching(store, sessionID, func(env hwtranscript.EventEnvelope) bool {
		return env.ParentSessionID == ""
	})
}

// eventStoreSubagentEvents returns the events for one subagent (matched by its
// native session id), or (nil, false) when F3 is off / absent.
func eventStoreSubagentEvents(store *sessions.Store, sessionID, subagentID string) ([]transcript.Event, bool) {
	if subagentID == "" {
		return nil, false
	}
	return eventStoreEventsMatching(store, sessionID, func(env hwtranscript.EventEnvelope) bool {
		return env.HarnessSessionID == subagentID
	})
}

func eventStoreSubagentIDs(store *sessions.Store, sessionID string) ([]string, bool) {
	if !serveFromEventStoreEnabled() {
		return nil, false
	}
	envs, err := store.LoadEnvelopes(sessionID)
	if err != nil || len(envs) == 0 {
		return nil, false
	}
	seen := make(map[string]struct{})
	var out []string
	for _, env := range envs {
		if env.ParentSessionID == "" || env.HarnessSessionID == "" {
			continue
		}
		if _, ok := seen[env.HarnessSessionID]; ok {
			continue
		}
		seen[env.HarnessSessionID] = struct{}{}
		out = append(out, env.HarnessSessionID)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func eventStoreEventsMatching(store *sessions.Store, sessionID string, keep func(hwtranscript.EventEnvelope) bool) ([]transcript.Event, bool) {
	if !serveFromEventStoreEnabled() {
		return nil, false
	}
	envs, err := store.LoadEnvelopes(sessionID)
	if err != nil || len(envs) == 0 {
		return nil, false
	}
	out := make([]transcript.Event, 0, len(envs))
	for _, env := range envs {
		if keep(env) {
			out = append(out, loomEvent(env.Event))
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
