package workflows

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// captureSlog routes the default slog logger into a buffer for the duration
// of the test, so EnsureBuiltinWorkflow's drift warning can be asserted.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// registerEpicRunnerAt registers the epic-runner builtin out-of-band (the
// `loom driver register` shape the e2e stack uses) with a real staged bundle,
// current runner specs, and the given source digest. Returns the registered
// active version id.
func registerEpicRunnerAt(t *testing.T, st store.Store, workDir, sourceDigest string) string {
	t.Helper()
	dist := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("create dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	registered, err := driverpkg.RegisterFlueDriver(context.Background(), st, driverpkg.RegisterFlueOptions{
		WorkspaceKey: "BUILTIN",
		WorkDir:      workDir,
		DistPath:     dist,
		DriverName:   BuiltinEpicRunnerWorkflowName,
		DriverID:     BuiltinEpicRunnerWorkflowName,
		WorkflowName: BuiltinEpicRunnerWorkflowName,
		SourceRef:    "builtin://workflows/epic-runner/versions/" + sourceDigest,
		SourceDigest: sourceDigest,
		CreatedBy:    "system",
		Activate:     true,
		RunnerSpecs: []driverpkg.DriverRunnerSpec{
			{Name: "local-task-runner", Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"},
			{Name: "daytona-task-runner", Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: "daytona-task-runner"},
		},
		Trust: domain.DriverTrustTrusted,
	})
	if err != nil {
		t.Fatalf("register epic-runner at digest %s: %v", sourceDigest, err)
	}
	return registered.Version.VersionID
}

func registerPromptAgentAt(t *testing.T, st store.Store, ws, workDir, sourceDigest string) string {
	t.Helper()
	dist := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("create dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	registered, err := driverpkg.RegisterFlueDriver(context.Background(), st, driverpkg.RegisterFlueOptions{
		WorkspaceKey: ws,
		WorkDir:      workDir,
		DistPath:     dist,
		DriverName:   BuiltinPromptAgentWorkflowName,
		DriverID:     BuiltinPromptAgentWorkflowName,
		WorkflowName: BuiltinPromptAgentWorkflowName,
		SourceRef:    "builtin://workflows/prompt-agent/versions/" + sourceDigest,
		SourceDigest: sourceDigest,
		CreatedBy:    "system",
		Activate:     true,
		RunnerSpecs: []driverpkg.DriverRunnerSpec{
			{Name: "local-task-runner", Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"},
		},
		Trust: domain.DriverTrustTrusted,
	})
	if err != nil {
		t.Fatalf("register prompt-agent at digest %s: %v", sourceDigest, err)
	}
	return registered.Version.VersionID
}

func registerOperatorPromptAgentAt(t *testing.T, st store.Store, ws, workDir, sourceDigest string, runnerNames ...string) string {
	t.Helper()
	if len(runnerNames) == 0 {
		runnerNames = []string{"local-task-runner"}
	}
	runners := make([]driverpkg.DriverRunnerSpec, 0, len(runnerNames))
	for _, name := range runnerNames {
		runners = append(runners, driverpkg.DriverRunnerSpec{Name: name, Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: name})
	}
	dist := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("create dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	registered, err := driverpkg.RegisterFlueDriver(context.Background(), st, driverpkg.RegisterFlueOptions{
		WorkspaceKey: ws, WorkDir: workDir, DistPath: dist,
		DriverName: BuiltinPromptAgentWorkflowName, DriverID: BuiltinPromptAgentWorkflowName,
		WorkflowName: BuiltinPromptAgentWorkflowName, SourceRef: "operator://prompt-agent/custom-version",
		SourceDigest: sourceDigest, CreatedBy: "operator", Activate: true,
		RunnerSpecs: runners,
		Trust:       domain.DriverTrustTrusted,
	})
	if err != nil {
		t.Fatalf("register operator prompt-agent at digest %s: %v", sourceDigest, err)
	}
	return registered.Version.VersionID
}

