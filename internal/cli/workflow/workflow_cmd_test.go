package workflow

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/spf13/cobra"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	workflows "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution/authoring"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
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
	if !errors.Is(err, persistence.ErrNotFound) {
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

func TestWorkflowManagementAndStoreLaneCommandsJSON(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	setupWorkflowManagementFixture(t)

	workflowVersionID = "version-1"
	workflowApproveJSON = true
	approvedJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowApprove(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowApprove: %v", err)
	}
	approved := decodeWorkflowVersionOutput(t, approvedJSON)
	if !approved.Approved || approved.EffectiveTrust != workflowcatalog.DriverTrustTrusted || approved.Version.VersionID != "version-1" {
		t.Fatalf("approved output = %+v, want trusted approved version-1", approved)
	}

	unapprovedJSON, err := captureWorkflowStdout(t, func() error {
		return runWorkflowUnapprove(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowUnapprove: %v", err)
	}
	unapproved := decodeWorkflowVersionOutput(t, unapprovedJSON)
	if unapproved.Approved || unapproved.EffectiveTrust != workflowcatalog.DriverTrustUntrusted {
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
	var run execution.DriverRunRecord
	if err := json.Unmarshal([]byte(runJSON), &run); err != nil {
		t.Fatalf("decode run JSON %q: %v", runJSON, err)
	}
	if run.DriverVersionID != "version-1" || run.EpicID != "EPIC-1" || run.Status != execution.DriverRunQueued {
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

}

func TestWorkflowApproveUnknownVersionReturnsError(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	setupWorkflowManagementFixture(t)
	workflowVersionID = "missing-version"
	workflowApproveJSON = true

	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowApprove(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("runWorkflowApprove err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowRunInvalidInputReturnsErrorWithoutCreatingRun(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	fixture := setupWorkflowManagementFixture(t)
	workflowRunInput = []string{"missing-equals"}
	workflowRunJSON = true

	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowRun(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	})
	if err == nil || !strings.Contains(err.Error(), "input must be key=value") {
		t.Fatalf("runWorkflowRun err = %v, want key=value validation error", err)
	}
	if got := fixture.paths(); len(got) != 0 {
		t.Fatalf("management requests = %v, want none after invalid input", got)
	}
}

func TestWorkflowRunUsesManagementAPIWithoutOpeningStore(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	fixture := setupWorkflowManagementFixture(t)
	workflowRunEpic = "EPIC-1"
	workflowRunJSON = true

	if _, err := captureWorkflowStdout(t, func() error {
		return runWorkflowRun(&cobra.Command{}, []string{workflows.BuiltinEpicRunnerWorkflowName})
	}); err != nil {
		t.Fatalf("runWorkflowRun: %v", err)
	}
	if paths := strings.Join(fixture.paths(), "\n"); !strings.Contains(paths, "POST /api/workspaces/TEST/execution/driver-runs") {
		t.Fatalf("management paths =\n%s", paths)
	}
}

func TestWorkflowVersionsUnknownWorkflowReturnsError(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	setupWorkflowManagementFixture(t)
	workflowVersionsJSON = true
	_, err := captureWorkflowStdout(t, func() error {
		return runWorkflowVersions(&cobra.Command{}, []string{"missing-workflow"})
	})
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("runWorkflowVersions err = %v, want ErrNotFound", err)
	}
}

func TestWorkflowBuildTextHandoffNamesManagementServerAndWorkspace(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	fixture := setupWorkflowManagementFixture(t)
	workflowBuildSource = writeWorkflowSourceLayout(t, "custom-flow")
	const versionID = "custom-flow-v-test"

	stdout, err := captureWorkflowStdout(t, func() error {
		return runWorkflowBuild(&cobra.Command{}, []string{"custom-flow"})
	})
	if err != nil {
		t.Fatalf("runWorkflowBuild: %v", err)
	}
	if fixture.authoringRequest == nil ||
		fixture.authoringRequest.Entrypoint != "workflows/custom-flow.ts" ||
		fixture.authoringRequest.Files["workflows/custom-flow.ts"] == "" {
		t.Fatalf("authoring request = %+v", fixture.authoringRequest)
	}
	if fixture.authoringRequestID == "" {
		t.Fatal("authoring request omitted Idempotency-Key")
	}
	if paths := strings.Join(fixture.paths(), "\n"); !strings.Contains(paths, "POST /api/workspaces/TEST/workflows/custom-flow/versions") {
		t.Fatalf("management paths =\n%s", paths)
	}
	for _, want := range []string{
		"Target workspace: TEST",
		"loom --server \"$LOOM_SERVER_URL\" --workspace TEST workflow approve custom-flow --version " + versionID,
		"loom --server \"$LOOM_SERVER_URL\" --workspace TEST workflow activate custom-flow --version " + versionID,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("build output = %q, want %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "loom workflow approve") {
		t.Fatalf("build output retained endpoint-free legacy handoff: %q", stdout)
	}
}

func TestWorkflowBuildJSONFailureReturnsServerErrorWithoutFallback(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	fixture := setupWorkflowManagementFixture(t)
	fixture.authoringFailureStatus = http.StatusBadRequest
	sourceDir := writeWorkflowSourceLayout(t, "custom-flow")
	workflowBuildSource = sourceDir
	workflowBuildJSON = true

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
	if payload.OK || payload.Status != "failed" || payload.Version != nil || payload.Diagnostics != "" ||
		payload.ErrorClass != "workflow_authoring_failed" || payload.SourceDigest == "" || !strings.Contains(payload.Error, "flue build failed") {
		t.Fatalf("build failure payload = %+v, want failed JSON diagnostics without version", payload)
	}
	if paths := fixture.paths(); len(paths) != 1 || paths[0] != "POST /api/workspaces/TEST/workflows/custom-flow/versions" {
		t.Fatalf("management paths = %v, want one authoring request and no fallback", paths)
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
