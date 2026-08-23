package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/noderuntime"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
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

// setupReadyzEnv pins every env var readyz reads (the desktop app exports
// LOOM_LOCAL_RUNTIME/LOOM_NODE_BIN/... into interactive shells), points
// LOOM_NODE_BIN at a fake executable and prepends its directory to PATH so
// neither the runtime nor the authoring "node" check depends on the host,
// and clears the baked index digest. Authoring inputs start cleared.
func setupReadyzEnv(t *testing.T) {
	t.Helper()
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	noderuntime.ResetForTest()
	t.Cleanup(noderuntime.ResetForTest)
	fakeNode := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(fakeNode, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // fake executable for tests.
		t.Fatalf("write fake node: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(fakeNode)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for key, value := range map[string]string{
		"LOOM_LOCAL_RUNTIME": "", "LOOM_BUILTIN_ARTIFACTS_DIR": "", "LOOM_NODE_BIN": fakeNode,
		"LOOM_SDK_ROOT": "", "LOOM_REAL_FLUE_CMD": "", "LOOM_REAL_FLUE_CMD_JSON": "",
		"LOOM_FLUE_RUNTIME_ROOT": "", "FLUE_RUNTIME_ROOT": "", "FLUE_REPO": "", "DAYTONA_SDK_ROOT": "",
		driverpkg.SandboxModeEnvVar: "",
	} {
		t.Setenv(key, value)
	}
	origDigest := packaged.ExpectedIndexDigest
	packaged.ExpectedIndexDigest = ""
	t.Cleanup(func() { packaged.ExpectedIndexDigest = origDigest })
}

func runReadyzJSON(t *testing.T) map[string]any {
	t.Helper()
	workflowReadyzJSON = true
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
	return payload
}

// nestedMap walks payload[path[0]][path[1]]... and fails unless every hop is
// a JSON object.
func nestedMap(t *testing.T, payload map[string]any, path ...string) map[string]any {
	t.Helper()
	current := payload
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("readyz payload %v: %v is not an object: %+v", path, key, current[key])
		}
		current = next
	}
	return current
}

func TestWorkflowReadyzJSONReportsLocalRoots(t *testing.T) {
	setupReadyzEnv(t)
	t.Setenv("LOOM_REAL_FLUE_CMD", "/bin/echo")
	t.Setenv("LOOM_SDK_ROOT", packageRoot(t))
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", packageRoot(t))

	payload := runReadyzJSON(t)
	for _, key := range []string{"ok", "authoring_ready", "node", "flue", "loom_sdk", "flue_runtime"} {
		if payload[key] != true {
			t.Fatalf("readyz payload[%s] = %v, want true; payload=%+v", key, payload[key], payload)
		}
	}
	if payload["sandbox_mode"] != driverpkg.SandboxProviderProcess || payload["untrusted_execution_possible"] != false {
		t.Fatalf("readyz payload = %+v, want required checks true", payload)
	}
	if node := nestedMap(t, payload, "builtin_runtime", "node"); node["source"] != noderuntime.SourceOverride || node["ok"] != true {
		t.Fatalf("builtin_runtime.node = %+v, want ok via LOOM_NODE_BIN override", node)
	}
	artifacts := nestedMap(t, payload, "builtin_runtime", "artifacts")
	if epic := nestedMap(t, artifacts, workflows.BuiltinEpicRunnerWorkflowName); epic["required"] != true {
		t.Fatalf("artifacts.epic-runner = %+v, want required=true", epic)
	}
	if review := nestedMap(t, artifacts, workflows.BuiltinGitHubReviewAgentWorkflowName); review["required"] != true {
		t.Fatalf("artifacts.github-review-agent = %+v, want required=true (ships in the desktop app)", review)
	}
}

