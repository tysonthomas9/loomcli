package trigger_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/trigger"
)

func TestFileIssueJournalCursorStore_MissingFileLoadsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-bridge-cursor.json")
	cs, err := trigger.NewFileIssueJournalCursorStore(path, nil)
	if err != nil {
		t.Fatalf("NewFileIssueJournalCursorStore on missing file: %v", err)
	}
	if cursor, found := cs.Load("WS"); found {
		t.Fatalf("Load on empty store: found=true cursor=%q, want not found", cursor)
	}
	// No file should have been written merely by constructing/loading.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("construction created a file unexpectedly: stat err=%v", statErr)
	}
}

func TestFileIssueJournalCursorStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-bridge-cursor.json")
	cs, err := trigger.NewFileIssueJournalCursorStore(path, nil)
	if err != nil {
		t.Fatalf("new cursor store: %v", err)
	}
	cs.Save("WS-A", "100")
	cs.Save("WS-B", "205")
	cs.Save("WS-A", "150") // overwrite advances the cursor

	// In-process Load reflects the latest Save.
	if cursor, found := cs.Load("WS-A"); !found || cursor != "150" {
		t.Fatalf("Load(WS-A) = (%q, %v), want (150, true)", cursor, found)
	}
	if cursor, found := cs.Load("WS-B"); !found || cursor != "205" {
		t.Fatalf("Load(WS-B) = (%q, %v), want (205, true)", cursor, found)
	}

	// A fresh store over the same path recovers the persisted map (durability +
	// atomic-write survived: the file is a complete, parseable snapshot).
	reopened, err := trigger.NewFileIssueJournalCursorStore(path, nil)
	if err != nil {
		t.Fatalf("reopen cursor store: %v", err)
	}
	if cursor, found := reopened.Load("WS-A"); !found || cursor != "150" {
		t.Fatalf("reopened Load(WS-A) = (%q, %v), want (150, true)", cursor, found)
	}
	if cursor, found := reopened.Load("WS-B"); !found || cursor != "205" {
		t.Fatalf("reopened Load(WS-B) = (%q, %v), want (205, true)", cursor, found)
	}
	if cursor, found := reopened.Load("WS-UNKNOWN"); found {
		t.Fatalf("reopened Load(WS-UNKNOWN) = (%q, %v), want not found", cursor, found)
	}
}

func TestFileIssueJournalCursorStore_AtomicWriteLeavesNoTempArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue-bridge-cursor.json")
	cs, err := trigger.NewFileIssueJournalCursorStore(path, nil)
	if err != nil {
		t.Fatalf("new cursor store: %v", err)
	}
	for i := 0; i < 5; i++ {
		cs.Save("WS", "cursor")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	// Atomic tmp+rename must leave exactly the target file, no leftover
	// .loom-atomic-* temp files.
	if len(entries) != 1 || entries[0].Name() != "issue-bridge-cursor.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir entries = %v, want exactly [issue-bridge-cursor.json]", names)
	}
}

func TestFileIssueJournalCursorStore_CorruptFileSurfacesError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-bridge-cursor.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	cs, err := trigger.NewFileIssueJournalCursorStore(path, nil)
	if err == nil {
		t.Fatalf("NewFileIssueJournalCursorStore on corrupt file: err=nil, want error surfaced")
	}
	if cs != nil {
		t.Fatalf("corrupt-file constructor returned a non-nil store: %#v", cs)
	}
}

func TestFileIssueJournalCursorStore_EmptyFileLoadsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-bridge-cursor.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("seed empty file: %v", err)
	}
	cs, err := trigger.NewFileIssueJournalCursorStore(path, nil)
	if err != nil {
		t.Fatalf("NewFileIssueJournalCursorStore on empty file: %v", err)
	}
	if cursor, found := cs.Load("WS"); found {
		t.Fatalf("Load on empty-file store: (%q, %v), want not found", cursor, found)
	}
}
