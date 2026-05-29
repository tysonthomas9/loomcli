package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
)

func TestParseInput(t *testing.T) {
	for _, input := range []string{"", "  ", `{"parentId":"EPIC-1"}`} {
		got, err := parseInput(input)
		if err != nil {
			t.Fatalf("parseInput(%q) error = %v", input, err)
		}
		if len(got) == 0 {
			t.Fatalf("parseInput(%q) returned empty input", input)
		}
	}
	if _, err := parseInput(`{"broken":`); err == nil {
		t.Fatal("parseInput() succeeded for invalid JSON")
	}
}

func TestRunWorkflowRunRejectsInvalidInputBeforeOpeningStore(t *testing.T) {
	old := workflowRunInput
	workflowRunInput = `{"broken":`
	t.Cleanup(func() { workflowRunInput = old })

	err := runWorkflowRun(nil, []string{"epic-runner"})
	if err == nil || !strings.Contains(err.Error(), "--input must be valid JSON") {
		t.Fatalf("runWorkflowRun() error = %v, want invalid JSON", err)
	}
}

func TestWaitWorkflowReturnsTerminalRun(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := workflowpkg.EnsureBuiltins(ctx, st, "WF"); err != nil {
		t.Fatalf("EnsureBuiltins() error = %v", err)
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, "WF", workflowpkg.RunParentWorkItemsName, []byte(`{"parentId":"EPIC-1"}`), "test")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	completed := domain.WorkflowRunCompleted
	if _, err := st.WorkflowRuns().Update(ctx, "WF", run.RunID, store.WorkflowRunUpdate{Status: &completed}); err != nil {
		t.Fatalf("complete workflow run: %v", err)
	}
	got, err := waitWorkflow(ctx, st, "WF", run.RunID)
	if err != nil {
		t.Fatalf("waitWorkflow() error = %v", err)
	}
	if got.RunID != run.RunID || got.Status != domain.WorkflowRunCompleted {
		t.Fatalf("waitWorkflow() = %+v, want completed run %s", got, run.RunID)
	}
}

func TestWorkflowActorName(t *testing.T) {
	t.Setenv("LOOM_ACTOR", " runner ")
	if got := actorName(); got != "runner" {
		t.Fatalf("actorName() = %q, want runner", got)
	}
	t.Setenv("LOOM_ACTOR", "")
	t.Setenv("USER", "")
	if got := actorName(); got != "loom" {
		t.Fatalf("actorName() = %q, want loom", got)
	}
}
