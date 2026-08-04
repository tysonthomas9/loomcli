package localredis

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewManager_StartsEmpty(t *testing.T) {
	m, err := NewManager("", false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	keys, err := m.Client().Keys(ctx, "*").Result()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected empty keyspace, got %v", keys)
	}
}

func TestDumpLoad_RoundTrip(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	// First manager: write some keys, dump, close.
	m1, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Persisted (tabmeta hash, ui-state hash, workspace-scoped string).
	if err := m1.Client().HSet(ctx, "terminal:meta:ws1:sess1", map[string]any{
		"label": "hello", "sort_order": "1", "pinned": "false",
	}).Err(); err != nil {
		t.Fatalf("HSet tabmeta: %v", err)
	}
	if err := m1.Client().HSet(ctx, "terminal:ui-state", map[string]any{
		"active_tab": "sess1",
	}).Err(); err != nil {
		t.Fatalf("HSet ui-state: %v", err)
	}
	// Workspace-scoped ui-state (the production key shape) — covered by the
	// same "terminal:ui-state" prefix via strings.HasPrefix.
	if err := m1.Client().HSet(ctx, "terminal:ui-state:ws1", map[string]any{
		"active_tab": "t1",
	}).Err(); err != nil {
		t.Fatalf("HSet scoped ui-state: %v", err)
	}
	if err := m1.Client().Set(ctx, "ws:ws1:issue:tabs:loomcli-abc.1", `{"tabs":[]}`, 24*time.Hour).Err(); err != nil {
		t.Fatalf("Set issuetabs: %v", err)
	}
	if err := m1.Client().Set(ctx, "ws:ws1:issue:sessions:loomcli-abc.1", `[]`, 0).Err(); err != nil {
		t.Fatalf("Set sessionhistory: %v", err)
	}
	// Excluded key — must not survive dump.
	if err := m1.Client().Set(ctx, "fleet:tasks:claimed:xyz", "worker-1", 0).Err(); err != nil {
		t.Fatalf("Set excluded: %v", err)
	}

	if err := m1.Close(); err != nil {
		t.Fatalf("Close m1: %v", err)
	}

	// Verify file exists and is valid JSON.
	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("ReadFile snapshot: %v", err)
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("Unmarshal snapshot: %v", err)
	}
	if snap.SchemaVersion != currentSchemaVersion {
		t.Errorf("schema_version = %d, want %d", snap.SchemaVersion, currentSchemaVersion)
	}

	foundFleetKey := false
	for _, e := range snap.Entries {
		if e.Key == "fleet:tasks:claimed:xyz" {
			foundFleetKey = true
		}
	}
	if foundFleetKey {
		t.Error("excluded key fleet:tasks:claimed:xyz was persisted")
	}

	// Second manager: load same snapshot, verify keys replayed.
	m2, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager m2: %v", err)
	}
	defer m2.Close()

	label, err := m2.Client().HGet(ctx, "terminal:meta:ws1:sess1", "label").Result()
	if err != nil {
		t.Fatalf("HGet after load: %v", err)
	}
	if label != "hello" {
		t.Errorf("label = %q, want %q", label, "hello")
	}

	active, err := m2.Client().HGet(ctx, "terminal:ui-state", "active_tab").Result()
	if err != nil {
		t.Fatalf("HGet ui-state: %v", err)
	}
	if active != "sess1" {
		t.Errorf("active_tab = %q, want %q", active, "sess1")
	}

	scopedActive, err := m2.Client().HGet(ctx, "terminal:ui-state:ws1", "active_tab").Result()
	if err != nil {
		t.Fatalf("HGet scoped ui-state: %v", err)
	}
	if scopedActive != "t1" {
		t.Errorf("scoped active_tab = %q, want %q", scopedActive, "t1")
	}

	tabs, err := m2.Client().Get(ctx, "ws:ws1:issue:tabs:loomcli-abc.1").Result()
	if err != nil {
		t.Fatalf("Get issuetabs: %v", err)
	}
	if tabs != `{"tabs":[]}` {
		t.Errorf("issuetabs = %q", tabs)
	}

	// TTL on issuetabs key must be preserved (within tolerance).
	ttl, err := m2.Client().TTL(ctx, "ws:ws1:issue:tabs:loomcli-abc.1").Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		t.Errorf("TTL = %v, want (0, 24h]", ttl)
	}

	// Excluded key must NOT have been replayed.
	exists, err := m2.Client().Exists(ctx, "fleet:tasks:claimed:xyz").Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists != 0 {
		t.Error("excluded key was loaded from snapshot")
	}
}

func TestFleetKeys_GatedByFlag(t *testing.T) {
	ctx := context.Background()
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")

	// fleetKeys=false: fleet:jwt-signing-key:* is NOT persisted.
	m1, _ := NewManager(snapPath, false, nil)
	_ = m1.Client().Set(ctx, "fleet:jwt-signing-key:current", "secret123", 0).Err()
	_ = m1.Close()

	data, _ := os.ReadFile(snapPath)
	if containsKey(t, data, "fleet:jwt-signing-key:current") {
		t.Error("fleet key persisted when fleetKeys=false")
	}

	// fleetKeys=true: fleet:jwt-signing-key:* IS persisted.
	snapPath2 := filepath.Join(t.TempDir(), "snapshot.json")
	m2, _ := NewManager(snapPath2, true, nil)
	_ = m2.Client().Set(ctx, "fleet:jwt-signing-key:current", "secret123", 0).Err()
	_ = m2.Close()

	data2, _ := os.ReadFile(snapPath2)
	if !containsKey(t, data2, "fleet:jwt-signing-key:current") {
		t.Error("fleet key not persisted when fleetKeys=true")
	}
}

