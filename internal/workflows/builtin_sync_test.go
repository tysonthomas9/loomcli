package workflows

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const epicSrcA = "export {};\n"
const epicSrcB = "export const build = 2;\n"

// syncEpic installs nothing; it runs a sync for epic-runner and fails on error.
func syncEpic(t *testing.T, ctx context.Context, st store.Store, opts BuiltinSyncOptions) *BuiltinSyncResult {
	t.Helper()
	res, err := SyncBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName, opts)
	if err != nil {
		t.Fatalf("SyncBuiltinWorkflow: %v", err)
	}
	return res
}

// installEpicTree writes a fresh packaged tree holding epic-runner (with the
// given server.mjs body) and clears the packaged-artifact cache so the next
// lookup reads the new tree. It does NOT reset the isolated env, so the data dir
// (LOOM_WORKSPACE_RUNTIME_DIR) stays stable across installs.
func installEpicTree(t *testing.T, serverSource string) {
	t.Helper()
	writePackagedTree(t, map[string]string{BuiltinEpicRunnerWorkflowName: serverSource})
	ResetPackagedCacheForTest()
}

func getBuiltinDriver(t *testing.T, st store.Store, name string) *domain.Driver {
	t.Helper()
	drv, err := st.Drivers().Get(context.Background(), "BUILTIN", name)
	if err != nil {
		t.Fatalf("get driver %q: %v", name, err)
	}
	return drv
}

func assertMeta(t *testing.T, drv *domain.Driver, key, want string) {
	t.Helper()
	if got := drv.Metadata[key]; got != want {
		t.Fatalf("driver metadata[%q] = %q, want %q", key, got, want)
	}
}

func bundleDirExists(t *testing.T, workDir string, version *domain.DriverVersion) bool {
	t.Helper()
	root := filepath.Join(workDir, filepath.FromSlash(version.BundleRef))
	info, err := os.Stat(filepath.Join(root, "manifest.json"))
	return err == nil && !info.IsDir()
}

// Scenario 1 + 2: fresh workspace registers + activates on the auto track with a
// {system, builtin_sync} record; the same tree again is a no-op.
func TestSyncBuiltinFreshThenIdempotent(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)
	installEpicTree(t, epicSrcA)

	r := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if !r.Packaged.RegisteredNew || !r.Activated {
		t.Fatalf("fresh sync: registered_new=%v activated=%v, want both true", r.Packaged.RegisteredNew, r.Activated)
	}
	if r.Track != driver.BuiltinTrackAuto {
		t.Fatalf("track = %q, want auto", r.Track)
	}
	drv := getBuiltinDriver(t, st, BuiltinEpicRunnerWorkflowName)
	assertMeta(t, drv, driver.MetadataKeyActivationActor, "system")
	assertMeta(t, drv, driver.MetadataKeyActivationReason, "builtin_sync")
	assertMeta(t, drv, driver.MetadataKeyBuiltinTrack, "auto")
	vA := drv.ActiveVersionID

	r2 := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if r2.Packaged.RegisteredNew || r2.Activated {
		t.Fatalf("idempotent sync: registered_new=%v activated=%v, want both false", r2.Packaged.RegisteredNew, r2.Activated)
	}
	if r2.ActiveVersionID != vA {
		t.Fatalf("active changed on idempotent sync: %q -> %q", vA, r2.ActiveVersionID)
	}
}

