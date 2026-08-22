package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelfBinPathEnv(t *testing.T) {
	realDir := t.TempDir()
	executable := filepath.Join(realDir, "loom")
	if err := os.WriteFile(executable, []byte("loom"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolvedDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("PATH absent", func(t *testing.T) {
		stubSelfExecutable(t, executable, nil)
		got := SelfBinPathEnv([]string{"HOME=/tmp"})
		want := []string{"HOME=/tmp", "PATH=" + resolvedDir}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("SelfBinPathEnv() = %v, want %v", got, want)
		}
	})

	t.Run("PATH present", func(t *testing.T) {
		stubSelfExecutable(t, executable, nil)
		path := filepath.Join("old", "one") + string(os.PathListSeparator) + filepath.Join("old", "two")
		got := SelfBinPathEnv([]string{"PATH=" + path, "HOME=/tmp"})
		want := []string{"PATH=" + resolvedDir + string(os.PathListSeparator) + path, "HOME=/tmp"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("SelfBinPathEnv() = %v, want %v", got, want)
		}
	})

	t.Run("executable directory already first", func(t *testing.T) {
		stubSelfExecutable(t, executable, nil)
		env := []string{"PATH=" + resolvedDir + string(os.PathListSeparator) + "/usr/bin", "HOME=/tmp"}
		got := SelfBinPathEnv(env)
		if !reflect.DeepEqual(got, env) {
			t.Fatalf("SelfBinPathEnv() = %v, want unchanged %v", got, env)
		}
	})

	t.Run("executable resolution error", func(t *testing.T) {
		stubSelfExecutable(t, "", errors.New("boom"))
		env := []string{"PATH=/usr/bin", "HOME=/tmp"}
		got := SelfBinPathEnv(env)
		if !reflect.DeepEqual(got, env) {
			t.Fatalf("SelfBinPathEnv() = %v, want unchanged %v", got, env)
		}
	})

	t.Run("symlinked executable", func(t *testing.T) {
		linkDir := t.TempDir()
		link := filepath.Join(linkDir, "loom")
		if err := os.Symlink(executable, link); err != nil {
			t.Fatal(err)
		}
		stubSelfExecutable(t, link, nil)
		got := SelfBinPathEnv([]string{"PATH=/usr/bin"})
		want := "PATH=" + resolvedDir + string(os.PathListSeparator) + "/usr/bin"
		if got[0] != want {
			t.Fatalf("SelfBinPathEnv()[0] = %q, want %q", got[0], want)
		}
	})
}

func stubSelfExecutable(t *testing.T, path string, err error) {
	t.Helper()
	old := selfExecutable
	selfExecutable = func() (string, error) { return path, err }
	t.Cleanup(func() { selfExecutable = old })
}
