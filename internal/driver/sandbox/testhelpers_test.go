package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExecutable writes a 0755 file under dir and returns its path. Local to
// the sandbox test package (the parent driver package has its own copy in
// executor_test.go).
func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}
