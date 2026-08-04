package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/entity"
)

// ptr returns a pointer to the given value. Generic helper for pointer literals.
func ptr[T any](v T) *T { return &v }

// ---- helpers ----------------------------------------------------------------

// fixedTime is a deterministic time used across tests.
var fixedTime = time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

// newFullIssue returns an entity.Issue with all fields populated.
func newFullIssue() *entity.Issue {
	return &entity.Issue{
		ID:                 "proj-42",
		Title:              "Fix login bug",
		Description:        "Users cannot log in",
		Design:             "## Design",
		AcceptanceCriteria: "Login works",
		Notes:              "Urgent",
		Status:             entity.StatusOpen,
		Priority:           2,
		IssueType:          entity.TypeBug,
		Assignee:           "alice",
		Owner:              "bob",
		EstimatedMinutes:   ptr(90),
		CreatedAt:          fixedTime,
		UpdatedAt:          fixedTime.Add(time.Hour),
		ClosedAt:           ptr(fixedTime.Add(2 * time.Hour)),
		CloseReason:        "fixed",
		DueAt:              ptr(fixedTime.Add(24 * time.Hour)),
		DeferUntil:         ptr(fixedTime.Add(12 * time.Hour)),
		ExternalRef:        ptr("GH-123"),
		SourceRepo:         "myrepo",
		Pinned:             true,
	}
}

// ---- IssueFromEntity --------------------------------------------------------

func TestIssueFromEntity_NilIssue(t *testing.T) {
	resp := IssueFromEntity(nil)

	if resp.ID != "" {
		t.Errorf("ID = %q, want empty", resp.ID)
	}
	if resp.Title != "" {
		t.Errorf("Title = %q, want empty", resp.Title)
	}
	if resp.Labels == nil {
		t.Fatal("Labels is nil, want non-nil empty slice")
	}
	if len(resp.Labels) != 0 {
		t.Errorf("Labels len = %d, want 0", len(resp.Labels))
	}
	if resp.Dependencies == nil {
		t.Fatal("Dependencies is nil, want non-nil empty slice")
	}
	if resp.Dependents == nil {
		t.Fatal("Dependents is nil, want non-nil empty slice")
	}
	if resp.Comments == nil {
		t.Fatal("Comments is nil, want non-nil empty slice")
	}
	if resp.Parent != nil {
		t.Errorf("Parent = %v, want nil", resp.Parent)
	}
	if resp.ParentTitle != nil {
		t.Errorf("ParentTitle = %v, want nil", resp.ParentTitle)
	}
	if resp.DependencyCount != 0 {
		t.Errorf("DependencyCount = %d, want 0", resp.DependencyCount)
	}
	if resp.DependentCount != 0 {
		t.Errorf("DependentCount = %d, want 0", resp.DependentCount)
	}
}

func TestIssueFromEntity_NilIssue_JSONEmptySlices(t *testing.T) {
	resp := IssueFromEntity(nil)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{"labels", "dependencies", "dependents", "comments"} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s field omitted from JSON output", field)
			continue
		}
		if string(val) != "[]" {
			t.Errorf("%s = %s, want []", field, val)
		}
	}
}

func TestIssueFromEntity_FullMapping(t *testing.T) {
	issue := newFullIssue()
	resp := IssueFromEntity(issue)

	if resp.ID != "proj-42" {
		t.Errorf("ID = %q, want %q", resp.ID, "proj-42")
	}
	if resp.Title != "Fix login bug" {
		t.Errorf("Title = %q, want %q", resp.Title, "Fix login bug")
	}
	if resp.Description != "Users cannot log in" {
		t.Errorf("Description = %q, want %q", resp.Description, "Users cannot log in")
	}
	if resp.Design != "## Design" {
		t.Errorf("Design = %q, want %q", resp.Design, "## Design")
	}
	if resp.AcceptanceCriteria != "Login works" {
		t.Errorf("AcceptanceCriteria = %q, want %q", resp.AcceptanceCriteria, "Login works")
	}
	if resp.Notes != "Urgent" {
		t.Errorf("Notes = %q, want %q", resp.Notes, "Urgent")
	}
	if resp.Assignee != "alice" {
		t.Errorf("Assignee = %q, want %q", resp.Assignee, "alice")
	}
	if resp.Owner != "bob" {
		t.Errorf("Owner = %q, want %q", resp.Owner, "bob")
	}
	if resp.SourceRepo != "myrepo" {
		t.Errorf("SourceRepo = %q, want %q", resp.SourceRepo, "myrepo")
	}
	if resp.CloseReason != "fixed" {
		t.Errorf("CloseReason = %q, want %q", resp.CloseReason, "fixed")
	}
	if !resp.Pinned {
		t.Error("Pinned = false, want true")
	}
}

