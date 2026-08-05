//go:build loom_packaged_builtins

package authoring

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

var expectedPackagedBuiltinRunners = map[string][]string{
	BuiltinBugFixAgentWorkflowName:       {"daytona-task-runner", "local-task-runner"},
	BuiltinEpicRunnerWorkflowName:        {"daytona-task-runner", "local-task-runner"},
	BuiltinGitHubReviewAgentWorkflowName: {BuiltinGitHubReviewTaskRunnerName},
	BuiltinLocalReviewAgentWorkflowName:  {BuiltinGitHubReviewTaskRunnerName},
	BuiltinPromptAgentWorkflowName:       {"local-task-runner"},
	BuiltinReviewLoopAgentWorkflowName:   {BuiltinGitHubReviewTaskRunnerName},
}

func TestEmbeddedPackagedBuiltinsMatchSource(t *testing.T) {
	for _, name := range BuiltinWorkflowNames() {
		t.Run(name, func(t *testing.T) {
			spec, ok := BuiltinWorkflow(name)
			if !ok {
				t.Fatalf("%s builtin missing", name)
			}
			distPath := filepath.ToSlash(filepath.Join("builtin-dist", name, "dist"))
			matches, err := packagedBuiltinDigestMatches(packagedBuiltinFS, distPath, mustSourceDigest(t, spec.Files))
			if err != nil {
				t.Fatalf("read embedded %s digest: %v", name, err)
			}
			if !matches {
				t.Fatalf("embedded %s bundle is missing or stale; rebuild it before packaging", name)
			}
			assertPackagedBundleHasNoBuildAnnotations(t, distPath)
		})
	}
}

func assertPackagedBundleHasNoBuildAnnotations(t *testing.T, distPath string) {
	t.Helper()
	err := fs.WalkDir(packagedBuiltinFS, distPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (filepath.Ext(path) != ".js" && filepath.Ext(path) != ".mjs" && filepath.Ext(path) != ".cjs") {
			return nil
		}
		content, err := fs.ReadFile(packagedBuiltinFS, path)
		if err != nil {
			return err
		}
		for _, marker := range []string{"//#region", "//#endregion", "sourceMappingURL="} {
			if strings.Contains(string(content), marker) {
				t.Errorf("packaged executable %s retained build annotation %q", path, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect packaged bundle %s: %v", distPath, err)
	}
}

func TestEmbeddedPackagedBuiltinsRegisterWithoutBuildToolchain(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_SDK_ROOT", "")
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	t.Setenv("LOOM_REAL_FLUE_CMD", "")

	st := memstore.New()
	for _, name := range BuiltinWorkflowNames() {
		t.Run(name, func(t *testing.T) {
			driverRecord, err := EnsureAndResolveDriver(t.Context(), st, "PACKAGED-DESKTOP", name)
			if err != nil {
				t.Fatalf("EnsureAndResolveDriver(%s) without build toolchain: %v", name, err)
			}
			if driverRecord == nil || strings.TrimSpace(driverRecord.ActiveVersionID) == "" {
				t.Fatalf("EnsureAndResolveDriver(%s) returned no active version", name)
			}
			version, err := st.DriverVersions().Get(t.Context(), "PACKAGED-DESKTOP", driverRecord.ActiveVersionID)
			if err != nil {
				t.Fatalf("get active %s version: %v", name, err)
			}
			spec, ok := BuiltinWorkflow(name)
			if !ok {
				t.Fatalf("%s builtin missing after registration", name)
			}
			if got, want := version.SourceDigest, mustSourceDigest(t, spec.Files); got != want {
				t.Fatalf("%s source digest = %q, want %q", name, got, want)
			}
			if got := driverpkg.DriverVersionEffectiveTrust(driverRecord, version); got != domain.DriverTrustTrusted {
				t.Fatalf("%s effective trust = %q, want %q", name, got, domain.DriverTrustTrusted)
			}
			var runnerSpecs []driverpkg.DriverRunnerSpec
			if err := json.Unmarshal([]byte(version.Manifest["runners"]), &runnerSpecs); err != nil {
				t.Fatalf("decode %s runner manifest: %v", name, err)
			}
			gotRunners := make([]string, 0, len(runnerSpecs))
			for _, runner := range runnerSpecs {
				gotRunners = append(gotRunners, runner.Name)
			}
			slices.Sort(gotRunners)
			wantRunners, ok := expectedPackagedBuiltinRunners[name]
			if !ok {
				t.Fatalf("missing required-runner expectation for packaged builtin %s", name)
			}
			if !slices.Equal(gotRunners, wantRunners) {
				t.Fatalf("%s runners = %v, want %v", name, gotRunners, wantRunners)
			}
			serverPath := filepath.Join(runtimeDir, filepath.FromSlash(version.BundleRef), "dist", "server.mjs")
			if info, err := os.Stat(serverPath); err != nil || info.IsDir() {
				t.Fatalf("staged %s server bundle is unavailable at %s: %v", name, serverPath, err)
			}
		})
	}
}
