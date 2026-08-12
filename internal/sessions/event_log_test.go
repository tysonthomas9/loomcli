package sessions

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/transcript"
)

func env(runID, hsid, parent, nativeID, text, source string, seq int, ts time.Time) transcript.EventEnvelope {
	return transcript.EventEnvelope{
		RunID: runID, Harness: "claude", HarnessSessionID: hsid, ParentSessionID: parent,
		Event: transcript.Event{
			Seq: seq, Timestamp: ts, Role: transcript.RoleAssistant, Type: transcript.EventText,
			Text: text, Source: source, NativeID: nativeID, SchemaVersion: transcript.SchemaVersion,
		},
	}
}

func TestAppendReadRoundTripPreservesInternalFields(t *testing.T) {
	s := openEventLog(t.TempDir())
	in := env("run-1", "sess", "", "live:text:m:0", "hello", transcript.SourceLive, 0, time.Unix(100, 0))
	if err := s.AppendEnvelope(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	e := got[0]
	if e.RunID != "run-1" || e.HarnessSessionID != "sess" || e.Event.Text != "hello" {
		t.Errorf("envelope fields wrong: %+v", e)
	}
	// The durable form MUST preserve the internal fields (public JSON omits them).
	if e.Event.Source != transcript.SourceLive || e.Event.NativeID != "live:text:m:0" || e.Event.SchemaVersion != transcript.SchemaVersion {
		t.Errorf("internal fields lost: Source=%q NativeID=%q Schema=%d", e.Event.Source, e.Event.NativeID, e.Event.SchemaVersion)
	}
}

func TestReadDedupsByIdentityLastWriterWins(t *testing.T) {
	s := openEventLog(t.TempDir())
	// Same (RunID,HarnessSessionID,NativeID) appended twice with different text
	// (e.g. a hooks re-parse / resume replay). Read keeps the LAST.
	_ = s.AppendEnvelope(env("r", "sess", "", "tool-use:t1", "first", transcript.SourceFile, 0, time.Unix(1, 0)))
	_ = s.AppendEnvelope(env("r", "sess", "", "tool-use:t1", "second", transcript.SourceFile, 0, time.Unix(1, 0)))
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (deduped by identity)", len(got))
	}
	if got[0].Event.Text != "second" {
		t.Errorf("last-writer-wins failed: got %q, want second", got[0].Event.Text)
	}
}

func TestReadOrdersByTimestampThenSeq(t *testing.T) {
	s := openEventLog(t.TempDir())
	// Out-of-order appends; distinct identities. Same timestamp pair disambiguated
	// by Seq (live arrival order).
	_ = s.AppendEnvelope(env("r", "s", "", "n3", "third", transcript.SourceLive, 2, time.Unix(0, 0)))
	_ = s.AppendEnvelope(env("r", "s", "", "n1", "first", transcript.SourceFile, 0, time.Unix(10, 0)))
	_ = s.AppendEnvelope(env("r", "s", "", "n2", "second", transcript.SourceLive, 1, time.Unix(0, 0)))
	got, _ := s.Read()
	var texts []string
	for _, e := range got {
		texts = append(texts, e.Event.Text)
	}
	// Unix(0) seq1, Unix(0) seq2, then Unix(10): second, third, first.
	want := []string{"second", "third", "first"}
	if len(texts) != 3 || texts[0] != want[0] || texts[1] != want[1] || texts[2] != want[2] {
		t.Errorf("order = %v, want %v", texts, want)
	}
}

func TestReadGroupsParentAndSubagent(t *testing.T) {
	s := openEventLog(t.TempDir())
	_ = s.AppendEnvelope(env("r", "parent", "", "msg:p1", "parent says", transcript.SourceFile, 0, time.Unix(1, 0)))
	_ = s.AppendEnvelope(env("r", "sub", "parent", "msg:s1", "subagent says", transcript.SourceFile, 0, time.Unix(2, 0)))
	got, _ := s.Read()
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (parent + subagent in one file)", len(got))
	}
	// Subagent is distinguished by ParentSessionID for Runs-tab nesting.
	var sub *transcript.EventEnvelope
	for i := range got {
		if got[i].HarnessSessionID == "sub" {
			sub = &got[i]
		}
	}
	if sub == nil || sub.ParentSessionID != "parent" {
		t.Errorf("subagent nesting lost: %+v", got)
	}
}

func TestHasTranscript(t *testing.T) {
	s := openEventLog(t.TempDir())
	if s.HasTranscript() {
		t.Error("empty store should report no transcript")
	}
	_ = s.AppendEnvelope(env("r", "s", "", "n", "x", transcript.SourceLive, 0, time.Unix(1, 0)))
	if !s.HasTranscript() {
		t.Error("store with an event should report has-transcript")
	}
}

func TestCompactCollapsesDuplicates(t *testing.T) {
	dir := t.TempDir()
	s := openEventLog(dir)
	for i := 0; i < 5; i++ {
		_ = s.AppendEnvelope(env("r", "s", "", "msg:dup", "v", transcript.SourceFile, 0, time.Unix(1, 0)))
	}
	_ = s.AppendEnvelope(env("r", "s", "", "msg:other", "o", transcript.SourceFile, 1, time.Unix(2, 0)))

	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	// After compaction the file has one row per identity (2), and Read is unchanged.
	rows, err := s.scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("after compact: %d rows on disk, want 2 (collapsed)", len(rows))
	}
	got, _ := s.Read()
	if len(got) != 2 {
		t.Errorf("Read after compact: %d, want 2", len(got))
	}
}

func TestConcurrentAppendsNoTornRows(t *testing.T) {
	s := openEventLog(t.TempDir())
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct identities so none dedup away.
			_ = s.AppendEnvelope(env("r", "s", "", "msg:"+strconv.Itoa(i), "t", transcript.SourceLive, i, time.Unix(int64(i), 0)))
		}(i)
	}
	wg.Wait()
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("got %d events after %d concurrent appends, want %d (flock serializes, no torn rows)", len(got), n, n)
	}
}

func TestReadMissingFileIsNil(t *testing.T) {
	s := openEventLog(filepath.Join(t.TempDir(), "nonexistent-run"))
	got, err := s.Read()
	if err != nil || got != nil {
		t.Fatalf("missing file: got (%v,%v), want (nil,nil)", got, err)
	}
	if s.HasTranscript() {
		t.Error("missing file should report no transcript")
	}
}