func TestIssueFromEntity_EnumConversion(t *testing.T) {
	tests := []struct {
		name       string
		status     entity.IssueStatus
		issueType  entity.IssueType
		wantStatus string
		wantType   string
	}{
		{"open/bug", entity.StatusOpen, entity.TypeBug, "open", "bug"},
		{"in_progress/feature", entity.StatusInProgress, entity.TypeFeature, "in_progress", "feature"},
		{"closed/task", entity.StatusClosed, entity.TypeTask, "closed", "task"},
		{"blocked/epic", entity.StatusBlocked, entity.TypeEpic, "blocked", "epic"},
		{"review/chore", entity.StatusReview, entity.TypeChore, "review", "chore"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &entity.Issue{
				Status:    tt.status,
				IssueType: tt.issueType,
			}
			resp := IssueFromEntity(issue)
			if resp.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", resp.Status, tt.wantStatus)
			}
			if resp.IssueType != tt.wantType {
				t.Errorf("IssueType = %q, want %q", resp.IssueType, tt.wantType)
			}
		})
	}
}

func TestIssueFromEntity_PointerFields(t *testing.T) {
	issue := newFullIssue()
	resp := IssueFromEntity(issue)

	if resp.EstimatedMinutes == nil || *resp.EstimatedMinutes != 90 {
		t.Errorf("EstimatedMinutes = %v, want 90", resp.EstimatedMinutes)
	}
	if resp.ClosedAt == nil || !resp.ClosedAt.Equal(fixedTime.Add(2*time.Hour)) {
		t.Errorf("ClosedAt = %v, want %v", resp.ClosedAt, fixedTime.Add(2*time.Hour))
	}
	if resp.DueAt == nil || !resp.DueAt.Equal(fixedTime.Add(24*time.Hour)) {
		t.Errorf("DueAt = %v, want %v", resp.DueAt, fixedTime.Add(24*time.Hour))
	}
	if resp.DeferUntil == nil || !resp.DeferUntil.Equal(fixedTime.Add(12*time.Hour)) {
		t.Errorf("DeferUntil = %v, want %v", resp.DeferUntil, fixedTime.Add(12*time.Hour))
	}
	if resp.ExternalRef == nil || *resp.ExternalRef != "GH-123" {
		t.Errorf("ExternalRef = %v, want %q", resp.ExternalRef, "GH-123")
	}
}

func TestIssueFromEntity_NilPointerFields(t *testing.T) {
	issue := &entity.Issue{
		Status:    entity.StatusOpen,
		IssueType: entity.TypeTask,
	}
	resp := IssueFromEntity(issue)

	if resp.EstimatedMinutes != nil {
		t.Errorf("EstimatedMinutes = %v, want nil", resp.EstimatedMinutes)
	}
	if resp.ClosedAt != nil {
		t.Errorf("ClosedAt = %v, want nil", resp.ClosedAt)
	}
	if resp.DueAt != nil {
		t.Errorf("DueAt = %v, want nil", resp.DueAt)
	}
	if resp.DeferUntil != nil {
		t.Errorf("DeferUntil = %v, want nil", resp.DeferUntil)
	}
	if resp.ExternalRef != nil {
		t.Errorf("ExternalRef = %v, want nil", resp.ExternalRef)
	}
}

func TestIssueFromEntity_Timestamps(t *testing.T) {
	issue := newFullIssue()
	resp := IssueFromEntity(issue)

	if !resp.CreatedAt.Equal(fixedTime) {
		t.Errorf("CreatedAt = %v, want %v", resp.CreatedAt, fixedTime)
	}
	if !resp.UpdatedAt.Equal(fixedTime.Add(time.Hour)) {
		t.Errorf("UpdatedAt = %v, want %v", resp.UpdatedAt, fixedTime.Add(time.Hour))
	}
}

// ---- WithLabels -------------------------------------------------------------

