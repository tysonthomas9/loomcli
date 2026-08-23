package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// captureEnsureLogs routes the default slog logger into a buffer for the
// duration of the test so EnsureBuiltinWorkflow's warnings can be asserted.
func captureEnsureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// registerEpicRunnerWithRunners registers the epic-runner builtin out-of-band
// (the `loom driver register` shape the e2e stacks use) with a real staged
// bundle and the given runner specs. Returns the active version id.
func registerEpicRunnerWithRunners(t *testing.T, st store.Store, workDir string, runners []driverpkg.DriverRunnerSpec) string {
	t.Helper()
	dist := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("create dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	digest := SourceDigest(spec.Files)
	registered, err := driverpkg.RegisterFlueDriver(context.Background(), st, driverpkg.RegisterFlueOptions{
		WorkspaceKey: "BUILTIN",
		WorkDir:      workDir,
		DistPath:     dist,
		DriverName:   BuiltinEpicRunnerWorkflowName,
		DriverID:     BuiltinEpicRunnerWorkflowName,
		WorkflowName: BuiltinEpicRunnerWorkflowName,
		SourceRef:    "builtin://workflows/epic-runner/versions/" + digest,
		SourceDigest: digest,
		CreatedBy:    "system",
		Activate:     true,
		RunnerSpecs:  runners,
		Trust:        domain.DriverTrustTrusted,
	})
	if err != nil {
		t.Fatalf("register epic-runner: %v", err)
	}
	return registered.Version.VersionID
}

func subsetRunnerSpecs() []driverpkg.DriverRunnerSpec {
	// Only local-task-runner — a strict SUBSET of the fresh derived set
	// {daytona-task-runner, local-task-runner}. This is exactly what
	// scripts/test-runner-pr-e2e.sh registers.
	return []driverpkg.DriverRunnerSpec{
		{Name: "local-task-runner", Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"},
	}
}

// TestEnsureBuiltinWorkflowRefreshesSubsetRunnerManifest: a builtin whose
// active manifest declares only a SUBSET of the freshly-derived runners must
// NOT be reused as-is — a later run requesting one of the missing runners
// (e.g. {"runner":"daytona-task-runner"}) would pin this version and
// resolveTaskRunRequestRunner/applyResolvedRunner would reject the child task.
// When a rebuild is possible, EnsureBuiltinWorkflow must re-register so the
// active manifest carries the full runner set.
func TestEnsureBuiltinWorkflowRefreshesSubsetRunnerManifest(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)

	versionID := registerEpicRunnerWithRunners(t, st, workDir, subsetRunnerSpecs())

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow: %v", err)
	}

	driver, err := st.Drivers().Get(ctx, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID == versionID {
		t.Fatalf("subset runner manifest was reused (active still %q); it must re-register to restore the full runner set", versionID)
	}
	version, err := st.DriverVersions().Get(ctx, "BUILTIN", driver.ActiveVersionID)
	if err != nil {
		t.Fatalf("get refreshed version: %v", err)
	}
	var refreshed []driverpkg.DriverRunnerSpec
	if err := json.Unmarshal([]byte(version.Manifest["runners"]), &refreshed); err != nil {
		t.Fatalf("decode refreshed runners: %v", err)
	}
	names := map[string]bool{}
	for _, r := range refreshed {
		names[r.Name] = true
	}
	if !names["daytona-task-runner"] || !names["local-task-runner"] {
		t.Fatalf("refreshed manifest runners = %+v, want the full fresh set", refreshed)
	}
}

// TestEnsureBuiltinWorkflowSubsetManifestReusedWhenRebuildUnavailable pins the
// fail-open door: when the subset-manifest re-register CANNOT run (a `loom
// serve` with no bundling toolchain on disk — the incident environment), the
// still-usable registered version is REUSED with a warning instead of failing
// the request. The incident failure mode (a hard error for runs the registered
// driver could serve) must not come back through the subset-staleness check.
func TestEnsureBuiltinWorkflowSubsetManifestReusedWhenRebuildUnavailable(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// NO installFakeWorkflowBuildDeps and a bare cwd: BuildAndRegister fails
	// at @loom/sdk resolution, exactly like the toolchain-less serve container.
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("LOOM_SDK_ROOT", "")
	// Fail-open is a compile-lane behavior; on the fail-closed path
	// (desktop/packaged) the packaged lane's error wins instead.
	t.Setenv("LOOM_LOCAL_RUNTIME", "")
	// builtinWorkflowWorkDir honors LOOM_WORKSPACE_RUNTIME_DIR before cwd; clear
	// the ambient value so the registered subset bundle is found under this cwd
	// and the fail-open-onto-reuse path is exercised (not a bundle-missing miss).
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

	versionID := registerEpicRunnerWithRunners(t, st, workDir, subsetRunnerSpecs())

	logs := captureEnsureLogs(t)
	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow must fail open onto the usable registered version, got error: %v", err)
	}

	driver, err := st.Drivers().Get(ctx, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID != versionID {
		t.Fatalf("active version changed to %q; rebuild-unavailable fallback must reuse %q", driver.ActiveVersionID, versionID)
	}
	out := logs.String()
	if !strings.Contains(out, "missing runners") || !strings.Contains(out, "daytona-task-runner") {
		t.Fatalf("expected a missing-runners reuse warning naming daytona-task-runner, logs:\n%s", out)
	}
}
