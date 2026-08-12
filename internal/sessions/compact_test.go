package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompactIndex_Empty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestCompactIndex_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create session dirs and index entries.
	for _, id := range []string{"sess-a", "sess-b"} {
		if err := os.MkdirAll(filepath.Join(store.Dir(), id), 0o700); err != nil {
			t.Fatal(err)
		}
		ended := time.Now().UTC()
		rec := SessionRecord{
			SessionID: id,
			AgentName: "test",
			Status:    StatusCompleted,
			StartedAt: ended.Add(-time.Hour),
			EndedAt:   &ended,
		}
		if err := store.ReIndex(rec); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (no duplicates, all dirs exist)", removed)
	}
}

func TestCompactIndex_RemovesDuplicates(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create session dir.
	sessID := "sess-dup"
	if err := os.MkdirAll(filepath.Join(store.Dir(), sessID), 0o700); err != nil {
		t.Fatal(err)
	}

	// Write running record, then finalized record (simulating real lifecycle).
	running := SessionRecord{
		SessionID: sessID,
		AgentName: "test",
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := store.ReIndex(running); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	finalized := SessionRecord{
		SessionID: sessID,
		AgentName: "test",
		Status:    StatusCompleted,
		StartedAt: running.StartedAt,
		EndedAt:   &ended,
	}
	if err := store.ReIndex(finalized); err != nil {
		t.Fatal(err)
	}

	// Before compaction: 2 lines.
	total, unique, err := store.CountIndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || unique != 1 {
		t.Fatalf("before compact: total=%d unique=%d, want 2/1", total, unique)
	}

	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	// After compaction: 1 line.
	total, unique, err = store.CountIndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || unique != 1 {
		t.Errorf("after compact: total=%d unique=%d, want 1/1", total, unique)
	}
}

func TestCompactIndex_RemovesOrphanedEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Create one session dir, but index has two entries.
	existsID := "sess-exists"
	orphanID := "sess-orphan"
	if err := os.MkdirAll(filepath.Join(store.Dir(), existsID), 0o700); err != nil {
		t.Fatal(err)
	}
	// Don't create dir for orphanID.

	ended := time.Now().UTC()
	for _, id := range []string{existsID, orphanID} {
		rec := SessionRecord{
			SessionID: id,
			AgentName: "test",
			Status:    StatusCompleted,
			StartedAt: ended.Add(-time.Hour),
			EndedAt:   &ended,
		}
		if err := store.ReIndex(rec); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (orphan removed)", removed)
	}

	// Verify surviving entry is the one with existing dir.
	records, err := store.readDedupedIndex(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].SessionID != existsID {
		t.Errorf("surviving ID = %q, want %q", records[0].SessionID, existsID)
	}
}

func TestCompactIndex_CorruptLines(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sessID := "sess-valid"
	if err := os.MkdirAll(filepath.Join(store.Dir(), sessID), 0o700); err != nil {
		t.Fatal(err)
	}

	// Write a valid entry then a corrupt line.
	indexPath := filepath.Join(store.Dir(), "index.jsonl")
	ended := time.Now().UTC()
	rec := SessionRecord{
		SessionID: sessID,
		AgentName: "test",
		Status:    StatusCompleted,
		StartedAt: ended.Add(-time.Hour),
		EndedAt:   &ended,
	}
	data, _ := json.Marshal(rec)
	content := string(data) + "\n{corrupt line\n"
	if err := os.WriteFile(indexPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Before: 2 lines (1 valid + 1 corrupt).
	total, _, err := store.CountIndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("before compact total = %d, want 2", total)
	}

	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (corrupt line dropped)", removed)
	}
}

func TestCompactIndex_PreservesRunning(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sessID := "sess-running"
	if err := os.MkdirAll(filepath.Join(store.Dir(), sessID), 0o700); err != nil {
		t.Fatal(err)
	}

	rec := SessionRecord{
		SessionID: sessID,
		AgentName: "test",
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := store.ReIndex(rec); err != nil {
		t.Fatal(err)
	}

	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (running session preserved)", removed)
	}
}

