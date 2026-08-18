package driver

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/cli/testdata/clitest"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func listResultFromSummaries(summaries []workitems.IssueSummary) *workitems.ListResult {
	items := make([]workitems.ListItem, len(summaries))
	for index := range summaries {
		items[index] = workitems.ListItem{IssueSummary: summaries[index]}
	}
	return &workitems.ListResult{Issues: items}
}

func TestLoadEpicSnapshotCountsOnlyOpenChildren(t *testing.T) {
	ib := clitest.NewMockWorkItems()
	ib.ReadyResult = []workitems.IssueSummary{{ID: "TASK-1", Title: "Ready", Status: "open", Parent: "EPIC-1"}}
	ib.BlockedResult = []workitems.IssueSummary{{ID: "TASK-2", Title: "Blocked", Status: "blocked", Parent: "EPIC-1", BlockedBy: []string{"TASK-0"}, BlockedByCount: 1}}
	ib.ListResult = listResultFromSummaries([]workitems.IssueSummary{
		{ID: "TASK-1", Status: "open", Parent: "EPIC-1"},
		{ID: "TASK-2", Status: "blocked", Parent: "EPIC-1"},
		{ID: "TASK-3", Status: "closed", Parent: "EPIC-1"},
		{ID: "TASK-4", Status: "deferred", Parent: "EPIC-1"},
	})

	got, err := LoadEpicSnapshot(context.Background(), ib, EpicSnapshotOptions{EpicID: "EPIC-1"})
	if err != nil {
		t.Fatalf("LoadEpicSnapshot: %v", err)
	}
	if got.EpicID != "EPIC-1" || got.ReadyCount != 1 || got.BlockedCount != 1 || got.OpenChildrenCount != 2 {
		t.Fatalf("snapshot = %+v, want ready=1 blocked=1 open=2", got)
	}
	if len(got.Blocked) != 1 || got.Blocked[0].BlockedByCount != 1 || got.Blocked[0].BlockedBy[0] != "TASK-0" {
		t.Fatalf("blocked summary = %+v, want blocker metadata", got.Blocked)
	}
}

func TestListActiveTaskRunsReturnsQueuedAndRunningOnly(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "driver-1",
		OwnerType:    workflowcatalog.DriverOwnerSystem,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
		WorkspaceKey:       "WS",
		VersionID:          "version-1",
		DriverID:           "driver-1",
		Version:            1,
		SourceDigest:       "sha256:source",
		BundleDigest:       "sha256:bundle",
		Runtime:            RuntimeFlueNode,
		ValidationStatus:   workflowcatalog.DriverVersionValidationPassed,
		AvailabilityStatus: workflowcatalog.DriverVersionAvailabilityAvailable,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, "WS", "driver-1", "version-1"); err != nil {
		t.Fatalf("Approve driver version: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, "WS", "driver-1", "version-1"); err != nil {
		t.Fatalf("Activate driver version: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "EPIC-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-2",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "EPIC-2",
	}); err != nil {
		t.Fatalf("Create other driver run: %v", err)
	}
	for _, run := range []execution.TaskRunCreate{
		{WorkspaceKey: "WS", TaskRunID: "task-run-queued", DriverRunID: "run-1", TaskID: "TASK-1", Status: execution.TaskRunRecordQueued},
		{WorkspaceKey: "WS", TaskRunID: "task-run-running", DriverRunID: "run-1", TaskID: "TASK-2", Status: execution.TaskRunRecordRunning},
		{WorkspaceKey: "WS", TaskRunID: "task-run-completed", DriverRunID: "run-1", TaskID: "TASK-3", Status: execution.TaskRunRecordCompleted},
		{WorkspaceKey: "WS", TaskRunID: "task-run-other", DriverRunID: "run-2", TaskID: "TASK-4", Status: execution.TaskRunRecordRunning},
	} {
		if _, err := st.TaskRuns().Create(ctx, run); err != nil {
			t.Fatalf("Create task run %s: %v", run.TaskRunID, err)
		}
	}

	got, err := ListActiveTaskRuns(ctx, st, ActiveTaskRunsOptions{WorkspaceKey: "WS", DriverRunID: "run-1", EpicID: "EPIC-1"})
	if err != nil {
		t.Fatalf("ListActiveTaskRuns: %v", err)
	}
	if got.DriverRunID != "run-1" || got.EpicID != "EPIC-1" || got.ActiveCount != 2 {
		t.Fatalf("active = %+v, want two active for run-1", got)
	}
	ids := map[string]bool{}
	for _, taskRun := range got.TaskRuns {
		ids[taskRun.ID] = true
		if taskRun.Status != execution.TaskRunRecordQueued && taskRun.Status != execution.TaskRunRecordRunning {
			t.Fatalf("task run %s has terminal status %s", taskRun.ID, taskRun.Status)
		}
	}
	if !ids["task-run-queued"] || !ids["task-run-running"] || ids["task-run-completed"] || ids["task-run-other"] {
		t.Fatalf("active ids = %+v, want queued/running for run-1 only", ids)
	}
}
