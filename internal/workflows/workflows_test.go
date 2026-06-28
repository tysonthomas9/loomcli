package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
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
		`case "taskRunParked":`,
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
