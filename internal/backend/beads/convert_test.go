package beads

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// ---------------------------------------------------------------------------
// issueToData
// ---------------------------------------------------------------------------

func TestIssueToData_FullyPopulated(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	due := now.Add(24 * time.Hour)
	defer_ := now.Add(12 * time.Hour)

	issue := &types.Issue{
		ID:         "bd-123",
		Title:      "Fix bug",
		Status:     types.StatusOpen,
		Priority:   2,
		IssueType:  types.TypeTask,
		Assignee:   "alice",
		Owner:      "bob",
		Labels:     []string{"urgent", "backend"},
		SourceRepo: "repo-a",
		CreatedAt:  now,
		UpdatedAt:  now.Add(time.Hour),
		DueAt:      &due,
		DeferUntil: &defer_,
	}

	got := issueToData(issue)

	if got.ID != "bd-123" {
		t.Errorf("ID = %q, want %q", got.ID, "bd-123")
	}
	if got.Title != "Fix bug" {
		t.Errorf("Title = %q, want %q", got.Title, "Fix bug")
	}
	if got.Status != "open" {
		t.Errorf("Status = %q, want %q", got.Status, "open")
	}
	if got.Priority != 2 {
		t.Errorf("Priority = %d, want %d", got.Priority, 2)
	}
	if got.IssueType != "task" {
		t.Errorf("IssueType = %q, want %q", got.IssueType, "task")
	}
	if got.Assignee != "alice" {
		t.Errorf("Assignee = %q, want %q", got.Assignee, "alice")
	}
	if got.Owner != "bob" {
		t.Errorf("Owner = %q, want %q", got.Owner, "bob")
	}
	if len(got.Labels) != 2 || got.Labels[0] != "urgent" || got.Labels[1] != "backend" {
		t.Errorf("Labels = %v, want [urgent backend]", got.Labels)
	}
	if got.SourceRepo != "repo-a" {
		t.Errorf("SourceRepo = %q, want %q", got.SourceRepo, "repo-a")
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
	if !got.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, now.Add(time.Hour))
	}
	if got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Errorf("DueAt = %v, want %v", got.DueAt, due)
	}
	if got.DeferUntil == nil || !got.DeferUntil.Equal(defer_) {
		t.Errorf("DeferUntil = %v, want %v", got.DeferUntil, defer_)
	}
}

func TestIssueToData_MinimalIssue(t *testing.T) {
	issue := &types.Issue{
		ID:    "bd-1",
		Title: "Minimal",
	}

	got := issueToData(issue)

	if got.ID != "bd-1" {
		t.Errorf("ID = %q, want %q", got.ID, "bd-1")
	}
	if got.Title != "Minimal" {
		t.Errorf("Title = %q, want %q", got.Title, "Minimal")
	}
	if got.Status != "" {
		t.Errorf("Status = %q, want empty", got.Status)
	}
	if got.Priority != 0 {
		t.Errorf("Priority = %d, want 0", got.Priority)
	}
	if got.DueAt != nil {
		t.Errorf("DueAt = %v, want nil", got.DueAt)
	}
	if got.DeferUntil != nil {
		t.Errorf("DeferUntil = %v, want nil", got.DeferUntil)
	}
}

func TestIssueToData_NilLabels(t *testing.T) {
	issue := &types.Issue{
		ID:     "bd-2",
		Title:  "No labels",
		Labels: nil,
	}

	got := issueToData(issue)

	if got.Labels == nil {
		t.Fatal("Labels should be non-nil empty slice, not nil")
	}
	if len(got.Labels) != 0 {
		t.Errorf("Labels length = %d, want 0", len(got.Labels))
	}
}

func TestIssueToData_EmptyLabels(t *testing.T) {
	issue := &types.Issue{
		ID:     "bd-3",
		Title:  "Empty labels",
		Labels: []string{},
	}

	got := issueToData(issue)

	if got.Labels == nil {
		t.Fatal("Labels should be non-nil empty slice, not nil")
	}
	if len(got.Labels) != 0 {
		t.Errorf("Labels length = %d, want 0", len(got.Labels))
	}
}

