package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		"loom.agents.message",
		"dryRun",
		"targetNodeId",
		"loom.taskRuns.recoverStale",
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
		"const workerPrefix = stringValue(input.workerPrefix);",
		"const workerProfileId = stringValue(input.workerProfileId);",
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

func TestBuiltinEpicRunnerWorkflowDrainsQueuedLeadMessagesBeforeTerminalResult(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, want := range []string{
		"const leadNotificationDrainMs =",
		"taskNotifications.drain(leadNotificationDrainMs)",
		"async drain(timeoutMs) {",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("built-in epic-runner source missing lead notification drain logic %q", want)
		}
	}
	if strings.Contains(source, "await taskNotifications.flush();\n        const suffix") {
		t.Fatalf("built-in epic-runner still completes after a single task notification flush")
	}
}

func TestBuiltinEpicRunnerWorkflowAwaitsQueuedTaskRuns(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	for _, want := range []string{
		"loom.taskRuns.await",
		"taskRunStillActive(result.status)",
		"function taskRunStillActive(status)",
		"if (!leaseToken) {",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("built-in epic-runner source missing async task-run behavior %q", want)
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