// TestEnsureBuiltinWorkflowRefreshesDigestDrift: a usable builtin registered
// under a DIFFERENT source digest is rebuilt and activated when the embedded
// build toolchain is available.
func TestEnsureBuiltinWorkflowRefreshesDigestDrift(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000aa"
	versionID := registerEpicRunnerAt(t, st, workDir, driftedDigest)

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow: %v", err)
	}

	driver, err := st.Drivers().Get(ctx, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID == versionID {
		t.Fatalf("active version stayed on drifted version %q; want refreshed registration", versionID)
	}
	version, err := st.DriverVersions().Get(ctx, "BUILTIN", driver.ActiveVersionID)
	if err != nil {
		t.Fatalf("get refreshed version: %v", err)
	}
	spec, _ := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if want := SourceDigest(spec.Files); version.SourceDigest != want {
		t.Fatalf("refreshed source digest = %q, want %q", version.SourceDigest, want)
	}
}

// TestEnsureBuiltinWorkflowDigestDriftFailsOpenWithoutBuildToolchain pins the
// packaged/minimal-serve fallback: an intact usable version remains active when
// refresh cannot run because build dependencies are absent. The warning keeps
// both digests and the classified failure visible to operators.
func TestEnsureBuiltinWorkflowDigestDriftFailsOpenWithoutBuildToolchain(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("LOOM_SDK_ROOT", "")

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000bb"
	versionID := registerEpicRunnerAt(t, st, workDir, driftedDigest)
	logs := captureSlog(t)

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow must fail open onto usable version: %v", err)
	}
	driver, err := st.Drivers().Get(ctx, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID != versionID {
		t.Fatalf("active version = %q, want reusable drifted version %q", driver.ActiveVersionID, versionID)
	}
	out := logs.String()
	if !strings.Contains(out, "builtin digest refresh unavailable") || !strings.Contains(out, "workflow build toolchain unavailable") {
		t.Fatalf("expected classified digest-refresh warning, logs:\n%s", out)
	}
	spec, _ := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	for _, want := range []string{driftedDigest, SourceDigest(spec.Files)} {
		if !strings.Contains(out, want) {
			t.Fatalf("refresh warning must carry both digests; missing %s in:\n%s", want, out)
		}
	}
}

func TestEnsureBuiltinWorkflowDigestDriftDoesNotHideBuildFailure(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", `["/bin/sh","-c","echo RolldownError: malformed workflow source >&2; exit 1"]`)

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000bd"
	versionID := registerEpicRunnerAt(t, st, workDir, driftedDigest)
	err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err == nil {
		t.Fatal("EnsureBuiltinWorkflow hid a workflow build failure")
	}
	if errors.Is(err, ErrBuildToolchainUnavailable) {
		t.Fatalf("malformed workflow error misclassified as unavailable toolchain: %v", err)
	}
	driver, getErr := st.Drivers().Get(ctx, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if getErr != nil {
		t.Fatalf("get driver: %v", getErr)
	}
	if driver.ActiveVersionID != versionID {
		t.Fatalf("failed refresh changed active version to %q, want %q", driver.ActiveVersionID, versionID)
	}
}

// TestEnsureBoundPromptAgentWorkflowsRefreshesExistingBindings covers the
// serve-start upgrade path. Only workspaces with a persisted prompt-agent
// binding are proactively refreshed; an unbound registration stays untouched.
func TestEnsureBoundPromptAgentWorkflowsRefreshesExistingBindings(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for _, ws := range []string{"BOUND", "UNBOUND"} {
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
			t.Fatalf("create workspace %s: %v", ws, err)
		}
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000cc"
	boundVersionID := registerPromptAgentAt(t, st, "BOUND", workDir, driftedDigest)
	unboundVersionID := registerPromptAgentAt(t, st, "UNBOUND", workDir, driftedDigest)
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:    "BOUND",
		BindingID:       "planner",
		Name:            "Planner",
		SourceKind:      store.InternalSourceKind,
		DriverID:        BuiltinPromptAgentWorkflowName,
		DriverVersionID: boundVersionID,
		Enabled:         true,
	}); err != nil {
		t.Fatalf("create existing prompt-agent binding: %v", err)
	}

	if err := EnsureBoundPromptAgentWorkflows(ctx, st); err != nil {
		t.Fatalf("EnsureBoundPromptAgentWorkflows: %v", err)
	}
	bound, err := st.Drivers().Get(ctx, "BOUND", BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get bound driver: %v", err)
	}
	if bound.ActiveVersionID == boundVersionID {
		t.Fatalf("bound prompt-agent stayed on drifted version %q", boundVersionID)
	}
	boundVersion, err := st.DriverVersions().Get(ctx, "BOUND", bound.ActiveVersionID)
	if err != nil {
		t.Fatalf("get refreshed bound version: %v", err)
	}
	spec, _ := BuiltinWorkflow(BuiltinPromptAgentWorkflowName)
	if want := SourceDigest(spec.Files); boundVersion.SourceDigest != want {
		t.Fatalf("bound source digest = %q, want %q", boundVersion.SourceDigest, want)
	}
	unbound, err := st.Drivers().Get(ctx, "UNBOUND", BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get unbound driver: %v", err)
	}
	if unbound.ActiveVersionID != unboundVersionID {
		t.Fatalf("unbound active version = %q, want unchanged %q", unbound.ActiveVersionID, unboundVersionID)
	}
}

