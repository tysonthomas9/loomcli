package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

func TestArchiveOwnsLocalSessionLifecycle(t *testing.T) {
	archive, err := OpenArchive(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := archive.Begin(CreateOptions{AgentName: "planner", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(source, []byte("{\"type\":\"message\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := archive.Capture(TranscriptCapture{
		SessionID: session.SessionID(), SourcePath: source, Format: TranscriptFormatRaw,
	}); err != nil {
		t.Fatal(err)
	}
	metadata, err := archive.LoadMetadata(session.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	metadata.Model = "gpt-5.6"
	if err := archive.UpdateMetadata(MetadataUpdate{
		SessionID: session.SessionID(), Metadata: metadata,
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := archive.LoadMetadata(session.SessionID())
	if err != nil || updated.Model != "gpt-5.6" {
		t.Fatalf("updated metadata = %#v, %v", updated, err)
	}
	if err := session.Finalize(FinalizeOptions{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	got, err := archive.LoadMetadata(session.SessionID())
	if err != nil || got.Status != StatusCompleted {
		t.Fatalf("final metadata = %#v, %v", got, err)
	}
}

func TestArchiveCleanupPreviewAndApply(t *testing.T) {
	archive, err := OpenArchive(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := archive.Begin(CreateOptions{AgentName: "coder", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Finalize(FinalizeOptions{ExitCode: 0}); err != nil {
		t.Fatal(err)
	}

	preview, err := archive.Cleanup(CleanupOptions{OlderThan: -time.Hour, DryRun: true, Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Purged != 1 {
		t.Fatalf("preview = %+v, want one purge", preview)
	}
	applied, err := archive.Cleanup(CleanupOptions{OlderThan: -time.Hour, Compact: true})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Purged != 1 {
		t.Fatalf("applied = %+v, want one purge", applied)
	}
	records, err := archive.Query(Filter{})
	if err != nil || len(records) != 0 {
		t.Fatalf("records after purge = %+v, %v", records, err)
	}
}

func TestArchiveAppendsSessionScopedEnvelope(t *testing.T) {
	archive, err := OpenArchive(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := archive.Begin(CreateOptions{AgentName: "coder", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	envelope := hwtranscript.EventEnvelope{
		RunID: "run-1",
		Event: hwtranscript.Event{Seq: 1, Type: transcript.EventText, Text: "hello"},
	}
	if err := archive.AppendEnvelope(session.SessionID(), envelope); err != nil {
		t.Fatal(err)
	}
	stored, err := archive.LoadEnvelopes(session.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].RunID != "run-1" || stored[0].Event.Text != "hello" {
		t.Fatalf("stored envelopes = %+v", stored)
	}
}