// ---------------------------------------------------------------------------
// issueWithCountsToData
// ---------------------------------------------------------------------------

func TestIssueWithCountsToData(t *testing.T) {
	issue := &types.Issue{
		ID:    "bd-10",
		Title: "With counts",
	}
	iwc := &types.IssueWithCounts{
		Issue:           issue,
		DependencyCount: 3,
		DependentCount:  5,
	}

	got := issueWithCountsToData(iwc)

	if got.ID != "bd-10" {
		t.Errorf("ID = %q, want %q", got.ID, "bd-10")
	}
	if got.DependencyCount != 3 {
		t.Errorf("DependencyCount = %d, want 3", got.DependencyCount)
	}
	if got.DependentCount != 5 {
		t.Errorf("DependentCount = %d, want 5", got.DependentCount)
	}
}

// ---------------------------------------------------------------------------
// issuesToData / issuesWithCountsToData (slice helpers)
// ---------------------------------------------------------------------------

func TestIssuesToData(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-a", Title: "A"},
		{ID: "bd-b", Title: "B"},
	}

	got := issuesToData(issues)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "bd-a" {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, "bd-a")
	}
	if got[1].ID != "bd-b" {
		t.Errorf("got[1].ID = %q, want %q", got[1].ID, "bd-b")
	}
}