func TestLoad_CorruptFileFallsBackToBackup(t *testing.T) {
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "snapshot.json")
	ctx := context.Background()

	// Seed manager, write data, dump — creates snapshot.json.
	m1, _ := NewManager(snapPath, false, nil)
	_ = m1.Client().HSet(ctx, "terminal:meta:ws1:sess1", "label", "v1").Err()
	_ = m1.Close() // writes snapshot.json

	// Second dump to rotate v1 → .bak.
	m2, _ := NewManager(snapPath, false, nil)
	_ = m2.Client().HSet(ctx, "terminal:meta:ws1:sess1", "label", "v2").Err()
	_ = m2.Close() // snapshot.json = v2, .bak = v1

	// Corrupt the primary.
	if err := os.WriteFile(snapPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Third manager should recover from .bak (v1).
	m3, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager with corrupt primary: %v", err)
	}
	defer m3.Close()
	label, err := m3.Client().HGet(ctx, "terminal:meta:ws1:sess1", "label").Result()
	if err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if label != "v1" {
		t.Errorf("label = %q, want %q (backup should have been used)", label, "v1")
	}
}

func TestLoad_MissingFileStartsEmpty(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "does-not-exist.json")
	m, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	keys, _ := m.Client().Keys(context.Background(), "*").Result()
	if len(keys) != 0 {
		t.Errorf("expected empty on missing snapshot, got %v", keys)
	}
}

func TestReplay_DropsExpiredByElapsedTime(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")

	// Forge a snapshot where DumpedAt is in the past and TTL is shorter
	// than the elapsed time — the entry must be skipped on load.
	past := time.Now().Add(-2 * time.Hour)
	fake := snapshot{
		SchemaVersion: currentSchemaVersion,
		DumpedAt:      past,
		Entries: []snapshotEntry{
			{
				Key:    "ws:ws1:issue:tabs:loomcli-expired.1",
				Type:   "string",
				TTLMs:  int64((30 * time.Minute) / time.Millisecond), // had 30min when dumped, but dumped 2h ago
				String: `{"tabs":[]}`,
			},
			{
				Key:   "terminal:meta:ws1:still-alive",
				Type:  "hash",
				TTLMs: -1, // no TTL, always loads
				Hash:  map[string]string{"label": "kept"},
			},
		},
	}
	data, err := json.Marshal(&fake)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	// Expired entry must NOT have been replayed.
	exists, err := m.Client().Exists(ctx, "ws:ws1:issue:tabs:loomcli-expired.1").Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists != 0 {
		t.Error("expired entry was replayed; TTL drift not corrected")
	}

	// Unexpired (no-TTL) entry must still load.
	label, err := m.Client().HGet(ctx, "terminal:meta:ws1:still-alive", "label").Result()
	if err != nil {
		t.Fatalf("HGet alive: %v", err)
	}
	if label != "kept" {
		t.Errorf("label = %q, want %q", label, "kept")
	}
}

func TestSnapshotFileMode_TightenedWhenFleetKeysOn(t *testing.T) {
	// Plain snapshot (no fleet keys): 0o644.
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	m1, _ := NewManager(snapPath, false, nil)
	_ = m1.Client().HSet(context.Background(), "terminal:meta:ws1:s", "label", "x").Err()
	_ = m1.Close()

	info, err := os.Stat(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("perm without fleet keys = %o, want 0644", got)
	}

	// Fleet-keyed snapshot: 0o600.
	snapPath2 := filepath.Join(t.TempDir(), "snapshot.json")
	m2, _ := NewManager(snapPath2, true, nil)
	_ = m2.Client().Set(context.Background(), "fleet:jwt-signing-key:current", "secret", 0).Err()
	_ = m2.Close()

	info2, err := os.Stat(snapPath2)
	if err != nil {
		t.Fatal(err)
	}
	if got := info2.Mode().Perm(); got != 0o600 {
		t.Errorf("perm with fleet keys = %o, want 0600", got)
	}
}

func TestSnapshotFileMode_TightensPreexistingFleetFiles(t *testing.T) {
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(snapPath, []byte(`{"schema_version":2,"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapPath+".tmp", []byte("stale tmp"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := NewManager(snapPath, true, nil)
	_ = m.Client().Set(context.Background(), "fleet:jwt-signing-key:current", "secret", 0).Err()
	_ = m.Close()

	for _, path := range []string{snapPath, snapPath + ".bak"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s perm = %o, want 0600", path, got)
		}
	}
	if _, err := os.Stat(snapPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale tmp should be removed after dump, err=%v", err)
	}
}

func TestDumpAtomicity_NoPartialOnRename(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	m, _ := NewManager(snapPath, false, nil)
	defer m.Close()

	_ = m.Client().HSet(ctx, "terminal:meta:ws1:sess1", "label", "hello").Err()
	if err := m.Dump(); err != nil {
		t.Fatalf("Dump: %v", err)
	}

	// Temp file must not linger after successful dump.
	if _, err := os.Stat(snapPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file lingered after Dump: err=%v", err)
	}
}

func containsKey(t *testing.T, data []byte, key string) bool {
	t.Helper()
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, e := range snap.Entries {
		if e.Key == key {
			return true
		}
	}
	return false
}