func TestEnsureBoundPromptAgentWorkflowsFailsClosedOnDigestDriftWithoutBuildToolchain(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BOUND-NO-TOOLCHAIN", Name: "Bound no toolchain"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("LOOM_SDK_ROOT", "")
	previousFS := packagedBuiltinFS
	packagedBuiltinFS = absentPackagedBuiltinFS{}
	t.Cleanup(func() { packagedBuiltinFS = previousFS })

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000cf"
	versionID := registerPromptAgentAt(t, st, "BOUND-NO-TOOLCHAIN", workDir, driftedDigest)
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "BOUND-NO-TOOLCHAIN", BindingID: "planner-no-toolchain", Name: "Planner no toolchain",
		SourceKind: store.InternalSourceKind, DriverID: BuiltinPromptAgentWorkflowName,
		DriverVersionID: versionID, Enabled: true,
	}); err != nil {
		t.Fatalf("create existing prompt-agent binding: %v", err)
	}

	err := EnsureBoundPromptAgentWorkflows(ctx, st)
	if err == nil || !strings.Contains(err.Error(), "workflow build toolchain unavailable") {
		t.Fatalf("digest-drifted bound prompt-agent error = %v, want fail-closed toolchain error", err)
	}
	driverRecord, getErr := st.Drivers().Get(ctx, "BOUND-NO-TOOLCHAIN", BuiltinPromptAgentWorkflowName)
	if getErr != nil {
		t.Fatalf("get prompt-agent driver: %v", getErr)
	}
	if driverRecord.ActiveVersionID != versionID {
		t.Fatalf("failed refresh changed active version to %q, want %q", driverRecord.ActiveVersionID, versionID)
	}
}

