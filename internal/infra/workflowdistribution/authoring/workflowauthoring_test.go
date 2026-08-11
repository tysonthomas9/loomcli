package authoring

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	workflowdistribution "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution"
)

func TestBuiltinEpicRunnerWorkflowSourceIncludesReconcilePrimitives(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, want := range []string{
		"startEpicRun",
		"loom.epics.get",
		"loom.agents.list",
		"loom.agents.orchestrationSession",
		"loom.agents.updateParent",
		"loom.agents.deliverAssignment",
		"loom.epics.watch",
		"dryRun",
		"targetNodeId",
		"loom.epics.snapshot",
		"loom.taskRuns.active",
		"loom.tasks.claimReady",
		"deterministicTaskRunId",
		"epic_blocked",
		"workerProfileId",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("built-in epic-runner source missing %q", want)
		}
	}
}

// The bundled task runner sources must ship (the embed list is unchanged), but
// they must NEVER carry the old fake "Completed by the built-in ..." synthetic
// completion path — local now runs the real backend CLI and openshell is a
// fail-closed stub.
func TestBuiltinTaskRunnerSourcesDropFakeCompletion(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	for _, path := range []string{
		"workflows/local-task-runner.ts",
		"workflows/daytona-task-runner.ts",
		"workflows/openshell-task-runner.ts",
	} {
		source := spec.Files[path]
		if source == "" {
			t.Fatalf("built-in task runner source missing %s", path)
		}
		if strings.Contains(source, "Completed by the built-in") {
			t.Fatalf("%s still contains the fake synthetic completion string", path)
		}
	}
}

// The main loop is edge-triggered off the epic watch stream: no polling
// cadence, no per-batch barrier, and no workflow-side awaiting of queued
// task runs (terminal journal events drive the bookkeeping instead).
func TestBuiltinEpicRunnerWorkflowIsWatchDriven(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, want := range []string{
		"for await (const event of loom.epics.watch({ epicId }))",
		"const inFlight = new Map();",
		"reconcileInFlight(",
		`case "taskRunCompleted":`,
		`case "taskRunFailed":`,
		`case "taskRunCancelled":`,
		"completed.push(taskId)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("built-in epic-runner source missing watch-driven loop element %q", want)
		}
	}
	for _, forbidden := range []string{
		"Promise.all",
		"loom.taskRuns.await",
		"function sleep(",
		"setTimeout",
		"intervalSeconds",
		"intervalMs",
		"completeChildTask",
		"data.leaseToken",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("built-in epic-runner source still has polling-loop machinery %q", forbidden)
		}
	}
}

// Lead notification delivery is owned by the server outbox dispatcher:
// startEpicRun makes exactly one fire-once deliverAssignment call per
// delivery site and the workflow carries no retry/drain machinery and no
// per-task lead messages.
func TestBuiltinEpicRunnerWorkflowDelegatesLeadDeliveryToServerOutbox(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, forbidden := range []string{
		"loom.agents.message",
		"startLeadDeliveryRetry",
		"startLeadMessageDeliveryRetry",
		"attemptLeadMessageDelivery",
		"formatTaskCompleteLeadMessage",
		"leadNotificationDrainMs",
		"taskNotifications",
		"leadDelivery.flush",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("built-in epic-runner source still has workflow-side lead delivery machinery %q", forbidden)
		}
	}
	if got := strings.Count(source, "loom.agents.deliverAssignment"); got != 1 {
		t.Fatalf("loom.agents.deliverAssignment call sites = %d, want exactly 1 fire-once call (server outbox owns retries)", got)
	}
}

// Stale task-run recovery is owned by the server-side sweeper; the workflow
// must not call recoverStale.
func TestBuiltinEpicRunnerWorkflowDoesNotRecoverStaleTaskRuns(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, forbidden := range []string{
		"loom.taskRuns.recoverStale",
		"staleTaskRunMaxAgeSeconds",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("built-in epic-runner source still has workflow-side stale recovery %q (server sweeper owns it)", forbidden)
		}
	}
}

func TestBuiltinEpicRunnerWorkflowWorkerProfilesAreOptIn(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, forbidden := range []string{
		"input.workerPrefix || input.worker_prefix || slug(epicId)",
		"input.worker_prefix",
		"input.worker_profile_id",
		"workerProfileId: opts.workerPrefix +",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("built-in epic-runner source still has default worker profile generation %q", forbidden)
		}
	}
	for _, want := range []string{
		"workerPrefix: stringValue(input.workerPrefix),",
		"workerProfileId: stringValue(input.workerProfileId),",
		"request.workerProfileId = workerProfileId;",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("built-in epic-runner source missing opt-in worker profile logic %q", want)
		}
	}
}

