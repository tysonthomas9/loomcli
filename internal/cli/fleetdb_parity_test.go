package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// --- Shared fixture data ---

var parityFixtureTime = time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)

var parityTypesIssues = []*types.Issue{
	{
		ID: "PARITY-1", Title: "Parity test issue", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask, Design: "some design",
		Assignee: "alice", SourceRepo: "repo-a",
		Labels: []string{"backend", "urgent"},
		Dependencies: []*types.Dependency{
			{
				IssueID: "PARITY-1", DependsOnID: "EPIC-1",
				Type: types.DepParentChild, CreatedAt: parityFixtureTime,
				CreatedBy: "admin",
			},
		},
	},
	{
		ID: "PARITY-2", Title: "Second issue", Status: types.StatusInProgress,
		Priority: 1, IssueType: types.TypeBug,
		Labels: nil, Dependencies: nil, // edge case: nil slices
	},
}

// parityExpectedBdIssues is what both backends should produce for parityTypesIssues.
var parityExpectedBdIssues = []BdIssue{
	{
		ID: "PARITY-1", Title: "Parity test issue", Status: "open",
		Priority: 2, IssueType: "task", Design: "some design",
		Assignee: "alice", SourceRepo: "repo-a",
		Labels: []string{"backend", "urgent"},
		Dependencies: []Dependency{
			{
				IssueID: "PARITY-1", DependsOnID: "EPIC-1",
				Type:      "parent-child",
				CreatedAt: "2026-03-14T12:00:00Z",
				CreatedBy: "admin",
			},
		},
	},
	{
		ID: "PARITY-2", Title: "Second issue", Status: "in_progress",
		Priority: 1, IssueType: "bug",
		Labels: []string{}, Dependencies: []Dependency{}, // NOT nil
	},
}

// --- Setup helpers ---

func setupParityBdBackend(t *testing.T, fixtureJSON string) *bdBackend {
	t.Helper()
	runner := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		return CommandResult{Stdout: fixtureJSON}
	}}
	return newBdBackend(runner, "/test")
}

func setupParityFleetDBBackend(t *testing.T, issues []*types.Issue) *fleetDBBackend {
	t.Helper()
	mock := &mockFleetDBClient{
		readyFn: func(_ *rpc.ReadyArgs) (*rpc.Response, error) {
			return successResp(issues), nil
		},
		listFn: func(_ *rpc.ListArgs) (*rpc.Response, error) {
			return successResp(issues), nil
		},
		blockedFn: func(_ *rpc.BlockedArgs) (*rpc.Response, error) {
			return successResp(issues), nil
		},
	}
	return newFleetDBBackend(mock, "test")
}

// --- Comparison helpers ---

func assertBdIssuesEqual(t *testing.T, label string, a, b []BdIssue) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s: length mismatch: %d vs %d", label, len(a), len(b))
	}
	for i := range a {
		prefix := label + "[" + a[i].ID + "]"
		if a[i].ID != b[i].ID {
			t.Errorf("%s.ID: %q vs %q", prefix, a[i].ID, b[i].ID)
		}
		if a[i].Title != b[i].Title {
			t.Errorf("%s.Title: %q vs %q", prefix, a[i].Title, b[i].Title)
		}
		if a[i].Status != b[i].Status {
			t.Errorf("%s.Status: %q vs %q", prefix, a[i].Status, b[i].Status)
		}
		if a[i].Priority != b[i].Priority {
			t.Errorf("%s.Priority: %d vs %d", prefix, a[i].Priority, b[i].Priority)
		}
		if a[i].IssueType != b[i].IssueType {
			t.Errorf("%s.IssueType: %q vs %q", prefix, a[i].IssueType, b[i].IssueType)
		}
		if a[i].Design != b[i].Design {
			t.Errorf("%s.Design: %q vs %q", prefix, a[i].Design, b[i].Design)
		}
		if a[i].Assignee != b[i].Assignee {
			t.Errorf("%s.Assignee: %q vs %q", prefix, a[i].Assignee, b[i].Assignee)
		}
		if a[i].SourceRepo != b[i].SourceRepo {
			t.Errorf("%s.SourceRepo: %q vs %q", prefix, a[i].SourceRepo, b[i].SourceRepo)
		}
		assertLabelsEqual(t, prefix, a[i].Labels, b[i].Labels)
		assertDepsEqual(t, prefix, a[i].Dependencies, b[i].Dependencies)
	}
}

func assertLabelsEqual(t *testing.T, prefix string, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Errorf("%s.Labels: length %d vs %d", prefix, len(a), len(b))
		return
	}
	for j := range a {
		if a[j] != b[j] {
			t.Errorf("%s.Labels[%d]: %q vs %q", prefix, j, a[j], b[j])
		}
	}
}

