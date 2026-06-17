package workflows

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
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
		"completeChildTask(loom, data)",
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

func TestSubmissionTrustDefaultsUntrustedFailClosed(t *testing.T) {
	if got := submissionTrust(""); got != domain.DriverTrustUntrusted {
		t.Fatalf("submissionTrust(\"\") = %q, want untrusted (external submissions fail closed)", got)
	}
	if got := submissionTrust(domain.DriverTrustTrusted); got != domain.DriverTrustTrusted {
		t.Fatalf("submissionTrust(trusted) = %q, want trusted (builtin path)", got)
	}
	if got := submissionTrust(domain.DriverTrustUntrusted); got != domain.DriverTrustUntrusted {
		t.Fatalf("submissionTrust(untrusted) = %q, want untrusted", got)
	}
}

func TestEmbeddedPrebuiltDigestMatchesRequiresCurrentMarker(t *testing.T) {
	distPath := "builtin-dist/example/dist"
	source := fstest.MapFS{
		distPath + "/source-digest.txt": &fstest.MapFile{Data: []byte("sha256:current\n")},
	}
	if ok, err := embeddedPrebuiltDigestMatches(source, distPath, "sha256:current"); err != nil || !ok {
		t.Fatalf("matching marker = %v, %v; want true, nil", ok, err)
	}
	if ok, err := embeddedPrebuiltDigestMatches(source, distPath, "sha256:stale"); err != nil || ok {
		t.Fatalf("stale marker = %v, %v; want false, nil", ok, err)
	}
	if ok, err := embeddedPrebuiltDigestMatches(fstest.MapFS{}, distPath, "sha256:current"); err != nil || ok {
		t.Fatalf("missing marker = %v, %v; want false, nil", ok, err)
	}
}

func TestEnsureBuiltinWorkflowUsesEmbeddedPrebuiltBundle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("LOOM_REAL_FLUE_CMD", filepath.Join(t.TempDir(), "missing-flue"))
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", BuiltinGitHubReviewAgentWorkflowName); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow returned error: %v", err)
	}
	driverID, err := ResolveDriverID(ctx, st, "BUILTIN", BuiltinGitHubReviewAgentWorkflowName)
	if err != nil {
		t.Fatalf("ResolveDriverID returned error: %v", err)
	}
	driver, err := st.Drivers().Get(ctx, "BUILTIN", driverID)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.Status != domain.DriverStatusActive {
		t.Fatalf("driver status = %q, want active", driver.Status)
	}
	if driver.TrustLevel != domain.DriverTrustTrusted {
		t.Fatalf("driver trust = %q, want trusted", driver.TrustLevel)
	}
	version, err := st.DriverVersions().Get(ctx, "BUILTIN", driver.ActiveVersionID)
	if err != nil {
		t.Fatalf("get active driver version: %v", err)
	}
	if version.Runtime != driverpkg.RuntimeFlueNode {
		t.Fatalf("runtime = %q, want %q", version.Runtime, driverpkg.RuntimeFlueNode)
	}
	if !strings.HasPrefix(version.SourceRef, "builtin://workflows/github-review-agent/versions/") {
		t.Fatalf("source ref = %q, want builtin github-review-agent ref", version.SourceRef)
	}
	bundleServer := filepath.Join(workDir, filepath.FromSlash(version.BundleRef), "dist", "server.mjs")
	if _, err := os.Stat(bundleServer); err != nil {
		t.Fatalf("embedded bundle server.mjs not staged at %s: %v", bundleServer, err)
	}
}

func TestEnsureBuiltinWorkflowUpdatesStaleEmbeddedPrebuiltBundle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BUILTIN", Name: "Builtins"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	name := BuiltinGitHubReviewAgentWorkflowName
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("LOOM_REAL_FLUE_CMD", filepath.Join(t.TempDir(), "missing-flue"))
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")

	staleDist := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(staleDist, 0o755); err != nil {
		t.Fatalf("create stale dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write stale server: %v", err)
	}
	stale, err := driverpkg.RegisterFlueDriver(ctx, st, driverpkg.RegisterFlueOptions{
		WorkspaceKey: "BUILTIN",
		WorkDir:      workDir,
		DistPath:     staleDist,
		DriverName:   name,
		DriverID:     name,
		WorkflowName: name,
		SourceRef:    "builtin://workflows/github-review-agent/versions/stale",
		SourceDigest: "sha256:stale",
		CreatedBy:    "system",
		Activate:     true,
		RunnerSpecs:  workflowRunnerSpecs(BuildAndRegisterOptions{Entrypoint: "workflows/github-review-agent.ts", Files: map[string]string{}}),
		Trust:        domain.DriverTrustTrusted,
	})
	if err != nil {
		t.Fatalf("register stale driver: %v", err)
	}

	if err := EnsureBuiltinWorkflow(ctx, st, "BUILTIN", name); err != nil {
		t.Fatalf("EnsureBuiltinWorkflow returned error: %v", err)
	}
	driver, err := st.Drivers().Get(ctx, "BUILTIN", name)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if driver.ActiveVersionID == stale.Version.VersionID {
		t.Fatalf("active version stayed stale %q", driver.ActiveVersionID)
	}
	version, err := st.DriverVersions().Get(ctx, "BUILTIN", driver.ActiveVersionID)
	if err != nil {
		t.Fatalf("get active version: %v", err)
	}
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		t.Fatal("built-in github-review-agent workflow missing")
	}
	if want := SourceDigest(spec.Files); version.SourceDigest != want {
		t.Fatalf("source digest = %q, want %q", version.SourceDigest, want)
	}
	bundleServer := filepath.Join(workDir, filepath.FromSlash(version.BundleRef), "dist", "server.mjs")
	if _, err := os.Stat(bundleServer); err != nil {
		t.Fatalf("updated embedded bundle server.mjs not staged at %s: %v", bundleServer, err)
	}
}
