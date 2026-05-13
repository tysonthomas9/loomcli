package sessions

import (
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
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		recordErr(span, err)
		return nil, fmt.Errorf("stat native transcript: %w", err)
	}

	events, err := backends.ParseEventsFromFile(meta.Backend, path)
	if err != nil {
		recordErr(span, err)
		return events, err
	}
	span.SetAttributes(attrResultCount(len(events)))
	return events, nil
}
