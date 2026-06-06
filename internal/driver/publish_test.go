package driver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPackWorkflowCreatesFlueLayout(t *testing.T) {
	root := t.TempDir()
	flueCommand := fakeFlueCommand(t)
	source := writeWorkflow(t, root, "complete-epic.ts", `export const run = defineDriver({
  name: "complete-epic",
  async run(ctx) {
    return ctx.run.complete({ summary: "done" });
  },
});
`)

	bundle, err := PackWorkflow(PackOptions{WorkDir: root, SourcePath: source, FlueCommand: flueCommand})
	if err != nil {
		t.Fatalf("PackWorkflow: %v", err)
	}
	if bundle.SourceRef != ".loom/workflows/complete-epic.ts" {
		t.Fatalf("SourceRef = %q, want .loom/workflows/complete-epic.ts", bundle.SourceRef)
	}
	if bundle.Manifest["runtime"] != RuntimeFlueNode || bundle.Manifest["entrypoint"] != EntrypointRun {
		t.Fatalf("manifest = %+v, want flue-node run", bundle.Manifest)
	}
	bundledSource, err := os.ReadFile(filepath.Join(bundle.Root, ".flue", "loom-sources", "complete-epic.ts"))
	if err != nil {
		t.Fatalf("read bundled source: %v", err)
	}
	if string(bundledSource) == "" {
		t.Fatal("bundled source is empty")
	}
	if _, err := os.Stat(filepath.Join(bundle.Root, "dist", "server.mjs")); err != nil {
		t.Fatalf("stat built server: %v", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(bundle.Root, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest["workflow_ref"] != ".flue/workflows/complete-epic.ts" {
		t.Fatalf("workflow_ref = %q, want bundled Flue workflow path", manifest["workflow_ref"])
	}
	if manifest["source_bundle_ref"] != ".flue/loom-sources/complete-epic.ts" {
		t.Fatalf("source_bundle_ref = %q, want bundled Loom source path", manifest["source_bundle_ref"])
	}
	if manifest["server_ref"] != "dist/server.mjs" {
		t.Fatalf("server_ref = %q, want built Flue server path", manifest["server_ref"])
	}

	again, err := PackWorkflow(PackOptions{WorkDir: root, SourcePath: ".loom/workflows/complete-epic.ts", FlueCommand: flueCommand})
	if err != nil {
		t.Fatalf("PackWorkflow again: %v", err)
	}
	if again.BundleDigest != bundle.BundleDigest {
		t.Fatalf("BundleDigest changed across identical packs: %q != %q", again.BundleDigest, bundle.BundleDigest)
	}
}

func TestPackWorkflowRejectsInvalidSources(t *testing.T) {
	root := t.TempDir()
	flueCommand := fakeFlueCommand(t)
	missingRun := writeWorkflow(t, root, "missing-run.ts", `export const nope = () => {};
`)
	if _, err := PackWorkflow(PackOptions{WorkDir: root, SourcePath: missingRun, FlueCommand: flueCommand}); !errors.As(err, new(*ValidationError)) {
		t.Fatalf("PackWorkflow missing run err = %v, want ValidationError", err)
	}

	unbalanced := writeWorkflow(t, root, "broken.ts", `export const run = () => {
`)
	if _, err := PackWorkflow(PackOptions{WorkDir: root, SourcePath: unbalanced, FlueCommand: flueCommand}); !errors.As(err, new(*ValidationError)) {
		t.Fatalf("PackWorkflow unbalanced err = %v, want ValidationError", err)
	}
}

func TestPublishWorkflowCreatesActivePinnedVersion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	flueCommand := fakeFlueCommand(t)
	source := writeWorkflow(t, root, "complete-epic.ts", `export async function run(ctx) {
  return ctx.run.complete({ summary: "done" });
}
`)
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}

	result, err := PublishWorkflow(ctx, st, PublishOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		SourcePath:   source,
		CreatedBy:    "tester",
		FlueCommand:  flueCommand,
	})
	if err != nil {
		t.Fatalf("PublishWorkflow: %v", err)
	}
	if !result.CreatedDriver || !result.CreatedVersion || result.ReusedVersion {
		t.Fatalf("publish result flags = %+v, want created driver/version", result)
	}
	if result.Driver.Status != domain.DriverStatusActive || result.Driver.ActiveVersionID != result.Version.VersionID {
		t.Fatalf("driver = %+v, want active pinned version", result.Driver)
	}
	if result.Version.ValidationStatus != domain.DriverVersionValidationPassed || result.Version.BundleRef == "" {
		t.Fatalf("version = %+v, want passed bundle ref", result.Version)
	}

	replay, err := PublishWorkflow(ctx, st, PublishOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		SourcePath:   ".loom/workflows/complete-epic.ts",
		CreatedBy:    "tester",
		FlueCommand:  flueCommand,
	})
	if err != nil {
		t.Fatalf("PublishWorkflow replay: %v", err)
	}
	if !replay.ReusedVersion || replay.Version.VersionID != result.Version.VersionID {
		t.Fatalf("replay = %+v, want reused version %s", replay, result.Version.VersionID)
	}
}

