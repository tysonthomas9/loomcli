package dto

import (
	"encoding/json"
	"testing"
	"time"
)

func TestIssueResponse_FullMarshal(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	dueAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	deferUntil := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	extRef := "GH-123"
	estMin := 90
	parent := "proj-100"
	parentTitle := "Epic title"
	parentID := int64(1)

	resp := IssueResponse{
		ID:                 "proj-42",
		Title:              "Fix bug",
		Description:        "A bug fix",
		Design:             "## Design",
		AcceptanceCriteria: "Works",
		Notes:              "Urgent",
		Status:             "open",
		Priority:           2,
		IssueType:          "bug",
		Assignee:           "alice",
		Owner:              "bob",
		EstimatedMinutes:   &estMin,
		CreatedAt:          now,
		UpdatedAt:          now,
		ClosedAt:           &closedAt,
		CloseReason:        "fixed",
		DueAt:              &dueAt,
		DeferUntil:         &deferUntil,
		ExternalRef:        &extRef,
		SourceRepo:         "myrepo",
		Labels:             []string{"urgent", "auth"},
		Dependencies: []DependencyRef{
			{ID: "proj-41", Title: "Dep", Status: "closed", Priority: 1, Type: "blocks", IssueType: "task"},
		},
		Dependents: []DependencyRef{
			{ID: "proj-43", Title: "Dependent", Status: "open", Priority: 3, Type: "blocks", IssueType: "feature"},
		},
		Comments: []CommentResponse{
			{ID: 1, Author: "alice", Text: "LGTM", CreatedAt: now, ParentID: &parentID},
		},
		Parent:          &parent,
		ParentTitle:     &parentTitle,
		DependencyCount: 1,
		DependentCount:  1,
		Pinned:          true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got IssueResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != resp.ID {
		t.Errorf("ID = %q, want %q", got.ID, resp.ID)
	}
	if got.Title != resp.Title {
		t.Errorf("Title = %q, want %q", got.Title, resp.Title)
	}
	if got.Status != resp.Status {
		t.Errorf("Status = %q, want %q", got.Status, resp.Status)
	}
	if got.Priority != resp.Priority {
		t.Errorf("Priority = %d, want %d", got.Priority, resp.Priority)
	}
	if got.IssueType != resp.IssueType {
		t.Errorf("IssueType = %q, want %q", got.IssueType, resp.IssueType)
	}
	if got.EstimatedMinutes == nil || *got.EstimatedMinutes != estMin {
		t.Errorf("EstimatedMinutes = %v, want %d", got.EstimatedMinutes, estMin)
	}
	if got.ClosedAt == nil || !got.ClosedAt.Equal(closedAt) {
		t.Errorf("ClosedAt = %v, want %v", got.ClosedAt, closedAt)
	}
	if got.DueAt == nil || !got.DueAt.Equal(dueAt) {
		t.Errorf("DueAt = %v, want %v", got.DueAt, dueAt)
	}
	if got.DeferUntil == nil || !got.DeferUntil.Equal(deferUntil) {
		t.Errorf("DeferUntil = %v, want %v", got.DeferUntil, deferUntil)
	}
	if got.ExternalRef == nil || *got.ExternalRef != extRef {
		t.Errorf("ExternalRef = %v, want %q", got.ExternalRef, extRef)
	}
	if len(got.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(got.Labels))
	}
	if len(got.Dependencies) != 1 {
		t.Errorf("Dependencies len = %d, want 1", len(got.Dependencies))
	}
	if len(got.Dependents) != 1 {
		t.Errorf("Dependents len = %d, want 1", len(got.Dependents))
	}
	if len(got.Comments) != 1 {
		t.Errorf("Comments len = %d, want 1", len(got.Comments))
	}
	if got.Parent == nil || *got.Parent != parent {
		t.Errorf("Parent = %v, want %q", got.Parent, parent)
	}
	if got.ParentTitle == nil || *got.ParentTitle != parentTitle {
		t.Errorf("ParentTitle = %v, want %q", got.ParentTitle, parentTitle)
	}
	if !got.Pinned {
		t.Error("Pinned = false, want true")
	}
}