func TestEnsureBoundPromptAgentWorkflowsUsesPackagedBundleWithoutBuildToolchain(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	const workspace = "BOUND-PACKAGED"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "Bound packaged"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", workDir)
	t.Setenv("LOOM_SDK_ROOT", "")
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000ce"
	versionID := registerPromptAgentAt(t, st, workspace, workDir, driftedDigest)
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: workspace, BindingID: "planner-packaged", Name: "Planner packaged",
		SourceKind: store.InternalSourceKind, DriverID: BuiltinPromptAgentWorkflowName,
		DriverVersionID: versionID, Enabled: true,
	}); err != nil {
		t.Fatalf("create existing prompt-agent binding: %v", err)
	}

	spec, ok := BuiltinWorkflow(BuiltinPromptAgentWorkflowName)
	if !ok {
		t.Fatal("prompt-agent builtin missing")
	}
	digest := SourceDigest(spec.Files)
	previousFS := packagedBuiltinFS
	packagedBuiltinFS = fstest.MapFS{
		"builtin-dist/prompt-agent/dist/server.mjs":        {Data: []byte("export const packaged = true;\n"), Mode: 0o644},
		"builtin-dist/prompt-agent/dist/source-digest.txt": {Data: []byte(digest + "\n"), Mode: 0o644},
	}
	t.Cleanup(func() { packagedBuiltinFS = previousFS })

	if err := EnsureBoundPromptAgentWorkflows(ctx, st); err != nil {
		t.Fatalf("EnsureBoundPromptAgentWorkflows with packaged bundle: %v", err)
	}
	driverRecord, err := st.Drivers().Get(ctx, workspace, BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver: %v", err)
	}
	if driverRecord.ActiveVersionID == versionID {
		t.Fatalf("packaged refresh left drifted version active: %q", versionID)
	}
	version, err := st.DriverVersions().Get(ctx, workspace, driverRecord.ActiveVersionID)
	if err != nil {
		t.Fatalf("get packaged prompt-agent version: %v", err)
	}
	if version.SourceDigest != digest || version.CreatedBy != "system" {
		t.Fatalf("packaged version provenance = digest %q created_by %q, want %q/system", version.SourceDigest, version.CreatedBy, digest)
	}
	if want := "builtin://workflows/prompt-agent/versions/" + digest; version.SourceRef != want {
		t.Fatalf("packaged version source_ref = %q, want %q", version.SourceRef, want)
	}
	if got := driverpkg.DriverVersionEffectiveTrust(driverRecord, version); got != domain.DriverTrustTrusted {
		t.Fatalf("packaged version trust = %q, want trusted", got)
	}
	if !strings.Contains(version.Manifest["runners"], "local-task-runner") {
		t.Fatalf("packaged version runners = %q, want local-task-runner", version.Manifest["runners"])
	}
	serverPath := filepath.Join(workDir, filepath.FromSlash(version.BundleRef), "dist", "server.mjs")
	server, err := os.ReadFile(serverPath)
	if err != nil {
		t.Fatalf("read staged packaged server: %v", err)
	}
	if !strings.Contains(string(server), "packaged = true") {
		t.Fatalf("staged server did not come from packaged FS: %q", server)
	}
}

func TestPackagedBuiltinDigestMarkerMustMatch(t *testing.T) {
	const distPath = "builtin-dist/prompt-agent/dist"
	for _, tc := range []struct {
		name string
		fs   fstest.MapFS
	}{
		{name: "missing", fs: fstest.MapFS{}},
		{name: "stale", fs: fstest.MapFS{distPath + "/source-digest.txt": {Data: []byte("sha256:stale\n")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches, err := packagedBuiltinDigestMatches(tc.fs, distPath, "sha256:current")
			if err != nil {
				t.Fatalf("packagedBuiltinDigestMatches: %v", err)
			}
			if matches {
				t.Fatal("missing/stale packaged digest marker matched current source")
			}
		})
	}
}

func TestEnsureBoundPromptAgentWorkflowsPreservesOperatorActiveVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CUSTOM", Name: "Custom"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)

	const customDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000dd"
	versionID := registerOperatorPromptAgentAt(t, st, "CUSTOM", workDir, customDigest, "operator-custom-runner")
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "CUSTOM", BindingID: "custom-planner", Name: "Custom planner",
		SourceKind: store.InternalSourceKind, DriverID: BuiltinPromptAgentWorkflowName,
		DriverVersionID: versionID, Enabled: true,
	}); err != nil {
		t.Fatalf("create custom prompt-agent binding: %v", err)
	}

	if err := EnsureBoundPromptAgentWorkflows(ctx, st); err != nil {
		t.Fatalf("EnsureBoundPromptAgentWorkflows: %v", err)
	}
	driverRecord, err := st.Drivers().Get(ctx, "CUSTOM", BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver: %v", err)
	}
	if driverRecord.ActiveVersionID != versionID {
		t.Fatalf("operator active version = %q, want preserved %q", driverRecord.ActiveVersionID, versionID)
	}
}

