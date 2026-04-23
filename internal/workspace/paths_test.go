package workspace

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestCentralSessionsDir_RequiresWorkspaceID ensures empty wsID errors out
// rather than silently landing in a shared fallback bucket.
func TestCentralSessionsDir_RequiresWorkspaceID(t *testing.T) {
	if _, err := CentralSessionsDir(""); !errors.Is(err, ErrMissingWorkspaceID) {
		t.Fatalf("CentralSessionsDir(\"\") err = %v, want ErrMissingWorkspaceID", err)
	}
	if _, err := CentralUsageDir(""); !errors.Is(err, ErrMissingWorkspaceID) {
		t.Fatalf("CentralUsageDir(\"\") err = %v, want ErrMissingWorkspaceID", err)
	}
}

// TestCentralSessionsDir_LandsUnderHome confirms the resolver honors HOME so
// tests can redirect ~/.loom to a tmpdir.
func TestCentralSessionsDir_LandsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const wsID = "00000000-0000-4000-8000-000000000abc"
	got, err := CentralSessionsDir(wsID)
	if err != nil {
		t.Fatalf("CentralSessionsDir: %v", err)
	}
	want := filepath.Join(home, ".loom", "sessions", wsID)
	if got != want {
		t.Fatalf("CentralSessionsDir = %q, want %q", got, want)
	}

	got, err = CentralUsageDir(wsID)
	if err != nil {
		t.Fatalf("CentralUsageDir: %v", err)
	}
	want = filepath.Join(home, ".loom", "usage", wsID)
	if got != want {
		t.Fatalf("CentralUsageDir = %q, want %q", got, want)
	}
}
