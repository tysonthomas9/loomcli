package api

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

func timePtr(t time.Time) *time.Time { return &t }

// --- issueToData ---

func TestIssueToData_AllNilPointers(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issue := gen.Issue{
		Id:        "a",
		Title:     "A",
		Priority:  3,
		CreatedAt: now,
		UpdatedAt: now,
	}
	d := issueToData(issue)
	if d.ID != "a" || d.Title != "A" || d.Priority != 3 {
		t.Errorf("basic fields mismatch: %+v", d)
	}
	if d.Status != "" {
		t.Errorf("Status should be empty, got %q", d.Status)
	}
	if d.IssueType != "" {
		t.Errorf("IssueType should be empty")
	}
	if d.Assignee != "" {
		t.Errorf("Assignee should be empty")
	}
	if d.Owner != "" {
		t.Errorf("Owner should be empty")
	}
	if d.Labels == nil {
		t.Errorf("Labels should be empty slice, not nil")
	}
	if len(d.Labels) != 0 {
		t.Errorf("Labels should be empty")
	}
	if d.SourceRepo != "" {
		t.Errorf("SourceRepo should be empty")
	}
	if d.Parent != "" {
		t.Errorf("Parent should be empty")
	}
	if d.Design != "" {
		t.Errorf("Design should be empty")
	}
	if d.DueAt != nil {
		t.Errorf("DueAt should be nil")
	}
	if d.DeferUntil != nil {
		t.Errorf("DeferUntil should be nil")
	}
}

func TestIssueToData_AllFieldsSet(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	status := gen.IssueStatus("open")
	issueType := gen.IssueIssueType("task")
	assignee := "alice"
	owner := "bob"
	labels := []string{"x", "y"}
	repo := "r1"
	parent := "p1"
	design := "design"
	due := now.Add(24 * time.Hour)
	defer_ := now.Add(12 * time.Hour)

	issue := gen.Issue{
		Id:         "loom-1",
		Title:      "Task",
		Status:     &status,
		IssueType:  &issueType,
		Priority:   2,
		Assignee:   &assignee,
		Owner:      &owner,
		Labels:     &labels,
		SourceRepo: &repo,
		Parent:     &parent,
		Design:     &design,
		CreatedAt:  now,
		UpdatedAt:  now,
		DueAt:      &due,
		DeferUntil: &defer_,
	}
	d := issueToData(issue)
	if d.Status != "open" {
		t.Errorf("Status = %q", d.Status)
	}
	if d.IssueType != "task" {
		t.Errorf("IssueType = %q", d.IssueType)
	}
	if d.Assignee != "alice" {
		t.Errorf("Assignee = %q", d.Assignee)
	}
	if d.Owner != "bob" {
		t.Errorf("Owner = %q", d.Owner)
	}
	if len(d.Labels) != 2 || d.Labels[0] != "x" || d.Labels[1] != "y" {
		t.Errorf("Labels = %v", d.Labels)
	}
	if d.SourceRepo != "r1" {
		t.Errorf("SourceRepo = %q", d.SourceRepo)
	}
	if d.Parent != "p1" {
		t.Errorf("Parent = %q", d.Parent)
	}
	if d.Design != "design" {
		t.Errorf("Design = %q", d.Design)
	}
	if d.DueAt == nil || !d.DueAt.Equal(due) {
		t.Errorf("DueAt = %v", d.DueAt)
	}
	if d.DeferUntil == nil || !d.DeferUntil.Equal(defer_) {
		t.Errorf("DeferUntil = %v", d.DeferUntil)
	}
}

// --- issueResponseToData ---