// writePackagedBuiltinTree builds a verifiable builtin-workflows root holding
// the named built-ins (each with its own stub server.mjs + nested @loom/sdk
// and the embedded spec's source digest/runner set) and returns the root
// plus its index digest.
func writePackagedBuiltinTree(t *testing.T, names ...string) (root, indexDigest string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "builtin-workflows")
	idx := packaged.Index{
		SchemaVersion: packaged.SchemaVersion,
		FlueCommit:    workflows.PinnedFlueCommit,
		NodeVersion:   workflows.PinnedNodeVersion,
		Target:        packaged.HostTargetTriple(),
		Builtins:      map[string]packaged.Entry{},
	}
	for _, name := range names {
		dist := filepath.Join(root, name, "dist")
		writeTestFile(t, filepath.Join(dist, "server.mjs"), "// "+name+"\nexport {};\n")
		for _, rel := range packaged.LoomSDKRuntimeFiles {
			content := "export {};\n"
			if rel == "package.json" {
				content = `{"name":"@loom/sdk"}` + "\n"
			}
			writeTestFile(t, filepath.Join(dist, "node_modules", "@loom", "sdk", rel), content)
		}
		artifactDigest, err := driverpkg.DigestDirectory(dist)
		if err != nil {
			t.Fatalf("digest dist: %v", err)
		}
		spec, ok := workflows.BuiltinWorkflow(name)
		if !ok {
			t.Fatalf("no built-in %q", name)
		}
		sourceDigest, runners, _ := workflows.BuiltinArtifactExpectation(name)
		idx.Builtins[name] = packaged.Entry{
			Path:           name,
			Entrypoint:     spec.Entrypoint,
			SourceDigest:   sourceDigest,
			ArtifactDigest: artifactDigest,
			Runners:        runners,
		}
	}
	encoded, err := packaged.EncodeIndex(idx)
	if err != nil {
		t.Fatalf("encode index: %v", err)
	}
	writeTestFile(t, filepath.Join(root, packaged.IndexFileName), string(encoded))
	return root, packaged.IndexDigest(encoded)
}

// writePackagedEpicRunnerTree builds a verifiable builtin-workflows root for
// epic-runner only (the Slice 1 shape) and returns the root plus its index
// digest.
func writePackagedEpicRunnerTree(t *testing.T) (root, indexDigest string) {
	t.Helper()
	return writePackagedBuiltinTree(t, workflows.BuiltinEpicRunnerWorkflowName)
}

func TestWorkflowReadyzBuiltinRuntimeReadyWithoutAuthoring(t *testing.T) {
	setupReadyzEnv(t)
	root, indexDigest := writePackagedBuiltinTree(t, packaged.RequiredBuiltins...)
	packaged.ExpectedIndexDigest = indexDigest
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", root)
	t.Chdir(t.TempDir())

	payload := runReadyzJSON(t)
	if payload["builtin_runtime_ready"] != true || payload["authoring_ready"] != false || payload["ok"] != false {
		t.Fatalf("readyz payload = %+v, want builtin_runtime_ready only", payload)
	}
	runtime := nestedMap(t, payload, "builtin_runtime")
	if runtime["packaged_build"] != true || runtime["root"] != root || runtime["index_digest"] != indexDigest {
		t.Fatalf("builtin_runtime = %+v, want packaged build at %s", runtime, root)
	}
	if node := nestedMap(t, runtime, "node"); node["source"] != noderuntime.SourceOverride {
		t.Fatalf("builtin_runtime.node = %+v, want override source", node)
	}
	artifacts := nestedMap(t, runtime, "artifacts")
	if epic := nestedMap(t, artifacts, workflows.BuiltinEpicRunnerWorkflowName); epic["verified"] != true || epic["error"] != "" {
		t.Fatalf("artifacts.epic-runner = %+v, want verified", epic)
	}
	if review := nestedMap(t, artifacts, workflows.BuiltinGitHubReviewAgentWorkflowName); review["required"] != true || review["verified"] != true || review["error"] != "" {
		t.Fatalf("artifacts.github-review-agent = %+v, want required and verified", review)
	}
	required, _ := runtime["required"].([]any)
	if len(required) != 2 || required[0] != workflows.BuiltinEpicRunnerWorkflowName || required[1] != workflows.BuiltinGitHubReviewAgentWorkflowName {
		t.Fatalf("builtin_runtime.required = %v, want [epic-runner github-review-agent]", runtime["required"])
	}
}

