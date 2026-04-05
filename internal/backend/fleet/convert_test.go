package fleet

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestIssueToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issue := &types.Issue{
		ID:         "test-123",
		Title:      "Test Issue",
		Status:     types.StatusOpen,
		Priority:   2,
		IssueType:  types.TypeTask,
		Assignee:   "agent-1",
		Owner:      "owner@example.com",
		Labels:     []string{"bug", "urgent"},
		SourceRepo: "repo-1",
		Design:     "some design",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	d := issueToData(issue)

	if d.ID != "test-123" {
		t.Errorf("ID = %q, want %q", d.ID, "test-123")
	}
	if d.Status != "open" {
		t.Errorf("Status = %q, want %q", d.Status, "open")
	}
	if d.Priority != 2 {
		t.Errorf("Priority = %d, want %d", d.Priority, 2)
	}
	if d.IssueType != "task" {
		t.Errorf("IssueType = %q, want %q", d.IssueType, "task")
	}
	if len(d.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(d.Labels))
	}
	if d.Design != "some design" {
		t.Errorf("Design = %q, want %q", d.Design, "some design")
	}
}

func TestIssueToData_NilLabels(t *testing.T) {
	issue := &types.Issue{ID: "test-1", Title: "X", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	d := issueToData(issue)
	if d.Labels == nil {
		t.Error("Labels should be empty slice, not nil")
	}
	if len(d.Labels) != 0 {
		t.Errorf("Labels len = %d, want 0", len(d.Labels))
	}
}

func TestIssueWithCountsToData(t *testing.T) {
	issue := &types.Issue{ID: "test-1", Title: "X", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	iwc := &types.IssueWithCounts{
		Issue:           issue,
		DependencyCount: 3,
		DependentCount:  1,
	}

	d := issueWithCountsToData(iwc)
	if d.DependencyCount != 3 {
		t.Errorf("DependencyCount = %d, want 3", d.DependencyCount)
	}
	if d.DependentCount != 1 {
		t.Errorf("DependentCount = %d, want 1", d.DependentCount)
	}
}

func TestIssueWithCountsToData_NilIssue(t *testing.T) {
	iwc := &types.IssueWithCounts{DependencyCount: 5, DependentCount: 2}
	d := issueWithCountsToData(iwc)
	if d.DependencyCount != 5 {
		t.Errorf("DependencyCount = %d, want 5", d.DependencyCount)
	}
}

func TestDetailsToDetailData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "epic-1"
	extRef := "gh-42"
	details := &types.IssueDetails{
		Issue: types.Issue{
			ID:          "test-1",
			Title:       "Test",
			Status:      types.StatusInProgress,
			Priority:    1,
			IssueType:   types.TypeTask,
			Description: "desc",
			Design:      "design",
			CreatedAt:   now,
			UpdatedAt:   now,
			CreatedBy:   "user",
			ExternalRef: &extRef,
		},
		Labels: []string{"label-1"},
		Parent: &parent,
		Dependencies: []*types.IssueWithDependencyMetadata{
			{
				Issue:          types.Issue{ID: "dep-1", Title: "Dep", Status: types.StatusOpen, CreatedAt: now},
				DependencyType: types.DepBlocks,
			},
		},
		Dependents: []*types.IssueWithDependencyMetadata{
			{
				Issue:          types.Issue{ID: "child-1", Title: "Child", Status: types.StatusOpen, CreatedAt: now},
				DependencyType: types.DepBlocks,
			},
		},
		Comments: []*types.Comment{
			{ID: 1, IssueID: "test-1", Author: "user", Text: "hello", CreatedAt: now},
		},
	}

	d := detailsToDetailData(details)

	if d.ID != "test-1" {
		t.Errorf("ID = %q, want %q", d.ID, "test-1")
	}
	if d.Description != "desc" {
		t.Errorf("Description = %q, want %q", d.Description, "desc")
	}
	if d.Design != "design" {
		t.Errorf("Design = %q, want %q", d.Design, "design")
	}
	if d.IssueData.Parent != "epic-1" {
		t.Errorf("Parent = %q, want %q", d.IssueData.Parent, "epic-1")
	}
	if d.ExternalRef != "gh-42" {
		t.Errorf("ExternalRef = %q, want %q", d.ExternalRef, "gh-42")
	}
	if len(d.Dependencies) != 1 {
		t.Fatalf("Dependencies len = %d, want 1", len(d.Dependencies))
	}
	if d.Dependencies[0].DependsOnID != "dep-1" {
		t.Errorf("dep DependsOnID = %q, want %q", d.Dependencies[0].DependsOnID, "dep-1")
	}
	if len(d.Dependents) != 1 {
		t.Fatalf("Dependents len = %d, want 1", len(d.Dependents))
	}
	if d.Dependents[0].IssueID != "child-1" {
		t.Errorf("dependent IssueID = %q, want %q", d.Dependents[0].IssueID, "child-1")
	}
	if len(d.Comments) != 1 {
		t.Fatalf("Comments len = %d, want 1", len(d.Comments))
	}
	if d.Comments[0].Text != "hello" {
		t.Errorf("Comment text = %q, want %q", d.Comments[0].Text, "hello")
	}
}

func TestStatisticsToStatsData(t *testing.T) {
	stats := &types.Statistics{
		TotalIssues:             50,
		OpenIssues:              10,
		InProgressIssues:        5,
		ClosedIssues:            20,
		BlockedIssues:           3,
		DeferredIssues:          2,
		ReadyIssues:             7,
		TombstoneIssues:         1,
		PinnedIssues:            2,
		EpicsEligibleForClosure: 1,
		AverageLeadTime:         24.5,
	}

	d := statisticsToStatsData(stats)

	if d.TotalIssues != 50 {
		t.Errorf("TotalIssues = %d, want 50", d.TotalIssues)
	}
	if d.OpenIssues != 10 {
		t.Errorf("OpenIssues = %d, want 10", d.OpenIssues)
	}
	if d.ReadyIssues != 7 {
		t.Errorf("ReadyIssues = %d, want 7", d.ReadyIssues)
	}
	if d.AverageLeadTime != 24.5 {
		t.Errorf("AverageLeadTime = %f, want 24.5", d.AverageLeadTime)
	}
}

func TestCommentToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c := &types.Comment{
		ID:        42,
		IssueID:   "test-1",
		Author:    "user",
		Text:      "hello world",
		CreatedAt: now,
	}

	d := commentToData(c)
	if d.ID != 42 {
		t.Errorf("ID = %d, want 42", d.ID)
	}
	if d.Text != "hello world" {
		t.Errorf("Text = %q, want %q", d.Text, "hello world")
	}
}

func TestEventToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	e := &types.Event{
		ID:        99,
		IssueID:   "test-1",
		EventType: types.EventCreated,
		Actor:     "user",
		CreatedAt: now,
	}

	d := eventToData(e)
	if d.ID != "99" {
		t.Errorf("ID = %q, want %q", d.ID, "99")
	}
	if d.Kind != "issue.created" {
		t.Errorf("Kind = %q, want %q", d.Kind, "issue.created")
	}
}

func TestReadyIssuesToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "epic-1"
	issues := []*readyIssueWithParent{
		{
			Issue:  &types.Issue{ID: "test-1", Title: "Ready 1", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
			Parent: &parent,
		},
		{
			Issue: &types.Issue{ID: "test-2", Title: "Ready 2", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
		},
		nil, // should be skipped
	}

	result := readyIssuesToData(issues)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Parent != "epic-1" {
		t.Errorf("Parent = %q, want %q", result[0].Parent, "epic-1")
	}
	if result[1].Parent != "" {
		t.Errorf("Parent = %q, want empty", result[1].Parent)
	}
}

func TestBlockedIssuesToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issues := []*types.BlockedIssue{
		{
			Issue:          types.Issue{ID: "test-1", Title: "Blocked", Status: types.StatusBlocked, CreatedAt: now, UpdatedAt: now},
			BlockedBy:      []string{"dep-1"},
			BlockedByCount: 1,
		},
		nil, // should be skipped
	}

	result := blockedIssuesToData(issues)
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].ID != "test-1" {
		t.Errorf("ID = %q, want %q", result[0].ID, "test-1")
	}
}

func TestCloseResultJSONToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cr := &closeResultJSON{
		Closed: &types.Issue{ID: "closed-1", Title: "Done", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now},
		Unblocked: []*types.Issue{
			{ID: "unblocked-1", Title: "Free", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
		},
	}

	result := closeResultJSONToData(cr)
	if result.Closed == nil {
		t.Fatal("Closed should not be nil")
	}
	if result.Closed.ID != "closed-1" {
		t.Errorf("Closed.ID = %q, want %q", result.Closed.ID, "closed-1")
	}
	if len(result.Unblocked) != 1 {
		t.Fatalf("Unblocked len = %d, want 1", len(result.Unblocked))
	}
	if result.Unblocked[0].ID != "unblocked-1" {
		t.Errorf("Unblocked[0].ID = %q, want %q", result.Unblocked[0].ID, "unblocked-1")
	}
}

func TestCloseResultJSONToData_NilClosed(t *testing.T) {
	cr := &closeResultJSON{}
	result := closeResultJSONToData(cr)
	if result.Closed != nil {
		t.Error("Closed should be nil")
	}
	if result.Unblocked == nil {
		t.Error("Unblocked should be empty slice, not nil")
	}
}
