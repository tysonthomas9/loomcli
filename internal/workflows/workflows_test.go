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
		"workerProfileId: opts.workerPrefix +",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("built-in epic-runner source still has default worker profile generation %q", forbidden)
		}
	}
	for _, want := range []string{
		"const workerPrefix = stringValue(input.workerPrefix || input.worker_prefix);",
		"const workerProfileId = stringValue(input.workerProfileId || input.worker_profile_id);",
		"request.workerProfileId = workerProfileId;",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("built-in epic-runner source missing opt-in worker profile logic %q", want)
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
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("node --check failed: %v\n%s", err, out)
	}
}
