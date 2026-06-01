package svcimpl

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/eventstore"
)

// seedEventStore creates a session store + writes a parent event and a subagent
// event into the session's events.jsonl, returning the store + session id.
func seedEventStore(t *testing.T) (*sessions.Store, string) {
	t.Helper()
	store, err := sessions.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const sid = "20260601-120000-claude-abcd"
	sessionDir := filepath.Dir(store.NativeTranscriptPath(sid))
	if err := os.MkdirAll(sessionDir, 0o755); err != nil { // production does this in eventStoreSink
		t.Fatal(err)
	}
	es := eventstore.Open(sessionDir)
	parent := hwtranscript.EventEnvelope{
		RunID: "r", Harness: "claude", HarnessSessionID: "parent-native",
		Event: hwtranscript.Event{Seq: 0, Timestamp: time.Unix(1, 0), Role: "assistant", Type: "text", Text: "parent says", Source: hwtranscript.SourceFile, NativeID: "msg:p1"},
	}
	sub := hwtranscript.EventEnvelope{
		RunID: "r", Harness: "claude", HarnessSessionID: "sub-1", ParentSessionID: "parent-native",
		Event: hwtranscript.Event{Seq: 0, Timestamp: time.Unix(2, 0), Role: "assistant", Type: "text", Text: "subagent says", Source: hwtranscript.SourceFile, NativeID: "msg:s1"},
	}
	if err := es.AppendEnvelope(parent); err != nil {
		t.Fatal(err)
	}
	if err := es.AppendEnvelope(sub); err != nil {
		t.Fatal(err)
	}
	return store, sid
}

func TestServeFromEventStoreDefaultOff(t *testing.T) {
	store, sid := seedEventStore(t)
	// F3 unset ⇒ helpers are inert (serving falls back to native).
	if eventStoreHasTranscript(store, sid) {
		t.Error("F3 off: eventStoreHasTranscript should be false")
	}
	if _, ok := eventStoreParentEvents(store, sid); ok {
		t.Error("F3 off: eventStoreParentEvents should report not-found (native fallback)")
	}
}

func TestEventStoreParentEventsFiltersSubagents(t *testing.T) {
	t.Setenv("LOOM_SERVE_FROM_EVENTSTORE", "1")
	store, sid := seedEventStore(t)

	if !eventStoreHasTranscript(store, sid) {
		t.Fatal("F3 on + populated: expected has_transcript")
	}
	evs, ok := eventStoreParentEvents(store, sid)
	if !ok {
		t.Fatal("expected parent events")
	}
	if len(evs) != 1 || evs[0].Text != "parent says" {
		t.Fatalf("parent events = %+v, want only the parent (subagents filtered out)", evs)
	}
	// Field mapping wrapper.Event → loom.Event preserved the public fields.
	if evs[0].Role != "assistant" || evs[0].Type != "text" {
		t.Errorf("mapped event fields wrong: %+v", evs[0])
	}
}

func TestEventStoreSubagentEvents(t *testing.T) {
	t.Setenv("LOOM_SERVE_FROM_EVENTSTORE", "1")
	store, sid := seedEventStore(t)

	evs, ok := eventStoreSubagentEvents(store, sid, "sub-1")
	if !ok || len(evs) != 1 || evs[0].Text != "subagent says" {
		t.Fatalf("subagent events = %+v (ok=%v), want the one subagent event", evs, ok)
	}
	// An unknown subagent id yields nothing.
	if _, ok := eventStoreSubagentEvents(store, sid, "nope"); ok {
		t.Error("unknown subagent id should yield no events")
	}
	if _, ok := eventStoreSubagentEvents(store, sid, ""); ok {
		t.Error("empty subagent id should yield no events")
	}
}

func TestEventStoreEmptyFallsBack(t *testing.T) {
	t.Setenv("LOOM_SERVE_FROM_EVENTSTORE", "1")
	store, err := sessions.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// No events written ⇒ helpers report not-found so the caller uses native.
	if eventStoreHasTranscript(store, "missing-sess") {
		t.Error("empty store: has_transcript should be false")
	}
	if _, ok := eventStoreParentEvents(store, "missing-sess"); ok {
		t.Error("empty store: parent events should report not-found")
	}
}
