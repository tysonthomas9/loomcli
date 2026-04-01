package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// helper: createTestStore sets up a Store using t.TempDir().
func createTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	return store
}

// helper: createTestSession creates a session with default options.
func createTestSession(t *testing.T, store *Store, agent, backend string) *Session {
	t.Helper()
	sess, err := store.CreateSession(CreateOptions{
		AgentName:  agent,
		Backend:    backend,
		Prompt:     "test prompt for " + agent,
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	return sess
}

func TestFinalize_Success(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	before := time.Now().UTC()

	err := sess.Finalize(FinalizeOptions{
		TaskID:   "task-001",
		ExitCode: 0,
		DiffStats: DiffStats{
			FilesChanged: 3,
			LinesAdded:   100,
			LinesRemoved: 20,
		},
		FilesTouched:     []string{"a.go", "b.go", "c.go"},
		InputTokens:      5000,
		OutputTokens:     2000,
		CacheReadTokens:  1000,
		CacheWriteTokens: 500,
		EstimatedCostUSD: 0.05,
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	after := time.Now().UTC().Add(time.Second)

	// Verify in-memory metadata.
	if sess.Meta.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", sess.Meta.Status, StatusCompleted)
	}
	if sess.Meta.TaskID != "task-001" {
		t.Errorf("TaskID = %q, want %q", sess.Meta.TaskID, "task-001")
	}
	if sess.Meta.EndedAt == nil {
		t.Fatal("EndedAt is nil")
	}
	if sess.Meta.EndedAt.Before(before) || sess.Meta.EndedAt.After(after) {
		t.Errorf("EndedAt = %v, want between %v and %v", *sess.Meta.EndedAt, before, after)
	}
	if sess.Meta.DurationS <= 0 {
		t.Errorf("DurationS = %f, want > 0", sess.Meta.DurationS)
	}
	if sess.Meta.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", sess.Meta.ExitCode)
	}

	// Verify metadata.json on disk.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.Status != StatusCompleted {
		t.Errorf("disk Status = %q, want %q", meta.Status, StatusCompleted)
	}
}

func TestFinalize_Failed(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "ember", "claude")

	err := sess.Finalize(FinalizeOptions{
		TaskID:     "task-002",
		ExitCode:   1,
		ErrorClass: "lint_failure",
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	if sess.Meta.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", sess.Meta.Status, StatusFailed)
	}
	if sess.Meta.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", sess.Meta.ExitCode)
	}
	if sess.Meta.ErrorClass != "lint_failure" {
		t.Errorf("ErrorClass = %q, want %q", sess.Meta.ErrorClass, "lint_failure")
	}

	// Verify on disk.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.Status != StatusFailed {
		t.Errorf("disk Status = %q, want %q", meta.Status, StatusFailed)
	}
}

func TestFinalize_IndexEntry(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	err := sess.Finalize(FinalizeOptions{
		TaskID:           "task-idx",
		ExitCode:         0,
		InputTokens:      12000,
		OutputTokens:     3500,
		CacheReadTokens:  8000,
		CacheWriteTokens: 400,
		EstimatedCostUSD: 0.12,
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// Query should find the session.
	records, err := store.Query(Filter{TaskID: "task-idx"})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	rec := records[0]
	if rec.SessionID != sess.SessionID() {
		t.Errorf("SessionID = %q, want %q", rec.SessionID, sess.SessionID())
	}
	if rec.TaskID != "task-idx" {
		t.Errorf("TaskID = %q, want %q", rec.TaskID, "task-idx")
	}
	if rec.InputTokens != 12000 {
		t.Errorf("InputTokens = %d, want 12000", rec.InputTokens)
	}
	if rec.OutputTokens != 3500 {
		t.Errorf("OutputTokens = %d, want 3500", rec.OutputTokens)
	}
	if rec.CacheReadTokens != 8000 {
		t.Errorf("CacheReadTokens = %d, want 8000", rec.CacheReadTokens)
	}
	if rec.CacheWriteTokens != 400 {
		t.Errorf("CacheWriteTokens = %d, want 400", rec.CacheWriteTokens)
	}
	if rec.EstimatedCostUSD != 0.12 {
		t.Errorf("EstimatedCostUSD = %f, want 0.12", rec.EstimatedCostUSD)
	}

	// Verify index.jsonl permissions.
	indexPath := filepath.Join(store.dir, "index.jsonl")
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index.jsonl: %v", err)
	}
	if got := indexInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("index.jsonl mode = %o, want %o", got, 0o600)
	}
}