func TestIssueResponseToData_Minimal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := gen.IssueResponse{
		Id:              "loom-1",
		Title:           "Title",
		Status:          gen.IssueResponseStatus("open"),
		Priority:        1,
		IssueType:       gen.IssueResponseIssueType("task"),
		CreatedAt:       now,
		UpdatedAt:       now,
		Labels:          nil,
		DependencyCount: 3,
		DependentCount:  2,
	}
	d := issueResponseToData(r)
	if d.ID != "loom-1" || d.Title != "Title" {
		t.Errorf("basic: %+v", d)
	}
	if d.Status != "open" || d.IssueType != "task" {
		t.Errorf("enum: %+v", d)
	}
	if d.DependencyCount != 3 || d.DependentCount != 2 {
		t.Errorf("counts: %d %d", d.DependencyCount, d.DependentCount)
	}
	if d.Labels == nil {
		t.Errorf("Labels should be non-nil empty slice")
	}
	if len(d.Labels) != 0 {
		t.Errorf("Labels should be empty")
	}
	if d.Assignee != "" || d.Owner != "" {
		t.Errorf("should be empty")
	}
}

func TestIssueResponseToData_AllFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	assignee := "a"
	owner := "o"
	repo := "r"
	parent := "p"
	design := "d"
	due := now.Add(time.Hour)
	defer_ := now.Add(2 * time.Hour)
	r := gen.IssueResponse{
		Id:         "loom-2",
		Title:      "Full",
		Status:     gen.IssueResponseStatus("in_progress"),
		IssueType:  gen.IssueResponseIssueType("bug"),
		Priority:   0,
		Assignee:   &assignee,
		Owner:      &owner,
		Labels:     []string{"l1"},
		SourceRepo: &repo,
		Parent:     &parent,
		Design:     &design,
		CreatedAt:  now,
		UpdatedAt:  now,
		DueAt:      &due,
		DeferUntil: &defer_,
	}
	d := issueResponseToData(r)
	if d.Assignee != "a" || d.Owner != "o" || d.SourceRepo != "r" {
		t.Errorf("string ptrs: %+v", d)
	}
	if d.Parent != "p" || d.Design != "d" {
		t.Errorf("p/d: %+v", d)
	}
	if len(d.Labels) != 1 || d.Labels[0] != "l1" {
		t.Errorf("labels: %v", d.Labels)
	}
	if d.DueAt == nil || !d.DueAt.Equal(due) {
		t.Errorf("DueAt: %v", d.DueAt)
	}
	if d.DeferUntil == nil || !d.DeferUntil.Equal(defer_) {
		t.Errorf("DeferUntil: %v", d.DeferUntil)
	}
}

// --- issueResponseToDetailData ---

func TestIssueResponseToDetailData_AllFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	desc := "desc"
	ac := "acceptance"
	notes := "notes"
	closedAt := now.Add(-time.Hour)
	closeReason := "done"
	externalRef := "EXT-1"
	est := 60

	r := gen.IssueResponse{
		Id:                 "loom-3",
		Title:              "Detail",
		Status:             gen.IssueResponseStatus("closed"),
		IssueType:          gen.IssueResponseIssueType("task"),
		Priority:           1,
		CreatedAt:          now,
		UpdatedAt:          now,
		Description:        &desc,
		AcceptanceCriteria: &ac,
		Notes:              &notes,
		ClosedAt:           &closedAt,
		CloseReason:        &closeReason,
		ExternalRef:        &externalRef,
		EstimatedMinutes:   &est,
		Dependencies: []gen.DependencyRef{
			{Id: "dep-1", Type: "blocks", Title: "dep 1", Status: "open", Priority: 2, IssueType: "task"},
		},
		Dependents: []gen.DependencyRef{
			{Id: "dep-2", Type: "blocks", Title: "dep 2", Status: "open", Priority: 1, IssueType: "bug"},
		},
		Comments: []gen.CommentResponse{
			{Id: 10, Author: "alice", Text: "hi", CreatedAt: now},
		},
	}
	detail := issueResponseToDetailData(r)
	if detail.ID != "loom-3" {
		t.Errorf("ID = %q", detail.ID)
	}
	if detail.Description != "desc" || detail.AcceptanceCriteria != "acceptance" || detail.Notes != "notes" {
		t.Errorf("content: %+v", detail)
	}
	if detail.ClosedAt == nil || !detail.ClosedAt.Equal(closedAt) {
		t.Errorf("ClosedAt: %v", detail.ClosedAt)
	}
	if detail.CloseReason != "done" {
		t.Errorf("CloseReason = %q", detail.CloseReason)
	}
	if detail.ExternalRef != "EXT-1" {
		t.Errorf("ExternalRef = %q", detail.ExternalRef)
	}
	if detail.EstimatedMinutes == nil || *detail.EstimatedMinutes != 60 {
		t.Errorf("EstimatedMinutes = %v", detail.EstimatedMinutes)
	}
	if len(detail.Dependencies) != 1 {
		t.Errorf("Dependencies len = %d", len(detail.Dependencies))
	}
	if detail.Dependencies[0].IssueID != "loom-3" || detail.Dependencies[0].DependsOnID != "dep-1" {
		t.Errorf("dep direction: %+v", detail.Dependencies[0])
	}
	if len(detail.Dependents) != 1 {
		t.Errorf("Dependents len = %d", len(detail.Dependents))
	}
	if detail.Dependents[0].IssueID != "dep-2" || detail.Dependents[0].DependsOnID != "loom-3" {
		t.Errorf("dependent direction: %+v", detail.Dependents[0])
	}
	if len(detail.Comments) != 1 {
		t.Errorf("Comments len = %d", len(detail.Comments))
	}
	if detail.Comments[0].IssueID != "loom-3" {
		t.Errorf("comment IssueID = %q", detail.Comments[0].IssueID)
	}
}

