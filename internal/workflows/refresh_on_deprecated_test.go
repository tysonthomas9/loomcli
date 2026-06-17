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
		wantStale bool
	}{
		{
			name:      "no runners is not stale",
			manifest:  map[string]string{},
			wantStale: false,
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
			if got := activeManifestRunnersAreStale(tc.manifest, fresh); got != tc.wantStale {
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
	t.Setenv("LOOM_REAL_FLUE_CMD", filepath.Join(t.TempDir(), "missing-flue"))
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")

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
}
