package dto

import (
	"encoding/json"
	"testing"
)

func TestCreateIssueRequest_RoundTrip(t *testing.T) {
	estMin := 120
	req := CreateIssueRequest{
		Title:              "Fix login bug",
		IssueType:          "bug",
		Priority:           2,
		ID:                 "proj-123",
		Parent:             "proj-100",
		Description:        "Users can't log in",
		Status:             "deferred",
		Design:             "## Design\nFix the auth flow",
		AcceptanceCriteria: "Login works",
		Notes:              "Urgent",
		Assignee:           "alice",
		Owner:              "bob",
		CreatedBy:          "charlie",
		ExternalRef:        "GH-456",
		EstimatedMinutes:   &estMin,
		Labels:             []string{"urgent", "auth"},
		Dependencies:       []string{"proj-99"},
		DueAt:              "2026-04-10T00:00:00Z",
		DeferUntil:         "2026-04-05T00:00:00Z",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got CreateIssueRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Title != req.Title {
		t.Errorf("Title = %q, want %q", got.Title, req.Title)
	}
	if got.IssueType != req.IssueType {
		t.Errorf("IssueType = %q, want %q", got.IssueType, req.IssueType)
	}
	if got.Priority != req.Priority {
		t.Errorf("Priority = %d, want %d", got.Priority, req.Priority)
	}
	if got.ID != req.ID {
		t.Errorf("ID = %q, want %q", got.ID, req.ID)
	}
	if got.Parent != req.Parent {
		t.Errorf("Parent = %q, want %q", got.Parent, req.Parent)
	}
	if got.Description != req.Description {
		t.Errorf("Description = %q, want %q", got.Description, req.Description)
	}
	if got.Status != req.Status {
		t.Errorf("Status = %q, want %q", got.Status, req.Status)
	}
	if got.Design != req.Design {
		t.Errorf("Design = %q, want %q", got.Design, req.Design)
	}
	if got.AcceptanceCriteria != req.AcceptanceCriteria {
		t.Errorf("AcceptanceCriteria = %q, want %q", got.AcceptanceCriteria, req.AcceptanceCriteria)
	}
	if got.Notes != req.Notes {
		t.Errorf("Notes = %q, want %q", got.Notes, req.Notes)
	}
	if got.Assignee != req.Assignee {
		t.Errorf("Assignee = %q, want %q", got.Assignee, req.Assignee)
	}
	if got.Owner != req.Owner {
		t.Errorf("Owner = %q, want %q", got.Owner, req.Owner)
	}
	if got.CreatedBy != req.CreatedBy {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, req.CreatedBy)
	}
	if got.ExternalRef != req.ExternalRef {
		t.Errorf("ExternalRef = %q, want %q", got.ExternalRef, req.ExternalRef)
	}
	if got.EstimatedMinutes == nil || *got.EstimatedMinutes != estMin {
		t.Errorf("EstimatedMinutes = %v, want %d", got.EstimatedMinutes, estMin)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "urgent" || got.Labels[1] != "auth" {
		t.Errorf("Labels = %v, want [urgent auth]", got.Labels)
	}
	if len(got.Dependencies) != 1 || got.Dependencies[0] != "proj-99" {
		t.Errorf("Dependencies = %v, want [proj-99]", got.Dependencies)
	}
	if got.DueAt != req.DueAt {
		t.Errorf("DueAt = %q, want %q", got.DueAt, req.DueAt)
	}
	if got.DeferUntil != req.DeferUntil {
		t.Errorf("DeferUntil = %q, want %q", got.DeferUntil, req.DeferUntil)
	}
}

func TestCreateIssueRequest_Minimal(t *testing.T) {
	input := `{"title":"t","issue_type":"task","priority":0}`
	var got CreateIssueRequest
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Title != "t" {
		t.Errorf("Title = %q, want %q", got.Title, "t")
	}
	if got.IssueType != "task" {
		t.Errorf("IssueType = %q, want %q", got.IssueType, "task")
	}
	if got.Priority != 0 {
		t.Errorf("Priority = %d, want 0", got.Priority)
	}
	if got.ID != "" {
		t.Errorf("ID = %q, want empty", got.ID)
	}
	if got.EstimatedMinutes != nil {
		t.Errorf("EstimatedMinutes = %v, want nil", got.EstimatedMinutes)
	}
	if got.Labels != nil {
		t.Errorf("Labels = %v, want nil", got.Labels)
	}
}

func TestCreateIssueRequest_PriorityZeroPreserved(t *testing.T) {
	req := CreateIssueRequest{
		Title:     "P0 bug",
		IssueType: "bug",
		Priority:  0,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify JSON contains "priority":0
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}
	if _, ok := raw["priority"]; !ok {
		t.Fatal("priority field omitted from JSON output")
	}
	if string(raw["priority"]) != "0" {
		t.Errorf("priority = %s, want 0", raw["priority"])
	}

	// Round-trip
	var got CreateIssueRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Priority != 0 {
		t.Errorf("Priority = %d, want 0", got.Priority)
	}
}

func TestPatchIssueRequest_RoundTrip(t *testing.T) {
	title := "New title"
	desc := "New desc"
	status := "in_progress"
	prio := 1
	assignee := "alice"
	owner := "bob"
	design := "## New design"
	ac := "New AC"
	notes := "New notes"
	extRef := "GH-789"
	estMin := 60
	issueType := "feature"
	pinned := true
	parent := "proj-100"
	dueAt := "2026-05-01T00:00:00Z"
	deferUntil := "2026-04-15T00:00:00Z"

	req := PatchIssueRequest{
		Title:              &title,
		Description:        &desc,
		Status:             &status,
		Priority:           &prio,
		Assignee:           &assignee,
		Owner:              &owner,
		Design:             &design,
		AcceptanceCriteria: &ac,
		Notes:              &notes,
		ExternalRef:        &extRef,
		EstimatedMinutes:   &estMin,
		IssueType:          &issueType,
		AddLabels:          []string{"new-label"},
		RemoveLabels:       []string{"old-label"},
		SetLabels:          []string{"only-label"},
		Pinned:             &pinned,
		Parent:             &parent,
		DueAt:              &dueAt,
		DeferUntil:         &deferUntil,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got PatchIssueRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Title == nil || *got.Title != title {
		t.Errorf("Title = %v, want %q", got.Title, title)
	}
	if got.Description == nil || *got.Description != desc {
		t.Errorf("Description = %v, want %q", got.Description, desc)
	}
	if got.Status == nil || *got.Status != status {
		t.Errorf("Status = %v, want %q", got.Status, status)
	}
	if got.Priority == nil || *got.Priority != prio {
		t.Errorf("Priority = %v, want %d", got.Priority, prio)
	}
	if got.Assignee == nil || *got.Assignee != assignee {
		t.Errorf("Assignee = %v, want %q", got.Assignee, assignee)
	}
	if got.Owner == nil || *got.Owner != owner {
		t.Errorf("Owner = %v, want %q", got.Owner, owner)
	}
	if got.Design == nil || *got.Design != design {
		t.Errorf("Design = %v, want %q", got.Design, design)
	}
	if got.AcceptanceCriteria == nil || *got.AcceptanceCriteria != ac {
		t.Errorf("AcceptanceCriteria = %v, want %q", got.AcceptanceCriteria, ac)
	}
	if got.Notes == nil || *got.Notes != notes {
		t.Errorf("Notes = %v, want %q", got.Notes, notes)
	}
	if got.ExternalRef == nil || *got.ExternalRef != extRef {
		t.Errorf("ExternalRef = %v, want %q", got.ExternalRef, extRef)
	}
	if got.EstimatedMinutes == nil || *got.EstimatedMinutes != estMin {
		t.Errorf("EstimatedMinutes = %v, want %d", got.EstimatedMinutes, estMin)
	}
	if got.IssueType == nil || *got.IssueType != issueType {
		t.Errorf("IssueType = %v, want %q", got.IssueType, issueType)
	}
	if len(got.AddLabels) != 1 || got.AddLabels[0] != "new-label" {
		t.Errorf("AddLabels = %v, want [new-label]", got.AddLabels)
	}
	if len(got.RemoveLabels) != 1 || got.RemoveLabels[0] != "old-label" {
		t.Errorf("RemoveLabels = %v, want [old-label]", got.RemoveLabels)
	}
	if len(got.SetLabels) != 1 || got.SetLabels[0] != "only-label" {
		t.Errorf("SetLabels = %v, want [only-label]", got.SetLabels)
	}
	if got.Pinned == nil || *got.Pinned != pinned {
		t.Errorf("Pinned = %v, want %v", got.Pinned, pinned)
	}
	if got.Parent == nil || *got.Parent != parent {
		t.Errorf("Parent = %v, want %q", got.Parent, parent)
	}
	if got.DueAt == nil || *got.DueAt != dueAt {
		t.Errorf("DueAt = %v, want %q", got.DueAt, dueAt)
	}
	if got.DeferUntil == nil || *got.DeferUntil != deferUntil {
		t.Errorf("DeferUntil = %v, want %q", got.DeferUntil, deferUntil)
	}
}

func TestPatchIssueRequest_Partial(t *testing.T) {
	input := `{"title":"new"}`
	var got PatchIssueRequest
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Title == nil || *got.Title != "new" {
		t.Errorf("Title = %v, want 'new'", got.Title)
	}
	if got.Description != nil {
		t.Errorf("Description = %v, want nil", got.Description)
	}
	if got.Status != nil {
		t.Errorf("Status = %v, want nil", got.Status)
	}
	if got.Priority != nil {
		t.Errorf("Priority = %v, want nil", got.Priority)
	}
	if got.Pinned != nil {
		t.Errorf("Pinned = %v, want nil", got.Pinned)
	}
	if got.AddLabels != nil {
		t.Errorf("AddLabels = %v, want nil", got.AddLabels)
	}
}

func TestPatchIssueRequest_EmptyJSON(t *testing.T) {
	input := `{}`
	var got PatchIssueRequest
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Title != nil {
		t.Errorf("Title = %v, want nil", got.Title)
	}
	if got.Status != nil {
		t.Errorf("Status = %v, want nil", got.Status)
	}
	if got.Priority != nil {
		t.Errorf("Priority = %v, want nil", got.Priority)
	}
	if got.AddLabels != nil {
		t.Errorf("AddLabels = %v, want nil", got.AddLabels)
	}
	if got.RemoveLabels != nil {
		t.Errorf("RemoveLabels = %v, want nil", got.RemoveLabels)
	}
	if got.SetLabels != nil {
		t.Errorf("SetLabels = %v, want nil", got.SetLabels)
	}
}

func TestPatchIssueRequest_LabelOperations(t *testing.T) {
	input := `{"add_labels":["a","b"],"remove_labels":["c"],"set_labels":["d","e","f"]}`
	var got PatchIssueRequest
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.AddLabels) != 2 || got.AddLabels[0] != "a" || got.AddLabels[1] != "b" {
		t.Errorf("AddLabels = %v, want [a b]", got.AddLabels)
	}
	if len(got.RemoveLabels) != 1 || got.RemoveLabels[0] != "c" {
		t.Errorf("RemoveLabels = %v, want [c]", got.RemoveLabels)
	}
	if len(got.SetLabels) != 3 {
		t.Errorf("SetLabels = %v, want [d e f]", got.SetLabels)
	}
}