func TestWithLabels_Populated(t *testing.T) {
	issue := &entity.Issue{Status: entity.StatusOpen, IssueType: entity.TypeTask}
	resp := IssueFromEntity(issue, WithLabels([]string{"urgent", "auth"}))

	if len(resp.Labels) != 2 {
		t.Fatalf("Labels len = %d, want 2", len(resp.Labels))
	}
	if resp.Labels[0] != "urgent" || resp.Labels[1] != "auth" {
		t.Errorf("Labels = %v, want [urgent auth]", resp.Labels)
	}
}

func TestWithLabels_Nil(t *testing.T) {
	issue := &entity.Issue{Status: entity.StatusOpen, IssueType: entity.TypeTask}
	resp := IssueFromEntity(issue, WithLabels(nil))

	if resp.Labels == nil {
		t.Fatal("WithLabels(nil) should produce non-nil empty slice")
	}
	if len(resp.Labels) != 0 {
		t.Errorf("Labels len = %d, want 0", len(resp.Labels))
	}
}

func TestWithLabels_Empty(t *testing.T) {
	issue := &entity.Issue{Status: entity.StatusOpen, IssueType: entity.TypeTask}
	resp := IssueFromEntity(issue, WithLabels([]string{}))

	if resp.Labels == nil {
		t.Fatal("WithLabels([]string{}) should produce non-nil empty slice")
	}
	if len(resp.Labels) != 0 {
		t.Errorf("Labels len = %d, want 0", len(resp.Labels))
	}
}

// ---- WithDependencies / WithDependents --------------------------------------

func TestWithDependencies(t *testing.T) {
	deps := []DependencyRef{
		{ID: "proj-10", Title: "Blocker", Status: "closed", Priority: 1, Type: "blocks", IssueType: "task"},
	}
	resp := IssueFromEntity(&entity.Issue{}, WithDependencies(deps))

	if len(resp.Dependencies) != 1 {
		t.Fatalf("Dependencies len = %d, want 1", len(resp.Dependencies))
	}
	if resp.Dependencies[0].ID != "proj-10" {
		t.Errorf("Dependencies[0].ID = %q, want %q", resp.Dependencies[0].ID, "proj-10")
	}
}

func TestWithDependents(t *testing.T) {
	deps := []DependencyRef{
		{ID: "proj-20", Title: "Downstream", Status: "open", Priority: 2, Type: "blocks", IssueType: "feature"},
	}
	resp := IssueFromEntity(&entity.Issue{}, WithDependents(deps))

	if len(resp.Dependents) != 1 {
		t.Fatalf("Dependents len = %d, want 1", len(resp.Dependents))
	}
	if resp.Dependents[0].ID != "proj-20" {
		t.Errorf("Dependents[0].ID = %q, want %q", resp.Dependents[0].ID, "proj-20")
	}
}

func TestWithDependencies_Nil(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{}, WithDependencies(nil))
	if resp.Dependencies == nil {
		t.Fatal("WithDependencies(nil) should produce non-nil empty slice")
	}
	if len(resp.Dependencies) != 0 {
		t.Errorf("Dependencies len = %d, want 0", len(resp.Dependencies))
	}
}

func TestWithDependents_Nil(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{}, WithDependents(nil))
	if resp.Dependents == nil {
		t.Fatal("WithDependents(nil) should produce non-nil empty slice")
	}
	if len(resp.Dependents) != 0 {
		t.Errorf("Dependents len = %d, want 0", len(resp.Dependents))
	}
}

// ---- WithComments -----------------------------------------------------------

func TestWithComments(t *testing.T) {
	comments := []CommentResponse{
		{ID: 1, Author: "alice", Text: "LGTM", CreatedAt: fixedTime},
		{ID: 2, Author: "bob", Text: "Approved", CreatedAt: fixedTime.Add(time.Minute)},
	}
	resp := IssueFromEntity(&entity.Issue{}, WithComments(comments))

	if len(resp.Comments) != 2 {
		t.Fatalf("Comments len = %d, want 2", len(resp.Comments))
	}
	if resp.Comments[0].Author != "alice" {
		t.Errorf("Comments[0].Author = %q, want %q", resp.Comments[0].Author, "alice")
	}
	if resp.Comments[1].Author != "bob" {
		t.Errorf("Comments[1].Author = %q, want %q", resp.Comments[1].Author, "bob")
	}
}

func TestWithComments_Nil(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{}, WithComments(nil))
	if resp.Comments == nil {
		t.Fatal("WithComments(nil) should produce non-nil empty slice")
	}
	if len(resp.Comments) != 0 {
		t.Errorf("Comments len = %d, want 0", len(resp.Comments))
	}
}

