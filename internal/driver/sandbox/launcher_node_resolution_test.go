package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/noderuntime"
)

// These tests pin ProcessLauncher.Launch onto noderuntime.Resolve: with no
// explicit NodePath it honors LOOM_NODE_BIN verbatim, and an unusable override
// fails closed BEFORE writeFlueRuntimeLauncher creates its temp file.

const missingNodeOverride = "/nonexistent/loom-test/node"

// launcherResolutionSandbox resets the resolver cache around the test and
// points TMPDIR at a fresh, empty directory so the launcher temp file can be
// observed by listing it. It returns that directory.
func launcherResolutionSandbox(t *testing.T) string {
	t.Helper()
	noderuntime.ResetForTest()
	t.Cleanup(noderuntime.ResetForTest)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

// writeFakeLauncherNode writes a shell stand-in for node that records "$0 $*"
// into marker and prints stdoutLine. The marker path is embedded since Launch
// passes spec.Env verbatim (no host env inheritance).
func writeFakeLauncherNode(t *testing.T, marker, stdoutLine string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-node")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s %%s\\n' \"$0\" \"$*\" >> '%s'\nprintf '%%s\\n' '%s'\n", marker, stdoutLine)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	return path
}

func launchAndWait(t *testing.T, launcher ProcessLauncher) string {
	t.Helper()
	root := t.TempDir()
	process, err := launcher.Launch(context.Background(), LaunchSpec{BundleRoot: root, Env: []string{}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	exit, err := process.Wait()
	if err != nil {
		t.Fatalf("Wait: %v (stderr: %s)", err, exit.Stderr)
	}
	return strings.TrimSpace(exit.Stdout)
}

func requireLauncherMarker(t *testing.T, marker, fakeNode string) {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("fake node was not exec'd (marker %s): %v", marker, err)
	}
	if !strings.Contains(string(data), fakeNode) {
		t.Fatalf("marker = %q, want exec of %s", data, fakeNode)
	}
	if !strings.Contains(string(data), "loom-flue-runtime-") {
		t.Fatalf("marker = %q, want runtime launcher arg", data)
	}
}

func requireNoLauncherTempFiles(t *testing.T, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", tmp, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("TMPDIR %s not empty: %v (launcher written before node resolution failed)", tmp, names)
	}
}

func TestProcessLauncherNodeResolutionExecsOverride(t *testing.T) {
	launcherResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	fakeNode := writeFakeLauncherNode(t, marker, "fake-node-ok")
	t.Setenv(noderuntime.EnvNodeBin, fakeNode)

	if got := launchAndWait(t, ProcessLauncher{}); got != "fake-node-ok" {
		t.Fatalf("stdout = %q, want fake node output", got)
	}
	requireLauncherMarker(t, marker, fakeNode)
}

func TestProcessLauncherNodeResolutionMissingOverrideFailsClosed(t *testing.T) {
	tmp := launcherResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv(noderuntime.EnvNodeBin, missingNodeOverride)

	process, err := ProcessLauncher{}.Launch(context.Background(), LaunchSpec{BundleRoot: t.TempDir(), Env: []string{}})
	if !errors.Is(err, noderuntime.ErrNodeRuntimeMissing) {
		t.Fatalf("Launch err = %v, want node_runtime_missing", err)
	}
	if !strings.Contains(err.Error(), noderuntime.EnvNodeBin) {
		t.Fatalf("Launch err = %q, want mention of %s", err, noderuntime.EnvNodeBin)
	}
	if process != nil {
		t.Fatalf("process = %v, want nil on resolution failure", process)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker stat err = %v, want not-exist (no node exec)", statErr)
	}
	requireNoLauncherTempFiles(t, tmp)
}

func TestProcessLauncherNodeResolutionExplicitNodePathIgnoresOverride(t *testing.T) {
	launcherResolutionSandbox(t)
	marker := filepath.Join(t.TempDir(), "marker")
	fakeNode := writeFakeLauncherNode(t, marker, "explicit-node-ok")
	t.Setenv(noderuntime.EnvNodeBin, missingNodeOverride)

	if got := launchAndWait(t, ProcessLauncher{NodePath: fakeNode}); got != "explicit-node-ok" {
		t.Fatalf("stdout = %q, want explicit NodePath output", got)
	}
	requireLauncherMarker(t, marker, fakeNode)
}