// Scenario 3-6: update (auto) → v2; rollback → v1 preserved + update_available;
// ForceTrack auto → v2; downgrade (auto) → v1 without a new version.
func TestSyncBuiltinLifecycle(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)
	workDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")

	installEpicTree(t, epicSrcA)
	vA := syncEpic(t, ctx, st, BuiltinSyncOptions{}).ActiveVersionID
	verA, err := st.DriverVersions().Get(ctx, "BUILTIN", vA)
	if err != nil {
		t.Fatalf("get vA: %v", err)
	}

	// Update to tree B on the auto track.
	installEpicTree(t, epicSrcB)
	rB := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	vB := rB.ActiveVersionID
	if vB == vA || !rB.Packaged.RegisteredNew || !rB.Activated {
		t.Fatalf("update sync: vB=%q vA=%q registered_new=%v activated=%v", vB, vA, rB.Packaged.RegisteredNew, rB.Activated)
	}
	if rB.PreviousActiveVersionID != vA {
		t.Fatalf("previous = %q, want vA %q", rB.PreviousActiveVersionID, vA)
	}
	// vA row + staged bundle still present (immutable + retained).
	if _, err := st.DriverVersions().Get(ctx, "BUILTIN", vA); err != nil {
		t.Fatalf("vA row missing after update: %v", err)
	}
	if !bundleDirExists(t, workDir, verA) {
		t.Fatalf("vA staged bundle removed by update")
	}

	// Rollback to vA (recorded previous).
	drv, _, err := driver.RollbackDriverVersion(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName, "")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if drv.ActiveVersionID != vA {
		t.Fatalf("rollback active = %q, want vA", drv.ActiveVersionID)
	}
	assertMeta(t, drv, driver.MetadataKeyBuiltinTrack, "pinned")

	// Sync under tree B while pinned: vB stays registered, vA stays active,
	// update_available=true.
	rPinned := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if rPinned.Activated || rPinned.Packaged.RegisteredNew {
		t.Fatalf("pinned sync must not activate/register: %+v", rPinned)
	}
	if rPinned.ActiveVersionID != vA || !rPinned.UpdateAvailable || rPinned.Track != driver.BuiltinTrackPinned {
		t.Fatalf("pinned sync: active=%q update_available=%v track=%q", rPinned.ActiveVersionID, rPinned.UpdateAvailable, rPinned.Track)
	}

	// Return to auto: ForceTrack auto activates vB with {user, operator, auto}.
	rForce := syncEpic(t, ctx, st, BuiltinSyncOptions{ForceTrack: driver.BuiltinTrackAuto})
	if rForce.ActiveVersionID != vB || rForce.Track != driver.BuiltinTrackAuto || !rForce.Activated {
		t.Fatalf("force auto: active=%q track=%q activated=%v", rForce.ActiveVersionID, rForce.Track, rForce.Activated)
	}
	drv = getBuiltinDriver(t, st, BuiltinEpicRunnerWorkflowName)
	assertMeta(t, drv, driver.MetadataKeyActivationActor, "user")
	assertMeta(t, drv, driver.MetadataKeyActivationReason, "operator")
	assertMeta(t, drv, driver.MetadataKeyBuiltinTrack, "auto")

	// Downgrade: tree A again on the auto track re-activates vA, no new version.
	installEpicTree(t, epicSrcA)
	rDown := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if rDown.ActiveVersionID != vA || rDown.Packaged.RegisteredNew || !rDown.Activated {
		t.Fatalf("downgrade: active=%q registered_new=%v activated=%v", rDown.ActiveVersionID, rDown.Packaged.RegisteredNew, rDown.Activated)
	}
}

// Scenario 8: a tree whose artifact bytes are identical but whose index digest
// differs (a second built-in added) mints no new version.
func TestSyncBuiltinSameArtifactDifferentIndexNoNewVersion(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)

	installEpicTree(t, epicSrcA)
	vA := syncEpic(t, ctx, st, BuiltinSyncOptions{}).ActiveVersionID

	// Tree A': epic-runner byte-identical, github-review-agent added so the
	// index digest changes.
	_, idxA := writePackagedTree(t, map[string]string{
		BuiltinEpicRunnerWorkflowName:        epicSrcA,
		BuiltinGitHubReviewAgentWorkflowName: "// gh\nexport {};\n",
	})
	ResetPackagedCacheForTest()
	_ = idxA

	r := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if r.Packaged.RegisteredNew {
		t.Fatalf("index-only churn minted a new version: %+v", r)
	}
	if r.ActiveVersionID != vA {
		t.Fatalf("active changed on index-only churn: %q -> %q", vA, r.ActiveVersionID)
	}
}

