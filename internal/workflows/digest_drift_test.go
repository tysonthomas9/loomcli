package workflows

import (
	"bytes"
	"context"
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

// TestEnsureBuiltinWorkflowWarnsOnDigestDrift: a usable builtin registered
// under a DIFFERENT source digest (e.g. by an older e2e stack that hand-rolled
// its own recipe) is REUSED — no rebuild — but the mismatch is logged as a
// structured drift warning so the version skew stays visible.
func TestEnsureBuiltinWorkflowWarnsOnDigestDrift(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	// builtinWorkflowWorkDir honors LOOM_WORKSPACE_RUNTIME_DIR before cwd; clear
	// the ambient value (this suite may run inside a Loom workspace) so the reuse
	// lookup finds the bundle registered under this test's workdir.
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

	const driftedDigest = "sha256:00000000000000000000000000000000000000000000000000000000000000aa"
	versionID := registerEpicRunnerAt(t, st, workDir, driftedDigest)

	logs := captureSlog(t)
	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinEpicRunnerWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow: %v", err)
	}

	driver, err := st.Drivers().Get(ctx, "BUILTIN", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID != versionID {
		t.Fatalf("active version changed to %q; drift must REUSE the registered version %q, not rebuild", driver.ActiveVersionID, versionID)
	}

	out := logs.String()
	if !strings.Contains(out, "builtin digest drift") {
		t.Fatalf("expected a 'builtin digest drift' warning, logs:\n%s", out)
	}
	spec, _ := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	for _, want := range []string{driftedDigest, SourceDigest(spec.Files)} {
		if !strings.Contains(out, want) {
			t.Fatalf("drift warning must carry both digests; missing %s in:\n%s", want, out)
		}
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
	// See the note in TestEnsureBuiltinWorkflowWarnsOnDigestDrift: clear the
	// ambient desktop workspace runtime dir so reuse resolves against this cwd.
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

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
	if out := logs.String(); strings.Contains(out, "builtin digest drift") {
		t.Fatalf("matching digest must NOT warn about drift, logs:\n%s", out)
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