// ---- WithParent -------------------------------------------------------------

func TestWithParent_Populated(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{}, WithParent("epic-1", "My Epic"))

	if resp.Parent == nil {
		t.Fatal("Parent is nil, want non-nil")
	}
	if *resp.Parent != "epic-1" {
		t.Errorf("Parent = %q, want %q", *resp.Parent, "epic-1")
	}
	if resp.ParentTitle == nil {
		t.Fatal("ParentTitle is nil, want non-nil")
	}
	if *resp.ParentTitle != "My Epic" {
		t.Errorf("ParentTitle = %q, want %q", *resp.ParentTitle, "My Epic")
	}
}

func TestWithParent_EmptyID(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{}, WithParent("", ""))

	if resp.Parent != nil {
		t.Errorf("Parent = %v, want nil when ID is empty", resp.Parent)
	}
	if resp.ParentTitle != nil {
		t.Errorf("ParentTitle = %v, want nil when ID is empty", resp.ParentTitle)
	}
}

func TestWithParent_EmptyIDNonEmptyTitle(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{}, WithParent("", "Should be ignored"))

	if resp.Parent != nil {
		t.Errorf("Parent = %v, want nil when ID is empty", resp.Parent)
	}
	if resp.ParentTitle != nil {
		t.Errorf("ParentTitle = %v, want nil when ID is empty", resp.ParentTitle)
	}
}

// ---- WithCounts -------------------------------------------------------------

func TestWithCounts_Overrides(t *testing.T) {
	deps := []DependencyRef{
		{ID: "proj-10", Title: "Dep", Status: "closed", Priority: 0, Type: "blocks"},
	}
	resp := IssueFromEntity(&entity.Issue{},
		WithDependencies(deps),
		WithDependents(deps),
		WithCounts(5, 10),
	)

	if resp.DependencyCount != 5 {
		t.Errorf("DependencyCount = %d, want 5", resp.DependencyCount)
	}
	if resp.DependentCount != 10 {
		t.Errorf("DependentCount = %d, want 10", resp.DependentCount)
	}
}

func TestWithCounts_ZeroValues(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{}, WithCounts(0, 0))

	if resp.DependencyCount != 0 {
		t.Errorf("DependencyCount = %d, want 0", resp.DependencyCount)
	}
	if resp.DependentCount != 0 {
		t.Errorf("DependentCount = %d, want 0", resp.DependentCount)
	}
}

func TestWithoutCounts_InfersFromSlices(t *testing.T) {
	deps := []DependencyRef{
		{ID: "d-1"}, {ID: "d-2"}, {ID: "d-3"},
	}
	depts := []DependencyRef{
		{ID: "dt-1"}, {ID: "dt-2"},
	}
	resp := IssueFromEntity(&entity.Issue{},
		WithDependencies(deps),
		WithDependents(depts),
	)

	if resp.DependencyCount != 3 {
		t.Errorf("DependencyCount = %d, want 3 (inferred from len)", resp.DependencyCount)
	}
	if resp.DependentCount != 2 {
		t.Errorf("DependentCount = %d, want 2 (inferred from len)", resp.DependentCount)
	}
}

func TestWithoutCounts_NoSlices(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{})

	if resp.DependencyCount != 0 {
		t.Errorf("DependencyCount = %d, want 0", resp.DependencyCount)
	}
	if resp.DependentCount != 0 {
		t.Errorf("DependentCount = %d, want 0", resp.DependentCount)
	}
}

// ---- Multiple options combined ----------------------------------------------

