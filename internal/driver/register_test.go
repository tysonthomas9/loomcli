//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRegisterFlueDriverStagesNativeArtifactAndActivates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "one")
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}

	result, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
		CreatedBy:    "tester",
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if !result.CreatedDriver || !result.CreatedVersion || result.ReusedVersion || !result.Activated {
		t.Fatalf("result flags = %+v, want created driver/version and activated", result)
	}
	if result.Driver.Status != domain.DriverStatusActive || result.Driver.ActiveVersionID != result.Version.VersionID {
		t.Fatalf("driver = %+v, want active pinned version", result.Driver)
	}
	if result.Version.Runtime != RuntimeFlueNode || result.Version.ValidationStatus != domain.DriverVersionValidationPassed {
		t.Fatalf("version = %+v, want passed flue-node version", result.Version)
	}
	if result.Version.Manifest["schema_version"] != NativeFlueSchemaVersion ||
		result.Version.Manifest["artifact_kind"] != NativeFlueArtifactKind ||
		result.Version.Manifest["artifact_ref"] != "dist" ||
		result.Version.Manifest["server_ref"] != "dist/server.mjs" ||
		result.Version.Manifest["workflow_name"] != "epic-runner" ||
		result.Version.Manifest["loom_sdk_package"] != LoomDriverSDKPackage {
		t.Fatalf("manifest = %+v, want native Flue manifest", result.Version.Manifest)
	}
	if result.Version.Manifest["workflow_ref"] != "" || result.Version.Manifest["source_bundle_ref"] != "" {
		t.Fatalf("manifest = %+v, generated-project refs must not be present", result.Version.Manifest)
	}
	// No fabrication (§4.6): without explicit RunnerSpecs the manifest declares
	// no runners — the prior local/daytona/openshell defaults are gone.
	if strings.TrimSpace(result.Version.Manifest["runners"]) != "" {
		t.Fatalf("runners manifest = %q, want empty (no fabricated runners)", result.Version.Manifest["runners"])
	}
	if result.Version.SourceDigest == "" || result.Version.SourceDigest != result.Version.Manifest["artifact_digest"] {
		t.Fatalf("source/artifact digest = %q/%q, want default source digest to artifact digest", result.Version.SourceDigest, result.Version.Manifest["artifact_digest"])
	}
	if _, err := os.Stat(filepath.Join(result.Bundle.Root, ".flue")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native bundle .flue stat err = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(result.Bundle.Root, "dist", "server.mjs")); err != nil {
		t.Fatalf("native bundle server.mjs missing: %v", err)
	}

	replay, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
		CreatedBy:    "tester",
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver replay: %v", err)
	}
	if !replay.ReusedVersion || replay.Version.VersionID != result.Version.VersionID {
		t.Fatalf("replay = %+v, want reused version %s", replay, result.Version.VersionID)
	}
}

func TestStageFlueDriverBundleIsPersistenceFreeAndPromotesIdempotently(t *testing.T) {
	root := t.TempDir()
	writeFlueDist(t, root, "custom-flow", "staged")

	staged, err := StageFlueDriverBundle(RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "custom-flow",
		SourceRef:    "file:///tmp/custom-flow#sha256:source",
		SourceDigest: "sha256:07ba20a2ad84dcc940d3a7adeb55288a8b76f5a5c97aeb12fe783d44567380b5",
		Trust:        domain.DriverTrustUntrusted,
	})
	if err != nil {
		t.Fatalf("StageFlueDriverBundle: %v", err)
	}
	t.Cleanup(staged.Cleanup)

	if staged.DriverID != "custom-flow" || staged.VersionID == "" ||
		staged.BundleRef == "" || staged.BundleDigest == "" || staged.Runtime != RuntimeFlueNode {
		t.Fatalf("staged registration = %+v, want content-addressed custom-flow metadata", staged)
	}
	if _, ok := staged.CatalogManifest[ManifestTrustLevelKey]; ok {
		t.Fatalf("catalog manifest = %+v, must not expose trust selection", staged.CatalogManifest)
	}
	if got := staged.Bundle.Manifest[ManifestTrustLevelKey]; got != string(domain.DriverTrustUntrusted) {
		t.Fatalf("bundle trust = %q, want untrusted", got)
	}
	if _, err := os.Stat(staged.Bundle.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final bundle exists before Promote, stat err = %v", err)
	}

	if err := staged.Promote(); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staged.Bundle.Root, "dist", "server.mjs")); err != nil {
		t.Fatalf("promoted server.mjs: %v", err)
	}
	if err := staged.Promote(); err != nil {
		t.Fatalf("second Promote: %v", err)
	}
	staged.Cleanup()
	if _, err := os.Stat(filepath.Join(staged.Bundle.Root, "manifest.json")); err != nil {
		t.Fatalf("Cleanup removed promoted bundle: %v", err)
	}
}

func TestRegisterFlueDriverNewDigestCreatesNewVersion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "one")
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	first, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver first: %v", err)
	}

	writeFlueDist(t, root, "epic-runner", "two")
	second, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver second: %v", err)
	}
	if second.Version.VersionID == first.Version.VersionID || second.Version.BundleDigest == first.Version.BundleDigest {
		t.Fatalf("second version = %+v, first = %+v, want new digest/version", second.Version, first.Version)
	}
	if second.Version.Version != 2 {
		t.Fatalf("second version number = %d, want 2", second.Version.Version)
	}
	if second.Driver.ActiveVersionID != second.Version.VersionID {
		t.Fatalf("driver active version = %q, want %q", second.Driver.ActiveVersionID, second.Version.VersionID)
	}
}

func TestRegisterFlueDriverRejectsInvalidManifest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "one")
	if err := os.WriteFile(filepath.Join(root, "dist", "loom-driver.json"), []byte(`{"server_ref":"../server.mjs"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}

	if _, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("RegisterFlueDriver invalid manifest err = %v, want ErrInvalid", err)
	}
	if _, err := st.Drivers().Get(ctx, "TEST", "epic-runner"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("driver after invalid manifest err = %v, want not found", err)
	}
}

func TestRegisterFlueDriverRejectsGeneratedManifestRefs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "one")
	if err := os.WriteFile(filepath.Join(root, "dist", "loom-driver.json"), []byte(`{"workflow_ref":".flue/workflows/epic-runner.ts"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}

	if _, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("RegisterFlueDriver generated refs err = %v, want ErrInvalid", err)
	}
}

func TestRegisterFlueDriverRejectsMissingServer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}

	if _, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		Activate:     true,
	}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RegisterFlueDriver missing server err = %v, want not exist", err)
	}
	if _, err := st.Drivers().Get(ctx, "TEST", "epic-runner"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("driver after missing server err = %v, want not found", err)
	}
}

func writeFlueDist(t *testing.T, root, workflowName, marker string) {
	t.Helper()
	dist := filepath.Join(root, "dist")
	if err := os.RemoveAll(dist); err != nil {
		t.Fatalf("remove dist: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	server := `if (process.send) {
  process.send({ version: 1, type: 'ready', target: 'workflow', name: process.env.FLUE_CLI_NAME || '` + workflowName + `' });
  process.on('message', (message) => {
    process.send({ version: 1, type: 'result', requestId: message.requestId, result: { status: 'completed', summary: '` + marker + `' } }, () => process.exit(0));
  });
}
`
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(server), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "workflow.mjs"), []byte("export const marker = "+strconv.Quote(marker)+";\n"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
}
