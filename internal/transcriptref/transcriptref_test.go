package transcriptref

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type fakeArtifactStore struct {
	store.ArtifactStore
	readData []byte
	readErr  error
	artifact *domain.Artifact
	gotID    string
}

func (s *fakeArtifactStore) ReadContent(_ context.Context, _ string, artifactID string) ([]byte, error) {
	s.gotID = artifactID
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.readData, nil
}

func (s *fakeArtifactStore) Get(_ context.Context, _ string, artifactID string) (*domain.Artifact, error) {
	s.gotID = artifactID
	if s.artifact == nil {
		return nil, domain.ErrNotFound
	}
	return s.artifact, nil
}

func TestResolveArtifactPrefersReadContent(t *testing.T) {
	artifacts := &fakeArtifactStore{readData: []byte(`{"role":"assistant","type":"text","text":"ok"}` + "\n")}
	got, err := Resolve(t.Context(), artifacts, "WS", "artifact://transcript-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != string(artifacts.readData) {
		t.Fatalf("got %q, want %q", got, artifacts.readData)
	}
	if artifacts.gotID != "transcript-1" {
		t.Fatalf("artifact id = %q, want transcript-1", artifacts.gotID)
	}
}

func TestResolveArtifactFallsBackToMetadataURIOnNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	want := []byte(`{"role":"assistant","type":"text","text":"fallback"}` + "\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	artifacts := &fakeArtifactStore{
		readErr:  domain.ErrNotFound,
		artifact: &domain.Artifact{ArtifactID: "transcript-1", URI: "file://" + path},
	}
	got, err := Resolve(t.Context(), artifacts, "WS", "artifact://transcript-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveArtifactReadContentErrorDoesNotFallback(t *testing.T) {
	wantErr := errors.New("backend unavailable")
	artifacts := &fakeArtifactStore{
		readErr:  wantErr,
		artifact: &domain.Artifact{ArtifactID: "transcript-1", URI: "file:///should-not-read"},
	}
	_, err := Resolve(t.Context(), artifacts, "WS", "artifact://transcript-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestResolveHTTPRejectsTooLarge(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxControlPlaneTranscriptBytes+1))
	}))
	defer ts.Close()

	_, err := Resolve(t.Context(), nil, "WS", ts.URL)
	if err == nil || err.Error() != "transcript is too large" {
		t.Fatalf("err = %v, want transcript is too large", err)
	}
}

func TestParseCanonicalTranscriptBytesAcceptsArrayAndJSONL(t *testing.T) {
	arrayEvents, err := ParseCanonicalTranscriptBytes([]byte(`[{"seq":1,"role":"assistant","type":"text","text":"array"}]`))
	if err != nil {
		t.Fatalf("Parse array: %v", err)
	}
	if len(arrayEvents) != 1 || arrayEvents[0].Text != "array" {
		t.Fatalf("array events = %+v", arrayEvents)
	}

	jsonlEvents, err := ParseCanonicalTranscriptBytes([]byte(
		`{"seq":1,"role":"assistant","type":"text","text":"line1"}` + "\n\n" +
			`{"seq":2,"role":"user","type":"text","text":"line2"}` + "\n",
	))
	if err != nil {
		t.Fatalf("Parse JSONL: %v", err)
	}
	if len(jsonlEvents) != 2 || jsonlEvents[1].Text != "line2" {
		t.Fatalf("jsonl events = %+v", jsonlEvents)
	}
}

func TestParseTranscriptBytesConvertsNativeCodexRollout(t *testing.T) {
	data, err := os.ReadFile("../../docs/design/fixtures/agent-observability/sessions/20260716-200554-codex-coder--9152880d/agent_transcript.jsonl")
	if err != nil {
		t.Fatalf("read codex fixture: %v", err)
	}
	events, err := ParseTranscriptBytes("codex", data)
	if err != nil {
		t.Fatalf("ParseTranscriptBytes codex: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events from native codex rollout")
	}
	withPayload := 0
	for _, ev := range events {
		if !transcript.KnownEventTypes[ev.Type] {
			t.Fatalf("non-canonical event type %q", ev.Type)
		}
		if ev.Text != "" || ev.ToolName != "" || ev.Output != "" {
			withPayload++
		}
	}
	if withPayload == 0 {
		t.Fatal("converted codex events carry no payload; native conversion did not run")
	}
}

func TestParseTranscriptBytesConvertsModernCodexEventStream(t *testing.T) {
	data, err := os.ReadFile("../../docs/design/fixtures/agent-observability/modern-codex-event-stream.jsonl")
	if err != nil {
		t.Fatalf("read modern codex fixture: %v", err)
	}
	events, err := ParseTranscriptBytes("codex", data)
	if err != nil {
		t.Fatalf("ParseTranscriptBytes modern codex: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one agent message", events)
	}
	if events[0].Role != transcript.RoleAssistant || events[0].Type != transcript.EventText || !strings.Contains(events[0].Text, "false_success_claim") {
		t.Fatalf("modern codex event = %+v", events[0])
	}
}

func TestParseTranscriptBytesKeepsCanonicalJSONL(t *testing.T) {
	events, err := ParseTranscriptBytes("codex", []byte(
		`{"seq":1,"role":"assistant","type":"text","text":"line1"}`+"\n"+
			`{"seq":2,"role":"user","type":"text","text":"line2"}`+"\n",
	))
	if err != nil {
		t.Fatalf("ParseTranscriptBytes canonical: %v", err)
	}
	if len(events) != 2 || events[0].Text != "line1" {
		t.Fatalf("canonical events = %+v", events)
	}
}