func assertDepsEqual(t *testing.T, prefix string, a, b []Dependency) {
	t.Helper()
	if len(a) != len(b) {
		t.Errorf("%s.Dependencies: length %d vs %d", prefix, len(a), len(b))
		return
	}
	for j := range a {
		dp := prefix + ".Dep[" + a[j].DependsOnID + "]"
		if a[j].IssueID != b[j].IssueID {
			t.Errorf("%s.IssueID: %q vs %q", dp, a[j].IssueID, b[j].IssueID)
		}
		if a[j].DependsOnID != b[j].DependsOnID {
			t.Errorf("%s.DependsOnID: %q vs %q", dp, a[j].DependsOnID, b[j].DependsOnID)
		}
		if a[j].Type != b[j].Type {
			t.Errorf("%s.Type: %q vs %q", dp, a[j].Type, b[j].Type)
		}
		if a[j].CreatedAt != b[j].CreatedAt {
			t.Errorf("%s.CreatedAt: %q vs %q", dp, a[j].CreatedAt, b[j].CreatedAt)
		}
		if a[j].CreatedBy != b[j].CreatedBy {
			t.Errorf("%s.CreatedBy: %q vs %q", dp, a[j].CreatedBy, b[j].CreatedBy)
		}
	}
}

func assertStatsEqual(t *testing.T, a, b BdStats) {
	t.Helper()
	if a.Summary.TotalIssues != b.Summary.TotalIssues {
		t.Errorf("TotalIssues: %d vs %d", a.Summary.TotalIssues, b.Summary.TotalIssues)
	}
	if a.Summary.OpenIssues != b.Summary.OpenIssues {
		t.Errorf("OpenIssues: %d vs %d", a.Summary.OpenIssues, b.Summary.OpenIssues)
	}
	if a.Summary.ClosedIssues != b.Summary.ClosedIssues {
		t.Errorf("ClosedIssues: %d vs %d", a.Summary.ClosedIssues, b.Summary.ClosedIssues)
	}
	if a.Summary.InProgressIssues != b.Summary.InProgressIssues {
		t.Errorf("InProgressIssues: %d vs %d", a.Summary.InProgressIssues, b.Summary.InProgressIssues)
	}
	if a.Summary.BlockedIssues != b.Summary.BlockedIssues {
		t.Errorf("BlockedIssues: %d vs %d", a.Summary.BlockedIssues, b.Summary.BlockedIssues)
	}
	if a.Summary.DeferredIssues != b.Summary.DeferredIssues {
		t.Errorf("DeferredIssues: %d vs %d", a.Summary.DeferredIssues, b.Summary.DeferredIssues)
	}
	if a.Summary.TombstoneIssues != b.Summary.TombstoneIssues {
		t.Errorf("TombstoneIssues: %d vs %d", a.Summary.TombstoneIssues, b.Summary.TombstoneIssues)
	}
	if a.Summary.PinnedIssues != b.Summary.PinnedIssues {
		t.Errorf("PinnedIssues: %d vs %d", a.Summary.PinnedIssues, b.Summary.PinnedIssues)
	}
}

// --- Parity tests ---

func TestBackendParity_Ready(t *testing.T) {
	fixtureJSON, _ := json.Marshal(parityExpectedBdIssues)
	bdB := setupParityBdBackend(t, string(fixtureJSON))
	fleetB := setupParityFleetDBBackend(t, parityTypesIssues)

	ctx := context.Background()
	bdResult, err := bdB.Ready(ctx, ReadyOpts{Limit: 50})
	if err != nil {
		t.Fatalf("bdBackend.Ready: %v", err)
	}
	fleetResult, err := fleetB.Ready(ctx, ReadyOpts{Limit: 50})
	if err != nil {
		t.Fatalf("fleetDBBackend.Ready: %v", err)
	}
	assertBdIssuesEqual(t, "Ready", bdResult, fleetResult)
}

func TestBackendParity_List(t *testing.T) {
	fixtureJSON, _ := json.Marshal(parityExpectedBdIssues)
	bdB := setupParityBdBackend(t, string(fixtureJSON))
	fleetB := setupParityFleetDBBackend(t, parityTypesIssues)

	ctx := context.Background()
	bdResult, err := bdB.List(ctx, ListOpts{Status: "in_progress"})
	if err != nil {
		t.Fatalf("bdBackend.List: %v", err)
	}
	fleetResult, err := fleetB.List(ctx, ListOpts{Status: "in_progress"})
	if err != nil {
		t.Fatalf("fleetDBBackend.List: %v", err)
	}
	assertBdIssuesEqual(t, "List", bdResult, fleetResult)
}