func TestIssueResponseToDetailData_NilOptionalFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := gen.IssueResponse{
		Id:        "loom-4",
		Title:     "Bare",
		Status:    gen.IssueResponseStatus("open"),
		IssueType: gen.IssueResponseIssueType("task"),
		CreatedAt: now,
		UpdatedAt: now,
	}
	detail := issueResponseToDetailData(r)
	if detail.Description != "" || detail.AcceptanceCriteria != "" || detail.Notes != "" {
		t.Errorf("content should be empty: %+v", detail)
	}
	if detail.ClosedAt != nil {
		t.Errorf("ClosedAt should be nil")
	}
	if detail.CloseReason != "" || detail.ExternalRef != "" {
		t.Errorf("strings should be empty")
	}
	if detail.EstimatedMinutes != nil {
		t.Errorf("EstimatedMinutes should be nil")
	}
	if detail.Comments == nil {
		t.Errorf("Comments should be non-nil empty")
	}
	if len(detail.Comments) != 0 {
		t.Errorf("Comments should be empty")
	}
}

// --- dependencyRefsToData ---

func TestDependencyRefsToData_Outgoing(t *testing.T) {
	refs := []gen.DependencyRef{
		{Id: "dep-1", Type: "blocks", Title: "T1", Status: "open", Priority: 1, IssueType: "task"},
		{Id: "dep-2", Type: "blocks", Title: "T2", Status: "closed", Priority: 2, IssueType: "bug"},
	}
	out := dependencyRefsToData("parent", refs, true)
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	for i, d := range out {
		if d.IssueID != "parent" {
			t.Errorf("[%d] IssueID = %q, want parent", i, d.IssueID)
		}
		if d.DependsOnID != refs[i].Id {
			t.Errorf("[%d] DependsOnID = %q", i, d.DependsOnID)
		}
		if d.Type != refs[i].Type {
			t.Errorf("[%d] Type", i)
		}
	}
}

func TestDependencyRefsToData_Incoming(t *testing.T) {
	refs := []gen.DependencyRef{
		{Id: "dep-1", Type: "blocks", Title: "T", Status: "open", Priority: 1, IssueType: "task"},
	}
	out := dependencyRefsToData("parent", refs, false)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].IssueID != "dep-1" {
		t.Errorf("IssueID = %q, want dep-1", out[0].IssueID)
	}
	if out[0].DependsOnID != "parent" {
		t.Errorf("DependsOnID = %q, want parent", out[0].DependsOnID)
	}
}

