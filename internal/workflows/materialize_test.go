package workflows

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
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

func TestBuildBuiltinBundleUsesMatchingPackagedBundle(t *testing.T) {
	spec, ok := BuiltinWorkflow("epic-runner")
	if !ok {
		t.Fatal("missing builtin epic-runner")
	}
	packagedRoot := t.TempDir()
	packaged := filepath.Join(packagedRoot, "epic-runner")
	if err := os.MkdirAll(filepath.Join(packaged, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packaged, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packaged, "assets", "runner.js"), []byte("export const bundled = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packaged, "source-digest.txt"), []byte(SourceDigest(spec.Files)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_BUILTIN_WORKFLOW_BUNDLE_DIR", packagedRoot)
	t.Setenv("LOOM_SDK_ROOT", "")
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")

	dest := filepath.Join(t.TempDir(), "dist")
	serverPath, output, err := BuildBuiltinBundle(context.Background(), "epic-runner", dest)
	if err != nil {
		t.Fatalf("BuildBuiltinBundle() error = %v", err)
	}
	if output != "packaged builtin bundle" {
		t.Fatalf("BuildBuiltinBundle() output = %q", output)
	}
	if _, err := os.Stat(filepath.Join(dest, "assets", "runner.js")); err != nil {
		t.Fatalf("packaged asset not copied: %v", err)
	}
	if serverPath != filepath.Join(dest, "server.mjs") {
		t.Fatalf("server path = %q", serverPath)
	}
}

func TestBuildBuiltinBundleRejectsStalePackagedBundle(t *testing.T) {
	packagedRoot := t.TempDir()
	packaged := filepath.Join(packagedRoot, "epic-runner")
	if err := os.MkdirAll(packaged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packaged, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packaged, "source-digest.txt"), []byte("sha256:stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_BUILTIN_WORKFLOW_BUNDLE_DIR", packagedRoot)

	_, _, err := BuildBuiltinBundle(context.Background(), "epic-runner", filepath.Join(t.TempDir(), "dist"))
	if err == nil || !strings.Contains(err.Error(), "packaged builtin bundle digest") {
		t.Fatalf("BuildBuiltinBundle() error = %v, want digest mismatch", err)
	}
}

func TestEnsureBuiltinWorkflowRegistersFromPackagedBundle(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("missing builtin epic-runner")
	}
	packagedRoot := t.TempDir()
	packaged := filepath.Join(packagedRoot, BuiltinEpicRunnerWorkflowName)
	if err := os.MkdirAll(packaged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packaged, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packaged, "source-digest.txt"), []byte(SourceDigest(spec.Files)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvBuiltinWorkflowBundleDir, packagedRoot)
	t.Setenv("LOOM_SDK_ROOT", "")
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	workDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", workDir)

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "PACKAGED", Name: "Packaged"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := EnsureBuiltinWorkflow(ctx, st, "PACKAGED", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow() error = %v", err)
	}
	driverRecord, err := st.Drivers().Get(ctx, "PACKAGED", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get registered driver: %v", err)
	}
	version, err := st.DriverVersions().Get(ctx, "PACKAGED", driverRecord.ActiveVersionID)
	if err != nil {
		t.Fatalf("get active version: %v", err)
	}
	if version.BuildDiagnostics != "packaged builtin bundle" {
		t.Fatalf("build diagnostics = %q", version.BuildDiagnostics)
	}
	if got := version.Manifest["trust_level"]; got != string(domain.DriverTrustTrusted) {
		t.Fatalf("trust level = %q", got)
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
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")
}
