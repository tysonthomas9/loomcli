package noderuntime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup resets the resolver cache, pins the env override to empty, and
// restores the executablePath/lookPath seams when the test ends.
func setup(t *testing.T) {
	t.Helper()
	ResetForTest()
	origExe, origLook := executablePath, lookPath
	t.Cleanup(func() {
		executablePath = origExe
		lookPath = origLook
		ResetForTest()
	})
	t.Setenv(EnvNodeBin, "")
	executablePath = func() string { return "" }
	lookPath = func(string) (string, error) { return "", errors.New("not on PATH") }
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

func writeExec(t *testing.T, path string) string {
	t.Helper()
	return writeFile(t, path, "#!/bin/sh\nexit 0\n", 0o755)
}

func assertMissing(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Resolve() error = nil, want node_runtime_missing")
	}
	if !errors.Is(err, ErrNodeRuntimeMissing) {
		t.Fatalf("Resolve() error = %v, want errors.Is ErrNodeRuntimeMissing", err)
	}
	for _, fragment := range append([]string{"node_runtime_missing"}, fragments...) {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("Resolve() error = %q, want it to contain %q", err.Error(), fragment)
		}
	}
}

func TestResolveOverrideWins(t *testing.T) {
	setup(t)
	override := writeExec(t, filepath.Join(t.TempDir(), "custom-node"))
	// A bundled sibling and a PATH node both exist; the override still wins.
	exeDir := t.TempDir()
	writeExec(t, filepath.Join(exeDir, "node"))
	executablePath = func() string { return filepath.Join(exeDir, "loom") }
	onPath := writeExec(t, filepath.Join(t.TempDir(), "node"))
	lookPath = func(string) (string, error) { return onPath, nil }
	t.Setenv(EnvNodeBin, override)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceOverride || got.Path != override {
		t.Fatalf("Resolve() = %+v, want source %q path %q", got, SourceOverride, override)
	}
}

func TestResolveOverrideInvalidHasNoFallback(t *testing.T) {
	setup(t)
	base := t.TempDir()
	cases := map[string]string{
		"missing file":   filepath.Join(base, "does-not-exist"),
		"directory":      base,
		"not executable": writeFile(t, filepath.Join(base, "plain-node"), "#!/bin/sh\n", 0o644),
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			ResetForTest()
			fallback := writeExec(t, filepath.Join(t.TempDir(), "node"))
			lookPath = func(string) (string, error) { return fallback, nil }
			t.Setenv(EnvNodeBin, override)

			got, err := Resolve()
			assertMissing(t, err, EnvNodeBin, override)
			if got.Path != "" || got.Source != "" {
				t.Fatalf("Resolve() = %+v, want zero value on override error (no fallback)", got)
			}
		})
	}
}

func TestResolveBundledSiblingBeatsDecoyPath(t *testing.T) {
	setup(t)
	marker := filepath.Join(t.TempDir(), "decoy-ran")
	decoy := writeFile(t, filepath.Join(t.TempDir(), "node"), "#!/bin/sh\ntouch "+marker+"\n", 0o755)
	lookPath = func(string) (string, error) { return decoy, nil }
	exeDir := t.TempDir()
	sibling := writeExec(t, filepath.Join(exeDir, "node"))
	executablePath = func() string { return filepath.Join(exeDir, "loom") }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceBundled || got.Path != sibling {
		t.Fatalf("Resolve() = %+v, want source %q path %q", got, SourceBundled, sibling)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decoy marker stat error = %v, want not-exist (decoy must never run)", err)
	}
}

