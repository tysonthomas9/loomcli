package workflows

import (
	"strings"
	"testing"
)

// The epic-runner must not abort on the first terminal-failed child: parked
// children are recorded and the DAG keeps draining, ending in needs_review
// (epic_tasks_parked) only once no ready or active work remains. With the
// explicit parked issue status, snapshot children parked by earlier runs
// count toward the parked remainder too.
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
		{name: "drained completion requires empty parked list", want: "if (open === 0 && parked.length === 0) {"},
		{name: "snapshot parked children derived from explicit status", want: `stringValue(child && child.status).toLowerCase() === "parked"`},
		{name: "parked terminal guard", want: "if (parked.length > 0 || parkedChildren.length > 0) {"},
		{name: "parked error class", want: `errorClass: "epic_tasks_parked",`},
		{name: "parked summary helper", want: "summarizeParkedTasks(entries)"},
		{name: "idempotent completion id", want: `completionId: "complete-" + taskRunId,`},
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

// Terminal-failed children must not be released by the workflow (the server
// park transition already released the claim and parked the issue): the only
// remaining safeRelease call site is the pre-execution request failure, plus
// the helper definition itself.
func TestBuiltinEpicRunnerWorkflowReleasesOnlyOnPreExecutionFailure(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]
	if got := strings.Count(source, "safeRelease("); got != 2 {
		t.Fatalf("safeRelease occurrences = %d, want 2 (definition + pre-execution request failure call site)", got)
	}
	if !strings.Contains(source, "await safeRelease(loom, task.id);") {
		t.Fatal("built-in epic-runner source no longer releases the claim on pre-execution request failure")
	}
	if !strings.Contains(source, `errorClass: "child_task_request_failed",`) {
		t.Fatal("built-in epic-runner source no longer parks pre-execution request failures")
	}
}