func TestBackendParity_Blocked(t *testing.T) {
	fixtureJSON, _ := json.Marshal(parityExpectedBdIssues)
	bdB := setupParityBdBackend(t, string(fixtureJSON))
	fleetB := setupParityFleetDBBackend(t, parityTypesIssues)

	ctx := context.Background()
	bdResult, err := bdB.Blocked(ctx)
	if err != nil {
		t.Fatalf("bdBackend.Blocked: %v", err)
	}
	fleetResult, err := fleetB.Blocked(ctx)
	if err != nil {
		t.Fatalf("fleetDBBackend.Blocked: %v", err)
	}
	assertBdIssuesEqual(t, "Blocked", bdResult, fleetResult)
}

func TestBackendParity_Stats(t *testing.T) {
	bdStatsFixture := BdStats{}
	bdStatsFixture.Summary.TotalIssues = 100
	bdStatsFixture.Summary.OpenIssues = 40
	bdStatsFixture.Summary.ClosedIssues = 30
	bdStatsFixture.Summary.InProgressIssues = 15
	bdStatsFixture.Summary.BlockedIssues = 5
	bdStatsFixture.Summary.DeferredIssues = 3
	bdStatsFixture.Summary.TombstoneIssues = 2
	bdStatsFixture.Summary.PinnedIssues = 5

	bdStatsJSON, _ := json.Marshal(bdStatsFixture)
	bdB := setupParityBdBackend(t, string(bdStatsJSON))

	fleetStats := types.Statistics{
		TotalIssues:      100,
		OpenIssues:       40,
		ClosedIssues:     30,
		InProgressIssues: 15,
		BlockedIssues:    5,
		DeferredIssues:   3,
		TombstoneIssues:  2,
		PinnedIssues:     5,
		// Extra fields NOT mapped to BdStats:
		ReadyIssues:             20,
		EpicsEligibleForClosure: 2,
		AverageLeadTime:         24.5,
	}
	fleetMock := &mockFleetDBClient{
		statsFn: func() (*rpc.Response, error) {
			return successResp(fleetStats), nil
		},
	}
	fleetB := newFleetDBBackend(fleetMock, "test")

	ctx := context.Background()
	bdResult, err := bdB.Stats(ctx)
	if err != nil {
		t.Fatalf("bdBackend.Stats: %v", err)
	}
	fleetResult, err := fleetB.Stats(ctx)
	if err != nil {
		t.Fatalf("fleetDBBackend.Stats: %v", err)
	}
	assertStatsEqual(t, *bdResult, *fleetResult)
}

func TestBackendParity_Show(t *testing.T) {
	// bdBackend: show returns a single-element JSON array
	singleIssue := []BdIssue{parityExpectedBdIssues[0]}
	bdJSON, _ := json.Marshal(singleIssue)
	bdB := setupParityBdBackend(t, string(bdJSON))

	// fleetDBBackend: show returns IssueDetails
	fleetMock := &mockFleetDBClient{
		showFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			details := types.IssueDetails{
				Issue: types.Issue{
					ID: "PARITY-1", Title: "Parity test issue",
					Status: types.StatusOpen, Priority: 2,
					IssueType: types.TypeTask, Design: "some design",
					Assignee: "alice", SourceRepo: "repo-a",
				},
				Labels: []string{"backend", "urgent"},
				Dependencies: []*types.IssueWithDependencyMetadata{
					{
						Issue: types.Issue{
							ID:        "EPIC-1",
							CreatedAt: parityFixtureTime,
							CreatedBy: "admin",
						},
						DependencyType: types.DepParentChild,
					},
				},
			}
			return successResp(details), nil
		},
	}
	fleetB := newFleetDBBackend(fleetMock, "test")

	ctx := context.Background()
	bdResult, err := bdB.GetIssue(ctx, "PARITY-1")
	if err != nil {
		t.Fatalf("bdBackend.GetIssue: %v", err)
	}
	fleetResult, err := fleetB.GetIssue(ctx, "PARITY-1")
	if err != nil {
		t.Fatalf("fleetDBBackend.GetIssue: %v", err)
	}
	assertBdIssuesEqual(t, "Show", []BdIssue{*bdResult}, []BdIssue{*fleetResult})
}

func TestBackendParity_EmptyResults(t *testing.T) {
	bdB := setupParityBdBackend(t, "[]")

	emptyIssues := []*types.Issue{}
	fleetB := setupParityFleetDBBackend(t, emptyIssues)

	ctx := context.Background()

	bdResult, err := bdB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("bdBackend.Ready: %v", err)
	}
	fleetResult, err := fleetB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("fleetDBBackend.Ready: %v", err)
	}

	// Both must return non-nil empty slices
	if bdResult == nil {
		t.Error("bdBackend returned nil, want empty slice")
	}
	if fleetResult == nil {
		t.Error("fleetDBBackend returned nil, want empty slice")
	}
	if len(bdResult) != 0 {
		t.Errorf("bdBackend returned %d items, want 0", len(bdResult))
	}
	if len(fleetResult) != 0 {
		t.Errorf("fleetDBBackend returned %d items, want 0", len(fleetResult))
	}

	// Verify JSON serialization produces "[]" not "null"
	bdJSON, _ := json.Marshal(bdResult)
	fleetJSON, _ := json.Marshal(fleetResult)
	if string(bdJSON) != "[]" {
		t.Errorf("bdBackend JSON = %s, want []", bdJSON)
	}
	if string(fleetJSON) != "[]" {
		t.Errorf("fleetDBBackend JSON = %s, want []", fleetJSON)
	}
}