func TestDependencyRefsToData_Empty(t *testing.T) {
	out := dependencyRefsToData("p", nil, true)
	if out == nil {
		t.Errorf("should be non-nil empty")
	}
	if len(out) != 0 {
		t.Errorf("len = %d", len(out))
	}
}

// --- commentResponseToData ---

func TestCommentResponseToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	edited := now.Add(time.Hour)
	parentID := int64(99)
	c := gen.CommentResponse{
		Id:        42,
		Author:    "alice",
		Text:      "hello",
		CreatedAt: now,
		EditedAt:  &edited,
		ParentId:  &parentID,
	}
	d := commentResponseToData(c, "loom-1")
	if d.ID != 42 {
		t.Errorf("ID = %d", d.ID)
	}
	if d.IssueID != "loom-1" {
		t.Errorf("IssueID = %q", d.IssueID)
	}
	if d.Author != "alice" || d.Text != "hello" {
		t.Errorf("fields: %+v", d)
	}
	if d.EditedAt == nil || !d.EditedAt.Equal(edited) {
		t.Errorf("EditedAt: %v", d.EditedAt)
	}
	if d.ParentID == nil || *d.ParentID != 99 {
		t.Errorf("ParentID: %v", d.ParentID)
	}
}

func TestCommentResponseToData_NilOptional(t *testing.T) {
	c := gen.CommentResponse{Id: 1, Author: "a", Text: "t", CreatedAt: time.Now()}
	d := commentResponseToData(c, "i")
	if d.EditedAt != nil {
		t.Errorf("EditedAt should be nil")
	}
	if d.ParentID != nil {
		t.Errorf("ParentID should be nil")
	}
}

// --- commentToData ---

func TestCommentToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c := gen.Comment{Id: 7, IssueId: "loom-1", Author: "bob", Text: "t", CreatedAt: now}
	d := commentToData(c)
	if d.ID != 7 || d.IssueID != "loom-1" || d.Author != "bob" || d.Text != "t" {
		t.Errorf("mismatch: %+v", d)
	}
	if !d.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt: %v", d.CreatedAt)
	}
}

// --- eventToData ---

func TestEventToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	e := gen.IssueEvent{
		Id:        123,
		IssueId:   "loom-1",
		EventType: "status_change",
		Actor:     "alice",
		CreatedAt: now,
	}
	d := eventToData(e)
	if d.ID != "123" {
		t.Errorf("ID = %q", d.ID)
	}
	if d.IssueID != "loom-1" || d.Kind != "status_change" || d.Actor != "alice" {
		t.Errorf("fields: %+v", d)
	}
	if !d.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt: %v", d.CreatedAt)
	}
}

// --- statisticsToStats ---

func TestStatisticsToStats(t *testing.T) {
	s := gen.Statistics{
		TotalIssues:             100,
		OpenIssues:              40,
		InProgressIssues:        20,
		ClosedIssues:            30,
		BlockedIssues:           5,
		DeferredIssues:          3,
		ReadyIssues:             15,
		TombstoneIssues:         2,
		PinnedIssues:            1,
		EpicsEligibleForClosure: 4,
		AverageLeadTimeHours:    12.5,
	}
	d := statisticsToStats(s)
	if d.TotalIssues != 100 {
		t.Errorf("TotalIssues = %d", d.TotalIssues)
	}
	if d.OpenIssues != 40 {
		t.Errorf("OpenIssues = %d", d.OpenIssues)
	}
	if d.InProgressIssues != 20 {
		t.Errorf("InProgressIssues = %d", d.InProgressIssues)
	}
	if d.ClosedIssues != 30 {
		t.Errorf("ClosedIssues = %d", d.ClosedIssues)
	}
	if d.BlockedIssues != 5 {
		t.Errorf("BlockedIssues = %d", d.BlockedIssues)
	}
	if d.DeferredIssues != 3 {
		t.Errorf("DeferredIssues = %d", d.DeferredIssues)
	}
	if d.ReadyIssues != 15 {
		t.Errorf("ReadyIssues = %d", d.ReadyIssues)
	}
	if d.TombstoneIssues != 2 {
		t.Errorf("TombstoneIssues = %d", d.TombstoneIssues)
	}
	if d.PinnedIssues != 1 {
		t.Errorf("PinnedIssues = %d", d.PinnedIssues)
	}
	if d.EpicsEligibleForClosure != 4 {
		t.Errorf("EpicsEligibleForClosure = %d", d.EpicsEligibleForClosure)
	}
	if d.AverageLeadTime != 12.5 {
		t.Errorf("AverageLeadTime = %f", d.AverageLeadTime)
	}
}