func TestIssuesToData_Empty(t *testing.T) {
	got := issuesToData(nil)
	if got == nil {
		t.Fatal("should return non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestIssuesWithCountsToData(t *testing.T) {
	issues := []*types.IssueWithCounts{
		{Issue: &types.Issue{ID: "bd-x"}, DependencyCount: 1, DependentCount: 2},
	}

	got := issuesWithCountsToData(issues)

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].DependencyCount != 1 {
		t.Errorf("DependencyCount = %d, want 1", got[0].DependencyCount)
	}
}

// ---------------------------------------------------------------------------
// detailsToDetailData
// ---------------------------------------------------------------------------

func TestDetailsToDetailData_FullyPopulated(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	closedAt := now.Add(-time.Hour)
	parent := "bd-parent"
	extRef := "gh-42"

	details := &types.IssueDetails{
		Issue: types.Issue{
			ID:                 "bd-100",
			Title:              "Detailed issue",
			Description:        "Full description",
			Design:             "Design doc",
			AcceptanceCriteria: "Must pass",
			Notes:              "Some notes",
			Status:             types.StatusClosed,
			Priority:           1,
			IssueType:          types.TypeBug,
			Assignee:           "alice",
			CreatedAt:          now,
			UpdatedAt:          now,
			CreatedBy:          "bob",
			ClosedAt:           &closedAt,
			CloseReason:        "fixed",
			ClosedBySession:    "session-1",
			ExternalRef:        &extRef,
			EstimatedMinutes:   intPtr(60),
		},
		Labels: []string{"v1", "critical"},
		Parent: &parent,
		Dependencies: []*types.IssueWithDependencyMetadata{
			{
				Issue: types.Issue{
					ID:        "bd-dep-1",
					Title:     "Dep 1",
					Status:    types.StatusOpen,
					Priority:  2,
					IssueType: types.TypeTask,
					CreatedAt: now,
					CreatedBy: "charlie",
				},
				DependencyType: types.DepBlocks,
			},
		},
		Dependents: []*types.IssueWithDependencyMetadata{
			{
				Issue: types.Issue{
					ID:        "bd-dep-2",
					Title:     "Dependent 1",
					Status:    types.StatusInProgress,
					Priority:  3,
					IssueType: types.TypeFeature,
					CreatedAt: now,
				},
				DependencyType: types.DepParentChild,
			},
		},
		Comments: []*types.Comment{
			{ID: 1, IssueID: "bd-100", Author: "alice", Text: "Hello", CreatedAt: now},
		},
	}

	got := detailsToDetailData(details)

	// IssueData fields
	if got.ID != "bd-100" {
		t.Errorf("ID = %q, want %q", got.ID, "bd-100")
	}
	if got.IssueData.Parent != "bd-parent" {
		t.Errorf("Parent = %q, want %q", got.IssueData.Parent, "bd-parent")
	}

	// Content fields
	if got.Description != "Full description" {
		t.Errorf("Description = %q, want %q", got.Description, "Full description")
	}
	if got.Design != "Design doc" {
		t.Errorf("Design = %q, want %q", got.Design, "Design doc")
	}
	if got.AcceptanceCriteria != "Must pass" {
		t.Errorf("AcceptanceCriteria = %q, want %q", got.AcceptanceCriteria, "Must pass")
	}
	if got.Notes != "Some notes" {
		t.Errorf("Notes = %q, want %q", got.Notes, "Some notes")
	}

	// Lifecycle
	if got.CreatedBy != "bob" {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, "bob")
	}
	if got.ClosedAt == nil || !got.ClosedAt.Equal(closedAt) {
		t.Errorf("ClosedAt = %v, want %v", got.ClosedAt, closedAt)
	}
	if got.CloseReason != "fixed" {
		t.Errorf("CloseReason = %q, want %q", got.CloseReason, "fixed")
	}
	if got.ClosedBySession != "session-1" {
		t.Errorf("ClosedBySession = %q, want %q", got.ClosedBySession, "session-1")
	}

	// External integration
	if got.ExternalRef != "gh-42" {
		t.Errorf("ExternalRef = %q, want %q", got.ExternalRef, "gh-42")
	}
	if got.EstimatedMinutes == nil || *got.EstimatedMinutes != 60 {
		t.Errorf("EstimatedMinutes = %v, want 60", got.EstimatedMinutes)
	}

	// Labels (should use details.Labels, not issue.Labels)
	if len(got.IssueData.Labels) != 2 || got.IssueData.Labels[0] != "v1" {
		t.Errorf("Labels = %v, want [v1 critical]", got.IssueData.Labels)
	}

	// Dependencies
	if len(got.Dependencies) != 1 {
		t.Fatalf("Dependencies len = %d, want 1", len(got.Dependencies))
	}
	dep := got.Dependencies[0]
	if dep.IssueID != "bd-100" {
		t.Errorf("dep.IssueID = %q, want %q", dep.IssueID, "bd-100")
	}
	if dep.DependsOnID != "bd-dep-1" {
		t.Errorf("dep.DependsOnID = %q, want %q", dep.DependsOnID, "bd-dep-1")
	}
	if dep.Type != "blocks" {
		t.Errorf("dep.Type = %q, want %q", dep.Type, "blocks")
	}

	// Dependents
	if len(got.Dependents) != 1 {
		t.Fatalf("Dependents len = %d, want 1", len(got.Dependents))
	}
	dependent := got.Dependents[0]
	if dependent.IssueID != "bd-dep-2" {
		t.Errorf("dependent.IssueID = %q, want %q", dependent.IssueID, "bd-dep-2")
	}

	// Comments
	if len(got.Comments) != 1 {
		t.Fatalf("Comments len = %d, want 1", len(got.Comments))
	}
	if got.Comments[0].Author != "alice" {
		t.Errorf("Comments[0].Author = %q, want %q", got.Comments[0].Author, "alice")
	}
}

func TestDetailsToDetailData_AllNil(t *testing.T) {
	details := &types.IssueDetails{
		Issue: types.Issue{
			ID:    "bd-200",
			Title: "Bare issue",
		},
		Labels:       nil,
		Dependencies: nil,
		Dependents:   nil,
		Comments:     nil,
		Parent:       nil,
	}

	got := detailsToDetailData(details)

	if got.IssueData.Parent != "" {
		t.Errorf("Parent = %q, want empty", got.IssueData.Parent)
	}
	if got.ExternalRef != "" {
		t.Errorf("ExternalRef = %q, want empty", got.ExternalRef)
	}

	// Nil slices should produce non-nil empty slices
	if got.IssueData.Labels == nil {
		t.Error("Labels should be non-nil empty slice")
	}
	if got.Dependencies == nil {
		t.Error("Dependencies should be non-nil empty slice")
	}
	if got.Dependents == nil {
		t.Error("Dependents should be non-nil empty slice")
	}
	if got.Comments == nil {
		t.Error("Comments should be non-nil empty slice")
	}
}

