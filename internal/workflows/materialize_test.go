package workflows

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	t.Setenv("LOOM_LOCAL_RUNTIME", "")
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", "")
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")
}
