package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreAdditionalValidationAndIndexBranches(t *testing.T) {
	fileRoot := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileRoot, []byte("x"), 0600); err != nil {
		t.Fatalf("write file root: %v", err)
	}
	if _, err := NewStore(fileRoot); err == nil || !strings.Contains(err.Error(), "create sessions dir") {
		t.Fatalf("NewStore under file err = %v", err)
	}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if store.Dir() == "" || !strings.HasSuffix(store.Dir(), "sessions") {
		t.Fatalf("store Dir = %q", store.Dir())
	}

	meta := initialSessionMetadata("sid-1", CreateOptions{
		AgentName:  "nova",
		Backend:    "codex",
		EpicID:     "EPIC-1",
		Phase:      "planning",
		AttemptNum: 2,
	})
	if meta.SessionID != "sid-1" || meta.AgentName != "nova" || meta.Status != StatusRunning || meta.AttemptNum != 2 {
		t.Fatalf("initialSessionMetadata = %+v", meta)
	}

	if _, err := store.resolveSessionDir("../bad"); err == nil || !strings.Contains(err.Error(), "path separator") {
		t.Fatalf("resolveSessionDir traversal err = %v", err)
	}
	if _, err := store.resolveSessionDir("missing-session"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("resolveSessionDir missing err = %v", err)
	}
	if err := store.UpdatePrompt("missing-session", "prompt"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("UpdatePrompt missing err = %v", err)
	}
	if err := store.SaveMetadata("../bad", &SessionMetadata{}); err == nil {
		t.Fatal("SaveMetadata accepted invalid session ID")
	}
	if _, err := store.CreateSession(CreateOptions{AgentName: "bad/name"}); err == nil || !strings.Contains(err.Error(), "generate session ID") {
		t.Fatalf("CreateSession invalid agent err = %v", err)
	}
	if err := store.AppendTranscript("../bad", TranscriptEntry{Role: "user"}); err == nil || !strings.Contains(err.Error(), "path separator") {
		t.Fatalf("AppendTranscript invalid ID err = %v", err)
	}
	if err := store.AppendTranscript("missing-session", TranscriptEntry{Role: "user"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("AppendTranscript missing session err = %v", err)
	}
	if err := store.SaveMetadata("missing-session", &SessionMetadata{}); err == nil || !strings.Contains(err.Error(), "write metadata tmp") {
		t.Fatalf("SaveMetadata missing session err = %v", err)
	}
	for _, read := range []struct {
		name string
		fn   func(string) error
	}{
		{name: "LoadMetadata", fn: func(id string) error { _, err := store.LoadMetadata(id); return err }},
		{name: "LoadTranscript", fn: func(id string) error { _, err := store.LoadTranscript(id); return err }},
		{name: "ReadPrompt", fn: func(id string) error { _, err := store.ReadPrompt(id); return err }},
		{name: "ReadDiff", fn: func(id string) error { _, err := store.ReadDiff(id); return err }},
	} {
		if err := read.fn("../bad"); err == nil {
			t.Fatalf("%s accepted invalid session ID", read.name)
		}
	}

	sess, err := store.CreateSession(CreateOptions{AgentName: "nova", Backend: "codex", Prompt: "old"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sessDir := filepath.Join(store.Dir(), sess.SessionID())
	if got := readAndIncrementSeq(sessDir); got != 1 {
		t.Fatalf("first seq = %d, want 1", got)
	}
	if got := readAndIncrementSeq(sessDir); got != 2 {
		t.Fatalf("second seq = %d, want 2", got)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "seq"), []byte("not-an-int"), sessFilePerm); err != nil {
		t.Fatalf("write bad seq: %v", err)
	}
	if got := readAndIncrementSeq(sessDir); got != 1 {
		t.Fatalf("bad seq reset = %d, want 1", got)
	}

	if err := store.UpdatePrompt(sess.SessionID(), "new prompt"); err != nil {
		t.Fatalf("UpdatePrompt: %v", err)
	}
	prompt, err := os.ReadFile(filepath.Join(sessDir, "prompt.txt"))
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if string(prompt) != "new prompt" {
		t.Fatalf("prompt = %q", prompt)
	}
	if err := store.AppendTranscript(sess.SessionID(), TranscriptEntry{Role: "assistant", Type: "text", Content: "second"}); err != nil {
		t.Fatalf("AppendTranscript first: %v", err)
	}
	if err := store.AppendTranscript(sess.SessionID(), TranscriptEntry{Role: "user", Type: "text", Content: "third"}); err != nil {
		t.Fatalf("AppendTranscript second: %v", err)
	}
	transcript, err := store.LoadTranscript(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(transcript) != 2 || transcript[0].Seq != 2 || transcript[1].Seq != 3 {
		t.Fatalf("transcript = %+v, want seq 2/3", transcript)
	}
	if _, err := store.LoadMetadata(sess.SessionID()); err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if _, err := store.ReadDiff(sess.SessionID()); err == nil || !strings.Contains(err.Error(), "read diff.patch") {
		t.Fatalf("ReadDiff missing err = %v", err)
	}

	indexPath := filepath.Join(store.Dir(), "custom-index.jsonl")
	records := []SessionRecord{
		{SessionID: "a", AgentName: "nova", Status: StatusCompleted},
		{SessionID: "b", AgentName: "spark", Status: StatusFailed},
	}
	if err := writeRecordsAtomic(indexPath, records); err != nil {
		t.Fatalf("writeRecordsAtomic: %v", err)
	}
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, sessFilePerm)
	if err != nil {
		t.Fatalf("open index append: %v", err)
	}
	if _, err := f.WriteString("\n{bad-json}\n"); err != nil {
		t.Fatalf("append corrupt line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close index: %v", err)
	}
	total, unique, err := countIndexLines(indexPath)
	if err != nil {
		t.Fatalf("countIndexLines: %v", err)
	}
	if total != 3 || unique != 2 {
		t.Fatalf("countIndexLines total=%d unique=%d, want 3/2", total, unique)
	}
}

func TestCompactIndexRemovesDuplicatesAndMissingSessions(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	total, unique, err := store.CountIndexEntries()
	if err != nil {
		t.Fatalf("CountIndexEntries missing: %v", err)
	}
	if total != 0 || unique != 0 {
		t.Fatalf("missing count total=%d unique=%d, want 0/0", total, unique)
	}
	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex empty: %v", err)
	}
	if removed != 0 {
		t.Fatalf("empty removed = %d, want 0", removed)
	}

	now := time.Now().UTC()
	liveDir := filepath.Join(store.Dir(), "live")
	if err := os.MkdirAll(liveDir, sessDirPerm); err != nil {
		t.Fatalf("mkdir live session: %v", err)
	}
	records := []SessionRecord{
		{SessionID: "live", AgentName: "old", Status: StatusRunning, StartedAt: now.Add(-time.Hour)},
		{SessionID: "missing", AgentName: "gone", Status: StatusFailed, StartedAt: now},
		{SessionID: "live", AgentName: "new", Status: StatusCompleted, StartedAt: now},
	}
	indexPath := filepath.Join(store.Dir(), "index.jsonl")
	if err := writeRecordsAtomic(indexPath, records); err != nil {
		t.Fatalf("write index: %v", err)
	}
	removed, err = store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex populated: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want duplicate+missing removed", removed)
	}
	total, unique, err = store.CountIndexEntries()
	if err != nil {
		t.Fatalf("CountIndexEntries compacted: %v", err)
	}
	if total != 1 || unique != 1 {
		t.Fatalf("compacted count total=%d unique=%d, want 1/1", total, unique)
	}
	removed, err = store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex no-op: %v", err)
	}
	if removed != 0 {
		t.Fatalf("no-op removed = %d, want 0", removed)
	}
}
