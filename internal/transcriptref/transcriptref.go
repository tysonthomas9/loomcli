package transcriptref

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript/backends"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const maxControlPlaneTranscriptBytes = 16 << 20

// Resolve resolves a transcript ref to raw bytes. Supported refs are
// artifact://<id>, file://<path>, and http(s)://<url>. artifacts may be nil
// when no artifact store is available.
func Resolve(ctx context.Context, artifacts store.ArtifactStore, wsID, ref string) ([]byte, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("empty transcript ref")
	}
	if strings.HasPrefix(ref, "artifact://") {
		artifactID := strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
		if artifactID == "" {
			return nil, errors.New("empty artifact transcript ref")
		}
		if artifacts == nil {
			return nil, errors.New("artifact store unavailable")
		}
		if reader, ok := artifacts.(store.ArtifactContentReader); ok {
			data, err := reader.ReadContent(ctx, wsID, artifactID)
			if err == nil {
				return data, nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return nil, err
			}
		}
		artifact, err := artifacts.Get(ctx, wsID, artifactID)
		if err != nil {
			return nil, err
		}
		return readURI(ctx, artifact.URI)
	}
	return readURI(ctx, ref)
}

func readURI(ctx context.Context, rawURI string) ([]byte, error) {
	rawURI = strings.TrimSpace(rawURI)
	switch {
	case strings.HasPrefix(rawURI, "file://"):
		parsed, err := url.Parse(rawURI)
		if err != nil {
			return nil, err
		}
		path := parsed.Path
		if path == "" {
			path = parsed.Host
		}
		if path == "" {
			return nil, errors.New("empty file transcript ref")
		}
		return os.ReadFile(path) //nolint:gosec // refs are emitted by the trusted runner/control-plane path.
	case strings.HasPrefix(rawURI, "http://"), strings.HasPrefix(rawURI, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURI, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, errors.New("transcript ref returned non-success status")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxControlPlaneTranscriptBytes+1))
		if err != nil {
			return nil, err
		}
		if len(body) > maxControlPlaneTranscriptBytes {
			return nil, errors.New("transcript is too large")
		}
		return body, nil
	default:
		return nil, errors.New("unsupported transcript ref")
	}
}

// ParseEventsFromFile parses an on-disk native transcript through the
// per-backend converter. Thin facade over the backends dispatcher so
// transcript consumers depend on one parse entry point.
func ParseEventsFromFile(backend, path string) ([]transcript.Event, error) {
	return backends.ParseEventsFromFile(backend, path) //nolint:wrapcheck // facade
}

// ParseTranscriptBytes parses resolved transcript bytes into canonical events.
// Uploaded transcripts come in two shapes: the TS-leaf host bridge uploads the
// canonical event stream, while the daemon supervisor and doctor backfill
// upload the backend-native transcript verbatim (e.g. the codex rollout
// JSONL). Native bytes decode "successfully" as canonical events with empty
// payloads, so shape is sniffed structurally and native data is routed through
// the per-backend converter.
func ParseTranscriptBytes(backend string, data []byte) ([]transcript.Event, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []transcript.Event{}, nil
	}
	if looksCanonicalTranscript(trimmed) {
		return ParseCanonicalTranscriptBytes(data)
	}
	events, err := backends.ParseEvents(backend, data)
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

// looksCanonicalTranscript reports whether the transcript bytes are already in
// the canonical event schema. Native formats wrap their content (codex rollout
// lines carry a top-level "payload", claude lines a top-level "message") and
// use type strings outside the canonical vocabulary; canonical session_meta
// collides with codex's session_meta line, so the wrapper keys are checked
// before the type vocabulary.
func looksCanonicalTranscript(trimmed []byte) bool {
	if trimmed[0] == '[' {
		return true
	}
	checked := 0
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var probe map[string]json.RawMessage
		if json.Unmarshal(line, &probe) != nil {
			return false
		}
		if _, ok := probe["payload"]; ok {
			return false
		}
		if _, ok := probe["message"]; ok {
			return false
		}
		var typed struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(line, &typed)
		if !transcript.KnownEventTypes[typed.Type] {
			return false
		}
		checked++
		if checked >= 5 {
			break
		}
	}
	return checked > 0
}

// ParseCanonicalTranscriptBytes parses canonical transcript bytes from either
// a JSON array or JSONL stream into transcript events.
func ParseCanonicalTranscriptBytes(data []byte) ([]transcript.Event, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []transcript.Event{}, nil
	}
	if trimmed[0] == '[' {
		var events []transcript.Event
		if err := json.Unmarshal(trimmed, &events); err != nil {
			return nil, err
		}
		return events, nil
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	events := make([]transcript.Event, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event transcript.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}
