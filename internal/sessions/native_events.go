package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"
	hwclaude "github.com/olesho/harness-wrapper/pkg/transcript/claudecode"
	hwcodex "github.com/olesho/harness-wrapper/pkg/transcript/codex"

	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// LoadNativeEvents parses a session's captured native transcript into the
// canonical backend-agnostic Event stream. The backend is resolved from the
// session's metadata.json so callers don't need to know the format up front.
//
// Returns (nil, nil) if the session has no native transcript yet (e.g., the
// first hook hasn't fired). Returns an error for I/O failures, malformed
// metadata, or an unsupported recorded backend.
func (s *Store) LoadNativeEvents(sessionID string) ([]transcript.Event, error) {
	_, span := startSpan(s.ctx, "service.Sessions.LoadNativeEvents",
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

	// Dispatch on the recorded format (SyncNativeTranscript stamps it). The TS leaf
	// writes a canonical transcript.Event stream; the Go-leaf hooks write the raw
	// backend stream. A legacy session has no marker — it predates canonical writes,
	// so it is raw by definition. No parse-then-guess: the marker is authoritative.
	if meta.TranscriptFormat == TranscriptFormatCanonical {
		events := decodeCanonicalEvents(data)
		span.SetAttributes(attrResultCount(len(events)))
		return events, nil
	}
	events, err := parseNativeEvents(meta.Backend, data)
	if err != nil {
		recordErr(span, err)
		return events, err
	}
	span.SetAttributes(attrResultCount(len(events)))
	return events, nil
}

// ParseNativeEventsFromFile reads and parses a raw backend transcript. A
// missing file has no events; an unknown backend fails closed instead of
// guessing a wire format.
func ParseNativeEventsFromFile(backend, path string) ([]transcript.Event, error) {
	data, err := os.ReadFile(path) //nolint:gosec // caller supplies a session-owned transcript path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read native transcript: %w", err)
	}
	return parseNativeEvents(backend, data)
}

func parseNativeEvents(backend string, data []byte) ([]transcript.Event, error) {
	if len(data) == 0 {
		return nil, nil
	}
	backend = strings.TrimSpace(backend)
	switch backend {
	case platformruntime.ProviderClaude:
		return parseClaudeEvents(data)
	case platformruntime.ProviderCodex:
		return parseCodexEvents(data)
	case platformruntime.ProviderOpenCode:
		return parseOpenCodeEvents(data)
	default:
		return nil, fmt.Errorf("unsupported native transcript backend %q", backend)
	}
}

func parseClaudeEvents(data []byte) ([]transcript.Event, error) {
	wrapperEvents, err := hwclaude.Events(data)
	if err != nil {
		return nil, fmt.Errorf("parse Claude transcript: %w", err)
	}
	return canonicalWrapperEvents(wrapperEvents), nil
}

func parseCodexEvents(data []byte) ([]transcript.Event, error) {
	wrapperEvents, err := hwcodex.Events(data)
	if err != nil {
		return nil, fmt.Errorf("parse Codex transcript: %w", err)
	}
	return canonicalWrapperEvents(wrapperEvents), nil
}

func canonicalWrapperEvents(wrapperEvents []hwtranscript.Event) []transcript.Event {
	events := make([]transcript.Event, len(wrapperEvents))
	for index, event := range wrapperEvents {
		events[index] = transcript.FromWrapper(event)
	}
	return events
}

// decodeCanonicalEvents decodes already-read agent_transcript.jsonl bytes that are
// themselves a stream of canonical transcript.Event objects (one per line).
// Returns nil unless EVERY non-blank line has a known type and role plus a
// non-zero timestamp — so a raw backend transcript that merely parsed to zero
// events (e.g. codex "response_item" lines) can never be misread as canonical.
// Seq is reassigned to file order so a session_meta head stays at index 0
// regardless of the source's seq.
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
		if transcript.ValidateCanonicalEvent(ev) != nil {
			return nil
		}
		ev.Seq = len(events)
		events = append(events, ev)
	}
	return events
}
