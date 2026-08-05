package workflowdistribution

import (
	"strings"
	"testing"
)

// The epic-runner must not abort on the first terminal-failed child: blocked
// failures are recorded and the DAG keeps draining, ending in needs_review
// (epic_tasks_blocked) only once no ready or active work remains.
func TestBuiltinEpicRunnerWorkflowBlocksFailedChildrenAndContinues(t *testing.T) {
	spec, ok := BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("built-in epic-runner workflow missing")
	}
	source := spec.Files[spec.Entrypoint]

	tests := []struct {
		name string
		want string
	}{
		{name: "blocked failure list declared", want: "const blockedFailures = [];"},
		{name: "failed children recorded instead of aborting", want: "blockedFailures.push({"},
		{name: "drained completion requires empty blocked failure list", want: "if (open === 0 && blockedFailures.length === 0) {"},
		{name: "blocked failure guard", want: "if (blockedFailures.length > 0) {"},
		{name: "blocked task error class", want: `errorClass: "epic_tasks_blocked",`},
		{name: "blocked task summary helper", want: "summarizeBlockedTasks(entries)"},
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

// Terminal-failed children and ambiguous TaskRun request failures must not be
// released by the workflow. The only remaining safeRelease call site is a
// certified pre-commit rejection, plus the helper definition itself.
func TestBuiltinEpicRunnerWorkflowReleasesOnlyOnCertifiedPreExecutionFailure(t *testing.T) {
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
	if !strings.Contains(source, "if (!isAmbiguousTaskRunRequestError(err)) {") {
		t.Fatal("built-in epic-runner no longer preserves claims after ambiguous TaskRun request responses")
	}
	if !strings.Contains(source, `errorClass: "child_task_request_failed",`) {
		t.Fatal("built-in epic-runner source no longer records pre-execution request failures")
	}
}