func TestResolveBundledTripleSuffix(t *testing.T) {
	setup(t)
	exeDir := t.TempDir()
	sibling := writeExec(t, filepath.Join(exeDir, "node-"+hostTargetTriple()))
	// A node-* directory must not be mistaken for the sidecar.
	if err := os.MkdirAll(filepath.Join(exeDir, "node-modules-dir"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Nor may any other executable that merely starts with node- (a CLI
	// install next to node-gyp is the realistic case).
	writeExec(t, filepath.Join(exeDir, "node-gyp"))
	executablePath = func() string { return filepath.Join(exeDir, "loom") }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceBundled || got.Path != sibling {
		t.Fatalf("Resolve() = %+v, want source %q path %q", got, SourceBundled, sibling)
	}
}

func TestResolveSkipsNonExecutableSiblingAndUsesPath(t *testing.T) {
	setup(t)
	exeDir := t.TempDir()
	writeFile(t, filepath.Join(exeDir, "node"), "#!/bin/sh\n", 0o644)
	executablePath = func() string { return filepath.Join(exeDir, "loom") }
	onPath := writeExec(t, filepath.Join(t.TempDir(), "node"))
	lookPath = func(name string) (string, error) {
		if name != "node" {
			t.Fatalf("lookPath(%q), want lookPath(\"node\")", name)
		}
		return onPath, nil
	}

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourcePath || got.Path != onPath {
		t.Fatalf("Resolve() = %+v, want source %q path %q", got, SourcePath, onPath)
	}
}

func TestResolveNothingFound(t *testing.T) {
	setup(t)
	exeDir := t.TempDir()
	executablePath = func() string { return filepath.Join(exeDir, "loom") }

	_, err := Resolve()
	assertMissing(t, err, exeDir, EnvNodeBin)
}

func TestResolveCacheInvalidatesOnOverrideChange(t *testing.T) {
	setup(t)
	overrideA := writeExec(t, filepath.Join(t.TempDir(), "node-a"))
	overrideB := writeExec(t, filepath.Join(t.TempDir(), "node-b"))

	t.Setenv(EnvNodeBin, overrideA)
	first, err := Resolve()
	if err != nil || first.Path != overrideA {
		t.Fatalf("Resolve() = %+v, %v; want path %q", first, err, overrideA)
	}
	// Same override: served from cache even though the file is gone.
	if err := os.Remove(overrideA); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	cached, err := Resolve()
	if err != nil || cached.Path != overrideA {
		t.Fatalf("cached Resolve() = %+v, %v; want path %q", cached, err, overrideA)
	}

	t.Setenv(EnvNodeBin, overrideB)
	second, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() after override change error = %v", err)
	}
	if second.Path != overrideB || second.Source != SourceOverride {
		t.Fatalf("Resolve() after override change = %+v, want path %q", second, overrideB)
	}
}

func TestDescribeShape(t *testing.T) {
	setup(t)
	override := writeExec(t, filepath.Join(t.TempDir(), "node"))
	t.Setenv(EnvNodeBin, override)

	ok := Describe()
	if ok["ok"] != true || ok["path"] != override || ok["source"] != SourceOverride || ok["error"] != "" {
		t.Fatalf("Describe() = %v, want ok/path/source/empty error", ok)
	}

	ResetForTest()
	t.Setenv(EnvNodeBin, filepath.Join(t.TempDir(), "missing"))
	failed := Describe()
	if failed["ok"] != false || failed["path"] != "" || failed["source"] != "" {
		t.Fatalf("Describe() = %v, want ok=false with empty path/source", failed)
	}
	msg, _ := failed["error"].(string)
	if !strings.Contains(msg, "node_runtime_missing") {
		t.Fatalf("Describe() error = %q, want node_runtime_missing", msg)
	}
	for _, key := range []string{"ok", "path", "source", "error"} {
		if _, present := failed[key]; !present {
			t.Fatalf("Describe() missing key %q: %v", key, failed)
		}
	}
}

func TestResolveIgnoresNodePrefixedSiblings(t *testing.T) {
	setup(t)
	exeDir := t.TempDir()
	writeExec(t, filepath.Join(exeDir, "node-gyp"))
	writeExec(t, filepath.Join(exeDir, "node-pre-gyp"))
	executablePath = func() string { return filepath.Join(exeDir, "loom") }
	onPath := writeExec(t, filepath.Join(t.TempDir(), "node"))
	lookPath = func(string) (string, error) { return onPath, nil }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourcePath || got.Path != onPath {
		t.Fatalf("Resolve() = %+v, want PATH node %q (node-gyp is not a sidecar)", got, onPath)
	}
}

// A failed resolution must not stick for the process lifetime: a Node that
// appears later (install, PATH fix) is picked up on the next call.
func TestResolveDoesNotCacheFailure(t *testing.T) {
	setup(t)
	executablePath = func() string { return filepath.Join(t.TempDir(), "loom") }
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if _, err := Resolve(); !errors.Is(err, ErrNodeRuntimeMissing) {
		t.Fatalf("first Resolve() error = %v, want ErrNodeRuntimeMissing", err)
	}

	onPath := writeExec(t, filepath.Join(t.TempDir(), "node"))
	lookPath = func(string) (string, error) { return onPath, nil }
	got, err := Resolve()
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if got.Path != onPath {
		t.Fatalf("Resolve() = %+v, want newly available %q", got, onPath)
	}
}
