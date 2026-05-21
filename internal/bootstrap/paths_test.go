package bootstrap

import (
	"path/filepath"
	"testing"
)

func TestLoomDirReturnsLoomConfigDirWhenSet(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmp)

	got := LoomDir()
	if got != tmp {
		t.Fatalf("LoomDir() = %q, want %q", got, tmp)
	}
}

func TestLoomDirPanicsUnderGoTestWithoutLoomConfigDir(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", "")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected LoomDir() to panic when LOOM_CONFIG_DIR is unset under go test, but it returned normally")
		}
	}()

	_ = LoomDir()
}

func TestStateFilePathDerivesFromLoomDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmp)

	got := StateFilePath()
	want := filepath.Join(tmp, "state.json")
	if got != want {
		t.Fatalf("StateFilePath() = %q, want %q", got, want)
	}
}
