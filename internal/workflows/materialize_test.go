package workflows

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMaterializeBuiltinBundle (Phase U) verifies the daemon execution leaf can
// obtain a runnable copy of the bundled local-task-runner (which ships inside the
// epic-runner bundle) from the embedded FS, without driver registration.
func TestMaterializeBuiltinBundle(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dist")
	serverPath, err := MaterializeBuiltinBundle("epic-runner", dest)
	if err != nil {
		t.Fatalf("MaterializeBuiltinBundle: %v", err)
	}
	if filepath.Base(serverPath) != "server.mjs" {
		t.Errorf("returned path = %q, want .../server.mjs", serverPath)
	}
	info, err := os.Stat(serverPath)
	if err != nil {
		t.Fatalf("server.mjs not materialized: %v", err)
	}
	if info.IsDir() || info.Size() == 0 {
		t.Fatalf("server.mjs is empty or a directory (size=%d)", info.Size())
	}

	if _, err := MaterializeBuiltinBundle("does-not-exist", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("expected an error materializing an unknown bundle")
	}
}