func TestFinalize_DiffStats(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "falcon", "claude")

	err := sess.Finalize(FinalizeOptions{
		TaskID:   "task-diff",
		ExitCode: 0,
		DiffStats: DiffStats{
			FilesChanged: 5,
			LinesAdded:   200,
			LinesRemoved: 50,
		},
		FilesTouched: []string{"x.go", "y.go", "z.go", "w.go", "v.go"},
		DiffPatch:    "diff --git a/x.go b/x.go\n+hello\n",
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// Check in-memory.
	if sess.Meta.FilesChanged != 5 {
		t.Errorf("FilesChanged = %d, want 5", sess.Meta.FilesChanged)
	}
	if sess.Meta.LinesAdded != 200 {
		t.Errorf("LinesAdded = %d, want 200", sess.Meta.LinesAdded)
	}
	if sess.Meta.LinesRemoved != 50 {
		t.Errorf("LinesRemoved = %d, want 50", sess.Meta.LinesRemoved)
	}

	// Check index entry.
	records, err := store.Query(Filter{TaskID: "task-diff"})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].FilesChanged != 5 {
		t.Errorf("index FilesChanged = %d, want 5", records[0].FilesChanged)
	}
	if records[0].LinesAdded != 200 {
		t.Errorf("index LinesAdded = %d, want 200", records[0].LinesAdded)
	}
	if records[0].LinesRemoved != 50 {
		t.Errorf("index LinesRemoved = %d, want 50", records[0].LinesRemoved)
	}

	// Check diff.patch was written.
	diffPath := filepath.Join(store.dir, sess.SessionID(), "diff.patch")
	data, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("read diff.patch: %v", err)
	}
	if string(data) != "diff --git a/x.go b/x.go\n+hello\n" {
		t.Errorf("diff.patch content = %q", string(data))
	}

	// Verify diff.patch permissions.
	diffInfo, err := os.Stat(diffPath)
	if err != nil {
		t.Fatalf("stat diff.patch: %v", err)
	}
	if got := diffInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("diff.patch mode = %o, want %o", got, 0o600)
	}
}

