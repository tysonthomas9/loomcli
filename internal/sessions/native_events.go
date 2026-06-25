package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/runtimectx"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript/backends"
)

// LoadNativeEvents parses a session's captured native transcript into the
// canonical backend-agnostic Event stream. The backend is resolved from the
// session's metadata.json so callers don't need to know the format up front.
//
// Returns (nil, nil) if the session has no native transcript yet (e.g., the
// first hook hasn't fired). Returns an error only for I/O failures or
// malformed metadata.
func (s *Store) LoadNativeEvents(sessionID string) ([]transcript.Event, error) {
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.LoadNativeEvents",
		attrLoomSessionID(sessionID),
	)
	defer span.End()

	meta, err := s.LoadMetadata(sessionID)
	if err != nil {
		recordErr(span, err)
		return nil, fmt.Errorf("load metadata: %w", err)
	}
	if meta != nil && meta.Backend != "" {
		span.SetAttributes(attrLoomBackend(meta.Backend))
	}

	path := s.NativeTranscriptPath(sessionID)
	data, err := os.ReadFile(path) //nolint:gosec // path is the session-owned transcript file
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		recordErr(span, err)
		return nil, fmt.Errorf("read native transcript: %w", err)
	}

	events, err := backends.ParseEvents(meta.Backend, data)
	if err != nil {
		recordErr(span, err)
		return events, err
	}
	// The daemon TS leaf writes its transcript ALREADY in the canonical Event
	// format (it returns parsed transcript_entries; SyncNativeTranscript copies them
	// verbatim), not the raw backend stream the Go-leaf hooks capture. The
	// backend-specific parser (keyed on meta.Backend) only understands the raw
	// stream, so it yields zero events for a canonical file. Decode the same bytes
	// directly in that case. The Go-leaf raw-stream path is unaffected: it parses to
	// >0 events and never reaches this fallback.
	if len(events) == 0 {
		if canonical := decodeCanonicalEvents(data); len(canonical) > 0 {
			events = canonical
		}
	}
	span.SetAttributes(attrResultCount(len(events)))
	return events, nil
}

// decodeCanonicalEvents decodes already-read agent_transcript.jsonl bytes that are
// themselves a stream of canonical transcript.Event objects (one per line). Returns
// nil unless EVERY non-blank line is a canonical event (known type + role) — so a
// raw backend transcript that merely parsed to zero events (e.g. codex
// "response_item" lines) can never be misread as canonical. Seq is reassigned to
// file order so a session_meta head stays at index 0 regardless of the source's seq.
func decodeCanonicalEvents(data []byte) []transcript.Event {
	var events []transcript.Event
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev transcript.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil
		}
		if !transcript.KnownEventTypes[ev.Type] || !transcript.KnownRoles[ev.Role] {
			return nil
		}
		ev.Seq = len(events)
		events = append(events, ev)
	}
	return events
}
