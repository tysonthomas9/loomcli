package workflows

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// activeManifestRunnersAreStale forces a refresh when the active manifest
// declares a deprecated runner (openshell) or a runner the freshly-derived set
// no longer contains (§4.6).
func TestActiveManifestRunnersAreStale(t *testing.T) {
	fresh := map[string]struct{}{
		"local-task-runner":   {},
		"daytona-task-runner": {},
	}
	cases := []struct {
		name      string
		manifest  map[string]string
		fresh     map[string]struct{}
		wantStale bool
	}{
		{
			name:      "missing runners is stale when builtin derives runners",
			manifest:  map[string]string{},
			wantStale: true,
		},
		{
			name:      "missing runners is not stale when builtin has no runners",
			manifest:  map[string]string{},
			wantStale: false,
			fresh:     map[string]struct{}{},
		},
		{
			name:      "current runners are not stale",
			manifest:  map[string]string{"runners": `[{"name":"local-task-runner"},{"name":"daytona-task-runner"}]`},
			wantStale: false,
		},
		{
			name:      "deprecated openshell is stale",
			manifest:  map[string]string{"runners": `[{"name":"local-task-runner"},{"name":"openshell-task-runner"}]`},
			wantStale: true,
		},
		{
			name:      "runner not in fresh set is stale",
			manifest:  map[string]string{"runners": `[{"name":"local-task-runner"},{"name":"gone-task-runner"}]`},
			wantStale: true,
		},
		{
			name:      "undecodable runner list is stale",
			manifest:  map[string]string{"runners": `not-json`},
			wantStale: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			freshSet := fresh
			if tc.fresh != nil {
				freshSet = tc.fresh
			}
			if got := activeManifestRunnersAreStale(tc.manifest, freshSet); got != tc.wantStale {
				t.Fatalf("activeManifestRunnersAreStale = %v, want %v", got, tc.wantStale)
			}
		})
	}
}

// workflowRunnerNameSet derives the same deny-listed set workflowRunnerSpecs
// produces for the epic-runner builtin (no openshell).
func TestWorkflowRunnerNameSetExcludesOpenShell(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	set := workflowRunnerNameSet(spec)
	if _, ok := set["openshell-task-runner"]; ok {
		t.Fatalf("derived runner set must not include openshell-task-runner: %+v", set)
	}
	for _, want := range []string{"local-task-runner", "daytona-task-runner"} {
		if _, ok := set[want]; !ok {
			t.Fatalf("derived runner set missing %q: %+v", want, set)
		}
	}
}

// EnsureBuiltinWorkflow re-registers an active version whose manifest still
// declares the deprecated openshell runner, even when its source digest would
// otherwise look current (§4.6 refresh-on-deprecated).
func TestEnsureBuiltinWorkflowRefreshesDeprecatedRunnerManifest(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	name := BuiltinGitHubReviewAgentWorkflowName
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		t.Fatal("github-review-agent builtin missing")
	}
	digest := SourceDigest(spec.Files)

	workDir := t.TempDir()
	t.Chdir(workDir)
	installFakeWorkflowBuildDeps(t)

	// Register an active version at the CURRENT source digest but with a
	// manifest that still declares the deprecated openshell runner.
	staleDist := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(staleDist, 0o755); err != nil {
		t.Fatalf("create stale dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write stale server: %v", err)
	}
	deprecated, err := driverpkg.RegisterFlueDriver(ctx, st, driverpkg.RegisterFlueOptions{
		WorkspaceKey: "BUILTIN",
		WorkDir:      workDir,
		DistPath:     staleDist,
		DriverName:   name,
		DriverID:     name,
		WorkflowName: name,
		SourceRef:    "builtin://workflows/github-review-agent/versions/" + digest,
		SourceDigest: digest,
		CreatedBy:    "system",
		Activate:     true,
		RunnerSpecs: []driverpkg.DriverRunnerSpec{
			{Name: "github-review-task-runner", Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: "github-review-task-runner"},
			{Name: "openshell-task-runner", Kind: driverpkg.RunnerKindFlueWorkflow, Entrypoint: "openshell-task-runner"},
		},
		Trust: domain.DriverTrustTrusted,
	})
	if err != nil {
		t.Fatalf("register deprecated driver: %v", err)
	}

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", name); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow returned error: %v", err)
	}
	driver, err := st.Drivers().Get(ctx, "BUILTIN", name)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID == deprecated.Version.VersionID {
		t.Fatalf("active version stayed on deprecated-runner manifest %q", driver.ActiveVersionID)
	}
	version, err := st.DriverVersions().Get(ctx, "BUILTIN", driver.ActiveVersionID)
	if err != nil {
		t.Fatalf("get active version: %v", err)
	}
	if got := version.Manifest["runners"]; got == "" {
		t.Fatalf("refreshed manifest runners empty, want derived runner list")
	}
	var runners []driverpkg.DriverRunnerSpec
	if err := json.Unmarshal([]byte(version.Manifest["runners"]), &runners); err != nil {
		t.Fatalf("decode refreshed runners: %v", err)
	}
	for _, runner := range runners {
		if runner.Name == "openshell-task-runner" {
			t.Fatalf("refreshed manifest still declares openshell-task-runner: %+v", runners)
		}
	}

	// DEV-V5-33: the legacy compile lane records {system, registration, auto} so
	// a later packaged build reads the auto track.
	wantMeta := map[string]string{
		driverpkg.MetadataKeyActivationActor:  "system",
		driverpkg.MetadataKeyActivationReason: "registration",
		driverpkg.MetadataKeyBuiltinTrack:     "auto",
	}
	for key, value := range wantMeta {
		if got := driver.Metadata[key]; got != value {
			t.Errorf("compile-path driver metadata[%s] = %q, want %q", key, got, value)
		}
	}
}

func installFakeWorkflowBuildDeps(t *testing.T) {
	t.Helper()
	// The compile fallback is only reachable off the fail-closed path
	// (DEV-V5-31): clear the desktop marker a desktop-spawned shell inherits.
	t.Setenv("LOOM_LOCAL_RUNTIME", "")
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", "")
	// Also clear the desktop workspace runtime dir: builtinWorkflowWorkDir honors
	// it before cwd, so an ambient value (this suite may run inside a Loom
	// workspace) would send builds/bundle lookups to the real data dir instead of
	// the test's t.Chdir workdir.
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		t.Fatalf("create fake sdk root: %v", err)
	}
	// Include the runtime files stageLoomSDKRuntime copies into the built dist.
	writeStubLoomSDKRuntime(t, sdkRoot)

	runtimeRoot := filepath.Join(root, "runtime")
	for _, dir := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create fake runtime dependency %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake runtime package: %v", err)
	}

	flue := filepath.Join(root, "fake-flue.sh")
	script := `#!/bin/sh
set -eu
out=""
prev=""
for arg in "$@"; do
	if [ "$prev" = "--output" ]; then
		out="$arg"
		break
	fi
	prev="$arg"
done
if [ -z "$out" ]; then
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
	t.Setenv("FLUE_RUNTIME_ROOT", runtimeRoot)
}
