package workflowdistribution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildBuiltinBundle (Phase U) verifies the daemon execution leaf can
// obtain a runnable copy of the bundled local-task-runner (which ships inside
// the epic-runner source tree), without driver registration.
func TestBuildBuiltinBundle(t *testing.T) {
	configureFakeBuiltinBundleBuild(t)

	dest := filepath.Join(t.TempDir(), "dist")
	serverPath, output, err := BuildBuiltinBundle(context.Background(), "epic-runner", dest)
	if err != nil {
		t.Fatalf("BuildBuiltinBundle: %v\n%s", err, output)
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

	if _, _, err := BuildBuiltinBundle(context.Background(), "does-not-exist", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("expected an error building an unknown bundle")
	}
}

func TestBuildBuiltinBundleClassifiesMissingRolldownNativeBinding(t *testing.T) {
	configureFakeBuiltinBundleBuild(t)

	root := t.TempDir()
	flue := filepath.Join(root, "missing-native-flue.sh")
	script := `#!/bin/sh
echo "Error: Cannot find module '@rolldown/binding-linux-arm64-gnu'" >&2
exit 1
`
	if err := os.WriteFile(flue, []byte(script), 0o755); err != nil {
		t.Fatalf("write failing fake flue: %v", err)
	}
	cmd, err := json.Marshal([]string{flue})
	if err != nil {
		t.Fatalf("encode failing fake flue command: %v", err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", string(cmd))

	_, diagnostics, err := BuildBuiltinBundle(context.Background(), BuiltinPromptAgentWorkflowName, filepath.Join(t.TempDir(), "dist"))
	if !errors.Is(err, ErrBuildToolchainUnavailable) {
		t.Fatalf("BuildBuiltinBundle error = %v, want ErrBuildToolchainUnavailable", err)
	}
	if !strings.Contains(diagnostics, "@rolldown/binding-linux-arm64-gnu") {
		t.Fatalf("diagnostics = %q, want missing native binding", diagnostics)
	}

	compileErr := classifyFlueBuildError(errors.New("exit status 1"), "RolldownError: Expected '}' in workflows/prompt-agent.ts")
	if errors.Is(compileErr, ErrBuildToolchainUnavailable) {
		t.Fatalf("workflow compile error was misclassified as missing toolchain: %v", compileErr)
	}
}

func configureFakeBuiltinBundleBuild(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	runtimeRoot := filepath.Join(root, "runtime")
	for _, path := range []string{
		sdkRoot,
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
		filepath.Join(runtimeRoot, "node_modules", "valibot"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(`{"name":"test"}`), 0o644); err != nil {
			t.Fatalf("write package.json for %s: %v", path, err)
		}
	}

	flue := filepath.Join(root, "fake-flue.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ -z "$out" ]]; then
  echo "missing --output" >&2
  exit 1
fi
mkdir -p "$out"
printf 'export {};\n' > "$out/server.mjs"
echo "fake flue build"
`
	if err := os.WriteFile(flue, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake flue: %v", err)
	}
	cmd, err := json.Marshal([]string{flue})
	if err != nil {
		t.Fatalf("encode fake flue command: %v", err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", string(cmd))
	t.Setenv("LOOM_REAL_FLUE_CMD", "")
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")
}
