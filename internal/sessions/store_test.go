package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore(%q) error: %v", dir, err)
	}
	if store == nil {
		t.Fatal("NewStore returned nil store")
	}

	sessionsDir := filepath.Join(dir, "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil {
		t.Fatalf("sessions/ directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("sessions/ is not a directory")
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("sessions/ dir mode = %o, want %o", got, 0o700)
	}
}

func TestCreateSession(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	sess, err := store.CreateSession(CreateOptions{
		AgentName:  "nova",
		Backend:    "claude",
		EpicID:     "epic-001",
		Prompt:     "Implement the widget feature",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	if sess == nil {
		t.Fatal("CreateSession returned nil session")
	}

	sid := sess.SessionID()
	if sid == "" {
		t.Fatal("SessionID() returned empty string")
	}

	sessDir := filepath.Join(dir, "sessions", sid)

	// Check session directory exists.
	info, err := os.Stat(sessDir)
	if err != nil {
		t.Fatalf("session directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("session path is not a directory")
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("session dir mode = %o, want %o", got, 0o700)
	}

	// Check prompt.txt exists with correct content.
	promptData, err := os.ReadFile(filepath.Join(sessDir, "prompt.txt"))
	if err != nil {
		t.Fatalf("read prompt.txt: %v", err)
	}
	if string(promptData) != "Implement the widget feature" {
		t.Errorf("prompt.txt = %q, want %q", string(promptData), "Implement the widget feature")
	}
	promptInfo, err := os.Stat(filepath.Join(sessDir, "prompt.txt"))
	if err != nil {
		t.Fatalf("stat prompt.txt: %v", err)
	}
	if got := promptInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("prompt.txt mode = %o, want %o", got, 0o600)
	}

	// Check metadata.json exists and has status=running.
	metaData, err := os.ReadFile(filepath.Join(sessDir, "metadata.json"))
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	var meta SessionMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("unmarshal metadata.json: %v", err)
	}
	if meta.Status != StatusRunning {
		t.Errorf("metadata status = %q, want %q", meta.Status, StatusRunning)
	}

	metaInfo, err := os.Stat(filepath.Join(sessDir, "metadata.json"))
	if err != nil {
		t.Fatalf("stat metadata.json: %v", err)
	}
	if got := metaInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("metadata.json mode = %o, want %o", got, 0o600)
	}
}

func TestCreateSession_MetadataFields(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	before := time.Now().UTC().Add(-1 * time.Second)

	sess, err := store.CreateSession(CreateOptions{
		AgentName:  "ember",
		Backend:    "claude",
		EpicID:     "epic-xyz",
		Prompt:     "Plan the refactoring",
		AttemptNum: 2,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}

	after := time.Now().UTC().Add(1 * time.Second)

	// Read metadata from disk.
	sid := sess.SessionID()
	metaData, err := os.ReadFile(filepath.Join(dir, "sessions", sid, "metadata.json"))
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	var meta SessionMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("unmarshal metadata.json: %v", err)
	}

	// Verify fields.
	if meta.AgentName != "ember" {
		t.Errorf("AgentName = %q, want %q", meta.AgentName, "ember")
	}
	if meta.Backend != "claude" {
		t.Errorf("Backend = %q, want %q", meta.Backend, "claude")
	}
	if meta.EpicID != "epic-xyz" {
		t.Errorf("EpicID = %q, want %q", meta.EpicID, "epic-xyz")
	}
	if meta.AttemptNum != 2 {
		t.Errorf("AttemptNum = %d, want %d", meta.AttemptNum, 2)
	}
	if meta.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", meta.Status, StatusRunning)
	}
	if meta.SessionID != sid {
		t.Errorf("SessionID = %q, want %q", meta.SessionID, sid)
	}
	if meta.StartedAt.Before(before) || meta.StartedAt.After(after) {
		t.Errorf("StartedAt = %v, want between %v and %v", meta.StartedAt, before, after)
	}
}

