package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeRecord_StampsVersion(t *testing.T) {
	rec := SessionRecord{
		SessionID: "test-v0",
		AgentName: "nova",
		Status:    StatusRunning,
		// SchemaVersion defaults to 0 (zero value).
	}

	normalizeRecord(&rec)

	if rec.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", rec.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestNormalizeRecord_AlreadyCurrent(t *testing.T) {
	rec := SessionRecord{
		SessionID:     "test-current",
		AgentName:     "ember",
		Status:        StatusCompleted,
		SchemaVersion: CurrentSchemaVersion,
	}

	normalizeRecord(&rec)

	if rec.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", rec.SchemaVersion, CurrentSchemaVersion)
	}
	// Verify other fields are not mutated.
	if rec.AgentName != "ember" {
		t.Errorf("AgentName = %q, want %q", rec.AgentName, "ember")
	}
	if rec.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", rec.Status, StatusCompleted)
	}
}

func TestNormalizeRecord_FutureVersion(t *testing.T) {
	futureVersion := CurrentSchemaVersion + 10
	rec := SessionRecord{
		SessionID:     "test-future",
		AgentName:     "falcon",
		Status:        StatusRunning,
		SchemaVersion: futureVersion,
	}

	normalizeRecord(&rec)

	if rec.SchemaVersion != futureVersion {
		t.Errorf("SchemaVersion = %d, want %d (should not downgrade future version)", rec.SchemaVersion, futureVersion)
	}
}

func TestNormalizeAfterLoad_DelegatesToNormalizeRecord(t *testing.T) {
	meta := SessionMetadata{
		SessionRecord: SessionRecord{
			SessionID: "test-meta-v0",
			AgentName: "nova",
			Status:    StatusRunning,
			// SchemaVersion defaults to 0.
		},
	}

	meta.NormalizeAfterLoad()

	if meta.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", meta.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestNormalizeRecord_Idempotent(t *testing.T) {
	rec := SessionRecord{
		SessionID: "test-idempotent",
		AgentName: "ember",
		Status:    StatusRunning,
	}

	normalizeRecord(&rec)
	first := rec.SchemaVersion

	normalizeRecord(&rec)
	second := rec.SchemaVersion

	if first != second {
		t.Errorf("not idempotent: first call = %d, second call = %d", first, second)
	}
	if second != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", second, CurrentSchemaVersion)
	}
}

func TestLoadMetadata_NormalizesOnLoad(t *testing.T) {
	store := createTestStore(t)

	// Create a session normally so we get a valid session directory.
	sess := createTestSession(t, store, "nova", "claude")
	sid := sess.SessionID()

	// Write metadata.json without schema_version to simulate v0 data on disk.
	v0Meta := map[string]interface{}{
		"session_id":  sid,
		"agent_name":  "nova",
		"backend":     "claude",
		"status":      "running",
		"started_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"attempt_num": 1,
	}
	v0Data, err := json.MarshalIndent(v0Meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal v0 metadata: %v", err)
	}
	metaPath := filepath.Join(store.dir, sid, "metadata.json")
	if err := os.WriteFile(metaPath, v0Data, 0o600); err != nil {
		t.Fatalf("write v0 metadata.json: %v", err)
	}

	// LoadMetadata should normalize the record.
	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", meta.SchemaVersion, CurrentSchemaVersion)
	}
	// Verify other fields survived the round-trip.
	if meta.AgentName != "nova" {
		t.Errorf("AgentName = %q, want %q", meta.AgentName, "nova")
	}
	if meta.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", meta.Status, StatusRunning)
	}
}

func TestQuery_NormalizesRecords(t *testing.T) {
	store := createTestStore(t)

	// Create a session to get a valid session directory and index entry.
	sess := createTestSession(t, store, "ember", "claude")
	sid := sess.SessionID()

	// Overwrite index.jsonl with a raw JSON line that has no schema_version field.
	v0Record := map[string]interface{}{
		"session_id":  sid,
		"agent_name":  "ember",
		"backend":     "claude",
		"status":      "running",
		"started_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"attempt_num": 1,
	}
	v0Line, err := json.Marshal(v0Record)
	if err != nil {
		t.Fatalf("marshal v0 record: %v", err)
	}
	indexPath := filepath.Join(store.dir, "index.jsonl")
	if err := os.WriteFile(indexPath, append(v0Line, '\n'), 0o600); err != nil {
		t.Fatalf("write v0 index.jsonl: %v", err)
	}

	// Query should normalize records.
	records, err := store.Query(Filter{})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", records[0].SchemaVersion, CurrentSchemaVersion)
	}
	if records[0].SessionID != sid {
		t.Errorf("SessionID = %q, want %q", records[0].SessionID, sid)
	}
}

func TestCreateSession_StampsSchemaVersion(t *testing.T) {
	store := createTestStore(t)

	sess, err := store.CreateSession(CreateOptions{
		AgentName:  "falcon",
		Backend:    "claude",
		Prompt:     "test schema version on create",
		AttemptNum: 1,
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}

	// Check in-memory metadata.
	if sess.Meta.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("in-memory SchemaVersion = %d, want %d", sess.Meta.SchemaVersion, CurrentSchemaVersion)
	}

	// Check metadata.json on disk.
	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("disk SchemaVersion = %d, want %d", meta.SchemaVersion, CurrentSchemaVersion)
	}

	// Check index.jsonl entry.
	records, err := store.Query(Filter{AgentName: "falcon"})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].SchemaVersion != CurrentSchemaVersion {
		t.Errorf("index SchemaVersion = %d, want %d", records[0].SchemaVersion, CurrentSchemaVersion)
	}
}
