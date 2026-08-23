package packaged

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeExecutable writes a tiny executable at path (the "loom" binary).
func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // fake executable for tests.
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

// realDir returns dir with every symlink resolved (macOS temp dirs live
// under /var -> /private/var; Root() reports physical paths).
func realDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return real
}

// appBundle lays out <tmp>/Loom Agents.app/Contents/{MacOS/loom,Resources}
// with a verified epic-runner tree under Resources/builtin-workflows and
// returns the exe path plus the expected resource root.
func appBundle(t *testing.T) (exe, want string) {
	t.Helper()
	contents := filepath.Join(realDir(t, t.TempDir()), "Loom Agents.app", "Contents")
	exe = writeExecutable(t, filepath.Join(contents, "MacOS", "loom"))
	want = filepath.Join(contents, "Resources", ResourceDirName)
	ExpectedIndexDigest = writeFakeTree(t, want, "epic-runner", testServerBody, testSourceDigest, testRunners())
	return exe, want
}

// TestRootThroughSymlinkedExecutable (DEV-V5-37 3h #13): the runbook's
// `/usr/local/bin/loom -> …/Contents/MacOS/loom` convenience link must
// still find the bundle's Resources tree — os.Executable returns the link.
func TestRootThroughSymlinkedExecutable(t *testing.T) {
	setup(t)
	t.Run("app bundle Resources via link", func(t *testing.T) {
		exe, want := appBundle(t)
		link := filepath.Join(t.TempDir(), "bin", "loom")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.Symlink(exe, link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		executablePath = func() (string, error) { return link, nil }

		root, err := Root()
		if err != nil || root != want {
			t.Fatalf("Root() = %q, %v; want %q", root, err, want)
		}
		art, err := lookupEpicRunner()
		if err != nil || art.Root != want {
			t.Fatalf("Lookup = %+v, %v; want root %q", art, err, want)
		}
	})
	t.Run("next to executable via link", func(t *testing.T) {
		exeDir := realDir(t, t.TempDir())
		exe := writeExecutable(t, filepath.Join(exeDir, "loom"))
		want := filepath.Join(exeDir, ResourceDirName)
		ExpectedIndexDigest = writeFakeTree(t, want, "epic-runner", testServerBody, testSourceDigest, testRunners())
		link := filepath.Join(t.TempDir(), "loom")
		if err := os.Symlink(exe, link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		executablePath = func() (string, error) { return link, nil }

		root, err := Root()
		if err != nil || root != want {
			t.Fatalf("Root() = %q, %v; want %q", root, err, want)
		}
	})
	t.Run("dangling link falls back to the raw path", func(t *testing.T) {
		exeDir := t.TempDir()
		want := filepath.Join(exeDir, ResourceDirName)
		ExpectedIndexDigest = writeFakeTree(t, want, "epic-runner", testServerBody, testSourceDigest, testRunners())
		link := filepath.Join(exeDir, "loom")
		if err := os.Symlink(filepath.Join(exeDir, "missing-target"), link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		executablePath = func() (string, error) { return link, nil }

		root, err := Root()
		if err != nil || root != want {
			t.Fatalf("Root() = %q, %v; want raw-dir fallback %q", root, err, want)
		}
	})
	t.Run("env override still wins over the link", func(t *testing.T) {
		exe, _ := appBundle(t)
		link := filepath.Join(t.TempDir(), "loom")
		if err := os.Symlink(exe, link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		executablePath = func() (string, error) { return link, nil }
		override := t.TempDir()
		ExpectedIndexDigest = writeFakeTree(t, override, "epic-runner", testServerBody, testSourceDigest, testRunners())
		t.Setenv(EnvArtifactsDir, override)

		root, err := Root()
		if err != nil || root != override {
			t.Fatalf("Root() = %q, %v; want %s override %q", root, err, EnvArtifactsDir, override)
		}
		empty := t.TempDir()
		t.Setenv(EnvArtifactsDir, empty)
		if _, err := Root(); !errors.Is(err, ErrNotPackaged) {
			t.Fatalf("Root() with an empty override = %v, want ErrNotPackaged (never the exe probe)", err)
		}
	})
}
