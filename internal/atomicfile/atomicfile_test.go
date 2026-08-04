package atomicfile

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	content := []byte("hello world")
	if err := WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestWriteFile_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	newContent := []byte("replacement")
	if err := WriteFile(path, newContent, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("content = %q, want %q", got, newContent)
	}
}

func TestWriteFile_Permissions_0644(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Errorf("permissions = %o, want %o", got, 0644)
	}
}

func TestWriteFile_Permissions_0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("permissions = %o, want %o", got, 0600)
	}
}

func TestWriteFile_NonExistentParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent", "test.txt")

	err := WriteFile(path, []byte("data"), 0644)
	if err == nil {
		t.Fatal("expected error for non-existent parent dir, got nil")
	}
}

func TestWriteFile_EmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	if err := WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

func TestWriteFile_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.txt")

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		content := []byte("writer-" + string(rune('A'+i)) + "-content")
		go func() {
			defer wg.Done()
			_ = WriteFile(path, content, 0644)
		}()
	}
	wg.Wait()

	// File must contain exactly one writer's complete content (not interleaved)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(got)
	valid := false
	for i := 0; i < n; i++ {
		expected := "writer-" + string(rune('A'+i)) + "-content"
		if s == expected {
			valid = true
			break
		}
	}
	if !valid {
		t.Errorf("file content %q does not match any single writer's output", s)
	}
}

func TestWriteFile_NoOrphanTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, ".loom-atomic-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("found orphan temp files: %v", matches)
	}
}
