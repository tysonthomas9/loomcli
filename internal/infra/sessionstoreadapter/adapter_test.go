package sessionstoreadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestAdapterOwnsLocalSessionMutations(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := Create(store, sessions.CreateOptions{AgentName: "planner", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(source, []byte("{\"type\":\"message\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SyncNativeTranscript(store, session.SessionID(), source, sessions.TranscriptFormatRaw); err != nil {
		t.Fatal(err)
	}
	metadata, err := store.LoadMetadata(session.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	metadata.Model = "gpt-5.6"
	if err := SaveMetadata(store, session.SessionID(), metadata); err != nil {
		t.Fatal(err)
	}
	if err := Finalize(session, sessions.FinalizeOptions{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.LoadMetadata(session.SessionID()); err != nil || got.Status != sessions.StatusCompleted {
		t.Fatalf("final metadata = %#v, %v", got, err)
	}
}

func TestEnvelopeAppenderRetainsSessionScopedSink(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := Create(store, sessions.CreateOptions{AgentName: "coder", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	appendEnvelope := EnvelopeAppender(store, session.SessionID())
	if err := appendEnvelope(transcript.EventEnvelope{
		RunID: "run-1",
		Event: transcript.Event{Seq: 1, Type: transcript.EventText, Text: "hello"},
	}); err != nil {
		t.Fatal(err)
	}
}
