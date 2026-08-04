package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// withUnsetConfigDir unsets LOOM_CONFIG_DIR for the duration of the test.
// t.Setenv can't unset, so save/restore manually (and use t.Setenv first
// so the test is flagged as env-mutating and excluded from t.Parallel).
func withUnsetConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", "placeholder")
	if err := os.Unsetenv("LOOM_CONFIG_DIR"); err != nil {
		t.Fatalf("unset LOOM_CONFIG_DIR: %v", err)
	}
}

func TestLoomDirUnderTestNeverTouchesRealHome(t *testing.T) {
	withUnsetConfigDir(t)

	dir := LoomDir()
	if dir == "" {
		t.Fatal("LoomDir() = \"\", want a per-process test temp dir")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if dir == filepath.Join(home, ".loom") {
			t.Fatalf("LoomDir() = %q, must not resolve to the real ~/.loom under go test", dir)
		}
	}
	if again := LoomDir(); again != dir {
		t.Fatalf("LoomDir() not stable across calls: %q then %q", dir, again)
	}
}

func TestLoomDirRespectsExplicitConfigDir(t *testing.T) {
	want := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", want)
	if got := LoomDir(); got != want {
		t.Fatalf("LoomDir() = %q, want %q", got, want)
	}
}

func TestSaveStateCacheWritesUnderTestDir(t *testing.T) {
	withUnsetConfigDir(t)

	if err := MutateStateCache(func(sc *StateCache) error {
		sc.Workspaces["GUARD-WS"] = WorkspaceLocalState{Path: "/tmp/guard"}
		return nil
	}); err != nil {
		t.Fatalf("MutateStateCache: %v", err)
	}
	path := StateFilePath()
	if path == "" {
		t.Fatal("StateFilePath() = \"\"")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if path == filepath.Join(home, ".loom", "state.json") {
			t.Fatalf("state file written to the real ~/.loom/state.json: %q", path)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not written under test dir: %v", err)
	}
}