func TestIssueFromEntity_AllOptions(t *testing.T) {
	issue := newFullIssue()
	labels := []string{"urgent", "auth"}
	deps := []DependencyRef{{ID: "dep-1", Title: "Dep", Status: "closed", Type: "blocks"}}
	depts := []DependencyRef{{ID: "dept-1", Title: "Dept", Status: "open", Type: "blocks"}}
	comments := []CommentResponse{{ID: 1, Author: "alice", Text: "ok", CreatedAt: fixedTime}}

	resp := IssueFromEntity(issue,
		WithLabels(labels),
		WithDependencies(deps),
		WithDependents(depts),
		WithComments(comments),
		WithParent("epic-1", "My Epic"),
		WithCounts(42, 99),
	)

	if resp.ID != "proj-42" {
		t.Errorf("ID = %q, want %q", resp.ID, "proj-42")
	}
	if len(resp.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(resp.Labels))
	}
	if len(resp.Dependencies) != 1 {
		t.Errorf("Dependencies len = %d, want 1", len(resp.Dependencies))
	}
	if len(resp.Dependents) != 1 {
		t.Errorf("Dependents len = %d, want 1", len(resp.Dependents))
	}
	if len(resp.Comments) != 1 {
		t.Errorf("Comments len = %d, want 1", len(resp.Comments))
	}
	if resp.Parent == nil || *resp.Parent != "epic-1" {
		t.Errorf("Parent = %v, want %q", resp.Parent, "epic-1")
	}
	if resp.ParentTitle == nil || *resp.ParentTitle != "My Epic" {
		t.Errorf("ParentTitle = %v, want %q", resp.ParentTitle, "My Epic")
	}
	if resp.DependencyCount != 42 {
		t.Errorf("DependencyCount = %d, want 42", resp.DependencyCount)
	}
	if resp.DependentCount != 99 {
		t.Errorf("DependentCount = %d, want 99", resp.DependentCount)
	}
}

// ---- DependencyRefFromEntity ------------------------------------------------

func TestDependencyRefFromEntity_NilDep(t *testing.T) {
	ref := DependencyRefFromEntity(nil, &entity.Issue{ID: "proj-1"})

	if ref.ID != "" {
		t.Errorf("ID = %q, want empty", ref.ID)
	}
	if ref.Type != "" {
		t.Errorf("Type = %q, want empty", ref.Type)
	}
}

func TestDependencyRefFromEntity_NilRelatedIssue(t *testing.T) {
	dep := &entity.Dependency{
		IssueID:     "proj-1",
		DependsOnID: "proj-2",
		Type:        entity.DepBlocks,
	}
	ref := DependencyRefFromEntity(dep, nil)

	if ref.Type != "blocks" {
		t.Errorf("Type = %q, want %q", ref.Type, "blocks")
	}
	if ref.ID != "" {
		t.Errorf("ID = %q, want empty (related issue is nil)", ref.ID)
	}
	if ref.Title != "" {
		t.Errorf("Title = %q, want empty", ref.Title)
	}
	if ref.Status != "" {
		t.Errorf("Status = %q, want empty", ref.Status)
	}
}

func TestDependencyRefFromEntity_BothNil(t *testing.T) {
	ref := DependencyRefFromEntity(nil, nil)

	if ref.ID != "" || ref.Title != "" || ref.Status != "" || ref.Type != "" {
		t.Errorf("expected zero-value DependencyRef, got %+v", ref)
	}
}

func TestDependencyRefFromEntity_Full(t *testing.T) {
	dep := &entity.Dependency{
		IssueID:     "proj-1",
		DependsOnID: "proj-2",
		Type:        entity.DepBlocks,
	}
	relatedIssue := &entity.Issue{
		ID:        "proj-2",
		Title:     "Blocker task",
		Status:    entity.StatusClosed,
		Priority:  1,
		IssueType: entity.TypeTask,
	}

	ref := DependencyRefFromEntity(dep, relatedIssue)

	if ref.ID != "proj-2" {
		t.Errorf("ID = %q, want %q", ref.ID, "proj-2")
	}
	if ref.Title != "Blocker task" {
		t.Errorf("Title = %q, want %q", ref.Title, "Blocker task")
	}
	if ref.Status != "closed" {
		t.Errorf("Status = %q, want %q", ref.Status, "closed")
	}
	if ref.Priority != 1 {
		t.Errorf("Priority = %d, want 1", ref.Priority)
	}
	if ref.Type != "blocks" {
		t.Errorf("Type = %q, want %q", ref.Type, "blocks")
	}
	if ref.IssueType != "task" {
		t.Errorf("IssueType = %q, want %q", ref.IssueType, "task")
	}
}

func TestDependencyRefFromEntity_DependencyTypeConversion(t *testing.T) {
	tests := []struct {
		depType  entity.DependencyType
		wantType string
	}{
		{entity.DepBlocks, "blocks"},
		{entity.DepParentChild, "parent-child"},
		{entity.DepRelated, "related"},
		{entity.DepDiscoveredFrom, "discovered-from"},
	}

	for _, tt := range tests {
		t.Run(tt.wantType, func(t *testing.T) {
			dep := &entity.Dependency{Type: tt.depType}
			ref := DependencyRefFromEntity(dep, nil)
			if ref.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", ref.Type, tt.wantType)
			}
		})
	}
}