func TestCompactIndex_MixedDuplicatesAndOrphans(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	ended := time.Now().UTC()

	// Create 3 session dirs.
	for _, id := range []string{"sess-a", "sess-b", "sess-c"} {
		if err := os.MkdirAll(filepath.Join(store.Dir(), id), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// Index: a(running), b(running), c(running), a(completed).
	for _, id := range []string{"sess-a", "sess-b", "sess-c"} {
		rec := SessionRecord{SessionID: id, AgentName: "t", Status: StatusRunning, StartedAt: ended.Add(-time.Hour)}
		if err := store.ReIndex(rec); err != nil {
			t.Fatal(err)
		}
	}
	finalRec := SessionRecord{
		SessionID: "sess-a",
		AgentName: "t",
		Status:    StatusCompleted,
		StartedAt: ended.Add(-time.Hour),
		EndedAt:   &ended,
	}
	if err := store.ReIndex(finalRec); err != nil {
		t.Fatal(err)
	}

	// Now remove sess-c's directory (orphan).
	if err := os.RemoveAll(filepath.Join(store.Dir(), "sess-c")); err != nil {
		t.Fatal(err)
	}

	// 4 total lines, after compaction: a(completed) + b(running) = 2 surviving.
	// Removed: 1 duplicate (a running) + 1 orphan (c) = 2.
	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (1 duplicate + 1 orphan)", removed)
	}

	total, unique, err := store.CountIndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if unique != 2 {
		t.Errorf("unique = %d, want 2", unique)
	}
}

func TestCompactIndex_Idempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sessID := "sess-idem"
	if err := os.MkdirAll(filepath.Join(store.Dir(), sessID), 0o700); err != nil {
		t.Fatal(err)
	}

	// Write running then completed entry (duplicate).
	running := SessionRecord{SessionID: sessID, AgentName: "t", Status: StatusRunning, StartedAt: time.Now().UTC()}
	if err := store.ReIndex(running); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	completed := SessionRecord{SessionID: sessID, AgentName: "t", Status: StatusCompleted, StartedAt: running.StartedAt, EndedAt: &ended}
	if err := store.ReIndex(completed); err != nil {
		t.Fatal(err)
	}

	// First compaction removes 1 duplicate.
	removed1, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex 1: %v", err)
	}
	if removed1 != 1 {
		t.Errorf("first compaction removed = %d, want 1", removed1)
	}

	// Second compaction is a no-op.
	removed2, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex 2: %v", err)
	}
	if removed2 != 0 {
		t.Errorf("second compaction removed = %d, want 0 (idempotent)", removed2)
	}
}

func TestCompactIndex_PreservesRecordData(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sessID := "sess-preserve"
	if err := os.MkdirAll(filepath.Join(store.Dir(), sessID), 0o700); err != nil {
		t.Fatal(err)
	}

	// Write running then completed with extra fields.
	running := SessionRecord{SessionID: sessID, AgentName: "falcon", Status: StatusRunning, StartedAt: time.Now().UTC()}
	if err := store.ReIndex(running); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	completed := SessionRecord{
		SessionID:   sessID,
		AgentName:   "falcon",
		Backend:     "claude",
		Status:      StatusCompleted,
		StartedAt:   running.StartedAt,
		EndedAt:     &ended,
		DurationS:   120.5,
		InputTokens: 5000,
		ExitCode:    0,
	}
	if err := store.ReIndex(completed); err != nil {
		t.Fatal(err)
	}

	// Compact removes the running entry.
	removed, err := store.CompactIndex()
	if err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	// Read back raw and verify fields preserved.
	indexPath := filepath.Join(store.Dir(), "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var rec SessionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rec.SessionID != sessID {
		t.Errorf("SessionID = %q, want %q", rec.SessionID, sessID)
	}
	if rec.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", rec.Status, StatusCompleted)
	}
	if rec.InputTokens != 5000 {
		t.Errorf("InputTokens = %d, want 5000", rec.InputTokens)
	}
	if rec.DurationS != 120.5 {
		t.Errorf("DurationS = %f, want 120.5", rec.DurationS)
	}
	if rec.Backend != "claude" {
		t.Errorf("Backend = %q, want %q", rec.Backend, "claude")
	}
}

func TestCountIndexEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Empty.
	total, unique, err := store.CountIndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || unique != 0 {
		t.Errorf("empty: total=%d unique=%d, want 0/0", total, unique)
	}

	// Add entries: 3 lines, 2 unique sessions.
	ended := time.Now().UTC()
	for _, id := range []string{"a", "b", "a"} {
		rec := SessionRecord{SessionID: id, AgentName: "t", Status: StatusCompleted, StartedAt: ended, EndedAt: &ended}
		if err := store.ReIndex(rec); err != nil {
			t.Fatal(err)
		}
	}

	total, unique, err = store.CountIndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if unique != 2 {
		t.Errorf("unique = %d, want 2", unique)
	}
}

func TestCountIndexEntries_CorruptLinesCountedInTotal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(t.Context(), dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Write a valid entry then corrupt lines.
	sessID := "sess-count"
	if err := os.MkdirAll(filepath.Join(store.Dir(), sessID), 0o700); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	rec := SessionRecord{SessionID: sessID, AgentName: "t", Status: StatusCompleted, StartedAt: ended, EndedAt: &ended}
	if err := store.ReIndex(rec); err != nil {
		t.Fatal(err)
	}

	indexPath := filepath.Join(store.Dir(), "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("corrupt line 1\n")
	_, _ = f.WriteString("{broken json\n")
	_ = f.Close()

	total, unique, err := store.CountIndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (1 valid + 2 corrupt)", total)
	}
	if unique != 1 {
		t.Errorf("unique = %d, want 1 (corrupt lines not counted as unique)", unique)
	}
}