// --- blockedIssueToData ---

func TestBlockedIssueToData_AllNilPointers(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	b := gen.BlockedIssue{
		Id:        "b-1",
		Title:     "Blocked",
		Priority:  2,
		CreatedAt: now,
		UpdatedAt: now,
	}
	d := blockedIssueToData(b)
	if d.ID != "b-1" || d.Title != "Blocked" || d.Priority != 2 {
		t.Errorf("basic: %+v", d)
	}
	if d.Labels == nil {
		t.Errorf("Labels should be non-nil")
	}
	if len(d.Labels) != 0 {
		t.Errorf("Labels should be empty")
	}
	if d.Status != "" || d.IssueType != "" {
		t.Errorf("enums should be empty")
	}
}

func TestBlockedIssueToData_AllFieldsSet(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	status := gen.BlockedIssueStatus("open")
	issueType := gen.BlockedIssueIssueType("task")
	assignee := "a"
	owner := "o"
	labels := []string{"x"}
	repo := "r"
	parent := "p"
	design := "d"
	designArtifactID := "design-b-2-sha256"
	designFormat := gen.BlockedIssueDesignFormat("html")
	hasDesign := true
	due := now.Add(time.Hour)
	defer_ := now.Add(2 * time.Hour)
	b := gen.BlockedIssue{
		Id:               "b-2",
		Title:            "Full",
		Status:           &status,
		IssueType:        &issueType,
		Priority:         1,
		Assignee:         &assignee,
		Owner:            &owner,
		Labels:           &labels,
		SourceRepo:       &repo,
		Parent:           &parent,
		Design:           &design,
		DesignArtifactId: &designArtifactID,
		DesignFormat:     &designFormat,
		HasDesign:        &hasDesign,
		CreatedAt:        now,
		UpdatedAt:        now,
		DueAt:            &due,
		DeferUntil:       &defer_,
		BlockedBy:        []string{"b-1"},
	}
	d := blockedIssueToData(b)
	if d.Status != "open" || d.IssueType != "task" {
		t.Errorf("enums: %+v", d)
	}
	if d.Assignee != "a" || d.Owner != "o" {
		t.Errorf("ptrs")
	}
	if len(d.Labels) != 1 || d.Labels[0] != "x" {
		t.Errorf("labels: %v", d.Labels)
	}
	if d.SourceRepo != "r" || d.Parent != "p" || d.Design != "d" {
		t.Errorf("more ptrs")
	}
	if !d.HasDesign || d.DesignArtifactID != designArtifactID || d.DesignFormat != "html" {
		t.Errorf("design metadata: %+v", d)
	}
	if d.DueAt == nil || !d.DueAt.Equal(due) {
		t.Errorf("DueAt")
	}
	if d.DeferUntil == nil || !d.DeferUntil.Equal(defer_) {
		t.Errorf("DeferUntil")
	}
}

// --- cloneTimePtr ---

func TestCloneTimePtr_Nil(t *testing.T) {
	if got := cloneTimePtr(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestCloneTimePtr_DeepCopy(t *testing.T) {
	now := time.Now().UTC()
	clone := cloneTimePtr(&now)
	if clone == nil {
		t.Fatal("clone is nil")
	}
	if clone == &now {
		t.Errorf("clone should be a different pointer")
	}
	if !clone.Equal(now) {
		t.Errorf("clone value mismatch")
	}
}
