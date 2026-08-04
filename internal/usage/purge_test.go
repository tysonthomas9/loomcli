package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPurgeOlderThan_NoFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	purged, err := s.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0", purged)
	}
}

func TestPurgeOlderThan_AllNew(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now().UTC()
	for range 3 {
		if err := s.Append(SessionUsage{
			AgentName: "test",
			StartedAt: now.Add(-time.Hour),
			EndedAt:   now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := s.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0 (all records newer than cutoff)", purged)
	}
}

func TestPurgeOlderThan_PurgesOld(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-time.Hour)

	// 2 old, 1 recent.
	for _, ended := range []time.Time{old, old, recent} {
		if err := s.Append(SessionUsage{
			AgentName: "test",
			StartedAt: ended.Add(-time.Minute),
			EndedAt:   ended,
		}); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := s.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 2 {
		t.Errorf("purged = %d, want 2", purged)
	}

	// Verify surviving records.
	records, err := s.Read(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
}

func TestPurgeOlderThan_ZeroEndedAt(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now().UTC()

	// Record with zero EndedAt but recent StartedAt → kept.
	if err := s.Append(SessionUsage{
		AgentName: "keep-started",
		StartedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// Record with zero EndedAt and zero StartedAt → kept (can't determine age).
	if err := s.Append(SessionUsage{
		AgentName: "keep-both-zero",
	}); err != nil {
		t.Fatal(err)
	}

	// Record with zero EndedAt and old StartedAt → purged (falls back to StartedAt).
	if err := s.Append(SessionUsage{
		AgentName: "purge-old-start",
		StartedAt: now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	purged, err := s.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}

	records, err := s.Read(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
}

func TestPurgeOlderThan_AllOld(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	old := time.Now().UTC().Add(-48 * time.Hour)
	for range 3 {
		if err := s.Append(SessionUsage{
			AgentName: "test",
			StartedAt: old.Add(-time.Minute),
			EndedAt:   old,
		}); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := s.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if purged != 3 {
		t.Errorf("purged = %d, want 3", purged)
	}

	// File should exist but be empty (or have no records).
	records, err := s.Read(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("len(records) = %d, want 0", len(records))
	}
}

func TestPurgeOlderThan_PreservesFormat(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now().UTC()
	if err := s.Append(SessionUsage{
		AgentName:   "recent",
		Backend:     "claude",
		InputTokens: 42,
		StartedAt:   now.Add(-time.Hour),
		EndedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	// Add an old record that will be purged.
	old := now.Add(-48 * time.Hour)
	if err := s.Append(SessionUsage{
		AgentName: "old",
		StartedAt: old.Add(-time.Minute),
		EndedAt:   old,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PurgeOlderThan(24 * time.Hour); err != nil {
		t.Fatal(err)
	}

	// Read raw file and verify it's valid JSONL.
	data, err := os.ReadFile(filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	var rec SessionUsage
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("surviving record is not valid JSON: %v", err)
	}
	if rec.AgentName != "recent" {
		t.Errorf("surviving AgentName = %q, want %q", rec.AgentName, "recent")
	}
	if rec.InputTokens != 42 {
		t.Errorf("surviving InputTokens = %d, want 42", rec.InputTokens)
	}
}