func TestIssueResponse_EmptyLabelsNotOmitted(t *testing.T) {
	resp := IssueResponse{
		ID:           "proj-1",
		Labels:       []string{},
		Dependencies: []DependencyRef{},
		Dependents:   []DependencyRef{},
		Comments:     []CommentResponse{},
	}

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

// TestIssueResponse_NilSlicesSerializeAsNull documents raw Go serialization
// behavior for nil slices. The mapping layer (moom5.3) MUST initialize these
// slices to []T{} before constructing IssueResponse. Sending null to the
// frontend is a bug — this test exists to document the footgun, not to
// validate null as an acceptable wire value.
func TestIssueResponse_NilSlicesSerializeAsNull(t *testing.T) {
	resp := IssueResponse{
		ID:           "proj-1",
		Labels:       nil,
		Dependencies: nil,
		Dependents:   nil,
		Comments:     nil,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	// nil slices without omitempty serialize as null, not omitted
	for _, field := range []string{"labels", "dependencies", "dependents", "comments"} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s field omitted from JSON output", field)
			continue
		}
		if string(val) != "null" {
			t.Errorf("%s = %s, want null", field, val)
		}
	}
}

func TestIssueResponse_NilOptionalFieldsOmitted(t *testing.T) {
	resp := IssueResponse{
		ID:           "proj-1",
		Labels:       []string{},
		Dependencies: []DependencyRef{},
		Dependents:   []DependencyRef{},
		Comments:     []CommentResponse{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{"closed_at", "due_at", "defer_until", "external_ref", "estimated_minutes"} {
		if _, ok := raw[field]; ok {
			t.Errorf("%s should be omitted when nil, but present: %s", field, raw[field])
		}
	}

	// Non-pointer zero-value fields should still be present
	for _, field := range []string{"pinned", "dependency_count", "dependent_count"} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s should be present even when zero-value, but omitted", field)
			continue
		}
		if field == "pinned" && string(val) != "false" {
			t.Errorf("%s = %s, want false", field, val)
		}
		if field != "pinned" && string(val) != "0" {
			t.Errorf("%s = %s, want 0", field, val)
		}
	}
}

func TestDependencyRef_RoundTrip(t *testing.T) {
	ref := DependencyRef{
		ID:        "proj-41",
		Title:     "Blocker task",
		Status:    "closed",
		Priority:  0,
		Type:      "blocks",
		IssueType: "task",
	}

	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got DependencyRef
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got != ref {
		t.Errorf("got %+v, want %+v", got, ref)
	}
}

func TestCommentResponse_RoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	editedAt := time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC)
	parentID := int64(5)

	c := CommentResponse{
		ID:        42,
		Author:    "alice",
		Text:      "Great work!",
		CreatedAt: now,
		ParentID:  &parentID,
		EditedAt:  &editedAt,
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got CommentResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != c.ID {
		t.Errorf("ID = %d, want %d", got.ID, c.ID)
	}
	if got.Author != c.Author {
		t.Errorf("Author = %q, want %q", got.Author, c.Author)
	}
	if got.Text != c.Text {
		t.Errorf("Text = %q, want %q", got.Text, c.Text)
	}
	if got.ParentID == nil || *got.ParentID != parentID {
		t.Errorf("ParentID = %v, want %d", got.ParentID, parentID)
	}
	if got.EditedAt == nil || !got.EditedAt.Equal(editedAt) {
		t.Errorf("EditedAt = %v, want %v", got.EditedAt, editedAt)
	}
}

func TestCommentResponse_Minimal(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	c := CommentResponse{
		ID:        1,
		Author:    "bob",
		Text:      "Hello",
		CreatedAt: now,
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	// Optional fields should be omitted
	if _, ok := raw["parent_id"]; ok {
		t.Error("parent_id should be omitted when nil")
	}
	if _, ok := raw["edited_at"]; ok {
		t.Error("edited_at should be omitted when nil")
	}

	// Required fields should be present
	for _, field := range []string{"id", "author", "text", "created_at"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("%s should be present", field)
		}
	}
}
