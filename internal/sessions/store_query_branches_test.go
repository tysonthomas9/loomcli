package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadTranscriptSortsSkipsCorruptAndHandlesMissing(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sessionID := "sess-transcript"
	sessionDir := filepath.Join(st.Dir(), sessionID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}

	entries := []TranscriptEntry{
		{Seq: 3, Timestamp: time.Now().UTC(), Role: "assistant", Type: "text", Content: "third"},
		{Seq: 1, Timestamp: time.Now().UTC(), Role: "user", Type: "text", Content: "first"},
	}
	var raw strings.Builder
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		raw.Write(data)
		raw.WriteByte('\n')
	}
	raw.WriteString("{bad-json\n\n")
	if err := os.WriteFile(filepath.Join(sessionDir, "transcript.jsonl"), []byte(raw.String()), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	got, err := st.LoadTranscript(sessionID)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 3 {
		t.Fatalf("LoadTranscript sorted entries = %+v", got)
	}

	missing, err := st.LoadTranscript("missing-transcript")
	if err != nil {
		t.Fatalf("LoadTranscript missing file: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing transcript = %+v, want nil", missing)
	}
	if _, err := st.LoadTranscript("../bad"); err == nil {
		t.Fatal("LoadTranscript invalid ID returned nil error")
	}
}

func TestMetadataPromptAndSaveBranches(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := st.CreateSession(CreateOptions{
		AgentName: "nova",
		Backend:   "codex",
		Phase:     "implementation",
		Prompt:    "initial prompt",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sessionID := sess.SessionID()

	if got, err := st.ReadPrompt(sessionID); err != nil || got != "initial prompt" {
		t.Fatalf("ReadPrompt = %q err=%v", got, err)
	}
	if err := st.UpdatePrompt(sessionID, "updated prompt"); err != nil {
		t.Fatalf("UpdatePrompt: %v", err)
	}
	if got, err := st.ReadPrompt(sessionID); err != nil || got != "updated prompt" {
		t.Fatalf("ReadPrompt after update = %q err=%v", got, err)
	}
	if err := st.UpdatePrompt("missing-session", "x"); err == nil {
		t.Fatal("UpdatePrompt missing session returned nil error")
	}
	if _, err := st.ReadPrompt("../bad"); err == nil {
		t.Fatal("ReadPrompt invalid ID returned nil error")
	}

	meta, err := st.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	meta.Status = StatusCompleted
	if err := st.SaveMetadata(sessionID, meta); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	loaded, err := st.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata saved: %v", err)
	}
	if loaded.Status != StatusCompleted {
		t.Fatalf("saved status = %q, want completed", loaded.Status)
	}
	if err := st.SaveMetadata("../bad", meta); err == nil {
		t.Fatal("SaveMetadata invalid ID returned nil error")
	}

	if err := os.WriteFile(filepath.Join(st.Dir(), sessionID, "metadata.json"), []byte("{bad-json"), 0o600); err != nil {
		t.Fatalf("write malformed metadata: %v", err)
	}
	if _, err := st.LoadMetadata(sessionID); err == nil {
		t.Fatal("LoadMetadata malformed JSON returned nil error")
	}
}

func TestWriteRecordsAtomicAndIndexErrorBranches(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	indexPath := filepath.Join(st.Dir(), "index.jsonl")
	if err := os.MkdirAll(indexPath+".tmp", 0o700); err != nil {
		t.Fatalf("mkdir tmp collision: %v", err)
	}
	err = writeRecordsAtomic(indexPath, []SessionRecord{{SessionID: "sess"}})
	if err == nil || !strings.Contains(err.Error(), "create compaction tmp") {
		t.Fatalf("writeRecordsAtomic tmp collision err = %v", err)
	}

	if _, _, err := countIndexLines(t.TempDir()); err == nil {
		t.Fatal("countIndexLines directory path returned nil error")
	}
}
