package backends

import (
	"testing"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	"github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

func TestTranscriptModeFromEnv(t *testing.T) {
	cases := map[string]hwharness.Mode{
		"":            hwharness.TranscriptOff,
		"off":         hwharness.TranscriptOff,
		"garbage":     hwharness.TranscriptOff,
		"stream":      hwharness.TranscriptStreamParse,
		"streamparse": hwharness.TranscriptStreamParse,
		"hooks":       hwharness.TranscriptHooks,
		"auto":        hwharness.TranscriptAuto,
		"AUTO":        hwharness.TranscriptAuto,
	}
	for in, want := range cases {
		t.Setenv("LOOM_TRANSCRIPT_MODE", in)
		if got := transcriptModeFromEnv(); got != want {
			t.Errorf("LOOM_TRANSCRIPT_MODE=%q ⇒ %v, want %v", in, got, want)
		}
	}
}

func TestEventStoreWriteEnabled(t *testing.T) {
	for _, on := range []string{"1", "true", "yes", "on", "TRUE"} {
		t.Setenv("LOOM_EVENTSTORE_WRITE", on)
		if !eventStoreWriteEnabled() {
			t.Errorf("LOOM_EVENTSTORE_WRITE=%q ⇒ false, want true", on)
		}
	}
	for _, off := range []string{"", "0", "false", "no"} {
		t.Setenv("LOOM_EVENTSTORE_WRITE", off)
		if eventStoreWriteEnabled() {
			t.Errorf("LOOM_EVENTSTORE_WRITE=%q ⇒ true, want false", off)
		}
	}
}

func TestEventStoreSinkDisabledByDefault(t *testing.T) {
	t.Setenv("LOOM_EVENTSTORE_WRITE", "") // F2 off
	sink, runID := eventStoreSink(t.Context(), "/some/wd")
	if sink != nil || runID != "" {
		t.Errorf("F2 off ⇒ no sink, got sink=%v runID=%q", sink != nil, runID)
	}
}

func TestEventStoreSinkNoActiveSession(t *testing.T) {
	t.Setenv("LOOM_EVENTSTORE_WRITE", "1")
	ClearActiveSessionEnv() // standalone: no session
	if sink, _ := eventStoreSink(t.Context(), "/some/wd"); sink != nil {
		t.Error("no active session ⇒ no sink")
	}
}

func TestEventStoreSinkWritesToSessionDir(t *testing.T) {
	t.Setenv("LOOM_EVENTSTORE_WRITE", "1")
	runtimeDir := t.TempDir()
	const sid = "20260601-120000-claude-abcd"
	SetActiveSessionRuntimeEnv(runtimeDir, sid)
	t.Cleanup(ClearActiveSessionEnv)

	sink, runID := eventStoreSink(t.Context(), "/no/such/worktree") // no lock ⇒ runID falls back to sid
	if sink == nil {
		t.Fatal("F2 on + active session ⇒ expected a sink")
	}
	if runID != sid {
		t.Errorf("runID = %q, want fallback to sid %q", runID, sid)
	}

	// The sink appends to the session dir's events.jsonl; reading it back yields
	// the event with its internal fields intact.
	env := transcript.EventEnvelope{
		RunID: runID, Harness: "claude", HarnessSessionID: sid,
		Event: transcript.Event{Type: transcript.EventText, Text: "hi", Source: transcript.SourceLive, NativeID: "live:text:m:0"},
	}
	if err := sink(env); err != nil {
		t.Fatalf("sink append: %v", err)
	}
	archive, err := sessions.OpenArchive(t.Context(), runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := archive.LoadEnvelopes(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Event.Text != "hi" || got[0].Event.Source != transcript.SourceLive {
		t.Fatalf("event store round-trip wrong: %+v", got)
	}
}
