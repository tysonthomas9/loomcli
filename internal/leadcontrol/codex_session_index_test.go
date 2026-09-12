package leadcontrol

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCodexIndex drops a session_index.jsonl into a fresh CODEX_HOME and
// returns it. t.TempDir keeps every case off the real workspace runtime dir.
func writeCodexIndex(t *testing.T, contents string) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, CodexSessionIndexFile)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return home
}

func TestReadCodexSessionIndexLastLinePerIDWins(t *testing.T) {
	home := writeCodexIndex(t, `{"id":"t1","thread_name":"Are you able to acce","updated_at":"2026-09-04T11:29:16.748482Z"}
{"id":"t2","thread_name":"Other thread","updated_at":"2026-09-04T11:00:00Z"}
{"id":"t1","thread_name":"Check plan access","updated_at":"2026-09-04T11:29:20.397667Z"}
`)

	index, err := ReadCodexSessionIndex(home)
	if err != nil {
		t.Fatalf("ReadCodexSessionIndex: %v", err)
	}
	if len(index) != 2 {
		t.Fatalf("want 2 distinct threads, got %d: %#v", len(index), index)
	}
	if got := index["t1"].ThreadName; got != "Check plan access" {
		t.Errorf("t1 should collapse to the newest record, got %q", got)
	}
	if index["t1"].UpdatedAt.IsZero() {
		t.Error("t1 updated_at should have parsed")
	}
	if got := index["t2"].ThreadName; got != "Other thread" {
		t.Errorf("t2 thread name = %q", got)
	}
}

func TestReadCodexSessionIndexSkipsMalformedLines(t *testing.T) {
	home := writeCodexIndex(t, `not json at all
{"id":"t1","thread_name":"Good"}

{"thread_name":"no id here"}
{"id":"   ","thread_name":"blank id"}
{"id":"t2","thread_name":"Bad time","updated_at":"nonsense"}
`)

	index, err := ReadCodexSessionIndex(home)
	if err != nil {
		t.Fatalf("ReadCodexSessionIndex: %v", err)
	}
	if len(index) != 2 {
		t.Fatalf("want only the 2 usable records, got %d: %#v", len(index), index)
	}
	if got := index["t1"].ThreadName; got != "Good" {
		t.Errorf("t1 thread name = %q", got)
	}
	// An unparseable timestamp must not cost the record its usable name.
	if got := index["t2"].ThreadName; got != "Bad time" {
		t.Errorf("t2 thread name = %q", got)
	}
	if !index["t2"].UpdatedAt.IsZero() {
		t.Error("t2 updated_at should stay zero when it does not parse")
	}
}

func TestReadCodexSessionIndexToleratesTruncatedFinalLine(t *testing.T) {
	// codex appends while lead reads, so the last line is routinely half a
	// record. Everything before it must still come back.
	home := writeCodexIndex(t, `{"id":"t1","thread_name":"Complete"}
{"id":"t2","thread_na`)

	index, err := ReadCodexSessionIndex(home)
	if err != nil {
		t.Fatalf("ReadCodexSessionIndex: %v", err)
	}
	if len(index) != 1 {
		t.Fatalf("want 1 complete record, got %d: %#v", len(index), index)
	}
	if got := index["t1"].ThreadName; got != "Complete" {
		t.Errorf("t1 thread name = %q", got)
	}
}

func TestReadCodexSessionIndexMissingFileIsNotAnError(t *testing.T) {
	for name, home := range map[string]string{
		"no index in an existing home": t.TempDir(),
		"empty codex home":             "",
		"home that does not exist":     filepath.Join(t.TempDir(), "absent"),
	} {
		index, err := ReadCodexSessionIndex(home)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
		if len(index) != 0 {
			t.Errorf("%s: want an empty index, got %#v", name, index)
		}
		if index == nil {
			t.Errorf("%s: index should be usable, not nil", name)
		}
	}
}

func TestCodexThreadName(t *testing.T) {
	index := map[string]CodexSessionIndexEntry{"t1": {ID: "t1", ThreadName: "Named"}}

	if got := CodexThreadName(index, "t1"); got != "Named" {
		t.Errorf("known id = %q, want Named", got)
	}
	if got := CodexThreadName(index, " t1 "); got != "Named" {
		t.Errorf("padded id = %q, want Named", got)
	}
	if got := CodexThreadName(index, "missing"); got != "" {
		t.Errorf("unknown id = %q, want empty", got)
	}
	if got := CodexThreadName(nil, "t1"); got != "" {
		t.Errorf("nil index = %q, want empty", got)
	}
	if got := CodexThreadName(index, ""); got != "" {
		t.Errorf("empty id = %q, want empty", got)
	}
}