// Scenario 9: a missing or tampered packaged bundle is repaired in place — the
// same version id, re-staged from the app resource.
func TestSyncBuiltinRepairsMissingBundle(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)
	workDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")

	installEpicTree(t, epicSrcA)
	vA := syncEpic(t, ctx, st, BuiltinSyncOptions{}).ActiveVersionID
	verA, err := st.DriverVersions().Get(ctx, "BUILTIN", vA)
	if err != nil {
		t.Fatalf("get vA: %v", err)
	}

	// Delete the staged bundle dir.
	if err := os.RemoveAll(filepath.Join(workDir, filepath.FromSlash(verA.BundleRef))); err != nil {
		t.Fatalf("remove staged bundle: %v", err)
	}
	r := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if !r.Repaired {
		t.Fatalf("expected repaired=true, got %+v", r)
	}
	if r.Packaged.VersionID != vA || r.ActiveVersionID != vA {
		t.Fatalf("repair changed version: packaged=%q active=%q, want vA %q", r.Packaged.VersionID, r.ActiveVersionID, vA)
	}
	if !bundleDirExists(t, workDir, verA) {
		t.Fatalf("bundle not restored after repair")
	}
	// Verify the restored bundle passes verification.
	if err := driver.VerifyStagedBundle(workDir, verA); err != nil {
		t.Fatalf("restored bundle fails verification: %v", err)
	}
}

func TestSyncBuiltinRepairsTamperedBundle(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)
	workDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")

	installEpicTree(t, epicSrcA)
	vA := syncEpic(t, ctx, st, BuiltinSyncOptions{}).ActiveVersionID
	verA, err := st.DriverVersions().Get(ctx, "BUILTIN", vA)
	if err != nil {
		t.Fatalf("get vA: %v", err)
	}
	// Tamper the staged server.mjs (digest mismatch, files still present).
	serverPath := filepath.Join(workDir, filepath.FromSlash(verA.BundleRef), "dist", "server.mjs")
	if err := os.WriteFile(serverPath, []byte("export const tampered = true;\n"), 0o644); err != nil {
		t.Fatalf("tamper server.mjs: %v", err)
	}
	if err := driver.VerifyStagedBundle(workDir, verA); err == nil {
		t.Fatal("tamper must fail verification before repair")
	}
	r := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if !r.Repaired || r.Packaged.VersionID != vA {
		t.Fatalf("tamper repair: repaired=%v version=%q, want true/vA", r.Repaired, r.Packaged.VersionID)
	}
	if err := driver.VerifyStagedBundle(workDir, verA); err != nil {
		t.Fatalf("bundle still fails verification after repair: %v", err)
	}
}

// stageActiveCustomEpicVersion registers a custom (untrusted, non-packaged)
// epic-runner version with a staged bundle and activates it (operator, pinned).
func stageActiveCustomEpicVersion(t *testing.T, ctx context.Context, st store.Store, workDir, serverSource string) *domain.DriverVersion {
	t.Helper()
	dist := filepath.Join(t.TempDir(), "dist")
	writePackagedDist(t, dist)
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(serverSource), 0o644); err != nil {
		t.Fatalf("write custom server.mjs: %v", err)
	}
	res, err := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
		WorkspaceKey: "BUILTIN",
		WorkDir:      workDir,
		DistPath:     dist,
		DriverName:   BuiltinEpicRunnerWorkflowName,
		DriverID:     BuiltinEpicRunnerWorkflowName,
		WorkflowName: BuiltinEpicRunnerWorkflowName,
		SourceRef:    "api://workflows/epic-runner/versions/custom",
		CreatedBy:    "api",
		Activate:     false,
		Trust:        domain.DriverTrustUntrusted,
	})
	if err != nil {
		t.Fatalf("register custom version: %v", err)
	}
	if _, _, err := driver.ActivateDriverVersion(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName, res.Version.VersionID); err != nil {
		t.Fatalf("activate custom version: %v", err)
	}
	return res.Version
}

