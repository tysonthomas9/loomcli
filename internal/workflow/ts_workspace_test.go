package workflow

import (
	"path/filepath"
	"testing"
)

func TestRuntimeProfileProjectRootRecognizesFlueSourceRoot(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, ".flue", "runtimes", "daytona.ts")

	if got := runtimeProfileProjectRoot(sourcePath); got != root {
		t.Fatalf("runtimeProfileProjectRoot(%q) = %q, want %q", sourcePath, got, root)
	}
}
