package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateLegacySessionsAndUsage_MovesEntries verifies the rename step
// pulls sessions/** and usage.jsonl across and leaves a sentinel behind.
func TestMigrateLegacySessionsAndUsage_MovesEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := t.TempDir()

	// Seed a legacy session dir and usage file
	sessDir := filepath.Join(legacy, "sessions", "20260421-000000-agent--deadbeef")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), []byte(`{"session_id":"x"}`), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "usage.jsonl"), []byte(`{"agent_name":"a"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write usage: %v", err)
	}

	const wsID = "00000000-0000-4000-8000-000000000abc"
	MigrateLegacySessionsAndUsage(wsID, legacy)

	// Session moved to central
	centralSess, _ := CentralSessionsDir(wsID)
	if _, err := os.Stat(filepath.Join(centralSess, "20260421-000000-agent--deadbeef", "metadata.json")); err != nil {
		t.Fatalf("expected session migrated, stat err: %v", err)
	}
	// Usage appended to central
	centralUsage, _ := CentralUsageDir(wsID)
	data, err := os.ReadFile(filepath.Join(centralUsage, "usage.jsonl"))
	if err != nil || len(data) == 0 {
		t.Fatalf("expected usage migrated, got err=%v len=%d", err, len(data))
	}
	// Sentinel written
	if _, err := os.Stat(filepath.Join(centralSess, sentinelName(legacy))); err != nil {
		t.Fatalf("expected session sentinel, stat err: %v", err)
	}

	// Legacy usage file should be removed after migration
	if _, err := os.Stat(filepath.Join(legacy, "usage.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy usage file gone, stat err: %v", err)
	}
}

// TestMigrateLegacySessionsAndUsage_NoLegacy_NoSentinel guards the bug where
// calling MigrateLegacy before legacy data exists stamps a sentinel and
// prevents a later seed from being picked up.
func TestMigrateLegacySessionsAndUsage_NoLegacy_NoSentinel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := t.TempDir()

	const wsID = "00000000-0000-4000-8000-000000000def"

	// First call with no legacy data — must not stamp a sentinel.
	MigrateLegacySessionsAndUsage(wsID, legacy)

	centralSess, _ := CentralSessionsDir(wsID)
	if _, err := os.Stat(filepath.Join(centralSess, sentinelName(legacy))); err == nil {
		t.Fatalf("sentinel written despite empty legacy source — future legacy data would be skipped")
	}

	// Now seed legacy and migrate again — entries must move.
	sessDir := filepath.Join(legacy, "sessions", "s1")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	MigrateLegacySessionsAndUsage(wsID, legacy)

	if _, err := os.Stat(filepath.Join(centralSess, "s1", "metadata.json")); err != nil {
		t.Fatalf("expected post-seed migration, stat err: %v", err)
	}
}
