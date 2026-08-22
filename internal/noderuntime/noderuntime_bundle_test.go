package noderuntime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// appBundle lays out <tmp>/Loom Agents.app/Contents/{MacOS,Resources} with
// a real "loom" executable and returns the Contents dir and the exe path.
// The Contents dir is symlink-resolved (macOS temp dirs live under /var ->
// /private/var) because the resolver reports physical paths.
func appBundle(t *testing.T) (contents, exe string) {
	t.Helper()
	contents = filepath.Join(realDir(t, t.TempDir()), "Loom Agents.app", "Contents")
	exe = writeExec(t, filepath.Join(contents, "MacOS", "loom"))
	return contents, exe
}

// realDir returns dir with every symlink resolved.
func realDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return real
}

func symlink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(%s → %s): %v", link, target, err)
	}
	return link
}

func resourcesNode(contents string) string {
	return filepath.Join(contents, "Resources", "node-runtime", "bin", "node")
}

// (a) The executable is reached through a symlink (the runbook's
// /usr/local/bin/loom convenience link): the sibling node in the real
// Contents/MacOS must still be found.
func TestResolveBundledSiblingThroughSymlinkedExecutable(t *testing.T) {
	setup(t)
	contents, exe := appBundle(t)
	sibling := writeExec(t, filepath.Join(contents, "MacOS", "node"))
	link := symlink(t, exe, filepath.Join(t.TempDir(), "bin", "loom"))
	executablePath = func() string { return link }
	onPath := writeExec(t, filepath.Join(t.TempDir(), "node"))
	lookPath = func(string) (string, error) { return onPath, nil }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceBundled || got.Path != sibling {
		t.Fatalf("Resolve() = %+v, want source %q path %q", got, SourceBundled, sibling)
	}
}

// (b) No sibling: the reserved Contents/Resources/node-runtime/bin/node
// location is used and reported as bundled; with both present the MacOS
// sibling wins.
func TestResolveResourcesNodeRuntimeFallbackAndPrecedence(t *testing.T) {
	setup(t)
	contents, exe := appBundle(t)
	resources := writeExec(t, resourcesNode(contents))
	executablePath = func() string { return exe }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceBundled || got.Path != resources {
		t.Fatalf("Resolve() = %+v, want source %q path %q", got, SourceBundled, resources)
	}

	ResetForTest()
	sibling := writeExec(t, filepath.Join(contents, "MacOS", "node"))
	got, err = Resolve()
	if err != nil {
		t.Fatalf("Resolve() with sibling error = %v", err)
	}
	if got.Path != sibling {
		t.Fatalf("Resolve() = %+v, want the MacOS sibling %q to win over Resources", got, sibling)
	}
}

// The Resources/resources/ variant (Tauri's nested resource layout) is
// probed after the plain Resources/ one.
func TestResolveResourcesNestedVariant(t *testing.T) {
	setup(t)
	contents, exe := appBundle(t)
	nested := writeExec(t, filepath.Join(contents, "Resources", "resources", "node-runtime", "bin", "node"))
	executablePath = func() string { return exe }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceBundled || got.Path != nested {
		t.Fatalf("Resolve() = %+v, want source %q path %q", got, SourceBundled, nested)
	}
}

// (c) A Resources candidate that is not executable is skipped; PATH is next.
func TestResolveSkipsNonExecutableResourcesNode(t *testing.T) {
	setup(t)
	contents, exe := appBundle(t)
	writeFile(t, resourcesNode(contents), "#!/bin/sh\n", 0o644)
	executablePath = func() string { return exe }
	onPath := writeExec(t, filepath.Join(t.TempDir(), "node"))
	lookPath = func(string) (string, error) { return onPath, nil }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourcePath || got.Path != onPath {
		t.Fatalf("Resolve() = %+v, want PATH node %q (Resources candidate is not executable)", got, onPath)
	}

	ResetForTest()
	lookPath = func(string) (string, error) { return "", errors.New("not on PATH") }
	_, err = Resolve()
	assertMissing(t, err, filepath.Join(contents, "MacOS"), EnvNodeBin)
}

// (d) A dangling symlink as the executable: EvalSymlinks fails, the raw
// directory is used, and resolution still works (no panic).
func TestResolveDanglingExecutableLinkFallsBackToRawDir(t *testing.T) {
	setup(t)
	exeDir := t.TempDir()
	link := symlink(t, filepath.Join(exeDir, "missing-target"), filepath.Join(exeDir, "loom"))
	sibling := writeExec(t, filepath.Join(exeDir, "node"))
	executablePath = func() string { return link }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceBundled || got.Path != sibling {
		t.Fatalf("Resolve() = %+v, want raw-dir sibling %q", got, sibling)
	}

	ResetForTest()
	if err := os.Remove(sibling); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err = Resolve()
	assertMissing(t, err, exeDir)
}

// (e) A decoy node on PATH loses to the Resources probe, and is never run.
func TestResolveResourcesNodeBeatsDecoyPath(t *testing.T) {
	setup(t)
	marker := filepath.Join(t.TempDir(), "decoy-ran")
	decoy := writeFile(t, filepath.Join(t.TempDir(), "node"), "#!/bin/sh\ntouch "+marker+"\n", 0o755)
	lookPath = func(string) (string, error) { return decoy, nil }
	contents, exe := appBundle(t)
	resources := writeExec(t, resourcesNode(contents))
	executablePath = func() string { return exe }

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Source != SourceBundled || got.Path != resources {
		t.Fatalf("Resolve() = %+v, want Resources node %q", got, resources)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decoy marker stat error = %v, want not-exist (decoy must never run)", err)
	}
}