func TestDetailsToDetailData_NilDependencyEntries(t *testing.T) {
	details := &types.IssueDetails{
		Issue: types.Issue{ID: "bd-300", Title: "Test"},
		Dependencies: []*types.IssueWithDependencyMetadata{
			nil,
			{
				Issue:          types.Issue{ID: "bd-dep-a", Title: "Real dep"},
				DependencyType: types.DepBlocks,
			},
			nil,
		},
		Dependents: []*types.IssueWithDependencyMetadata{nil},
		Comments:   []*types.Comment{nil},
	}

	got := detailsToDetailData(details)

	if len(got.Dependencies) != 1 {
		t.Errorf("Dependencies len = %d, want 1 (nils should be skipped)", len(got.Dependencies))
	}
	if len(got.Dependents) != 0 {
		t.Errorf("Dependents len = %d, want 0 (nils should be skipped)", len(got.Dependents))
	}
	if len(got.Comments) != 0 {
		t.Errorf("Comments len = %d, want 0 (nils should be skipped)", len(got.Comments))
	}
}

// ---------------------------------------------------------------------------
// statisticsToStatsData
// ---------------------------------------------------------------------------

func TestStatisticsToStatsData(t *testing.T) {
	stats := &types.Statistics{
		TotalIssues:             100,
		OpenIssues:              40,
		InProgressIssues:        20,
		ClosedIssues:            30,
		BlockedIssues:           5,
		DeferredIssues:          3,
		ReadyIssues:             15,
		TombstoneIssues:         2,
		PinnedIssues:            4,
		EpicsEligibleForClosure: 1,
		AverageLeadTime:         48.5,
	}

	got := statisticsToStatsData(stats)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"TotalIssues", got.TotalIssues, 100},
		{"OpenIssues", got.OpenIssues, 40},
		{"InProgressIssues", got.InProgressIssues, 20},
		{"ClosedIssues", got.ClosedIssues, 30},
		{"BlockedIssues", got.BlockedIssues, 5},
		{"DeferredIssues", got.DeferredIssues, 3},
		{"ReadyIssues", got.ReadyIssues, 15},
		{"TombstoneIssues", got.TombstoneIssues, 2},
		{"PinnedIssues", got.PinnedIssues, 4},
		{"EpicsEligibleForClosure", got.EpicsEligibleForClosure, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if got.AverageLeadTime != 48.5 {
		t.Errorf("AverageLeadTime = %f, want 48.5", got.AverageLeadTime)
	}
}

func TestStatisticsToStatsData_ZeroValues(t *testing.T) {
	stats := &types.Statistics{}
	got := statisticsToStatsData(stats)

	if got.TotalIssues != 0 {
		t.Errorf("TotalIssues = %d, want 0", got.TotalIssues)
	}
	if got.AverageLeadTime != 0.0 {
		t.Errorf("AverageLeadTime = %f, want 0.0", got.AverageLeadTime)
	}
}

// ---------------------------------------------------------------------------
// commentToData
// ---------------------------------------------------------------------------

