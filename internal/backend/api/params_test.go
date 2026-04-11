package api

import (
	"net/url"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func intPtr(v int) *int       { return &v }
func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }

// --- listOptsToQuery ---

func TestListOptsToQuery_Empty(t *testing.T) {
	q := listOptsToQuery(backend.ListOpts{})
	if q != "" {
		t.Errorf("empty opts yielded %q, want empty", q)
	}
}

func TestListOptsToQuery_CoreFilters(t *testing.T) {
	p := 2
	opts := backend.ListOpts{
		Status:      "open",
		Priority:    &p,
		IssueType:   "task",
		Assignee:    "agent-1",
		Labels:      []string{"urgent", "bug"},
		SourceRepos: []string{"repo-a", "repo-b"},
		Limit:       25,
	}
	q := listOptsToQuery(opts)
	values, err := url.ParseQuery(q)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	checks := map[string]string{
		"status":       "open",
		"priority":     "2",
		"type":         "task",
		"assignee":     "agent-1",
		"labels":       "urgent,bug",
		"source_repos": "repo-a,repo-b",
		"limit":        "25",
	}
	for k, want := range checks {
		if got := values.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestListOptsToQuery_SearchAndDates(t *testing.T) {
	opts := backend.ListOpts{
		Query:               "hello",
		TitleContains:       "foo",
		DescriptionContains: "bar",
		NotesContains:       "baz",
		CreatedAfter:        "2026-01-01",
		CreatedBefore:       "2026-12-31",
		UpdatedAfter:        "2026-02-01",
		UpdatedBefore:       "2026-11-30",
	}
	q := listOptsToQuery(opts)
	values, _ := url.ParseQuery(q)
	for k, want := range map[string]string{
		"q":                    "hello",
		"title_contains":       "foo",
		"description_contains": "bar",
		"notes_contains":       "baz",
		"created_after":        "2026-01-01",
		"created_before":       "2026-12-31",
		"updated_after":        "2026-02-01",
		"updated_before":       "2026-11-30",
	} {
		if got := values.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestListOptsToQuery_AdvancedFilters(t *testing.T) {
	truePinned := true
	opts := backend.ListOpts{
		EmptyDescription: true,
		NoAssignee:       true,
		NoLabels:         true,
		Pinned:           &truePinned,
		ExcludeStatus:    []string{"closed", "tombstone"},
	}
	q := listOptsToQuery(opts)
	values, _ := url.ParseQuery(q)
	if values.Get("empty_description") != "true" {
		t.Errorf("empty_description missing")
	}
	if values.Get("no_assignee") != "true" {
		t.Errorf("no_assignee missing")
	}
	if values.Get("no_labels") != "true" {
		t.Errorf("no_labels missing")
	}
	if values.Get("pinned") != "true" {
		t.Errorf("pinned = %q, want true", values.Get("pinned"))
	}
	if values.Get("exclude_status") != "closed,tombstone" {
		t.Errorf("exclude_status = %q", values.Get("exclude_status"))
	}
}

func TestListOptsToQuery_NilPointerFieldsOmitted(t *testing.T) {
	q := listOptsToQuery(backend.ListOpts{Status: "open"})
	if strings.Contains(q, "priority") {
		t.Errorf("nil priority should not appear: %q", q)
	}
	if strings.Contains(q, "pinned") {
		t.Errorf("nil pinned should not appear: %q", q)
	}
}

func TestListOptsToQuery_PinnedFalse(t *testing.T) {
	f := false
	q := listOptsToQuery(backend.ListOpts{Pinned: &f})
	values, _ := url.ParseQuery(q)
	if values.Get("pinned") != "false" {
		t.Errorf("pinned = %q, want false", values.Get("pinned"))
	}
}

func TestListOptsToQuery_BoolFalseOmitted(t *testing.T) {
	q := listOptsToQuery(backend.ListOpts{EmptyDescription: false, NoAssignee: false})
	if strings.Contains(q, "empty_description") || strings.Contains(q, "no_assignee") {
		t.Errorf("false bools should be omitted: %q", q)
	}
}

// --- readyOptsToQuery ---

func TestReadyOptsToQuery_Empty(t *testing.T) {
	if q := readyOptsToQuery(backend.ReadyOpts{}); q != "" {
		t.Errorf("empty = %q", q)
	}
}

func TestReadyOptsToQuery_AllFields(t *testing.T) {
	p := 3
	opts := backend.ReadyOpts{
		Assignee:        "alice",
		Unassigned:      true,
		Priority:        &p,
		Type:            "task",
		ParentID:        "epic-1",
		Limit:           10,
		SortPolicy:      "priority",
		Labels:          []string{"l1", "l2"},
		LabelsAny:       []string{"any1"},
		MolType:         "atom",
		IncludeDeferred: true,
		SourceRepos:     []string{"r1"},
	}
	q := readyOptsToQuery(opts)
	values, _ := url.ParseQuery(q)
	checks := map[string]string{
		"assignee":         "alice",
		"unassigned":       "true",
		"priority":         "3",
		"type":             "task",
		"parent_id":        "epic-1",
		"limit":            "10",
		"sort":             "priority",
		"labels":           "l1,l2",
		"labels_any":       "any1",
		"mol_type":         "atom",
		"include_deferred": "true",
		"source_repos":     "r1",
	}
	for k, want := range checks {
		if got := values.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// --- blockedOptsToQuery ---

func TestBlockedOptsToQuery_Empty(t *testing.T) {
	if q := blockedOptsToQuery(backend.BlockedOpts{}); q != "" {
		t.Errorf("empty = %q", q)
	}
}

func TestBlockedOptsToQuery_AllFields(t *testing.T) {
	p := 1
	opts := backend.BlockedOpts{
		ParentID: "epic-9",
		Assignee: "bob",
		Priority: &p,
		Type:     "feature",
		Limit:    5,
	}
	q := blockedOptsToQuery(opts)
	values, _ := url.ParseQuery(q)
	for k, want := range map[string]string{
		"parent_id": "epic-9",
		"assignee":  "bob",
		"priority":  "1",
		"type":      "feature",
		"limit":     "5",
	} {
		if got := values.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// --- updateParamsToPatchRequest ---

func TestUpdateParamsToPatchRequest_Empty(t *testing.T) {
	req := updateParamsToPatchRequest(backend.UpdateParams{})
	if req.Title != nil {
		t.Errorf("Title should be nil")
	}
	if req.Status != nil {
		t.Errorf("Status should be nil")
	}
	if req.AgentState != nil {
		t.Errorf("AgentState should be nil")
	}
	if req.AddLabels != nil {
		t.Errorf("AddLabels should be nil")
	}
	if req.RemoveLabels != nil {
		t.Errorf("RemoveLabels should be nil")
	}
	if req.SetLabels != nil {
		t.Errorf("SetLabels should be nil")
	}
	if req.Priority != nil {
		t.Errorf("Priority should be nil")
	}
}

func TestUpdateParamsToPatchRequest_SetPointers(t *testing.T) {
	title := "new title"
	status := "in_progress"
	agentState := "working"
	priority := 1
	params := backend.UpdateParams{
		Title:      &title,
		Status:     &status,
		AgentState: &agentState,
		Priority:   &priority,
	}
	req := updateParamsToPatchRequest(params)
	if req.Title == nil || *req.Title != title {
		t.Errorf("Title = %v, want %q", req.Title, title)
	}
	if req.Status == nil || string(*req.Status) != status {
		t.Errorf("Status = %v, want %q", req.Status, status)
	}
	if req.AgentState == nil || string(*req.AgentState) != agentState {
		t.Errorf("AgentState = %v, want %q", req.AgentState, agentState)
	}
	if req.Priority == nil || *req.Priority != priority {
		t.Errorf("Priority = %v, want %d", req.Priority, priority)
	}
}

func TestUpdateParamsToPatchRequest_Labels(t *testing.T) {
	params := backend.UpdateParams{
		AddLabels:    []string{"a", "b"},
		RemoveLabels: []string{"c"},
		SetLabels:    []string{"d", "e", "f"},
	}
	req := updateParamsToPatchRequest(params)
	if req.AddLabels == nil || len(*req.AddLabels) != 2 {
		t.Errorf("AddLabels = %v", req.AddLabels)
	}
	if req.RemoveLabels == nil || len(*req.RemoveLabels) != 1 {
		t.Errorf("RemoveLabels = %v", req.RemoveLabels)
	}
	if req.SetLabels == nil || len(*req.SetLabels) != 3 {
		t.Errorf("SetLabels = %v", req.SetLabels)
	}
}

func TestUpdateParamsToPatchRequest_EmptyLabelSlicesOmitted(t *testing.T) {
	req := updateParamsToPatchRequest(backend.UpdateParams{
		AddLabels:    []string{},
		RemoveLabels: nil,
		SetLabels:    []string{},
	})
	if req.AddLabels != nil {
		t.Errorf("AddLabels should be nil when empty")
	}
	if req.RemoveLabels != nil {
		t.Errorf("RemoveLabels should be nil when empty")
	}
	if req.SetLabels != nil {
		t.Errorf("SetLabels should be nil when empty")
	}
}

func TestUpdateParamsToPatchRequest_PlainFields(t *testing.T) {
	desc := "desc"
	assignee := "alice"
	params := backend.UpdateParams{
		Description: &desc,
		Assignee:    &assignee,
	}
	req := updateParamsToPatchRequest(params)
	if req.Description == nil || *req.Description != desc {
		t.Errorf("Description = %v", req.Description)
	}
	if req.Assignee == nil || *req.Assignee != assignee {
		t.Errorf("Assignee = %v", req.Assignee)
	}
}

// --- createParamsToCreateRequest ---

func TestCreateParamsToCreateRequest_AllFields(t *testing.T) {
	est := 30
	params := backend.CreateParams{
		ID:                 "loom-123",
		Parent:             "epic-1",
		Title:              "Do the thing",
		Description:        "description",
		IssueType:          "task",
		Priority:           2,
		Design:             "design text",
		AcceptanceCriteria: "AC",
		Notes:              "notes",
		Assignee:           "alice",
		Owner:              "bob",
		CreatedBy:          "charlie",
		ExternalRef:        "JIRA-1",
		EstimatedMinutes:   &est,
		Labels:             []string{"a", "b"},
		Dependencies:       []string{"loom-1", "loom-2"},
		DueAt:              "2026-06-01",
		DeferUntil:         "2026-05-01",
	}
	req := createParamsToCreateRequest(params)
	if req.Title != "Do the thing" {
		t.Errorf("Title = %q", req.Title)
	}
	if string(req.IssueType) != "task" {
		t.Errorf("IssueType = %q", req.IssueType)
	}
	if req.Priority != 2 {
		t.Errorf("Priority = %d", req.Priority)
	}
	if req.Id == nil || *req.Id != "loom-123" {
		t.Errorf("Id = %v", req.Id)
	}
	if req.Parent == nil || *req.Parent != "epic-1" {
		t.Errorf("Parent = %v", req.Parent)
	}
	if req.Description == nil || *req.Description != "description" {
		t.Errorf("Description = %v", req.Description)
	}
	if req.Design == nil || *req.Design != "design text" {
		t.Errorf("Design = %v", req.Design)
	}
	if req.AcceptanceCriteria == nil || *req.AcceptanceCriteria != "AC" {
		t.Errorf("AC = %v", req.AcceptanceCriteria)
	}
	if req.Notes == nil || *req.Notes != "notes" {
		t.Errorf("Notes = %v", req.Notes)
	}
	if req.Assignee == nil || *req.Assignee != "alice" {
		t.Errorf("Assignee = %v", req.Assignee)
	}
	if req.Owner == nil || *req.Owner != "bob" {
		t.Errorf("Owner = %v", req.Owner)
	}
	if req.CreatedBy == nil || *req.CreatedBy != "charlie" {
		t.Errorf("CreatedBy = %v", req.CreatedBy)
	}
	if req.ExternalRef == nil || *req.ExternalRef != "JIRA-1" {
		t.Errorf("ExternalRef = %v", req.ExternalRef)
	}
	if req.EstimatedMinutes == nil || *req.EstimatedMinutes != 30 {
		t.Errorf("EstimatedMinutes = %v", req.EstimatedMinutes)
	}
	if req.Labels == nil || len(*req.Labels) != 2 {
		t.Errorf("Labels = %v", req.Labels)
	}
	if req.Dependencies == nil || len(*req.Dependencies) != 2 {
		t.Errorf("Dependencies = %v", req.Dependencies)
	}
	if req.DueAt == nil || *req.DueAt != "2026-06-01" {
		t.Errorf("DueAt = %v", req.DueAt)
	}
	if req.DeferUntil == nil || *req.DeferUntil != "2026-05-01" {
		t.Errorf("DeferUntil = %v", req.DeferUntil)
	}
}

func TestCreateParamsToCreateRequest_MinimalFields(t *testing.T) {
	params := backend.CreateParams{
		Title:     "Minimal",
		IssueType: "bug",
		Priority:  0,
	}
	req := createParamsToCreateRequest(params)
	if req.Title != "Minimal" {
		t.Errorf("Title = %q", req.Title)
	}
	if req.Id != nil {
		t.Errorf("Id should be nil, got %v", req.Id)
	}
	if req.Description != nil {
		t.Errorf("Description should be nil")
	}
	if req.Labels != nil {
		t.Errorf("Labels should be nil")
	}
	if req.Dependencies != nil {
		t.Errorf("Dependencies should be nil")
	}
	if req.EstimatedMinutes != nil {
		t.Errorf("EstimatedMinutes should be nil")
	}
}

// --- Helpers ---

func TestJoinCSV(t *testing.T) {
	q := url.Values{}
	joinCSV(q, "key", []string{"a", "b", "c"})
	if got := q.Get("key"); got != "a,b,c" {
		t.Errorf("got %q, want a,b,c", got)
	}

	// Empty slice should be no-op
	q2 := url.Values{}
	joinCSV(q2, "key", nil)
	if q2.Get("key") != "" {
		t.Errorf("nil slice should be no-op")
	}
	joinCSV(q2, "key", []string{})
	if q2.Get("key") != "" {
		t.Errorf("empty slice should be no-op")
	}

	// Single value
	q3 := url.Values{}
	joinCSV(q3, "key", []string{"only"})
	if q3.Get("key") != "only" {
		t.Errorf("single value = %q", q3.Get("key"))
	}
}

func TestSetNonEmpty(t *testing.T) {
	q := url.Values{}
	setNonEmpty(q, "a", "")
	if q.Has("a") {
		t.Errorf("empty should not set")
	}
	setNonEmpty(q, "a", "val")
	if q.Get("a") != "val" {
		t.Errorf("got %q", q.Get("a"))
	}
}

func TestSetOptInt(t *testing.T) {
	q := url.Values{}
	setOptInt(q, "p", nil)
	if q.Has("p") {
		t.Errorf("nil should not set")
	}
	v := 5
	setOptInt(q, "p", &v)
	if q.Get("p") != "5" {
		t.Errorf("got %q", q.Get("p"))
	}
}

func TestSetOptBool(t *testing.T) {
	q := url.Values{}
	setOptBool(q, "b", nil)
	if q.Has("b") {
		t.Errorf("nil should not set")
	}
	tr := true
	setOptBool(q, "b", &tr)
	if q.Get("b") != "true" {
		t.Errorf("got %q", q.Get("b"))
	}
	f := false
	setOptBool(q, "b2", &f)
	if q.Get("b2") != "false" {
		t.Errorf("got %q", q.Get("b2"))
	}
}

func TestSetBoolIfTrue(t *testing.T) {
	q := url.Values{}
	setBoolIfTrue(q, "b", false)
	if q.Has("b") {
		t.Errorf("false should not set")
	}
	setBoolIfTrue(q, "b", true)
	if q.Get("b") != "true" {
		t.Errorf("got %q", q.Get("b"))
	}
}
