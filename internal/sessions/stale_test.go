package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsStale_Fresh(t *testing.T) {
	rec := SessionRecord{
		SessionID: "test-fresh",
		Status:    StatusRunning,
		StartedAt: time.Now().UTC().Add(-1 * time.Hour),
		EndedAt:   nil,
	}
	if isStale(rec) {
		t.Error("1h-old running session should NOT be stale")
	}
}

func TestIsStale_Old(t *testing.T) {
	rec := SessionRecord{
		SessionID: "test-old",
		Status:    StatusRunning,
		StartedAt: time.Now().UTC().Add(-5 * time.Hour),
		EndedAt:   nil,
	}
	if !isStale(rec) {
		t.Error("5h-old running session should be stale")
	}
}

func TestIsStale_Completed(t *testing.T) {
	ended := time.Now().UTC().Add(-4 * time.Hour)
	rec := SessionRecord{
		SessionID: "test-completed",
		Status:    StatusCompleted,
		StartedAt: time.Now().UTC().Add(-5 * time.Hour),
		EndedAt:   &ended,
	}
	if isStale(rec) {
		t.Error("5h-old completed session should NOT be stale")
	}
}

func TestQuery_HealsStaleSession(t *testing.T) {
	store := createTestStore(t)

	// Create a running session normally.
	sess := createTestSession(t, store, "nova", "claude")
	sid := sess.SessionID()

	// Backdate StartedAt to 5 hours ago in metadata.json.
	fiveHoursAgo := time.Now().UTC().Add(-5 * time.Hour)
	sess.Meta.StartedAt = fiveHoursAgo
	sessDir := filepath.Join(store.dir, sid)
	if err := writeMetadataAtomic(sessDir, sess.Meta); err != nil {
		t.Fatalf("rewrite metadata: %v", err)
	}

	// Rewrite the index entry with the backdated StartedAt.
	rewriteIndex(t, store, sess.Meta.SessionRecord)

	// Query should auto-heal: return the session as aborted.
	records, err := store.Query(Filter{})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	rec := records[0]
	if rec.Status != StatusAborted {
		t.Errorf("Status = %q, want %q", rec.Status, StatusAborted)
	}
	if rec.EndedAt == nil {
		t.Fatal("EndedAt should be set after healing")
	}
	expectedEnd := fiveHoursAgo.Add(StaleSessionThreshold)
	if !rec.EndedAt.Equal(expectedEnd) {
		t.Errorf("EndedAt = %v, want %v", *rec.EndedAt, expectedEnd)
	}
	expectedDuration := StaleSessionThreshold.Seconds()
	if rec.DurationS != expectedDuration {
		t.Errorf("DurationS = %f, want %f", rec.DurationS, expectedDuration)
	}

	// Verify metadata.json was updated on disk.
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.Status != StatusAborted {
		t.Errorf("disk metadata Status = %q, want %q", meta.Status, StatusAborted)
	}

	// A second Query should still return aborted (healed record in index).
	records2, err := store.Query(Filter{})
	if err != nil {
		t.Fatalf("Query2 error: %v", err)
	}
	if len(records2) != 1 {
		t.Fatalf("Query2 got %d records, want 1", len(records2))
	}
	if records2[0].Status != StatusAborted {
		t.Errorf("Query2 Status = %q, want %q", records2[0].Status, StatusAborted)
	}
}

func TestQuery_StaleFilterInteraction(t *testing.T) {
	store := createTestStore(t)

	// Create a running session and backdate it to make it stale.
	sess := createTestSession(t, store, "ember", "claude")
	sid := sess.SessionID()

	fiveHoursAgo := time.Now().UTC().Add(-5 * time.Hour)
	sess.Meta.StartedAt = fiveHoursAgo
	sessDir := filepath.Join(store.dir, sid)
	if err := writeMetadataAtomic(sessDir, sess.Meta); err != nil {
		t.Fatalf("rewrite metadata: %v", err)
	}
	rewriteIndex(t, store, sess.Meta.SessionRecord)

	// Query with Status=running should return nothing (stale session gets healed to aborted).
	records, err := store.Query(Filter{Status: StatusRunning})
	if err != nil {
		t.Fatalf("Query running error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records for Status=running, want 0 (stale session should be healed)", len(records))
	}

	// Query with Status=aborted should return the healed session.
	records2, err := store.Query(Filter{Status: StatusAborted})
	if err != nil {
		t.Fatalf("Query aborted error: %v", err)
	}
	if len(records2) != 1 {
		t.Errorf("got %d records for Status=aborted, want 1", len(records2))
	}
}

func TestSweepOrphans_NoSessions(t *testing.T) {
	store := createTestStore(t)
	count, err := store.SweepOrphans()
	if err != nil {
		t.Fatalf("SweepOrphans error: %v", err)
	}
	if count != 0 {
		t.Errorf("SweepOrphans = %d, want 0", count)
	}
}

func TestSweepOrphans_NoStale(t *testing.T) {
	store := createTestStore(t)
	sess := createTestSession(t, store, "nova", "claude")
	// Session started just now — not stale.
	count, err := store.SweepOrphans()
	if err != nil {
		t.Fatalf("SweepOrphans error: %v", err)
	}
	if count != 0 {
		t.Errorf("SweepOrphans = %d, want 0", count)
	}
	// Verify session is still running.
	recs, _ := store.Query(Filter{})
	if len(recs) != 1 || recs[0].Status != StatusRunning {
		t.Errorf("session should still be running, got %v", recs)
	}
	_ = sess
}