// TestWorkflowReadyzOnlyEpicRunnerPackagedIsNotReady (R1 after widening,
// DEV-V5-37): a packaged build that ships only epic-runner is NOT ready —
// github-review-agent is required and reports builtin_artifact_missing.
func TestWorkflowReadyzOnlyEpicRunnerPackagedIsNotReady(t *testing.T) {
	setupReadyzEnv(t)
	root, indexDigest := writePackagedEpicRunnerTree(t)
	packaged.ExpectedIndexDigest = indexDigest
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", root)
	t.Chdir(t.TempDir())

	payload := runReadyzJSON(t)
	if payload["builtin_runtime_ready"] != false {
		t.Fatalf("readyz payload = %+v, want builtin_runtime_ready=false with github-review-agent missing", payload)
	}
	artifacts := nestedMap(t, payload, "builtin_runtime", "artifacts")
	if epic := nestedMap(t, artifacts, workflows.BuiltinEpicRunnerWorkflowName); epic["verified"] != true {
		t.Fatalf("artifacts.epic-runner = %+v, want verified (per-name verification is independent)", epic)
	}
	review := nestedMap(t, artifacts, workflows.BuiltinGitHubReviewAgentWorkflowName)
	errText, _ := review["error"].(string)
	if review["required"] != true || review["verified"] != false || !strings.Contains(errText, "builtin_artifact_missing") {
		t.Fatalf("artifacts.github-review-agent = %+v, want required, unverified, builtin_artifact_missing", review)
	}
}

func TestWorkflowReadyzDesktopWithoutArtifactsIsNotReady(t *testing.T) {
	setupReadyzEnv(t)
	t.Setenv("LOOM_LOCAL_RUNTIME", "desktop")
	t.Chdir(t.TempDir())

	payload := runReadyzJSON(t)
	if payload["builtin_runtime_ready"] != false {
		t.Fatalf("readyz payload = %+v, want builtin_runtime_ready=false on desktop without artifacts", payload)
	}
	runtime := nestedMap(t, payload, "builtin_runtime")
	if runtime["desktop"] != true || runtime["fail_closed"] != true || runtime["packaged_build"] != false {
		t.Fatalf("builtin_runtime = %+v, want desktop fail-closed without a packaged build", runtime)
	}
	epic := nestedMap(t, runtime, "artifacts", workflows.BuiltinEpicRunnerWorkflowName)
	errText, _ := epic["error"].(string)
	if epic["verified"] != false || !strings.Contains(errText, "builtin_artifact_missing") || !strings.Contains(errText, packaged.FailClosedGuidance) {
		t.Fatalf("artifacts.epic-runner = %+v, want builtin_artifact_missing with fail-closed guidance", epic)
	}
}

func TestWorkflowReadyzTextPrintsNestedKeys(t *testing.T) {
	setupReadyzEnv(t)
	workflowReadyzJSON = false

	stdout, err := captureWorkflowStdout(t, func() error {
		return runWorkflowReadyz(&cobra.Command{}, nil)
	})
	if err != nil {
		t.Fatalf("runWorkflowReadyz: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if !sort.StringsAreSorted(lines) {
		t.Fatalf("readyz text lines are not sorted:\n%s", stdout)
	}
	index := func(prefix string) int {
		for i, line := range lines {
			if strings.HasPrefix(line, prefix) {
				return i
			}
		}
		t.Fatalf("no line starting with %q in:\n%s", prefix, stdout)
		return -1
	}
	if got := lines[index("builtin_runtime.node.source=")]; got != "builtin_runtime.node.source="+noderuntime.SourceOverride {
		t.Fatalf("node source line = %q, want override", got)
	}
	if index("authoring.daytona_sdk=") >= index("authoring.flue=") {
		t.Fatalf("authoring.daytona_sdk should sort before authoring.flue:\n%s", stdout)
	}
	for _, prefix := range []string{
		"builtin_runtime.artifacts.epic-runner.required=true",
		"builtin_runtime.artifacts.github-review-agent.required=true",
		"builtin_runtime_ready=", "ok=",
	} {
		index(prefix)
	}
	if got := lines[index("builtin_runtime.required=")]; got != "builtin_runtime.required=[epic-runner github-review-agent]" {
		t.Fatalf("required line = %q, want both names", got)
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
	workflowPackageDist = ""
	workflowPackageOut = ""
	workflowPackageLoomSDK = ""
	workflowPackageFlueCommit = ""
	workflowPackageNodeVersion = ""
	workflowPackageTarget = ""
	workflowPackageAllowDrift = false
	workflowPackageRequireAll = false
	workflowPackageJSON = false
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