func TestPublishWorkflowRecordsFailedVersionWithoutActivatingDriver(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	flueCommand := fakeFlueCommand(t)
	source := writeWorkflow(t, root, "broken.ts", `export const run = () => {
`)
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}

	result, err := PublishWorkflow(ctx, st, PublishOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		SourcePath:   source,
		CreatedBy:    "tester",
		FlueCommand:  flueCommand,
	})
	if !errors.As(err, new(*ValidationError)) {
		t.Fatalf("PublishWorkflow err = %v, want ValidationError", err)
	}
	if result == nil || result.Version == nil {
		t.Fatalf("PublishWorkflow result = %+v, want failed version result", result)
	}
	if result.Version.ValidationStatus != domain.DriverVersionValidationFailed {
		t.Fatalf("version status = %q, want failed", result.Version.ValidationStatus)
	}
	if result.Driver.Status != domain.DriverStatusDraft || result.Driver.ActiveVersionID != "" {
		t.Fatalf("driver = %+v, want draft without active version", result.Driver)
	}
	if result.Version.BuildDiagnostics == "" {
		t.Fatal("failed version diagnostics are empty")
	}

	replay, err := PublishWorkflow(ctx, st, PublishOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		SourcePath:   source,
		CreatedBy:    "tester",
		FlueCommand:  flueCommand,
	})
	if !errors.As(err, new(*ValidationError)) {
		t.Fatalf("PublishWorkflow failed replay err = %v, want ValidationError", err)
	}
	if replay == nil || !replay.ReusedVersion || replay.Version.VersionID != result.Version.VersionID {
		t.Fatalf("failed replay = %+v, want reused failed version %s", replay, result.Version.VersionID)
	}
}

func writeWorkflow(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, ".loom", "workflows", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return path
}

func fakeFlueCommand(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-flue")
	script := `#!/bin/sh
set -eu
out=""
while [ "$#" -gt 0 ]; do
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
if [ -z "$out" ]; then
  echo "missing --output" >&2
  exit 2
fi
mkdir -p "$out"
cat > "$out/server.mjs" <<'EOS'
const leaked = [
  'LOOM_FLEET_DB_URL',
  'LOOM_FLEET_DB_API_KEY',
  'LOOM_TASK_RUN_LEASE_TOKEN',
  'OPENAI_API_KEY',
  'AWS_SECRET_ACCESS_KEY',
  'GITHUB_TOKEN',
].filter((key) => process.env[key]);

if (process.env.FLUE_MODE === 'local' && process.send) {
  process.send({ version: 1, type: 'ready', target: 'workflow', name: process.env.FLUE_CLI_NAME || 'complete-epic' });
  process.on('message', (message) => {
    if (leaked.length) {
      process.send({ version: 1, type: 'result', requestId: message.requestId, result: { status: 'failed', summary: 'leaked env: ' + leaked.join(','), errorClass: 'env_leak' } }, () => process.exit(0));
      return;
    }
    process.send({ version: 1, type: 'result', requestId: message.requestId, result: { status: 'completed', summary: 'fake flue' } }, () => process.exit(0));
  });
} else {
  console.log('fake flue server');
}
EOS
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake flue command: %v", err)
	}
	return []string{path}
}