// ---- CommentFromEntity ------------------------------------------------------

func TestCommentFromEntity_Nil(t *testing.T) {
	resp := CommentFromEntity(nil)

	if resp.ID != 0 {
		t.Errorf("ID = %d, want 0", resp.ID)
	}
	if resp.Author != "" {
		t.Errorf("Author = %q, want empty", resp.Author)
	}
	if resp.Text != "" {
		t.Errorf("Text = %q, want empty", resp.Text)
	}
}

func TestCommentFromEntity_Full(t *testing.T) {
	editedAt := fixedTime.Add(time.Hour)
	c := &entity.Comment{
		ID:        42,
		IssueID:   "proj-1",
		Author:    "alice",
		Text:      "Great work!",
		CreatedAt: fixedTime,
		ParentID:  ptr(int64(5)),
		EditedAt:  &editedAt,
	}

	resp := CommentFromEntity(c)

	if resp.ID != 42 {
		t.Errorf("ID = %d, want 42", resp.ID)
	}
	if resp.Author != "alice" {
		t.Errorf("Author = %q, want %q", resp.Author, "alice")
	}
	if resp.Text != "Great work!" {
		t.Errorf("Text = %q, want %q", resp.Text, "Great work!")
	}
	if !resp.CreatedAt.Equal(fixedTime) {
		t.Errorf("CreatedAt = %v, want %v", resp.CreatedAt, fixedTime)
	}
	if resp.ParentID == nil || *resp.ParentID != 5 {
		t.Errorf("ParentID = %v, want 5", resp.ParentID)
	}
	if resp.EditedAt == nil || !resp.EditedAt.Equal(editedAt) {
		t.Errorf("EditedAt = %v, want %v", resp.EditedAt, editedAt)
	}
}

func TestCommentFromEntity_MinimalFields(t *testing.T) {
	c := &entity.Comment{
		ID:        1,
		Author:    "bob",
		Text:      "Hello",
		CreatedAt: fixedTime,
	}

	resp := CommentFromEntity(c)

	if resp.ParentID != nil {
		t.Errorf("ParentID = %v, want nil", resp.ParentID)
	}
	if resp.EditedAt != nil {
		t.Errorf("EditedAt = %v, want nil", resp.EditedAt)
	}
}

// ---- CommentsFromEntities ---------------------------------------------------