func TestAppendTranscript(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	sess, err := store.CreateSession(CreateOptions{
		AgentName:  "nova",
		Backend:    "claude",
		Prompt:     "Test transcript",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}

	sid := sess.SessionID()
	now := time.Now().UTC()

	entries := []TranscriptEntry{
		{Seq: 1, Timestamp: now, Role: "user", Type: "text", Content: "Hello"},
		{Seq: 2, Timestamp: now.Add(time.Second), Role: "assistant", Type: "text", Content: "Hi there"},
		{Seq: 3, Timestamp: now.Add(2 * time.Second), Role: "assistant", Type: "tool_use", ToolName: "Read", ToolInput: `{"file_path":"/foo"}`},
	}

	for _, e := range entries {
		if err := store.AppendTranscript(sid, e); err != nil {
			t.Fatalf("AppendTranscript seq=%d error: %v", e.Seq, err)
		}
	}

	// Read back and verify.
	txPath := filepath.Join(dir, "sessions", sid, "transcript.jsonl")
	f, err := os.Open(txPath)
	if err != nil {
		t.Fatalf("open transcript.jsonl: %v", err)
	}
	defer f.Close()

	var got []TranscriptEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e TranscriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal transcript line: %v", err)
		}
		got = append(got, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(got) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(got), len(entries))
	}

	for i, e := range got {
		if e.Seq != entries[i].Seq {
			t.Errorf("entry[%d].Seq = %d, want %d", i, e.Seq, entries[i].Seq)
		}
		if e.Role != entries[i].Role {
			t.Errorf("entry[%d].Role = %q, want %q", i, e.Role, entries[i].Role)
		}
		if e.Type != entries[i].Type {
			t.Errorf("entry[%d].Type = %q, want %q", i, e.Type, entries[i].Type)
		}
		if e.Content != entries[i].Content {
			t.Errorf("entry[%d].Content = %q, want %q", i, e.Content, entries[i].Content)
		}
		if e.ToolName != entries[i].ToolName {
			t.Errorf("entry[%d].ToolName = %q, want %q", i, e.ToolName, entries[i].ToolName)
		}
	}

	txInfo, err := os.Stat(txPath)
	if err != nil {
		t.Fatalf("stat transcript.jsonl: %v", err)
	}
	if got := txInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("transcript.jsonl mode = %o, want %o", got, 0o600)
	}

	seqInfo, err := os.Stat(filepath.Join(dir, "sessions", sid, "seq"))
	if err != nil {
		t.Fatalf("stat seq: %v", err)
	}
	if got := seqInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("seq mode = %o, want %o", got, 0o600)
	}
}

func TestAppendTranscript_Concurrent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	sess, err := store.CreateSession(CreateOptions{
		AgentName:  "nova",
		Backend:    "claude",
		Prompt:     "Concurrent test",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}

	sid := sess.SessionID()
	const numGoroutines = 50
	now := time.Now().UTC()

	var wg sync.WaitGroup
	errs := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			entry := TranscriptEntry{
				Seq:       seq,
				Timestamp: now,
				Role:      "assistant",
				Type:      "text",
				Content:   fmt.Sprintf("message-%d", seq),
			}
			errs[seq] = store.AppendTranscript(sid, entry)
		}(i)
	}

	wg.Wait()

	// Check for errors.
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d error: %v", i, err)
		}
	}

	// Read back all entries.
	txPath := filepath.Join(dir, "sessions", sid, "transcript.jsonl")
	f, err := os.Open(txPath)
	if err != nil {
		t.Fatalf("open transcript.jsonl: %v", err)
	}
	defer f.Close()

	seen := make(map[int]bool)
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var e TranscriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("line %d: unmarshal error: %v\nline: %q", lineNum, err, scanner.Text())
		}
		if seen[e.Seq] {
			t.Errorf("duplicate seq %d", e.Seq)
		}
		seen[e.Seq] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if lineNum != numGoroutines {
		t.Errorf("got %d lines, want %d", lineNum, numGoroutines)
	}

	// Verify all seqs are present (1-based, auto-assigned).
	for i := 1; i <= numGoroutines; i++ {
		if !seen[i] {
			t.Errorf("missing seq %d", i)
		}
	}
}

func TestAppendTranscript_NonexistentSession(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	entry := TranscriptEntry{
		Seq:       1,
		Timestamp: time.Now().UTC(),
		Role:      "user",
		Type:      "text",
		Content:   "hello",
	}

	err = store.AppendTranscript("nonexistent-session-id", entry)
	if err == nil {
		t.Fatal("AppendTranscript to nonexistent session should return error")
	}
	t.Logf("Got expected error: %v", err)
}

func TestAppendTranscript_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	entry := TranscriptEntry{Seq: 1, Role: "user", Type: "text", Content: "hello"}

	traversalIDs := []string{
		"../etc/passwd",
		"../../outside",
		"valid/../../../escape",
		"a/b/c",
		"session\\id",
	}

	for _, id := range traversalIDs {
		err := store.AppendTranscript(id, entry)
		if err == nil {
			t.Errorf("AppendTranscript(%q) should have returned error", id)
		}
	}
}

// TestSessionDir locks the load-bearing path contract: SessionDir is
// <runtimeDir>/sessions/<id> and is the directory the native transcript lives
// in — so the event-store writer (OnEvent sink) and reader (serving), which both
// resolve through SessionDir, can never diverge.
func TestSessionDir(t *testing.T) {
	rt := t.TempDir()
	store, err := NewStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	const sid = "20260601-120000-claude-abcd"
	want := filepath.Join(rt, "sessions", sid)
	if got := store.SessionDir(sid); got != want {
		t.Errorf("SessionDir = %q, want %q", got, want)
	}
	// The native transcript lives in SessionDir (events.jsonl is its sibling).
	if got := filepath.Dir(store.NativeTranscriptPath(sid)); got != store.SessionDir(sid) {
		t.Errorf("NativeTranscriptPath dir %q != SessionDir %q", got, store.SessionDir(sid))
	}
}