func TestBackendParity_NilSlices(t *testing.T) {
	// Issue with nil Labels and nil Dependencies
	nilSliceIssue := []*types.Issue{parityTypesIssues[1]} // PARITY-2: nil labels, nil deps

	fixtureJSON, _ := json.Marshal([]BdIssue{parityExpectedBdIssues[1]})
	bdB := setupParityBdBackend(t, string(fixtureJSON))
	fleetB := setupParityFleetDBBackend(t, nilSliceIssue)

	ctx := context.Background()
	bdResult, err := bdB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("bdBackend.Ready: %v", err)
	}
	fleetResult, err := fleetB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("fleetDBBackend.Ready: %v", err)
	}

	if len(fleetResult) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(fleetResult))
	}
	// fleetDBBackend must convert nil -> empty slice
	if fleetResult[0].Labels == nil {
		t.Error("fleetDBBackend: Labels is nil, want empty slice")
	}
	if fleetResult[0].Dependencies == nil {
		t.Error("fleetDBBackend: Dependencies is nil, want empty slice")
	}

	assertBdIssuesEqual(t, "NilSlices", bdResult, fleetResult)
}

func TestBackendParity_IssueTypeMapping(t *testing.T) {
	// Verify both backends produce "issue_type" field with correct value
	taskIssue := []*types.Issue{{
		ID: "TYPE-1", Title: "Type check", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}}
	expectedBd := []BdIssue{{
		ID: "TYPE-1", Title: "Type check", Status: "open",
		Priority: 1, IssueType: "task",
		Labels: []string{}, Dependencies: []Dependency{},
	}}

	fixtureJSON, _ := json.Marshal(expectedBd)
	bdB := setupParityBdBackend(t, string(fixtureJSON))
	fleetB := setupParityFleetDBBackend(t, taskIssue)

	ctx := context.Background()
	bdResult, err := bdB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("bdBackend: %v", err)
	}
	fleetResult, err := fleetB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("fleetDBBackend: %v", err)
	}

	if bdResult[0].IssueType != "task" {
		t.Errorf("bdBackend.IssueType = %q, want task", bdResult[0].IssueType)
	}
	if fleetResult[0].IssueType != "task" {
		t.Errorf("fleetDBBackend.IssueType = %q, want task", fleetResult[0].IssueType)
	}

	// Verify JSON field name is "issue_type" not "type"
	fleetJSON, _ := json.Marshal(fleetResult[0])
	if !jsonContainsField(string(fleetJSON), "issue_type") {
		t.Error("fleetDBBackend JSON missing 'issue_type' field")
	}
}

func TestBackendParity_DependencyTimeConversion(t *testing.T) {
	// Verify time.Time -> RFC3339 string conversion produces identical results
	fixtureJSON, _ := json.Marshal(parityExpectedBdIssues[:1]) // PARITY-1 with deps
	bdB := setupParityBdBackend(t, string(fixtureJSON))
	fleetB := setupParityFleetDBBackend(t, parityTypesIssues[:1])

	ctx := context.Background()
	bdResult, err := bdB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("bdBackend: %v", err)
	}
	fleetResult, err := fleetB.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("fleetDBBackend: %v", err)
	}

	if len(bdResult[0].Dependencies) != 1 {
		t.Fatalf("bdBackend deps: expected 1, got %d", len(bdResult[0].Dependencies))
	}
	if len(fleetResult[0].Dependencies) != 1 {
		t.Fatalf("fleetDBBackend deps: expected 1, got %d", len(fleetResult[0].Dependencies))
	}

	bdTime := bdResult[0].Dependencies[0].CreatedAt
	fleetTime := fleetResult[0].Dependencies[0].CreatedAt
	expectedTime := "2026-03-14T12:00:00Z"

	if bdTime != expectedTime {
		t.Errorf("bdBackend CreatedAt = %q, want %q", bdTime, expectedTime)
	}
	if fleetTime != expectedTime {
		t.Errorf("fleetDBBackend CreatedAt = %q, want %q", fleetTime, expectedTime)
	}
	if bdTime != fleetTime {
		t.Errorf("CreatedAt mismatch: bd=%q fleet=%q", bdTime, fleetTime)
	}
}
