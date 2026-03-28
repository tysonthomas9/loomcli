package webui

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateAPIKey_Length verifies that GenerateAPIKey produces a hex string
// of the expected length (64 hex characters for 32 bytes).
func TestGenerateAPIKey_Length(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}

	// 32 bytes = 64 hex characters
	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}

	// Verify it's valid hex
	_, err = hex.DecodeString(key)
	if err != nil {
		t.Errorf("key is not valid hex: %v", err)
	}
}

// TestGenerateAPIKey_Unique verifies that successive calls to GenerateAPIKey
// produce different keys (tests randomness).
func TestGenerateAPIKey_Unique(t *testing.T) {
	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey() iteration %d error = %v", i, err)
		}
		if keys[key] {
			t.Fatalf("GenerateAPIKey() produced duplicate key on iteration %d: %q", i, key)
		}
		keys[key] = true
	}
}

// TestLoadOrCreateAPIKey_CreatesNewFile verifies that LoadOrCreateAPIKey
// creates a new file with correct permissions when the file does not exist.
func TestLoadOrCreateAPIKey_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	// Key should be non-empty and correct length
	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}

	// File should exist with correct permissions
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", keyPath, err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want %o", perm, 0600)
	}

	// File content should match the returned key (with trailing newline)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", keyPath, err)
	}
	if string(data) != key+"\n" {
		t.Errorf("file content = %q, want %q", string(data), key+"\n")
	}
}

// TestLoadOrCreateAPIKey_ReadsExistingFile verifies that LoadOrCreateAPIKey
// reads and returns an existing key from a file.
func TestLoadOrCreateAPIKey_ReadsExistingFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	existingKey := "existing-api-key-abcdef1234567890"
	if err := os.WriteFile(keyPath, []byte(existingKey+"\n"), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	if key != existingKey {
		t.Errorf("key = %q, want %q", key, existingKey)
	}
}

// TestLoadOrCreateAPIKey_ReadsExistingFileWithWhitespace verifies that
// LoadOrCreateAPIKey trims whitespace when reading an existing file.
func TestLoadOrCreateAPIKey_ReadsExistingFileWithWhitespace(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	existingKey := "existing-key-with-whitespace"
	if err := os.WriteFile(keyPath, []byte("  "+existingKey+"  \n"), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	if key != existingKey {
		t.Errorf("key = %q, want %q", key, existingKey)
	}
}

// TestLoadOrCreateAPIKey_CreatesParentDirectories verifies that
// LoadOrCreateAPIKey creates parent directories if they do not exist.
func TestLoadOrCreateAPIKey_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "nested", "deep", "dir", "api-key")

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	if key == "" {
		t.Error("key should not be empty")
	}

	// Parent directory should exist with 0700 permissions
	parentDir := filepath.Dir(keyPath)
	info, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", parentDir, err)
	}
	if !info.IsDir() {
		t.Errorf("%q should be a directory", parentDir)
	}

	// File should be readable
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", keyPath, err)
	}
	if len(data) == 0 {
		t.Error("file should not be empty")
	}
}

// TestLoadOrCreateAPIKey_EmptyFileGeneratesNewKey verifies that an empty
// file causes a new key to be generated and written.
func TestLoadOrCreateAPIKey_EmptyFileGeneratesNewKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	// Create an empty file
	if err := os.WriteFile(keyPath, []byte(""), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}
}

// TestLoadOrCreateAPIKey_WhitespaceOnlyFileGeneratesNewKey verifies that a
// file containing only whitespace causes a new key to be generated.
func TestLoadOrCreateAPIKey_WhitespaceOnlyFileGeneratesNewKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	// Create a file with only whitespace
	if err := os.WriteFile(keyPath, []byte("   \n\t  \n"), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}
}

// TestLoadOrCreateAPIKey_Idempotent verifies that calling LoadOrCreateAPIKey
// twice returns the same key.
func TestLoadOrCreateAPIKey_Idempotent(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	key1, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("first LoadOrCreateAPIKey() error = %v", err)
	}

	key2, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("second LoadOrCreateAPIKey() error = %v", err)
	}

	if key1 != key2 {
		t.Errorf("keys differ: first = %q, second = %q", key1, key2)
	}
}
