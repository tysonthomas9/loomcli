package localredis

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestDump_SkipsWhenKeyspaceUnchanged verifies the content-hash short
// circuit: a Dump that produces the same entries as the previous one
// must NOT rewrite the snapshot file. Detected via mtime — second
// Dump leaves the file's mod time unchanged.
func TestDump_SkipsWhenKeyspaceUnchanged(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()
	m, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	// Write something persistable so the first Dump has content.
	if err := m.Client().HSet(ctx, "terminal:meta:ws1:sess1", map[string]any{"label": "x"}).Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	if err := m.Dump(); err != nil {
		t.Fatalf("Dump 1: %v", err)
	}
	stat1, err := os.Stat(snapPath)
	if err != nil {
		t.Fatalf("stat 1: %v", err)
	}
	// Second Dump with no changes — must be a no-op (file mtime unchanged).
	if err := m.Dump(); err != nil {
		t.Fatalf("Dump 2: %v", err)
	}
	stat2, err := os.Stat(snapPath)
	if err != nil {
		t.Fatalf("stat 2: %v", err)
	}
	if !stat2.ModTime().Equal(stat1.ModTime()) {
		t.Errorf("snapshot mtime changed despite no keyspace change: %v -> %v", stat1.ModTime(), stat2.ModTime())
	}

	// Now mutate and Dump — file should rewrite.
	if err := m.Client().HSet(ctx, "terminal:meta:ws1:sess1", map[string]any{"label": "y"}).Err(); err != nil {
		t.Fatalf("HSet 2: %v", err)
	}
	if err := m.Dump(); err != nil {
		t.Fatalf("Dump 3: %v", err)
	}
	stat3, err := os.Stat(snapPath)
	if err != nil {
		t.Fatalf("stat 3: %v", err)
	}
	if stat3.ModTime().Equal(stat1.ModTime()) {
		t.Error("snapshot mtime did NOT change after keyspace mutation")
	}
}

// TestDump_SkipsAfterReload exercises the embedded-CLI pattern: each
// CLI invocation creates a fresh Manager that loads the existing
// snapshot. A read-only command must not rewrite the file. Regresses
// loomcli-26v50.41.
func TestDump_SkipsAfterReload(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()
	m1, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager 1: %v", err)
	}
	if err := m1.Client().HSet(ctx, "terminal:meta:ws1:sess1", map[string]any{"label": "x"}).Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	if err := m1.Dump(); err != nil {
		t.Fatalf("Dump 1: %v", err)
	}
	stat1, err := os.Stat(snapPath)
	if err != nil {
		t.Fatalf("stat 1: %v", err)
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// Simulate a fresh CLI invocation: a new Manager that loads the
	// existing snapshot. A subsequent Dump with no mutations must NOT
	// rewrite the file.
	m2, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager 2: %v", err)
	}
	defer m2.Close()

	if err := m2.Dump(); err != nil {
		t.Fatalf("Dump 2: %v", err)
	}
	stat2, err := os.Stat(snapPath)
	if err != nil {
		t.Fatalf("stat 2: %v", err)
	}
	if !stat2.ModTime().Equal(stat1.ModTime()) {
		t.Errorf("snapshot mtime changed despite no mutations across Manager restart: %v -> %v", stat1.ModTime(), stat2.ModTime())
	}
}

// TestStreamSnapshotCap verifies the maxStreamEntriesPerKey cap: a
// stream with more than the cap is serialized with only the newest
// maxStreamEntriesPerKey entries, in oldest-first order so replay
// preserves IDs.
func TestStreamSnapshotCap(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()
	m, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Write maxStreamEntriesPerKey + 5 entries with monotonic IDs.
	overflow := 5
	for i := 1; i <= maxStreamEntriesPerKey+overflow; i++ {
		_, err := m.Client().XAdd(ctx, xAddArgsN("fleet-db:WSA:events", i, map[string]any{"n": i})).Result()
		if err != nil {
			t.Fatalf("XAdd %d: %v", i, err)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	m2, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager m2: %v", err)
	}
	defer m2.Close()

	got, err := m2.Client().XLen(ctx, "fleet-db:WSA:events").Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}
	if got != int64(maxStreamEntriesPerKey) {
		t.Errorf("stream len after cap: want %d, got %d", maxStreamEntriesPerKey, got)
	}
	// The first surviving entry should be ID `(overflow+1)-0` (oldest after cap).
	first, err := m2.Client().XRangeN(ctx, "fleet-db:WSA:events", "-", "+", 1).Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	wantFirstID := strconvI(overflow+1) + "-0"
	if len(first) != 1 || first[0].ID != wantFirstID {
		t.Errorf("first surviving entry ID: want %q, got %v", wantFirstID, first)
	}
}