func TestBuiltinEpicRunnerWorkflowUsesCamelCaseInputContract(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, forbidden := range []string{
		"input.epic_id",
		"input.dry_run",
		"input.max_concurrency",
		"input.provider_profile",
		"input.worker_prefix",
		"input.worker_profile_id",
		"input.target_node_id",
		"input.interval_seconds",
		"input.lead_notification_drain_seconds",
		"input.stale_task_run_max_age_seconds",
		"input.parent_session_id",
		"input.lead_name",
		"input.orchestrator_session_id",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("built-in epic-runner source still accepts legacy input field %q", forbidden)
		}
	}
}

func TestBuiltinWorkflowSourcesDoNotUseProviderProfileRouting(t *testing.T) {
	for _, name := range BuiltinWorkflowNames() {
		spec, ok := BuiltinWorkflow(name)
		if !ok {
			t.Fatalf("built-in workflow %q missing", name)
		}
		for rel, source := range spec.Files {
			for _, forbidden := range []string{
				"providerProfile",
				"provider_profile",
				"supportedProviders",
				"supported_providers",
				"sandboxPlacement",
				"sandbox_placement",
			} {
				if strings.Contains(source, forbidden) {
					t.Fatalf("%s %s still uses provider-profile routing token %q", name, rel, forbidden)
				}
			}
		}
	}
}

func TestBuiltinEpicRunnerWorkflowSourceParsesAsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	path := filepath.Join(t.TempDir(), "epic-runner.mjs")
	if err := os.WriteFile(path, []byte(spec.Files[spec.Entrypoint]), 0o644); err != nil {
		t.Fatalf("write workflow source: %v", err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:norawexec // syntax-check via the node binary located by the test itself
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}

func TestCloneBuiltinSourceWritesLocalSourceLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".loom", "workflows", BuiltinEpicRunnerWorkflowName)
	manifest, err := CloneBuiltinSource(BuiltinEpicRunnerWorkflowName, root)
	if err != nil {
		t.Fatalf("CloneBuiltinSource: %v", err)
	}
	if manifest.DriverID != BuiltinEpicRunnerWorkflowName || manifest.Entrypoint != "workflows/epic-runner.ts" {
		t.Fatalf("manifest = %+v, want epic-runner entrypoint", manifest)
	}
	for _, rel := range []string{"workflow.json", "workflows/epic-runner.ts", "workflows/local-task-runner.ts", "workflows/daytona-task-runner.ts"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected cloned source %s: %v", rel, err)
		}
	}
	source, err := ReadLocalSource(BuiltinEpicRunnerWorkflowName, root)
	if err != nil {
		t.Fatalf("ReadLocalSource: %v", err)
	}
	if _, ok := source.Files[source.Manifest.Entrypoint]; !ok {
		t.Fatalf("entrypoint %s missing from local source", source.Manifest.Entrypoint)
	}
	if len(source.Runners) == 0 {
		t.Fatalf("expected cloned runner manifest to declare task runners")
	}
	for _, runner := range source.Runners {
		if runner.Name == driverpkg.OpenShellRunnerName {
			t.Fatalf("deprecated openshell runner should not be declared")
		}
	}
}

func TestReadLocalSourceValidatesExplicitRunnerManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("create workflows dir: %v", err)
	}
	manifest := `{
  "schema_version": "1",
  "driver_id": "custom-runner",
  "entrypoint": "workflows/custom-runner.ts",
  "dependencies": {"@loom/sdk": "local"},
  "runners": [{"name": "bad", "kind": "shell", "entrypoint": "../bad"}]
}
`
	if err := os.WriteFile(filepath.Join(root, "workflow.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "custom-runner.ts"), []byte("export async function run() {}\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	if _, err := ReadLocalSource("custom-runner", root); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("ReadLocalSource err = %v, want explicit runner validation error", err)
	}
}

func TestReadLocalSourceDoesNotInferUndeclaredSiblingRunners(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("create workflows dir: %v", err)
	}
	manifest := `{
  "schema_version": "1",
  "driver_id": "custom",
  "entrypoint": "workflows/custom.ts",
  "dependencies": {"@loom/sdk": "local"}
}
`
	if err := os.WriteFile(filepath.Join(root, "workflow.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for rel, source := range map[string]string{
		"custom.ts":              "export async function run() {}\n",
		"local-task-runner.ts":   "export async function run() {}\n",
		"daytona-task-runner.ts": "export async function run() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, "workflows", rel), []byte(source), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	source, err := ReadLocalSource("custom", root)
	if err != nil {
		t.Fatalf("ReadLocalSource: %v", err)
	}
	if len(source.Runners) != 0 {
		t.Fatalf("source runners = %+v, want no inferred runners without explicit manifest", source.Runners)
	}
}

func TestFlueRuntimeRootHonorsLoomPrefixedEnv(t *testing.T) {
	runtimeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write runtime package: %v", err)
	}
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")

	got, err := workflowdistribution.FlueRuntimeRoot()
	if err != nil {
		t.Fatalf("flueRuntimeRoot returned error: %v", err)
	}
	if got != runtimeRoot {
		t.Fatalf("flueRuntimeRoot = %q, want %q", got, runtimeRoot)
	}
}

