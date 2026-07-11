package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

func TestWorkflowCloneJSONWritesSourceLayout(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), ".loom", "workflows", workflows.BuiltinEpicRunnerWorkflowName)
	resetWorkflowCloneFlags := func() {
		workflowCloneOut = ""
		workflowCloneJSON = false
	}
	t.Cleanup(resetWorkflowCloneFlags)
	workflowCloneOut = outDir
	workflowCloneJSON = true

	stdout, err := captureWorkflowStdout(t, func() error {
		return runWorkflowClone(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowClone: %v", err)
	}
	var payload struct {
		Workflow string                   `json:"workflow"`
		Out      string                   `json:"out"`
		Manifest workflows.SourceManifest `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode clone JSON %q: %v", stdout, err)
	}
	if payload.Workflow != workflows.BuiltinEpicRunnerWorkflowName || payload.Manifest.DriverID != workflows.BuiltinEpicRunnerWorkflowName {
		t.Fatalf("payload = %+v, want epic-runner source manifest", payload)
	}
	for _, rel := range []string{"workflow.json", "workflows/epic-runner.ts"} {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected cloned file %s: %v", rel, err)
		}
	}
}

func TestWorkflowCloneUnknownWorkflowReturnsError(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	workflowCloneOut = filepath.Join(t.TempDir(), ".loom", "workflows", "missing-workflow")
	workflowCloneJSON = true

	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowClone(&cobra.Command{}, []string{"missing-workflow"})
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("runWorkflowClone err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowReadyzJSONReportsLocalRoots(t *testing.T) {
	sdkRoot := packageRoot(t)
	flueRuntimeRoot := packageRoot(t)
	t.Setenv("LOOM_REAL_FLUE_CMD", "/bin/echo")
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", flueRuntimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	workflowReadyzJSON = true
	t.Cleanup(func() { workflowReadyzJSON = false })

	stdout, err := captureWorkflowStdout(t, func() error {
		return runWorkflowReadyz(&cobra.Command{}, nil)
	})
	if err != nil {
		t.Fatalf("runWorkflowReadyz: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode readyz JSON %q: %v", stdout, err)
	}
	for _, key := range []string{"ok", "node", "flue", "loom_sdk", "flue_runtime"} {
		if payload[key] != true {
			t.Fatalf("readyz payload[%s] = %v, want true; payload=%+v", key, payload[key], payload)
		}
	}
	if payload["sandbox_mode"] != driverpkg.SandboxProviderProcess || payload["untrusted_execution_possible"] != false {
		t.Fatalf("readyz payload = %+v, want required checks true", payload)
	}
}

func TestWorkflowStoreCommandsJSON(t *testing.T) {
	ctx, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)

	workflowVersionID = "version-1"
	workflowApproveJSON = true
	approvedJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowApprove(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowApprove: %v", err)
	}
	approved := decodeWorkflowVersionOutput(t, approvedJSON)
	if !approved.Approved || approved.EffectiveTrust != domain.DriverTrustTrusted || approved.Version.VersionID != "version-1" {
		t.Fatalf("approved output = %+v, want trusted approved version-1", approved)
	}

	unapprovedJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowUnapprove(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowUnapprove: %v", err)
	}
	unapproved := decodeWorkflowVersionOutput(t, unapprovedJSON)
	if unapproved.Approved || unapproved.EffectiveTrust != domain.DriverTrustUntrusted {
		t.Fatalf("unapproved output = %+v, want untrusted unapproved version", unapproved)
	}

	workflowVersionID = "version-2"
	workflowActivateJSON = true
	activatedJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowActivate(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowActivate: %v", err)
	}
	activated := decodeWorkflowVersionOutput(t, activatedJSON)
	if !activated.Active || activated.Version.VersionID != "version-2" {
		t.Fatalf("activated output = %+v, want active version-2", activated)
	}

	workflowRunVersion = "version-1"
	workflowRunEpic = "EPIC-1"
	workflowRunInput = []string{"requestedBy=cli-test", "runner=daytona-task-runner"}
	workflowRunJSON = true
	runJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowRun(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowRun: %v", err)
	}
	var run domain.DriverRun
	if err := json.Unmarshal([]byte(runJSON), &run); err != nil {
		t.Fatalf("decode run JSON %q: %v", runJSON, err)
	}
	if run.DriverVersionID != "version-1" || run.EpicID != "EPIC-1" || run.Status != domain.DriverRunQueued {
		t.Fatalf("run = %+v, want queued preview run on version-1", run)
	}
	var runPayload map[string]string
	if err := json.Unmarshal(run.Payload, &runPayload); err != nil {
		t.Fatalf("decode run payload %s: %v", run.Payload, err)
	}
	if runPayload["requestedBy"] != "cli-test" || runPayload["epicId"] != "EPIC-1" || runPayload["runner"] != "daytona-task-runner" {
		t.Fatalf("run payload = %+v, want epic/input fields", runPayload)
	}

	workflowListJSON = true
	listJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowList(&cobra.Command{}, nil)
	})
	if err != nil {
		t.Fatalf("runWorkflowList: %v", err)
	}
	var listed struct {
		Workflows []map[string]any `json:"workflows"`
	}
	if err := json.Unmarshal([]byte(listJSON), &listed); err != nil {
		t.Fatalf("decode list JSON %q: %v", listJSON, err)
	}
	if len(listed.Workflows) != 1 || listed.Workflows[0]["active_version_id"] != "version-2" {
		t.Fatalf("listed workflows = %+v, want active version-2", listed.Workflows)
	}

	workflowVersionsJSON = true
	versionsJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowVersions(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowVersions: %v", err)
	}
	var versions struct {
		DriverID string                  `json:"driver_id"`
		Versions []workflowVersionOutput `json:"versions"`
	}
	if err := json.Unmarshal([]byte(versionsJSON), &versions); err != nil {
		t.Fatalf("decode versions JSON %q: %v", versionsJSON, err)
	}
	if versions.DriverID != workflows.BuiltinEpicRunnerWorkflowName || len(versions.Versions) != 2 {
		t.Fatalf("versions = %+v, want two epic-runner versions", versions)
	}
	activeCount := 0
	for _, item := range versions.Versions {
		if item.Active {
			activeCount++
			if item.Version.VersionID != "version-2" {
				t.Fatalf("active version = %s, want version-2", item.Version.VersionID)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("active version count = %d, want 1", activeCount)
	}

	if _, err := st.DriverRuns().Get(ctx, "TEST", run.RunID); err != nil {
		t.Fatalf("preview run was not persisted: %v", err)
	}
}

func TestWorkflowApproveUnknownVersionReturnsError(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	workflowVersionID = "missing-version"
	workflowApproveJSON = true

	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowApprove(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("runWorkflowApprove err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowRunInvalidInputReturnsErrorWithoutCreatingRun(t *testing.T) {
	ctx, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	workflowRunInput = []string{"missing-equals"}
	workflowRunJSON = true

	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowRun(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err == nil || !strings.Contains(err.Error(), "input must be key=value") {
		t.Fatalf("runWorkflowRun err = %v, want key=value validation error", err)
	}
	runs, listErr := st.DriverRuns().List(ctx, "TEST", store.DriverRunFilter{})
	if listErr != nil {
		t.Fatalf("list runs: %v", listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %+v, want no persisted run after invalid input", runs)
	}
}

func TestWorkflowRunLocalRunnerPreflightFailureDoesNotCreateRun(t *testing.T) {
	ctx, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	restore := runtimepreflight.SetHealthCheckerForTest(func(name string) (backends.HealthStatus, bool) {
		return backends.HealthStatus{Installed: false, APIKeySet: false, Healthy: false, Message: "missing test backend"}, true
	})
	t.Cleanup(restore)
	workflowRunEpic = "EPIC-1"
	workflowRunJSON = true

	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowRun(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err == nil || !strings.Contains(err.Error(), "local_backend_unavailable") {
		t.Fatalf("runWorkflowRun err = %v, want local backend preflight failure", err)
	}
	runs, listErr := st.DriverRuns().List(ctx, "TEST", store.DriverRunFilter{})
	if listErr != nil {
		t.Fatalf("list runs: %v", listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %+v, want no persisted run after preflight failure", runs)
	}
}

func TestWorkflowVersionsUnknownWorkflowReturnsError(t *testing.T) {
	_, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	workflowVersionsJSON = true
	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowVersions(&cobra.Command{}, []string{"missing-workflow"})
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("runWorkflowVersions err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowBuildJSONFailureDoesNotCreateVersion(t *testing.T) {
	ctx, st := setupWorkflowCommandStore(t)
	withWorkflowCommandStore(t, st)
	sourceDir := writeWorkflowSourceLayout(t, "custom-flow")
	workflowBuildSource = sourceDir
	workflowBuildJSON = true
	origBuild := workflowBuildAndRegister
	workflowBuildAndRegister = func(context.Context, store.Store, workflows.BuildAndRegisterOptions) (*driverpkg.RegisterFlueResult, string, error) {
		return nil, "redacted diagnostics", errors.New("flue build failed")
	}
	t.Cleanup(func() { workflowBuildAndRegister = origBuild })

	stdout, err := captureWorkflowStdout(t, func() error {
		return runWorkflowBuild(&cobra.Command{}, []string{"custom-flow"})
	})
	if err == nil || !strings.Contains(err.Error(), "flue build failed") {
		t.Fatalf("runWorkflowBuild err = %v, want flue build failure", err)
	}
	var payload workflowBuildOutput
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode build failure JSON %q: %v", stdout, err)
	}
	if payload.OK || payload.Status != "failed" || payload.Version != nil || payload.Diagnostics != "redacted diagnostics" ||
		payload.ErrorClass != "flue_build_failed" || payload.SourceDigest == "" || !strings.Contains(payload.Error, "flue build failed") {
		t.Fatalf("build failure payload = %+v, want failed JSON diagnostics without version", payload)
	}
	versions, err := st.DriverVersions().List(ctx, "TEST", store.DriverVersionFilter{DriverID: "custom-flow"})
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("versions = %+v, want no custom-flow version after failed build", versions)
	}
}

func packageRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"test"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	return root
}

func setupWorkflowCommandStore(t *testing.T) (context.Context, store.Store) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey:    "TEST",
		DriverID:        workflows.BuiltinEpicRunnerWorkflowName,
		Name:            workflows.BuiltinEpicRunnerWorkflowName,
		Status:          domain.DriverStatusActive,
		ActiveVersionID: "version-1",
		TrustLevel:      domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	for _, in := range []store.DriverVersionCreate{
		{
			WorkspaceKey:     "TEST",
			VersionID:        "version-1",
			DriverID:         workflows.BuiltinEpicRunnerWorkflowName,
			Version:          1,
			SourceDigest:     "sha256:source-1",
			BundleDigest:     "sha256:bundle-1",
			Manifest:         map[string]string{driverpkg.ManifestTrustLevelKey: string(domain.DriverTrustUntrusted)},
			ValidationStatus: domain.DriverVersionValidationPassed,
			CreatedBy:        "tester",
		},
		{
			WorkspaceKey:     "TEST",
			VersionID:        "version-2",
			DriverID:         workflows.BuiltinEpicRunnerWorkflowName,
			Version:          2,
			SourceDigest:     "sha256:source-2",
			BundleDigest:     "sha256:bundle-2",
			Manifest:         map[string]string{driverpkg.ManifestTrustLevelKey: string(domain.DriverTrustUntrusted)},
			ValidationStatus: domain.DriverVersionValidationPassed,
			CreatedBy:        "tester",
		},
	} {
		if _, err := st.DriverVersions().Create(ctx, in); err != nil {
			t.Fatalf("Create version %s: %v", in.VersionID, err)
		}
	}
	return ctx, st
}

func withWorkflowCommandStore(t *testing.T, st store.Store) {
	t.Helper()
	resetWorkflowCommandGlobals()
	origWith := workflowWithActiveWorkspace
	workflowWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, "TEST")
	}
	t.Cleanup(func() {
		workflowWithActiveWorkspace = origWith
		resetWorkflowCommandGlobals()
	})
}

func resetWorkflowCommandGlobals() {
	workflowCloneOut = ""
	workflowCloneJSON = false
	workflowBuildSource = ""
	workflowBuildJSON = false
	workflowVersionID = ""
	workflowApproveJSON = false
	workflowActivateJSON = false
	workflowRunVersion = ""
	workflowRunEpic = ""
	workflowRunInput = nil
	workflowRunJSON = false
	workflowListJSON = false
	workflowVersionsJSON = false
	workflowReadyzJSON = false
}

func decodeWorkflowVersionOutput(t *testing.T, raw string) workflowVersionOutput {
	t.Helper()
	var out workflowVersionOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode workflow version JSON %q: %v", raw, err)
	}
	return out
}

func writeWorkflowSourceLayout(t *testing.T, driverID string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".loom", "workflows", driverID)
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("mkdir source layout: %v", err)
	}
	manifest := workflows.SourceManifest{
		SchemaVersion: workflows.WorkflowSourceSchemaVersion,
		DriverID:      driverID,
		Entrypoint:    "workflows/" + driverID + ".ts",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal source manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.json"), data, 0o644); err != nil {
		t.Fatalf("write workflow manifest: %v", err)
	}
	source := "export async function run() { return { status: \"completed\" }; }\n"
	if err := os.WriteFile(filepath.Join(root, "workflows", driverID+".ts"), []byte(source), 0o644); err != nil {
		t.Fatalf("write workflow source: %v", err)
	}
	return root
}

func captureWorkflowStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = orig
	data, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil && runErr == nil {
		runErr = readErr
	}
	return string(data), runErr
}
