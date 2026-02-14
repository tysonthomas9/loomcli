package logrouter

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingWriter_WriteAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	w, err := newRotatingWriter(logPath, 0, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter failed: %v", err)
	}

	// Close the underlying file
	w.Close()

	// Attempt to write after close — should return an error, not panic
	_, err = w.Write([]byte("after close"))
	if err == nil {
		t.Error("Write after Close should return an error")
	}
}

func TestRotatingWriter_AppendToExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Create an existing file with some data
	existingData := []byte("existing data\n")
	if err := os.WriteFile(logPath, existingData, 0600); err != nil {
		t.Fatalf("failed to write existing file: %v", err)
	}

	// Open with rotation enabled to verify size tracking
	w, err := newRotatingWriter(logPath, 1000, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter failed: %v", err)
	}

	// Verify initial size is tracked correctly
	if w.size != int64(len(existingData)) {
		t.Errorf("initial size = %d, want %d", w.size, len(existingData))
	}

	// Write new data
	newData := []byte("new data\n")
	if _, err := w.Write(newData); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	w.Close()

	// Verify the file has both existing and new data
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	expected := string(existingData) + string(newData)
	if string(content) != expected {
		t.Errorf("file content = %q, want %q", string(content), expected)
	}
}

func TestBackupPath_ReturnsCorrectFormat(t *testing.T) {
	w := &rotatingWriter{path: "/var/log/test.log"}

	tests := []struct {
		n    int
		want string
	}{
		{1, "/var/log/test.log.1"},
		{2, "/var/log/test.log.2"},
		{3, "/var/log/test.log.3"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("n=%d", tt.n), func(t *testing.T) {
			got := w.backupPath(tt.n)
			if got != tt.want {
				t.Errorf("backupPath(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}
