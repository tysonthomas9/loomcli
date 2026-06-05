package backendcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupRecognizesExternalBackendPlugin(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "loom-backend-localdogfood")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write backend plugin: %v", err)
	}
	t.Setenv("PATH", dir)

	info, err := Lookup("localdogfood")
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if !info.Installed {
		t.Fatalf("Installed = false, want true; hint=%q", info.InstallHint)
	}
	if info.Binary != "loom-backend-localdogfood" {
		t.Fatalf("Binary = %q, want loom-backend-localdogfood", info.Binary)
	}
	if info.Path != bin {
		t.Fatalf("Path = %q, want %q", info.Path, bin)
	}
}
