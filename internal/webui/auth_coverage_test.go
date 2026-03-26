package webui

import (
	"os"
	"path/filepath"
	"testing"
)

// --- GenerateAPIKey uniqueness/format coverage ---

func TestGenerateAPIKey_UniqueAcrossMany(t *testing.T) {
	// Generate a large number of keys and verify all are unique and valid hex.
	const count = 500
	keys := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		key, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey() iteration %d error = %v", i, err)
		}
		if len(key) != apiKeyLength*2 {
			t.Fatalf("iteration %d: len(key) = %d, want %d", i, len(key), apiKeyLength*2)
		}
		if _, dup := keys[key]; dup {
			t.Fatalf("duplicate key on iteration %d: %q", i, key)
		}
		keys[key] = struct{}{}
	}
}

func TestGenerateAPIKey_IsLowercaseHex(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}
	for _, c := range key {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("key contains non-lowercase-hex character: %c in %q", c, key)
		}
	}
}

// --- LoadOrCreateAPIKey coverage ---

func TestLoadOrCreateAPIKey_ExistingKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	// Write a known key
	knownKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	os.WriteFile(keyPath, []byte(knownKey+"\n"), 0600)

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}
	if key != knownKey {
		t.Errorf("key = %q, want %q", key, knownKey)
	}
}

func TestLoadOrCreateAPIKey_MissingFileCreatesKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "subdir", "api-key")

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}
	if len(key) != apiKeyLength*2 {
		t.Errorf("len(key) = %d, want %d", len(key), apiKeyLength*2)
	}

	// File should now exist
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != key+"\n" {
		t.Errorf("file content = %q, want %q", string(data), key+"\n")
	}
}

func TestLoadOrCreateAPIKey_PermissionErrorOnWrite(t *testing.T) {
	dir := t.TempDir()
	// Create a read-only directory so the file write fails.
	readonlyDir := filepath.Join(dir, "readonly")
	os.MkdirAll(readonlyDir, 0500)
	defer os.Chmod(readonlyDir, 0700) // Restore so cleanup works

	keyPath := filepath.Join(readonlyDir, "api-key")

	_, err := LoadOrCreateAPIKey(keyPath)
	if err == nil {
		t.Fatal("expected error when directory is read-only")
	}
}

func TestLoadOrCreateAPIKey_PermissionErrorOnMkdir(t *testing.T) {
	dir := t.TempDir()
	// Create a read-only directory so MkdirAll fails for nested path.
	readonlyDir := filepath.Join(dir, "readonly")
	os.MkdirAll(readonlyDir, 0500)
	defer os.Chmod(readonlyDir, 0700) // Restore so cleanup works

	keyPath := filepath.Join(readonlyDir, "nested", "api-key")

	_, err := LoadOrCreateAPIKey(keyPath)
	if err == nil {
		t.Fatal("expected error when parent directory cannot be created")
	}
}

func TestLoadOrCreateAPIKey_ConsistentAfterCreate(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	// First call creates the key
	key1, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}

	// Second call should read the existing key
	key2, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}

	if key1 != key2 {
		t.Errorf("keys differ between calls: %q vs %q", key1, key2)
	}
}

func TestLoadOrCreateAPIKey_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	_, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("os.Stat error = %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want %o", perm, 0600)
	}
}
