package localredis

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestShouldPersist_FleetFTSExcluded pins the snapshot boundary for
// fleet-db's full-text-search namespace: every fleet-db:*:fts:* key is
// derived state rebuilt by fleet-db at startup, so none of it belongs
// in the snapshot — while everything else fleet-mode persists today
// keeps persisting, and non-fleet-db keys are never affected.
func TestShouldPersist_FleetFTSExcluded(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	m, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	excluded := []string{
		"fleet-db:WS1:fts:tok:webhook",
		"fleet-db:WS1:fts:issue:WS1-42",
		"fleet-db:WS1:fts:registry",
		"fleet-db:WS1:fts:version",
	}
	kept := []string{
		"fleet-db:WS1:issues:WS1-42",
		"fleet-db:WS1:idx:status:open",
		"fleet-db:workspaces",
		"fleet:jwt-signing-key:current",
		// A non-fleet-db key containing ":fts:" — the exclusion is scoped
		// to the fleet-db: namespace and must not touch it.
		"ws:ws1:fts:weird",
	}
	for _, key := range append(append([]string{}, excluded...), kept...) {
		if err := m.Client().Set(ctx, key, "v", 0).Err(); err != nil {
			t.Fatalf("Set %s: %v", key, err)
		}
	}
	if err := m.Close(); err != nil { // final dump writes the snapshot
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	for _, key := range excluded {
		if containsKey(t, data, key) {
			t.Errorf("excluded fts key %s was persisted", key)
		}
	}
	for _, key := range kept {
		if !containsKey(t, data, key) {
			t.Errorf("key %s missing from snapshot", key)
		}
	}

	// Reload: excluded keys must not reappear; kept keys must round-trip.
	m2, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager m2: %v", err)
	}
	defer m2.Close()
	for _, key := range excluded {
		if n, _ := m2.Client().Exists(ctx, key).Result(); n != 0 {
			t.Errorf("excluded key %s was replayed", key)
		}
	}
	for _, key := range kept {
		if n, _ := m2.Client().Exists(ctx, key).Result(); n != 1 {
			t.Errorf("kept key %s did not round-trip", key)
		}
	}
}

// TestShouldPersist_FTSExclusionInactiveWithoutFleetMode guards the
// non-fleet path: with fleetKeys=false, fleet-db keys (fts or not) were
// never persisted, and the new exclusion must not change anything else.
func TestShouldPersist_FTSExclusionInactiveWithoutFleetMode(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	m, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Client().Set(ctx, "fleet-db:WS1:fts:tok:webhook", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := m.Client().Set(ctx, "ws:ws1:fts:weird", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if containsKey(t, data, "fleet-db:WS1:fts:tok:webhook") {
		t.Error("fleet-db key persisted with fleetKeys=false")
	}
	if !containsKey(t, data, "ws:ws1:fts:weird") {
		t.Error("ws: key with :fts: fragment must still persist")
	}
}