func TestDaytonaSDKRootDerivesFromResolvedFlueRuntimeRoot(t *testing.T) {
	flueRoot := t.TempDir()
	runtimeRoot := filepath.Join(flueRoot, "packages", "runtime")
	daytonaRoot := filepath.Join(flueRoot, "node_modules", ".pnpm", "node_modules", "@daytona", "sdk")
	for _, root := range []string{runtimeRoot, daytonaRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"test"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write package.json for %s: %v", root, err)
		}
	}
	t.Setenv("DAYTONA_SDK_ROOT", "")
	t.Setenv("FLUE_REPO", "")

	got, err := workflowdistribution.DaytonaSDKRoot(runtimeRoot)
	if err != nil {
		t.Fatalf("daytonaSDKRoot returned error: %v", err)
	}
	if got != daytonaRoot {
		t.Fatalf("daytonaSDKRoot = %q, want %q", got, daytonaRoot)
	}
}

func TestLinkFlueBuildDependenciesLinksRequiredRuntimeDependencies(t *testing.T) {
	flueRoot := t.TempDir()
	runtimeRoot := filepath.Join(flueRoot, "packages", "runtime")
	for _, root := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
		filepath.Join(runtimeRoot, "node_modules", "valibot"),
	} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"test"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write package.json for %s: %v", root, err)
		}
	}
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")

	buildRoot := t.TempDir()
	if err := workflowdistribution.LinkFlueBuildDependencies(buildRoot); err != nil {
		t.Fatalf("linkFlueBuildDependencies returned error: %v", err)
	}
	link := filepath.Join(buildRoot, "node_modules", "valibot")
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read valibot symlink: %v", err)
	}
	want := filepath.Join(runtimeRoot, "node_modules", "valibot")
	if got != want {
		t.Fatalf("valibot symlink target = %q, want %q", got, want)
	}
	if _, err := os.Lstat(filepath.Join(buildRoot, "node_modules", "@daytona")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workflow build unexpectedly links the Daytona SDK: %v", err)
	}
}

func TestLinkFlueBuildDependenciesDoesNotResolveDaytonaSDK(t *testing.T) {
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	for _, root := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
		filepath.Join(runtimeRoot, "node_modules", "valibot"),
	} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"test"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write package.json for %s: %v", root, err)
		}
	}
	missingDaytonaRoot := filepath.Join(t.TempDir(), "missing-daytona-sdk")
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", missingDaytonaRoot)

	buildRoot := t.TempDir()
	if err := workflowdistribution.LinkFlueBuildDependencies(buildRoot); err != nil {
		t.Fatalf("linkFlueBuildDependencies consulted unused DAYTONA_SDK_ROOT: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(buildRoot, "node_modules", "@daytona")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workflow build unexpectedly links the Daytona SDK: %v", err)
	}
}

func TestBuildBuiltinEpicRunnerWithRealFlueDerivesDaytonaSDK(t *testing.T) {
	repoRoot := workflowTestRepoRoot(t)
	flueRoot := workflowTestFlueRepoRoot(t, repoRoot)
	flueBin := filepath.Join(flueRoot, "packages", "cli", "bin", "flue.mjs")
	runtimeRoot := filepath.Join(flueRoot, "packages", "runtime")
	daytonaRoot := filepath.Join(flueRoot, "node_modules", ".pnpm", "node_modules", "@daytona", "sdk")
	for _, path := range []string{flueBin, filepath.Join(runtimeRoot, "package.json"), filepath.Join(daytonaRoot, "package.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("real Flue dependency unavailable at %s: %v", path, err)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	flueCmd, err := json.Marshal([]string{node, flueBin})
	if err != nil {
		t.Fatalf("encode Flue command: %v", err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", string(flueCmd))
	t.Setenv("LOOM_REAL_FLUE_CMD", "")
	t.Setenv("LOOM_SDK_ROOT", filepath.Join(repoRoot, "sdk"))
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")

	serverPath, diagnostics, err := BuildBuiltinBundle(context.Background(), BuiltinEpicRunnerWorkflowName, filepath.Join(t.TempDir(), "dist"))
	if err != nil {
		t.Fatalf("BuildBuiltinBundle diagnostics:\n%s\nerr: %v", diagnostics, err)
	}
	if _, err := os.Stat(serverPath); err != nil {
		t.Fatalf("stat built server: %v", err)
	}
}

func workflowTestFlueRepoRoot(t *testing.T, repoRoot string) string {
	t.Helper()
	for _, candidate := range []string{
		filepath.Join(repoRoot, "..", "flue"),
		filepath.Join(repoRoot, "..", "..", "flue"),
	} {
		candidate = filepath.Clean(candidate)
		if _, err := os.Stat(filepath.Join(candidate, "packages", "cli", "bin", "flue.mjs")); err == nil {
			return candidate
		}
	}
	t.Skip("real Flue checkout unavailable")
	return ""
}

func workflowTestRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