func TestEnsureBoundPromptAgentWorkflowsFailsClosedOnUnusableOperatorVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CUSTOM-BROKEN", Name: "Custom broken"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)

	const customDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000de"
	versionID := registerOperatorPromptAgentAt(t, st, "CUSTOM-BROKEN", workDir, customDigest)
	version, err := st.DriverVersions().Get(ctx, "CUSTOM-BROKEN", versionID)
	if err != nil {
		t.Fatalf("get custom version: %v", err)
	}
	if err := os.Remove(filepath.Join(workDir, filepath.FromSlash(version.BundleRef), "dist", "server.mjs")); err != nil {
		t.Fatalf("remove custom bundle entrypoint: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "CUSTOM-BROKEN", BindingID: "custom-broken", Name: "Custom broken",
		SourceKind: store.InternalSourceKind, DriverID: BuiltinPromptAgentWorkflowName,
		DriverVersionID: versionID, Enabled: true,
	}); err != nil {
		t.Fatalf("create custom prompt-agent binding: %v", err)
	}

	err = EnsureBoundPromptAgentWorkflows(ctx, st)
	if err == nil || !strings.Contains(err.Error(), "operator-managed active version") || !strings.Contains(err.Error(), "refusing to replace") {
		t.Fatalf("unusable operator version error = %v", err)
	}
	driverRecord, getErr := st.Drivers().Get(ctx, "CUSTOM-BROKEN", BuiltinPromptAgentWorkflowName)
	if getErr != nil {
		t.Fatalf("get prompt-agent driver: %v", getErr)
	}
	if driverRecord.ActiveVersionID != versionID {
		t.Fatalf("unusable operator version was replaced by %q, want preserved %q", driverRecord.ActiveVersionID, versionID)
	}
}

func TestEnsureBoundPromptAgentWorkflowsIgnoresDisabledBindings(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "DISABLED", Name: "Disabled"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000ee"
	versionID := registerPromptAgentAt(t, st, "DISABLED", workDir, driftedDigest)
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "DISABLED", BindingID: "disabled-planner", Name: "Disabled planner",
		SourceKind: store.InternalSourceKind, DriverID: BuiltinPromptAgentWorkflowName,
		DriverVersionID: versionID, Enabled: false,
	}); err != nil {
		t.Fatalf("create disabled prompt-agent binding: %v", err)
	}

	if err := EnsureBoundPromptAgentWorkflows(ctx, st); err != nil {
		t.Fatalf("EnsureBoundPromptAgentWorkflows: %v", err)
	}
	driverRecord, err := st.Drivers().Get(ctx, "DISABLED", BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver: %v", err)
	}
	if driverRecord.ActiveVersionID != versionID {
		t.Fatalf("disabled binding refreshed version to %q, want untouched %q", driverRecord.ActiveVersionID, versionID)
	}
}

// TestEnsureBuiltinWorkflowMatchingDigestIsSilent: a builtin registered under
// the CANONICAL digest (the `loom workflow digest` → `loom driver register`
// flow) hits the exact-match fast path — reused with NO drift warning.
func TestEnsureBuiltinWorkflowMatchingDigestIsSilent(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)

	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	canonical := SourceDigest(spec.Files)
	versionID := registerEpicRunnerAt(t, st, workDir, canonical)

	logs := captureSlog(t)
	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow: %v", err)
	}

	driver, err := st.Drivers().Get(ctx, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID != versionID {
		t.Fatalf("active version changed to %q; matching digest must reuse %q", driver.ActiveVersionID, versionID)
	}
	if out := logs.String(); strings.Contains(out, "builtin digest refresh") {
		t.Fatalf("matching digest must NOT warn about refresh, logs:\n%s", out)
	}
}

// TestSourceDigestNormalizesPathSeparators pins the key-normalization step of
// the canonical recipe: a Windows-style key hashes identically to its
// slash-form equivalent.
func TestSourceDigestNormalizesPathSeparators(t *testing.T) {
	slash := SourceDigest(map[string]string{"workflows/a.ts": "x", "workflows/b.ts": "y"})
	backslash := SourceDigest(map[string]string{filepath.FromSlash("workflows/a.ts"): "x", "workflows/b.ts": "y"})
	if slash != backslash {
		t.Fatalf("digest differs across path-separator forms: %s vs %s", slash, backslash)
	}
}
