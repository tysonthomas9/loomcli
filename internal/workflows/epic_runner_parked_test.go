package workflows

import (
	"strings"
	"testing"
)

// The epic-runner must no longer abort on the first terminal-failed child:
// parked children are recorded and the DAG keeps draining, ending in
// needs_review (epic_tasks_parked) only once no ready or active work remains.
func TestBuiltinEpicRunnerWorkflowParksFailedChildrenAndContinues(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]

	tests := []struct {
		name string
		want string
	}{
		{name: "parked list declared", want: "const parked = [];"},
		{name: "failed children recorded instead of aborting", want: "parked.push({"},
		{name: "drained completion requires empty parked list", want: "activeCount === 0 && parked.length === 0"},
		{name: "parked terminal guard", want: "snapshot.readyCount === 0 && activeCount === 0 && parked.length > 0"},
		{name: "parked error class", want: `errorClass: "epic_tasks_parked",`},
		{name: "parked summary helper", want: "summarizeParkedTasks(parked)"},
		{name: "idempotent completion id", want: `completionId: "complete-" + completedTaskRunId,`},
		{name: "blocked guard kept", want: `errorClass: "epic_blocked",`},
		{name: "no-progress guard kept", want: `errorClass: "epic_no_progress",`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(source, tt.want) {
				t.Fatalf("built-in epic-runner source missing %q", tt.want)
			}
		})
	}
}

// Terminal-failed children must stay claimed (no release): the only remaining
// safeRelease call site is the pre-execution request failure, plus the helper
// definition itself.
func TestBuiltinEpicRunnerWorkflowReleasesOnlyOnPreExecutionFailure(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	if got := strings.Count(source, "safeRelease("); got != 2 {
		t.Fatalf("safeRelease occurrences = %d, want 2 (definition + pre-execution request failure call site)", got)
	}
	if !strings.Contains(source, "result = await loom.taskRuns.request(request);\n  } catch (err) {\n    await safeRelease(loom, task.id);") {
		t.Fatal("built-in epic-runner source no longer releases the claim on pre-execution request failure")
	}
}