func TestCommentsFromEntities_NilInput(t *testing.T) {
	result := CommentsFromEntities(nil)

	if result == nil {
		t.Fatal("CommentsFromEntities(nil) returned nil, want non-nil empty slice")
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestCommentsFromEntities_EmptyInput(t *testing.T) {
	result := CommentsFromEntities([]*entity.Comment{})

	if result == nil {
		t.Fatal("CommentsFromEntities(empty) returned nil, want non-nil empty slice")
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestCommentsFromEntities_FiltersNilEntries(t *testing.T) {
	comments := []*entity.Comment{
		{ID: 1, Author: "alice", Text: "Hello", CreatedAt: fixedTime},
		nil,
		{ID: 3, Author: "bob", Text: "World", CreatedAt: fixedTime.Add(time.Minute)},
	}

	result := CommentsFromEntities(comments)

	if len(result) != 2 {
		t.Fatalf("len = %d, want 2 (nil entry should be filtered)", len(result))
	}
	if result[0].ID != 1 {
		t.Errorf("result[0].ID = %d, want 1", result[0].ID)
	}
	if result[1].ID != 3 {
		t.Errorf("result[1].ID = %d, want 3", result[1].ID)
	}
}

func TestCommentsFromEntities_FiltersSoftDeleted(t *testing.T) {
	deletedAt := fixedTime.Add(-time.Hour)
	comments := []*entity.Comment{
		{ID: 1, Author: "alice", Text: "Active", CreatedAt: fixedTime},
		{ID: 2, Author: "bob", Text: "Deleted", CreatedAt: fixedTime, DeletedAt: &deletedAt},
		{ID: 3, Author: "charlie", Text: "Also active", CreatedAt: fixedTime},
	}

	result := CommentsFromEntities(comments)

	if len(result) != 2 {
		t.Fatalf("len = %d, want 2 (soft-deleted should be filtered)", len(result))
	}
	if result[0].ID != 1 {
		t.Errorf("result[0].ID = %d, want 1", result[0].ID)
	}
	if result[1].ID != 3 {
		t.Errorf("result[1].ID = %d, want 3", result[1].ID)
	}
}

func TestCommentsFromEntities_AllFiltered(t *testing.T) {
	deletedAt := fixedTime
	comments := []*entity.Comment{
		nil,
		{ID: 1, Author: "bob", Text: "Deleted", CreatedAt: fixedTime, DeletedAt: &deletedAt},
	}

	result := CommentsFromEntities(comments)

	if result == nil {
		t.Fatal("result is nil, want non-nil empty slice")
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0 (all entries filtered)", len(result))
	}
}

func TestCommentsFromEntities_JSONEmptyArray(t *testing.T) {
	result := CommentsFromEntities(nil)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("JSON = %s, want []", data)
	}
}

// ---- IssuesFromEntities -----------------------------------------------------

func TestIssuesFromEntities_NilInput(t *testing.T) {
	result := IssuesFromEntities(nil)

	if result == nil {
		t.Fatal("IssuesFromEntities(nil) returned nil, want non-nil empty slice")
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestIssuesFromEntities_EmptyInput(t *testing.T) {
	result := IssuesFromEntities([]*entity.Issue{})

	if result == nil {
		t.Fatal("IssuesFromEntities(empty) returned nil, want non-nil empty slice")
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestIssuesFromEntities_JSONEmptyArray(t *testing.T) {
	result := IssuesFromEntities(nil)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("JSON = %s, want []", data)
	}
}

func TestIssuesFromEntities_MapsMultiple(t *testing.T) {
	issues := []*entity.Issue{
		{ID: "proj-1", Title: "First", Status: entity.StatusOpen, IssueType: entity.TypeTask},
		{ID: "proj-2", Title: "Second", Status: entity.StatusClosed, IssueType: entity.TypeBug, Priority: 1},
	}

	result := IssuesFromEntities(issues)

	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].ID != "proj-1" {
		t.Errorf("result[0].ID = %q, want %q", result[0].ID, "proj-1")
	}
	if result[0].Status != "open" {
		t.Errorf("result[0].Status = %q, want %q", result[0].Status, "open")
	}
	if result[1].ID != "proj-2" {
		t.Errorf("result[1].ID = %q, want %q", result[1].ID, "proj-2")
	}
	if result[1].Status != "closed" {
		t.Errorf("result[1].Status = %q, want %q", result[1].Status, "closed")
	}
	if result[1].Priority != 1 {
		t.Errorf("result[1].Priority = %d, want 1", result[1].Priority)
	}
}

func TestIssuesFromEntities_OptionsAppliedToAll(t *testing.T) {
	issues := []*entity.Issue{
		{ID: "proj-1", Status: entity.StatusOpen, IssueType: entity.TypeTask},
		{ID: "proj-2", Status: entity.StatusOpen, IssueType: entity.TypeTask},
	}

	labels := []string{"shared-label"}
	result := IssuesFromEntities(issues, WithLabels(labels), WithCounts(10, 20))

	for i, resp := range result {
		if len(resp.Labels) != 1 || resp.Labels[0] != "shared-label" {
			t.Errorf("result[%d].Labels = %v, want [shared-label]", i, resp.Labels)
		}
		if resp.DependencyCount != 10 {
			t.Errorf("result[%d].DependencyCount = %d, want 10", i, resp.DependencyCount)
		}
		if resp.DependentCount != 20 {
			t.Errorf("result[%d].DependentCount = %d, want 20", i, resp.DependentCount)
		}
	}
}

func TestIssuesFromEntities_SliceFieldsNeverNull(t *testing.T) {
	issues := []*entity.Issue{
		{ID: "proj-1", Status: entity.StatusOpen, IssueType: entity.TypeTask},
	}

	result := IssuesFromEntities(issues)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	if len(raw) != 1 {
		t.Fatalf("len = %d, want 1", len(raw))
	}

	for _, field := range []string{"labels", "dependencies", "dependents", "comments"} {
		val, ok := raw[0][field]
		if !ok {
			t.Errorf("%s field omitted from JSON output", field)
			continue
		}
		if string(val) != "[]" {
			t.Errorf("%s = %s, want []", field, val)
		}
	}
}

func TestIssuesFromEntities_FiltersNilEntries(t *testing.T) {
	issues := []*entity.Issue{
		{ID: "proj-1", Status: entity.StatusOpen, IssueType: entity.TypeTask},
		nil,
		{ID: "proj-2", Status: entity.StatusOpen, IssueType: entity.TypeBug},
	}

	result := IssuesFromEntities(issues)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2 (nil entry should be filtered)", len(result))
	}
	if result[0].ID != "proj-1" {
		t.Errorf("result[0].ID = %q, want %q", result[0].ID, "proj-1")
	}
	if result[1].ID != "proj-2" {
		t.Errorf("result[1].ID = %q, want %q", result[1].ID, "proj-2")
	}
}

// ---- emptyStringSlice / emptyDepSlice / emptyCommentSlice (via integration) -

func TestNoOptions_AllSlicesAreEmptyNotNil(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{})

	if resp.Labels == nil {
		t.Error("Labels is nil without options")
	}
	if resp.Dependencies == nil {
		t.Error("Dependencies is nil without options")
	}
	if resp.Dependents == nil {
		t.Error("Dependents is nil without options")
	}
	if resp.Comments == nil {
		t.Error("Comments is nil without options")
	}
}

func TestNoOptions_JSONSlicesAreEmptyArrays(t *testing.T) {
	resp := IssueFromEntity(&entity.Issue{})
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{"labels", "dependencies", "dependents", "comments"} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s field omitted from JSON output", field)
			continue
		}
		if string(val) != "[]" {
			t.Errorf("%s = %s, want [] (not null)", field, val)
		}
	}
}

// ---- JSON serialization integration -----------------------------------------

func TestIssueFromEntity_FullJSON_RoundTrip(t *testing.T) {
	issue := newFullIssue()
	labels := []string{"critical"}
	deps := []DependencyRef{{ID: "d-1", Title: "Dep", Status: "closed", Priority: 0, Type: "blocks", IssueType: "task"}}
	depts := []DependencyRef{{ID: "dt-1", Title: "Dept", Status: "open", Priority: 2, Type: "blocks", IssueType: "feature"}}
	comments := []CommentResponse{{ID: 1, Author: "alice", Text: "ok", CreatedAt: fixedTime}}

	resp := IssueFromEntity(issue,
		WithLabels(labels),
		WithDependencies(deps),
		WithDependents(depts),
		WithComments(comments),
		WithParent("epic-99", "Epic Title"),
		WithCounts(3, 7),
	)

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got IssueResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Spot-check key fields survive round-trip
	if got.ID != "proj-42" {
		t.Errorf("ID = %q, want %q", got.ID, "proj-42")
	}
	if got.Status != "open" {
		t.Errorf("Status = %q, want %q", got.Status, "open")
	}
	if got.IssueType != "bug" {
		t.Errorf("IssueType = %q, want %q", got.IssueType, "bug")
	}
	if len(got.Labels) != 1 || got.Labels[0] != "critical" {
		t.Errorf("Labels = %v, want [critical]", got.Labels)
	}
	if got.DependencyCount != 3 {
		t.Errorf("DependencyCount = %d, want 3", got.DependencyCount)
	}
	if got.DependentCount != 7 {
		t.Errorf("DependentCount = %d, want 7", got.DependentCount)
	}
	if got.Parent == nil || *got.Parent != "epic-99" {
		t.Errorf("Parent = %v, want %q", got.Parent, "epic-99")
	}
	if got.ParentTitle == nil || *got.ParentTitle != "Epic Title" {
		t.Errorf("ParentTitle = %v, want %q", got.ParentTitle, "Epic Title")
	}
	if !got.Pinned {
		t.Error("Pinned = false, want true")
	}
	if got.EstimatedMinutes == nil || *got.EstimatedMinutes != 90 {
		t.Errorf("EstimatedMinutes = %v, want 90", got.EstimatedMinutes)
	}
	if got.ExternalRef == nil || *got.ExternalRef != "GH-123" {
		t.Errorf("ExternalRef = %v, want %q", got.ExternalRef, "GH-123")
	}
}

func TestIssueFromEntity_NilIssue_OptionalFieldsOmittedInJSON(t *testing.T) {
	resp := IssueFromEntity(nil)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	// Pointer fields should be omitted when nil
	for _, field := range []string{"closed_at", "due_at", "defer_until", "external_ref", "estimated_minutes", "parent", "parent_title"} {
		if _, ok := raw[field]; ok {
			t.Errorf("%s should be omitted when nil, but present: %s", field, raw[field])
		}
	}
}