func TestSweepOrphans_HealsStale(t *testing.T) {
	store := createTestStore(t)

	sess := createTestSession(t, store, "nova", "claude")
	sid := sess.SessionID()

	// Backdate to 5 hours ago.
	fiveHoursAgo := time.Now().UTC().Add(-5 * time.Hour)
	sess.Meta.StartedAt = fiveHoursAgo
	sessDir := filepath.Join(store.dir, sid)
	if err := writeMetadataAtomic(sessDir, sess.Meta); err != nil {
		t.Fatalf("rewrite metadata: %v", err)
	}
	rewriteIndex(t, store, sess.Meta.SessionRecord)

	// SweepOrphans should heal exactly 1 session.
	count, err := store.SweepOrphans()
	if err != nil {
		t.Fatalf("SweepOrphans error: %v", err)
	}
	if count != 1 {
		t.Errorf("SweepOrphans = %d, want 1", count)
	}

	// Verify metadata.json on disk has status=aborted.
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.Status != StatusAborted {
		t.Errorf("disk metadata Status = %q, want %q", meta.Status, StatusAborted)
	}
	if meta.EndedAt == nil {
		t.Fatal("EndedAt should be set")
	}

	// A subsequent Query returns the session as aborted.
	recs, _ := store.Query(Filter{})
	if len(recs) != 1 || recs[0].Status != StatusAborted {
		t.Errorf("Query after sweep: got %v", recs)
	}

	// A subsequent SweepOrphans returns 0 (no re-healing).
	count2, err := store.SweepOrphans()
	if err != nil {
		t.Fatalf("SweepOrphans2 error: %v", err)
	}
	if count2 != 0 {
		t.Errorf("SweepOrphans2 = %d, want 0 (no re-healing)", count2)
	}
}

func TestSweepOrphans_MultipleStale(t *testing.T) {
	store := createTestStore(t)

	fiveHoursAgo := time.Now().UTC().Add(-5 * time.Hour)

	// Create 3 sessions: backdate 2 of them.
	for i, name := range []string{"alpha", "beta", "gamma"} {
		sess := createTestSession(t, store, name, "claude")
		if i < 2 { // backdate alpha and beta
			sess.Meta.StartedAt = fiveHoursAgo
			sessDir := filepath.Join(store.dir, sess.SessionID())
			if err := writeMetadataAtomic(sessDir, sess.Meta); err != nil {
				t.Fatalf("rewrite metadata %s: %v", name, err)
			}
			rewriteIndex(t, store, sess.Meta.SessionRecord)
		}
	}

	count, err := store.SweepOrphans()
	if err != nil {
		t.Fatalf("SweepOrphans error: %v", err)
	}
	if count != 2 {
		t.Errorf("SweepOrphans = %d, want 2", count)
	}

	// Verify the fresh session remains running.
	recs, _ := store.Query(Filter{Status: StatusRunning})
	if len(recs) != 1 {
		t.Errorf("expected 1 running session, got %d", len(recs))
	}
}

func TestSweepOrphans_SkipsCompleted(t *testing.T) {
	store := createTestStore(t)

	sess := createTestSession(t, store, "nova", "claude")

	// Finalize the session as completed.
	if err := sess.Finalize(FinalizeOptions{ExitCode: 0}); err != nil {
		t.Fatalf("Finalize error: %v", err)
	}

	// Backdate StartedAt (completed sessions should not be swept regardless).
	sid := sess.SessionID()
	meta, _ := store.LoadMetadata(sid)
	meta.StartedAt = time.Now().UTC().Add(-5 * time.Hour)
	sessDir := filepath.Join(store.dir, sid)
	if err := writeMetadataAtomic(sessDir, *meta); err != nil {
		t.Fatalf("rewrite metadata: %v", err)
	}

	count, err := store.SweepOrphans()
	if err != nil {
		t.Fatalf("SweepOrphans error: %v", err)
	}
	if count != 0 {
		t.Errorf("SweepOrphans = %d, want 0 (completed sessions are not stale)", count)
	}
}

func TestPurgeOlderThan_PurgesHealed(t *testing.T) {
	store := createTestStore(t)

	// Create a running session and backdate it to 5 hours ago (stale).
	sess := createTestSession(t, store, "falcon", "claude")
	sid := sess.SessionID()

	fiveHoursAgo := time.Now().UTC().Add(-5 * time.Hour)
	sess.Meta.StartedAt = fiveHoursAgo
	sessDir := filepath.Join(store.dir, sid)
	if err := writeMetadataAtomic(sessDir, sess.Meta); err != nil {
		t.Fatalf("rewrite metadata: %v", err)
	}
	rewriteIndex(t, store, sess.Meta.SessionRecord)

	// PurgeOlderThan triggers Query internally, which heals the stale session.
	// The healed session has EndedAt = StartedAt + 4h = 1h ago.
	// Purging anything older than 30 minutes should remove it.
	purged, err := store.PurgeOlderThan(30 * time.Minute)
	if err != nil {
		t.Fatalf("PurgeOlderThan error: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}

	// Verify session directory is removed.
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Errorf("session dir should be removed after purge, stat err: %v", err)
	}

	// Verify metadata was healed before purge by checking the index.
	// Read the raw index to confirm the healed record was appended.
	indexPath := filepath.Join(store.dir, "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var foundAborted bool
	for _, line := range splitLines(data) {
		var rec SessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.SessionID == sid && rec.Status == StatusAborted {
			foundAborted = true
		}
	}
	if !foundAborted {
		t.Error("expected an aborted index entry for the healed session")
	}
}