func TestCommentToData(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	c := &types.Comment{
		ID:        42,
		IssueID:   "bd-5",
		Author:    "alice",
		Text:      "Looks good",
		CreatedAt: now,
	}

	got := commentToData(c)

	if got.ID != 42 {
		t.Errorf("ID = %d, want 42", got.ID)
	}
	if got.IssueID != "bd-5" {
		t.Errorf("IssueID = %q, want %q", got.IssueID, "bd-5")
	}
	if got.Author != "alice" {
		t.Errorf("Author = %q, want %q", got.Author, "alice")
	}
	if got.Text != "Looks good" {
		t.Errorf("Text = %q, want %q", got.Text, "Looks good")
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
}

// ---------------------------------------------------------------------------
// eventToData
// ---------------------------------------------------------------------------

func TestEventToData(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	e := &types.Event{
		ID:        9876543210,
		IssueID:   "bd-7",
		EventType: types.EventCreated,
		Actor:     "bob",
		CreatedAt: now,
	}

	got := eventToData(e)

	if got.ID != "9876543210" {
		t.Errorf("ID = %q, want %q", got.ID, "9876543210")
	}
	if got.IssueID != "bd-7" {
		t.Errorf("IssueID = %q, want %q", got.IssueID, "bd-7")
	}
	if got.Kind != "issue.created" {
		t.Errorf("Kind = %q, want %q", got.Kind, "issue.created")
	}
	if got.Actor != "bob" {
		t.Errorf("Actor = %q, want %q", got.Actor, "bob")
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
	// Target and Payload should be empty (not populated by current daemon)
	if got.Target != "" {
		t.Errorf("Target = %q, want empty", got.Target)
	}
	if got.Payload != "" {
		t.Errorf("Payload = %q, want empty", got.Payload)
	}
}

func TestEventToData_IDConversion(t *testing.T) {
	tests := []struct {
		name   string
		id     int64
		wantID string
	}{
		{"zero", 0, "0"},
		{"positive", 1, "1"},
		{"large", 9223372036854775807, "9223372036854775807"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &types.Event{ID: tt.id}
			got := eventToData(e)
			if got.ID != tt.wantID {
				t.Errorf("ID = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// closeResultToData
// ---------------------------------------------------------------------------

func TestCloseResultToData_WithUnblocked(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cr := &rpc.CloseResult{
		Closed: &types.Issue{
			ID:        "bd-closed",
			Title:     "Closed issue",
			Status:    types.StatusClosed,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Unblocked: []*types.Issue{
			{
				ID:        "bd-unblocked-1",
				Title:     "Unblocked 1",
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "bd-unblocked-2",
				Title:     "Unblocked 2",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	got := closeResultToData(cr)

	if got.Closed == nil {
		t.Fatal("Closed should not be nil")
	}
	if got.Closed.ID != "bd-closed" {
		t.Errorf("Closed.ID = %q, want %q", got.Closed.ID, "bd-closed")
	}
	if len(got.Unblocked) != 2 {
		t.Fatalf("Unblocked len = %d, want 2", len(got.Unblocked))
	}
	if got.Unblocked[0].ID != "bd-unblocked-1" {
		t.Errorf("Unblocked[0].ID = %q, want %q", got.Unblocked[0].ID, "bd-unblocked-1")
	}
}

func TestCloseResultToData_NilClosed(t *testing.T) {
	cr := &rpc.CloseResult{
		Closed:    nil,
		Unblocked: nil,
	}

	got := closeResultToData(cr)

	if got.Closed != nil {
		t.Errorf("Closed = %v, want nil", got.Closed)
	}
	if got.Unblocked == nil {
		t.Fatal("Unblocked should be non-nil empty slice")
	}
	if len(got.Unblocked) != 0 {
		t.Errorf("Unblocked len = %d, want 0", len(got.Unblocked))
	}
}

func TestCloseResultToData_NilUnblockedEntries(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cr := &rpc.CloseResult{
		Closed: &types.Issue{ID: "bd-c", Title: "C", CreatedAt: now, UpdatedAt: now},
		Unblocked: []*types.Issue{
			nil,
			{ID: "bd-u1", Title: "U1", CreatedAt: now, UpdatedAt: now},
			nil,
		},
	}

	got := closeResultToData(cr)

	if len(got.Unblocked) != 1 {
		t.Errorf("Unblocked len = %d, want 1 (nils should be skipped)", len(got.Unblocked))
	}
}

// ---------------------------------------------------------------------------
// mutationToData / mutationsToData
// ---------------------------------------------------------------------------

func TestMutationToData(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	m := &rpc.MutationEvent{
		Type:       rpc.MutationCreate,
		IssueID:    "bd-50",
		Title:      "New issue",
		Assignee:   "alice",
		Actor:      "bob",
		Timestamp:  now,
		OldStatus:  "",
		NewStatus:  "open",
		ParentID:   "bd-parent",
		SourceRepo: "repo-x",
		StepCount:  3,
	}

	got := mutationToData(m)

	if got.Type != "create" {
		t.Errorf("Type = %q, want %q", got.Type, "create")
	}
	if got.IssueID != "bd-50" {
		t.Errorf("IssueID = %q, want %q", got.IssueID, "bd-50")
	}
	if got.Title != "New issue" {
		t.Errorf("Title = %q, want %q", got.Title, "New issue")
	}
	if got.Assignee != "alice" {
		t.Errorf("Assignee = %q, want %q", got.Assignee, "alice")
	}
	if got.Actor != "bob" {
		t.Errorf("Actor = %q, want %q", got.Actor, "bob")
	}
	if !got.Timestamp.Equal(now) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, now)
	}
	if got.OldStatus != "" {
		t.Errorf("OldStatus = %q, want empty", got.OldStatus)
	}
	if got.NewStatus != "open" {
		t.Errorf("NewStatus = %q, want %q", got.NewStatus, "open")
	}
	if got.ParentID != "bd-parent" {
		t.Errorf("ParentID = %q, want %q", got.ParentID, "bd-parent")
	}
	if got.SourceRepo != "repo-x" {
		t.Errorf("SourceRepo = %q, want %q", got.SourceRepo, "repo-x")
	}
	if got.StepCount != 3 {
		t.Errorf("StepCount = %d, want 3", got.StepCount)
	}
}

func TestMutationsToData(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mutations := []rpc.MutationEvent{
		{Type: rpc.MutationCreate, IssueID: "bd-1", Timestamp: now},
		{Type: rpc.MutationUpdate, IssueID: "bd-2", Timestamp: now},
	}

	got := mutationsToData(mutations)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].IssueID != "bd-1" {
		t.Errorf("got[0].IssueID = %q, want %q", got[0].IssueID, "bd-1")
	}
	if got[1].Type != "update" {
		t.Errorf("got[1].Type = %q, want %q", got[1].Type, "update")
	}
}

func TestMutationsToData_Empty(t *testing.T) {
	got := mutationsToData(nil)
	if got == nil {
		t.Fatal("should return non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// ---------------------------------------------------------------------------
// dependencyMetaToData
// ---------------------------------------------------------------------------

func TestDependencyMetaToData(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	iwdm := &types.IssueWithDependencyMetadata{
		Issue: types.Issue{
			ID:        "bd-dep-target",
			Title:     "Target issue",
			Status:    types.StatusOpen,
			Priority:  1,
			IssueType: types.TypeTask,
			CreatedAt: now,
			CreatedBy: "alice",
		},
		DependencyType: types.DepBlocks,
	}

	got := dependencyMetaToData("bd-owner", iwdm)

	if got.IssueID != "bd-owner" {
		t.Errorf("IssueID = %q, want %q", got.IssueID, "bd-owner")
	}
	if got.DependsOnID != "bd-dep-target" {
		t.Errorf("DependsOnID = %q, want %q", got.DependsOnID, "bd-dep-target")
	}
	if got.Type != "blocks" {
		t.Errorf("Type = %q, want %q", got.Type, "blocks")
	}
	if got.Title != "Target issue" {
		t.Errorf("Title = %q, want %q", got.Title, "Target issue")
	}
	if got.Status != "open" {
		t.Errorf("Status = %q, want %q", got.Status, "open")
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
	if got.IssueType != "task" {
		t.Errorf("IssueType = %q, want %q", got.IssueType, "task")
	}
	if got.CreatedBy != "alice" {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, "alice")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func intPtr(v int) *int {
	return &v
}

// Verify return type satisfies backend types (compile-time checks).
var _ backend.IssueData = issueToData(&types.Issue{})
var _ backend.IssueDetailData = detailsToDetailData(&types.IssueDetails{})
var _ backend.StatsData = statisticsToStatsData(&types.Statistics{})
var _ backend.CommentData = commentToData(&types.Comment{})
var _ backend.EventData = eventToData(&types.Event{})
var _ backend.MutationData = mutationToData(&rpc.MutationEvent{})