func TestQuery_ByTaskID(t *testing.T) {
	store := createTestStore(t)

	// Create 3 sessions for 2 different tasks.
	s1 := createTestSession(t, store, "nova", "claude")
	s2 := createTestSession(t, store, "ember", "claude")
	s3 := createTestSession(t, store, "falcon", "claude")

	// Finalize with different task IDs.
	if err := s1.Finalize(FinalizeOptions{TaskID: "task-A", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize s1: %v", err)
	}
	if err := s2.Finalize(FinalizeOptions{TaskID: "task-A", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize s2: %v", err)
	}
	if err := s3.Finalize(FinalizeOptions{TaskID: "task-B", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize s3: %v", err)
	}

	// Query task-A should return 2.
	recordsA, err := store.Query(Filter{TaskID: "task-A"})
	if err != nil {
		t.Fatalf("Query task-A: %v", err)
	}
	if len(recordsA) != 2 {
		t.Errorf("task-A: got %d records, want 2", len(recordsA))
	}

	// Query task-B should return 1.
	recordsB, err := store.Query(Filter{TaskID: "task-B"})
	if err != nil {
		t.Fatalf("Query task-B: %v", err)
	}
	if len(recordsB) != 1 {
		t.Errorf("task-B: got %d records, want 1", len(recordsB))
	}
}

func TestQuery_ByAgentName(t *testing.T) {
	store := createTestStore(t)

	s1 := createTestSession(t, store, "nova", "claude")
	s2 := createTestSession(t, store, "ember", "claude")
	s3 := createTestSession(t, store, "nova", "claude")

	for _, s := range []*Session{s1, s2, s3} {
		if err := s.Finalize(FinalizeOptions{TaskID: "task-X", ExitCode: 0}); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
	}

	records, err := store.Query(Filter{AgentName: "nova"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("got %d records for agent nova, want 2", len(records))
	}
	for _, rec := range records {
		if rec.AgentName != "nova" {
			t.Errorf("record agent = %q, want nova", rec.AgentName)
		}
	}
}

func TestQuery_CorruptLine(t *testing.T) {
	store := createTestStore(t)

	// Create and finalize a valid session.
	sess := createTestSession(t, store, "nova", "claude")
	if err := sess.Finalize(FinalizeOptions{TaskID: "task-ok", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Inject a corrupt line into index.jsonl.
	indexPath := filepath.Join(store.dir, "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if _, err := f.WriteString("this is not valid json\n"); err != nil {
		t.Fatalf("write corrupt line: %v", err)
	}
	f.Close()

	// Create and finalize another valid session after the corrupt line.
	sess2 := createTestSession(t, store, "ember", "claude")
	if err := sess2.Finalize(FinalizeOptions{TaskID: "task-ok2", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Query should return both valid records, skipping the corrupt line.
	records, err := store.Query(Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("got %d records, want 2 (corrupt line should be skipped)", len(records))
	}
}

func TestSessionsByTask(t *testing.T) {
	store := createTestStore(t)

	s1 := createTestSession(t, store, "nova", "claude")
	s2 := createTestSession(t, store, "ember", "claude")

	if err := s1.Finalize(FinalizeOptions{TaskID: "task-conv", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize s1: %v", err)
	}
	if err := s2.Finalize(FinalizeOptions{TaskID: "task-other", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize s2: %v", err)
	}

	records, err := store.SessionsByTask("task-conv")
	if err != nil {
		t.Fatalf("SessionsByTask: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("got %d records, want 1", len(records))
	}
	if len(records) > 0 && records[0].TaskID != "task-conv" {
		t.Errorf("TaskID = %q, want %q", records[0].TaskID, "task-conv")
	}
}

func TestLoadMetadata(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	// Finalize the session.
	err := sess.Finalize(FinalizeOptions{
		TaskID:       "task-meta",
		ExitCode:     0,
		InputTokens:  9000,
		OutputTokens: 2500,
		DiffStats: DiffStats{
			FilesChanged: 2,
			LinesAdded:   50,
			LinesRemoved: 10,
		},
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Load metadata back.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}

	// Verify round-trip.
	if meta.SessionID != sess.SessionID() {
		t.Errorf("SessionID = %q, want %q", meta.SessionID, sess.SessionID())
	}
	if meta.TaskID != "task-meta" {
		t.Errorf("TaskID = %q, want %q", meta.TaskID, "task-meta")
	}
	if meta.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", meta.Status, StatusCompleted)
	}
	if meta.InputTokens != 9000 {
		t.Errorf("InputTokens = %d, want 9000", meta.InputTokens)
	}
	if meta.FilesChanged != 2 {
		t.Errorf("FilesChanged = %d, want 2", meta.FilesChanged)
	}
	if meta.EndedAt == nil {
		t.Error("EndedAt is nil")
	}
}

func TestLoadTranscript(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")
	sid := sess.SessionID()
	now := time.Now().UTC()

	// Append entries in order — Seq is auto-assigned by AppendTranscript.
	entries := []TranscriptEntry{
		{Timestamp: now, Role: "user", Type: "text", Content: "Hello"},
		{Timestamp: now.Add(time.Second), Role: "assistant", Type: "text", Content: "Hi"},
		{Timestamp: now.Add(2 * time.Second), Role: "assistant", Type: "tool_use", ToolName: "Read"},
	}
	for _, e := range entries {
		if err := store.AppendTranscript(sid, e); err != nil {
			t.Fatalf("AppendTranscript: %v", err)
		}
	}

	// Load transcript.
	loaded, err := store.LoadTranscript(sid)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("got %d entries, want 3", len(loaded))
	}

	// Verify Seq is auto-assigned monotonically.
	for i, e := range loaded {
		expected := i + 1
		if e.Seq != expected {
			t.Errorf("entry[%d].Seq = %d, want %d", i, e.Seq, expected)
		}
	}

	// Verify content preserves write order.
	if loaded[0].Content != "Hello" {
		t.Errorf("entry[0].Content = %q, want %q", loaded[0].Content, "Hello")
	}
	if loaded[1].Content != "Hi" {
		t.Errorf("entry[1].Content = %q, want %q", loaded[1].Content, "Hi")
	}
	if loaded[2].ToolName != "Read" {
		t.Errorf("entry[2].ToolName = %q, want %q", loaded[2].ToolName, "Read")
	}
}

func TestReadPrompt(t *testing.T) {
	store := createTestStore(t)
	sess, err := store.CreateSession(CreateOptions{
		AgentName:  "nova",
		Backend:    "claude",
		Prompt:     "Implement the widget feature with tests",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	prompt, err := store.ReadPrompt(sess.SessionID())
	if err != nil {
		t.Fatalf("ReadPrompt: %v", err)
	}
	if prompt != "Implement the widget feature with tests" {
		t.Errorf("prompt = %q, want %q", prompt, "Implement the widget feature with tests")
	}
}

func TestPurgeOlderThan(t *testing.T) {
	store := createTestStore(t)

	// Create an "old" session.
	oldSess := createTestSession(t, store, "nova", "claude")
	if err := oldSess.Finalize(FinalizeOptions{TaskID: "task-old", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize old: %v", err)
	}

	// Backdated the EndedAt to 10 days ago by rewriting metadata and index.
	tenDaysAgo := time.Now().UTC().Add(-10 * 24 * time.Hour)
	oldSess.Meta.EndedAt = &tenDaysAgo
	sessDir := filepath.Join(store.dir, oldSess.SessionID())
	if err := writeMetadataAtomic(sessDir, oldSess.Meta); err != nil {
		t.Fatalf("rewrite metadata: %v", err)
	}
	// Rewrite index.jsonl with the backdated record.
	rewriteIndex(t, store, oldSess.Meta.SessionRecord)

	// Create a "new" session.
	newSess := createTestSession(t, store, "ember", "claude")
	if err := newSess.Finalize(FinalizeOptions{TaskID: "task-new", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize new: %v", err)
	}

	// Purge sessions older than 5 days.
	purged, err := store.PurgeOlderThan(5 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}

	// Verify old session directory is removed.
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Errorf("old session dir should be removed, stat err: %v", err)
	}

	// Verify new session directory still exists.
	newDir := filepath.Join(store.dir, newSess.SessionID())
	if _, err := os.Stat(newDir); err != nil {
		t.Errorf("new session dir should still exist: %v", err)
	}
}

func TestPurgeOlderThan_SkipsRunning(t *testing.T) {
	store := createTestStore(t)

	// Create a session but do NOT finalize (status=running).
	runningSess := createTestSession(t, store, "nova", "claude")

	// Manually write a backdated running record to index.jsonl
	// to simulate an old running session.
	tenDaysAgo := time.Now().UTC().Add(-10 * 24 * time.Hour)
	rec := runningSess.Meta.SessionRecord
	rec.StartedAt = tenDaysAgo
	rec.EndedAt = &tenDaysAgo // even with old EndedAt, running status protects it
	rec.Status = StatusRunning
	writeIndexRecord(t, store, rec)

	// Create and finalize an old completed session.
	oldSess := createTestSession(t, store, "ember", "claude")
	if err := oldSess.Finalize(FinalizeOptions{TaskID: "task-old", ExitCode: 0}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	oldSess.Meta.EndedAt = &tenDaysAgo
	rewriteIndex(t, store, oldSess.Meta.SessionRecord)

	// Purge: should only remove the completed old session, not the running one.
	purged, err := store.PurgeOlderThan(5 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}

	// Running session dir should still exist.
	runDir := filepath.Join(store.dir, runningSess.SessionID())
	if _, err := os.Stat(runDir); err != nil {
		t.Errorf("running session dir should still exist: %v", err)
	}
}

func TestLoadMetadata_PathTraversal(t *testing.T) {
	store := createTestStore(t)

	traversalIDs := []string{
		"../etc/passwd",
		"../../outside",
		"a/b/c",
		"session\\id",
		"..",
		"valid/../../../escape",
	}

	for _, id := range traversalIDs {
		_, err := store.LoadMetadata(id)
		if err == nil {
			t.Errorf("LoadMetadata(%q) should have returned error", id)
		}
	}

	// Also test LoadTranscript and ReadPrompt with traversal.
	for _, id := range traversalIDs {
		_, err := store.LoadTranscript(id)
		if err == nil {
			t.Errorf("LoadTranscript(%q) should have returned error", id)
		}
		_, err = store.ReadPrompt(id)
		if err == nil {
			t.Errorf("ReadPrompt(%q) should have returned error", id)
		}
	}
}

func TestSaveMetadata_RoundTrip(t *testing.T) {
	store := createTestStore(t)
	sessionID := "test-save-meta-roundtrip"

	// Create the session directory manually.
	sessDir := filepath.Join(store.Dir(), sessionID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}

	// Write initial metadata.
	now := time.Now().UTC()
	meta := &SessionMetadata{
		SessionRecord: SessionRecord{
			SchemaVersion:    CurrentSchemaVersion,
			SessionID:        sessionID,
			AgentName:        "nova",
			Backend:          "claude",
			Status:           StatusRunning,
			StartedAt:        now,
			InputTokens:      0,
			OutputTokens:     0,
			CacheReadTokens:  0,
			CacheWriteTokens: 0,
		},
	}
	if err := store.SaveMetadata(sessionID, meta); err != nil {
		t.Fatalf("initial SaveMetadata: %v", err)
	}

	// Patch token usage fields and save again.
	meta.InputTokens = 5000
	meta.OutputTokens = 2000
	meta.CacheReadTokens = 1000
	meta.CacheWriteTokens = 500
	meta.EstimatedCostUSD = 0.075
	if err := store.SaveMetadata(sessionID, meta); err != nil {
		t.Fatalf("patched SaveMetadata: %v", err)
	}

	// Load and verify round-trip.
	loaded, err := store.LoadMetadata(sessionID)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if loaded.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", loaded.SessionID, sessionID)
	}
	if loaded.AgentName != "nova" {
		t.Errorf("AgentName = %q, want %q", loaded.AgentName, "nova")
	}
	if loaded.InputTokens != 5000 {
		t.Errorf("InputTokens = %d, want 5000", loaded.InputTokens)
	}
	if loaded.OutputTokens != 2000 {
		t.Errorf("OutputTokens = %d, want 2000", loaded.OutputTokens)
	}
	if loaded.CacheReadTokens != 1000 {
		t.Errorf("CacheReadTokens = %d, want 1000", loaded.CacheReadTokens)
	}
	if loaded.CacheWriteTokens != 500 {
		t.Errorf("CacheWriteTokens = %d, want 500", loaded.CacheWriteTokens)
	}
	if loaded.EstimatedCostUSD != 0.075 {
		t.Errorf("EstimatedCostUSD = %f, want 0.075", loaded.EstimatedCostUSD)
	}
}

func TestSaveMetadata_InvalidSessionID(t *testing.T) {
	store := createTestStore(t)

	meta := &SessionMetadata{
		SessionRecord: SessionRecord{
			SessionID: "valid-id",
			Status:    StatusRunning,
		},
	}

	invalidIDs := []string{
		"../escape",
		"a/b",
		"a\\b",
		"",
	}

	for _, id := range invalidIDs {
		err := store.SaveMetadata(id, meta)
		if err == nil {
			t.Errorf("SaveMetadata(%q) should return error", id)
		}
	}
}

func TestFinalize_NonZeroOptsTokens(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	// Simulate hook-captured data already on disk via SaveMetadata.
	sess.Meta.InputTokens = 1000
	sess.Meta.OutputTokens = 500
	sess.Meta.CacheReadTokens = 200
	sess.Meta.CacheWriteTokens = 100
	sess.Meta.EstimatedCostUSD = 0.01
	if err := store.SaveMetadata(sess.SessionID(), &sess.Meta); err != nil {
		t.Fatalf("SaveMetadata (hook sim): %v", err)
	}

	// Finalize with non-zero opts tokens — should use opts values, not disk.
	err := sess.Finalize(FinalizeOptions{
		TaskID:           "task-nonzero",
		ExitCode:         0,
		InputTokens:      8000,
		OutputTokens:     3000,
		CacheReadTokens:  6000,
		CacheWriteTokens: 700,
		EstimatedCostUSD: 0.09,
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// In-memory should reflect opts values.
	if sess.Meta.InputTokens != 8000 {
		t.Errorf("InputTokens = %d, want 8000", sess.Meta.InputTokens)
	}
	if sess.Meta.OutputTokens != 3000 {
		t.Errorf("OutputTokens = %d, want 3000", sess.Meta.OutputTokens)
	}
	if sess.Meta.CacheReadTokens != 6000 {
		t.Errorf("CacheReadTokens = %d, want 6000", sess.Meta.CacheReadTokens)
	}
	if sess.Meta.CacheWriteTokens != 700 {
		t.Errorf("CacheWriteTokens = %d, want 700", sess.Meta.CacheWriteTokens)
	}
	if sess.Meta.EstimatedCostUSD != 0.09 {
		t.Errorf("EstimatedCostUSD = %f, want 0.09", sess.Meta.EstimatedCostUSD)
	}

	// Verify on disk as well.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.InputTokens != 8000 {
		t.Errorf("disk InputTokens = %d, want 8000", meta.InputTokens)
	}
	if meta.OutputTokens != 3000 {
		t.Errorf("disk OutputTokens = %d, want 3000", meta.OutputTokens)
	}
}

func TestFinalize_ZeroOptsPreservesHookData(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	// Simulate the SessionEnd hook writing token data to disk via SaveMetadata.
	sess.Meta.InputTokens = 4200
	sess.Meta.OutputTokens = 1800
	sess.Meta.CacheReadTokens = 3000
	sess.Meta.CacheWriteTokens = 600
	sess.Meta.EstimatedCostUSD = 0.042
	if err := store.SaveMetadata(sess.SessionID(), &sess.Meta); err != nil {
		t.Fatalf("SaveMetadata (hook sim): %v", err)
	}

	// Reset in-memory token fields to zero to simulate a fresh Finalize call
	// where the caller (e.g., auto-mode) has no collector data.
	sess.Meta.InputTokens = 0
	sess.Meta.OutputTokens = 0
	sess.Meta.CacheReadTokens = 0
	sess.Meta.CacheWriteTokens = 0
	sess.Meta.EstimatedCostUSD = 0

	// Finalize with zero token opts — should re-read hook data from disk.
	err := sess.Finalize(FinalizeOptions{
		TaskID:   "task-hook",
		ExitCode: 0,
		// All token fields intentionally zero (default).
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// In-memory should now have the hook-captured values from disk.
	if sess.Meta.InputTokens != 4200 {
		t.Errorf("InputTokens = %d, want 4200", sess.Meta.InputTokens)
	}
	if sess.Meta.OutputTokens != 1800 {
		t.Errorf("OutputTokens = %d, want 1800", sess.Meta.OutputTokens)
	}
	if sess.Meta.CacheReadTokens != 3000 {
		t.Errorf("CacheReadTokens = %d, want 3000", sess.Meta.CacheReadTokens)
	}
	if sess.Meta.CacheWriteTokens != 600 {
		t.Errorf("CacheWriteTokens = %d, want 600", sess.Meta.CacheWriteTokens)
	}
	if sess.Meta.EstimatedCostUSD != 0.042 {
		t.Errorf("EstimatedCostUSD = %f, want 0.042", sess.Meta.EstimatedCostUSD)
	}

	// Verify on disk.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.InputTokens != 4200 {
		t.Errorf("disk InputTokens = %d, want 4200", meta.InputTokens)
	}
	if meta.OutputTokens != 1800 {
		t.Errorf("disk OutputTokens = %d, want 1800", meta.OutputTokens)
	}
	if meta.CacheReadTokens != 3000 {
		t.Errorf("disk CacheReadTokens = %d, want 3000", meta.CacheReadTokens)
	}
	if meta.CacheWriteTokens != 600 {
		t.Errorf("disk CacheWriteTokens = %d, want 600", meta.CacheWriteTokens)
	}
	if meta.EstimatedCostUSD != 0.042 {
		t.Errorf("disk EstimatedCostUSD = %f, want 0.042", meta.EstimatedCostUSD)
	}
}

func TestFinalize_ZeroOptsMetadataReadFails(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	// Delete the metadata.json file on disk so LoadMetadata will fail.
	metaPath := filepath.Join(store.Dir(), sess.SessionID(), "metadata.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove metadata.json: %v", err)
	}

	// Finalize with zero token opts — LoadMetadata will fail, tokens stay zero.
	err := sess.Finalize(FinalizeOptions{
		TaskID:   "task-nometafile",
		ExitCode: 0,
		// All token fields intentionally zero.
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// Token fields should all be zero (graceful degradation).
	if sess.Meta.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", sess.Meta.InputTokens)
	}
	if sess.Meta.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0", sess.Meta.OutputTokens)
	}
	if sess.Meta.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", sess.Meta.CacheReadTokens)
	}
	if sess.Meta.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens = %d, want 0", sess.Meta.CacheWriteTokens)
	}
	if sess.Meta.EstimatedCostUSD != 0 {
		t.Errorf("EstimatedCostUSD = %f, want 0", sess.Meta.EstimatedCostUSD)
	}

	// Non-token fields should still be set correctly.
	if sess.Meta.TaskID != "task-nometafile" {
		t.Errorf("TaskID = %q, want %q", sess.Meta.TaskID, "task-nometafile")
	}
	if sess.Meta.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", sess.Meta.Status, StatusCompleted)
	}
}

func TestFinalize_PartialOptsPreservesDiskValues(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	// Simulate hook-captured data on disk.
	sess.Meta.InputTokens = 5000
	sess.Meta.OutputTokens = 2000
	sess.Meta.CacheReadTokens = 3000
	sess.Meta.CacheWriteTokens = 600
	sess.Meta.EstimatedCostUSD = 0.05
	if err := store.SaveMetadata(sess.SessionID(), &sess.Meta); err != nil {
		t.Fatalf("SaveMetadata (hook sim): %v", err)
	}

	// Reset in-memory to zero before Finalize.
	sess.Meta.InputTokens = 0
	sess.Meta.OutputTokens = 0
	sess.Meta.CacheReadTokens = 0
	sess.Meta.CacheWriteTokens = 0
	sess.Meta.EstimatedCostUSD = 0

	// Finalize with partial opts: some non-zero, some zero.
	err := sess.Finalize(FinalizeOptions{
		TaskID:           "task-partial",
		ExitCode:         0,
		InputTokens:      8000, // non-zero → opts wins
		OutputTokens:     0,    // zero → disk preserved (2000)
		CacheReadTokens:  0,    // zero → disk preserved (3000)
		CacheWriteTokens: 700,  // non-zero → opts wins
		EstimatedCostUSD: 0,    // zero → disk preserved (0.05)
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// Verify in-memory: per-field merge.
	if sess.Meta.InputTokens != 8000 {
		t.Errorf("InputTokens = %d, want 8000", sess.Meta.InputTokens)
	}
	if sess.Meta.OutputTokens != 2000 {
		t.Errorf("OutputTokens = %d, want 2000", sess.Meta.OutputTokens)
	}
	if sess.Meta.CacheReadTokens != 3000 {
		t.Errorf("CacheReadTokens = %d, want 3000", sess.Meta.CacheReadTokens)
	}
	if sess.Meta.CacheWriteTokens != 700 {
		t.Errorf("CacheWriteTokens = %d, want 700", sess.Meta.CacheWriteTokens)
	}
	if sess.Meta.EstimatedCostUSD != 0.05 {
		t.Errorf("EstimatedCostUSD = %f, want 0.05", sess.Meta.EstimatedCostUSD)
	}

	// Verify on disk.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.InputTokens != 8000 {
		t.Errorf("disk InputTokens = %d, want 8000", meta.InputTokens)
	}
	if meta.OutputTokens != 2000 {
		t.Errorf("disk OutputTokens = %d, want 2000", meta.OutputTokens)
	}
	if meta.CacheReadTokens != 3000 {
		t.Errorf("disk CacheReadTokens = %d, want 3000", meta.CacheReadTokens)
	}
	if meta.CacheWriteTokens != 700 {
		t.Errorf("disk CacheWriteTokens = %d, want 700", meta.CacheWriteTokens)
	}
	if meta.EstimatedCostUSD != 0.05 {
		t.Errorf("disk EstimatedCostUSD = %f, want 0.05", meta.EstimatedCostUSD)
	}
}

func TestFinalize_PartialDiskPreservesOptsValues(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")

	// Simulate hook-captured data on disk — partial (only OutputTokens non-zero).
	sess.Meta.InputTokens = 0
	sess.Meta.OutputTokens = 2000
	sess.Meta.CacheReadTokens = 0
	sess.Meta.CacheWriteTokens = 0
	sess.Meta.EstimatedCostUSD = 0
	if err := store.SaveMetadata(sess.SessionID(), &sess.Meta); err != nil {
		t.Fatalf("SaveMetadata (hook sim): %v", err)
	}

	// Reset in-memory to zero before Finalize.
	sess.Meta.InputTokens = 0
	sess.Meta.OutputTokens = 0
	sess.Meta.CacheReadTokens = 0
	sess.Meta.CacheWriteTokens = 0
	sess.Meta.EstimatedCostUSD = 0

	// Finalize with opts providing different fields.
	err := sess.Finalize(FinalizeOptions{
		TaskID:           "task-partial2",
		ExitCode:         0,
		InputTokens:      8000, // opts
		OutputTokens:     0,    // zero → disk preserved (2000)
		CacheReadTokens:  1000, // opts
		CacheWriteTokens: 0,    // both zero → 0
		EstimatedCostUSD: 0.09, // opts
	})
	if err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// Verify in-memory.
	if sess.Meta.InputTokens != 8000 {
		t.Errorf("InputTokens = %d, want 8000", sess.Meta.InputTokens)
	}
	if sess.Meta.OutputTokens != 2000 {
		t.Errorf("OutputTokens = %d, want 2000", sess.Meta.OutputTokens)
	}
	if sess.Meta.CacheReadTokens != 1000 {
		t.Errorf("CacheReadTokens = %d, want 1000", sess.Meta.CacheReadTokens)
	}
	if sess.Meta.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens = %d, want 0", sess.Meta.CacheWriteTokens)
	}
	if sess.Meta.EstimatedCostUSD != 0.09 {
		t.Errorf("EstimatedCostUSD = %f, want 0.09", sess.Meta.EstimatedCostUSD)
	}

	// Verify on disk.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.InputTokens != 8000 {
		t.Errorf("disk InputTokens = %d, want 8000", meta.InputTokens)
	}
	if meta.OutputTokens != 2000 {
		t.Errorf("disk OutputTokens = %d, want 2000", meta.OutputTokens)
	}
	if meta.CacheReadTokens != 1000 {
		t.Errorf("disk CacheReadTokens = %d, want 1000", meta.CacheReadTokens)
	}
	if meta.CacheWriteTokens != 0 {
		t.Errorf("disk CacheWriteTokens = %d, want 0", meta.CacheWriteTokens)
	}
	if meta.EstimatedCostUSD != 0.09 {
		t.Errorf("disk EstimatedCostUSD = %f, want 0.09", meta.EstimatedCostUSD)
	}
}

// rewriteIndex replaces the last line in index.jsonl with a modified record.
// Used in tests to backdate EndedAt for purge testing.
func rewriteIndex(t *testing.T, store *Store, rec SessionRecord) {
	t.Helper()
	indexPath := filepath.Join(store.dir, "index.jsonl")

	// Read existing lines.
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	// Parse all lines, replace matching SessionID.
	var lines [][]byte
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var existing SessionRecord
		if err := json.Unmarshal(line, &existing); err != nil {
			lines = append(lines, line)
			continue
		}
		if existing.SessionID == rec.SessionID {
			updated, err := json.Marshal(rec)
			if err != nil {
				t.Fatalf("marshal updated record: %v", err)
			}
			lines = append(lines, updated)
		} else {
			lines = append(lines, line)
		}
	}

	// Rewrite the file.
	var out []byte
	for _, line := range lines {
		out = append(out, line...)
		out = append(out, '\n')
	}
	if err := os.WriteFile(indexPath, out, 0o600); err != nil {
		t.Fatalf("rewrite index: %v", err)
	}
}

// writeIndexRecord appends a single record to index.jsonl.
func writeIndexRecord(t *testing.T, store *Store, rec SessionRecord) {
	t.Helper()
	if err := store.appendIndex(rec); err != nil {
		t.Fatalf("appendIndex: %v", err)
	}
}

// splitLines splits data by newlines, returning non-empty byte slices.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := data[start:]
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