// Scenario 7: a custom pinned active version is preserved across a packaged
// update — the packaged version registers inactive and update_available flips.
func TestSyncBuiltinCustomPinnedPreserved(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)
	workDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")

	installEpicTree(t, epicSrcA)
	syncEpic(t, ctx, st, BuiltinSyncOptions{}) // register + activate vA (trusted, auto)

	custom := stageActiveCustomEpicVersion(t, ctx, st, workDir, "export const custom = 1;\n")

	// New app build (tree B) while pinned to the custom version.
	installEpicTree(t, epicSrcB)
	r := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if r.Activated {
		t.Fatalf("custom pin must not be replaced by sync: %+v", r)
	}
	if r.ActiveVersionID != custom.VersionID {
		t.Fatalf("active = %q, want custom %q", r.ActiveVersionID, custom.VersionID)
	}
	if !r.UpdateAvailable || r.Track != driver.BuiltinTrackPinned {
		t.Fatalf("custom pinned: update_available=%v track=%q", r.UpdateAvailable, r.Track)
	}
	if !r.ActiveBundleAvailable {
		t.Fatalf("custom bundle should be available")
	}
	// vB registered but inactive.
	if r.Packaged.VersionID == custom.VersionID {
		t.Fatalf("packaged version id must differ from the active custom version")
	}
}

// Scenario 10: a pinned custom active version whose bundle is deleted is
// reported unavailable, and EnsureBuiltinWorkflow fails
// builtin_active_version_unavailable — the active version is never auto-switched.
func TestSyncBuiltinCustomBundleDeletedUnavailable(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)
	workDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")

	installEpicTree(t, epicSrcA)
	syncEpic(t, ctx, st, BuiltinSyncOptions{})
	custom := stageActiveCustomEpicVersion(t, ctx, st, workDir, "export const custom = 1;\n")

	// Delete the custom staged bundle.
	if err := os.RemoveAll(filepath.Join(workDir, filepath.FromSlash(custom.BundleRef))); err != nil {
		t.Fatalf("remove custom bundle: %v", err)
	}
	r := syncEpic(t, ctx, st, BuiltinSyncOptions{})
	if r.ActiveBundleAvailable {
		t.Fatalf("deleted custom bundle must report active_bundle_available=false")
	}
	if r.ActiveVersionID != custom.VersionID {
		t.Fatalf("active must not change on unavailable bundle: %q", r.ActiveVersionID)
	}
	err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err == nil || !strings.Contains(err.Error(), "builtin_active_version_unavailable") {
		t.Fatalf("EnsureBuiltinWorkflow err = %v, want builtin_active_version_unavailable", err)
	}
	if !strings.Contains(err.Error(), "--builtin") {
		t.Fatalf("error must name the activate --builtin remedy: %v", err)
	}
}

// DescribeBuiltinVersions surfaces the packaged block without mutating.
func TestDescribeBuiltinVersions(t *testing.T) {
	isolatePackagedEnv(t)
	ctx := context.Background()
	st := newBuiltinStore(t)

	installEpicTree(t, epicSrcA)
	vA := syncEpic(t, ctx, st, BuiltinSyncOptions{}).ActiveVersionID

	info, err := DescribeBuiltinVersions(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if info.PackagedVersionID != vA {
		t.Fatalf("packaged_version_id = %q, want vA %q", info.PackagedVersionID, vA)
	}
	if info.Track != driver.BuiltinTrackAuto || info.UpdateAvailable {
		t.Fatalf("describe on auto+current: track=%q update_available=%v", info.Track, info.UpdateAvailable)
	}
	if info.PackagedError != "" {
		t.Fatalf("unexpected packaged_error: %q", info.PackagedError)
	}

	// After an app update on a pinned track, update_available should be true.
	installEpicTree(t, epicSrcB)
	// Pin by rolling back onto vA (there is no previous; instead pin explicitly).
	if _, _, err := driver.ActivateDriverVersion(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName, vA); err != nil {
		t.Fatalf("pin vA: %v", err)
	}
	// Register vB inactive via a pinned sync.
	syncEpic(t, ctx, st, BuiltinSyncOptions{})
	info, err = DescribeBuiltinVersions(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("describe after update: %v", err)
	}
	if !info.UpdateAvailable || info.Track != driver.BuiltinTrackPinned {
		t.Fatalf("describe pinned+newer: track=%q update_available=%v", info.Track, info.UpdateAvailable)
	}
}
