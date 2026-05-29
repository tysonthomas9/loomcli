package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
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

func TestWorkflowCommandsUseActiveWorkspaceStore(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WF", Name: "Workflow CLI"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	withWorkflowStore(t, st, "WF")
	withWorkflowGlobals(t, func() {
		ib := clitest.NewMockIssueBackend()
		task := backend.IssueData{ID: "TASK-1", Title: "Build inbox", Status: "open"}
		ib.ReadyResult = []backend.IssueData{task}
		ib.ListResult = []backend.IssueData{task}
		cli.SetDefaultIssueBackend(ib)
		t.Cleanup(cli.ResetDefaultIssueBackend)

		workflowRunInput = `{"parentId":"EPIC-1","role":"task","maxConcurrency":1}`
		workflowRunOnce = true
		workflowRunWait = false

		if err := runWorkflowList(nil, nil); err != nil {
			t.Fatalf("runWorkflowList() error = %v", err)
		}
		if err := runWorkflowRun(nil, []string{workflowpkg.RunParentWorkItemsName}); err != nil {
			t.Fatalf("runWorkflowRun() error = %v", err)
		}
		runs, err := st.WorkflowRuns().List(ctx, "WF", store.WorkflowRunFilter{})
		if err != nil {
			t.Fatalf("list workflow runs: %v", err)
		}
		if len(runs) != 1 || runs[0].Status != domain.WorkflowRunWaiting {
			t.Fatalf("runs = %+v, want one waiting run", runs)
		}
		if err := runWorkflowShow(nil, []string{runs[0].RunID}); err != nil {
			t.Fatalf("runWorkflowShow() error = %v", err)
		}
		if err := runWorkflowLogs(nil, []string{runs[0].RunID}); err != nil {
			t.Fatalf("runWorkflowLogs() error = %v", err)
		}
		if err := runWorkflowCancel(nil, []string{runs[0].RunID}); err != nil {
			t.Fatalf("runWorkflowCancel() error = %v", err)
		}
		cancelled, err := st.WorkflowRuns().Get(ctx, "WF", runs[0].RunID)
		if err != nil {
			t.Fatalf("get cancelled run: %v", err)
		}
		if cancelled.Status != domain.WorkflowRunCancelled {
			t.Fatalf("cancelled status = %s, want cancelled", cancelled.Status)
		}
	})
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

func withWorkflowStore(t *testing.T, st store.Store, workspace string) {
	t.Helper()
	old := workflowWithActiveWorkspace
	workflowWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, workspace)
	}
	t.Cleanup(func() { workflowWithActiveWorkspace = old })
}

func withWorkflowGlobals(t *testing.T, fn func()) {
	t.Helper()
	oldWorkflowJSON := workflowJSON
	oldWorkflowRunInput, oldWorkflowRunWait, oldWorkflowRunOnce := workflowRunInput, workflowRunWait, workflowRunOnce
	oldWorkflowLogsJSON, oldWorkflowShowJSON, oldWorkflowListJSON := workflowLogsJSON, workflowShowJSON, workflowListJSON
	t.Cleanup(func() {
		workflowJSON = oldWorkflowJSON
		workflowRunInput, workflowRunWait, workflowRunOnce = oldWorkflowRunInput, oldWorkflowRunWait, oldWorkflowRunOnce
		workflowLogsJSON, workflowShowJSON, workflowListJSON = oldWorkflowLogsJSON, oldWorkflowShowJSON, oldWorkflowListJSON
	})
	fn()
}
