package fleet

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/types"
)

// TestFleetIssueWire_FieldDriftGuard catches drift between fleetIssueWire
// and types.Issue. Both must carry the same field set so that fleet-db's
// wire shape projects losslessly into the canonical type. Adding a field
// to types.Issue without mirroring it here will silently drop that field
// on every fleet response.
//
// Compares the json-tagged keys of fleetIssueWire to the keys this package
// claims to project. Bumping the allowlist below documents the deliberate
// choice (e.g. fields fleet-db will never emit).
func TestFleetIssueWire_FieldDriftGuard(t *testing.T) {
	wireT := reflect.TypeOf(fleetIssueWire{})
	wireKeys := make(map[string]bool)
	for i := 0; i < wireT.NumField(); i++ {
		tag := wireT.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key := tag
		if idx := indexComma(tag); idx >= 0 {
			key = tag[:idx]
		}
		if key == "type" {
			wireKeys["kind"] = true
			continue
		}
		if key == "repo" || key == "source_repo" {
			wireKeys["repo_canonical"] = true
			continue
		}
		if key == "parent_id" {
			wireKeys["parent"] = true
			continue
		}
		wireKeys[key] = true
	}
	canonical := map[string]bool{
		"id": true, "title": true, "status": true, "priority": true,
		"kind": true, "assignee": true, "owner": true, "labels": true,
		"repo_canonical": true, "parent": true, "design": true,
		"design_artifact_id": true, "has_design": true, "description": true,
		"acceptance_criteria": true, "notes": true, "external_ref": true,
		"created_at": true, "created_by": true, "updated_at": true,
		"due_at": true, "defer_until": true, "closed_at": true,
		"close_reason": true,
	}
	for k := range canonical {
		if !wireKeys[k] {
			t.Errorf("fleetIssueWire missing canonical field %q — fleet-db responses will silently drop it", k)
		}
	}
	for k := range wireKeys {
		if !canonical[k] {
			t.Errorf("fleetIssueWire has %q but it isn't in the canonical set — update the test if intentional", k)
		}
	}
}

func indexComma(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return i
		}
	}
	return -1
}

// TestFleetIssueWire_RoundTrip verifies a maximally-populated wire shape
// projects to types.Issue without dropping fields, then back to
// backend.IssueData via issueToData with all values preserved.
func TestFleetIssueWire_RoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	closed := now.Add(2 * time.Hour)
	due := now.Add(24 * time.Hour)
	defer_ := now.Add(time.Hour)
	wire := fleetIssueWire{
		ID:          "PARITY-1",
		Title:       "round-trip",
		Status:      "closed",
		Priority:    1,
		Type:        "bug",
		Assignee:    "agent-a",
		Owner:       "owner@example.com",
		Labels:      []string{"x", "y"},
		Repo:        "repo",
		ParentID:    "EPIC-1",
		Design:      "design notes",
		Notes:       "BLOCKED: dep missing",
		Description: "desc",
		CreatedAt:   now,
		CreatedBy:   "creator",
		UpdatedAt:   now,
		DueAt:       &due,
		DeferUntil:  &defer_,
		ClosedAt:    &closed,
		CloseReason: "fixed",
	}
	d := fleetIssueWithCountsWire{fleetIssueWire: wire}.toIssueData()
	want := map[string]any{
		"ID": "PARITY-1", "Title": "round-trip", "Status": "closed",
		"Priority": 1, "IssueType": "bug",
		"Assignee": "agent-a", "Owner": "owner@example.com",
		"SourceRepo": "repo", "Parent": "EPIC-1", "Design": "design notes",
		"Notes":     "BLOCKED: dep missing",
		"CreatedBy": "creator", "CloseReason": "fixed",
	}
	got := map[string]any{
		"ID": d.ID, "Title": d.Title, "Status": d.Status,
		"Priority": d.Priority, "IssueType": d.IssueType,
		"Assignee": d.Assignee, "Owner": d.Owner,
		"SourceRepo": d.SourceRepo, "Parent": d.Parent, "Design": d.Design,
		"Notes":     d.Notes,
		"CreatedBy": d.CreatedBy, "CloseReason": d.CloseReason,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if d.ClosedAt == nil || !d.ClosedAt.Equal(closed) {
		t.Errorf("ClosedAt = %v, want %v", d.ClosedAt, closed)
	}
	if d.DueAt == nil || !d.DueAt.Equal(due) {
		t.Errorf("DueAt = %v, want %v", d.DueAt, due)
	}
	if d.DeferUntil == nil || !d.DeferUntil.Equal(defer_) {
		t.Errorf("DeferUntil = %v, want %v", d.DeferUntil, defer_)
	}
}

func TestIssueToData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issue := &types.Issue{
		ID:               "test-123",
		Title:            "Test Issue",
		Status:           types.StatusOpen,
		Priority:         2,
		IssueType:        types.TypeTask,
		Assignee:         "agent-1",
		Owner:            "owner@example.com",
		Labels:           []string{"bug", "urgent"},
		SourceRepo:       "repo-1",
		Design:           "some design",
		HasDesign:        true,
		DesignArtifactID: "design-test-123-hash",
		Notes:            "BLOCKED: waiting on upstream",
		CreatedAt:        now,
		UpdatedAt:        now,
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
	// Notes is in the slim list projection so the kanban "blocked with notes"
	// needs-attention state works without a detail fetch (regression guard).
	if d.Notes != "BLOCKED: waiting on upstream" {
		t.Errorf("Notes = %q, want %q", d.Notes, "BLOCKED: waiting on upstream")
	}
	if !d.HasDesign || d.DesignArtifactID != "design-test-123-hash" {
		t.Errorf("design reference = (%v, %q), want (true, %q)", d.HasDesign, d.DesignArtifactID, "design-test-123-hash")
	}
}

func TestFleetIssueWireArtifactDesignReference(t *testing.T) {
	var wire fleetIssueWithCountsWire
	err := json.Unmarshal([]byte(`{"id":"FLEET-1","title":"Artifact design","type":"task","has_design":true,"design_artifact_id":"design-fleet-1-hash"}`), &wire)
	if err != nil {
		t.Fatal(err)
	}
	d := wire.toIssueData()
	if d.Design != "" || !d.HasDesign || d.DesignArtifactID != "design-fleet-1-hash" {
		t.Fatalf("artifact design projection = %#v", d)
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

func TestCountIssuesResponse_ZeroValueGroups(t *testing.T) {
	// Verify that countIssuesResponse can be unmarshaled and that
	// missing keys in Groups produce zero values via Go map semantics.
	raw := `{"total": 5, "groups": {"open": 3, "closed": 2}}`
	var resp countIssuesResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.Total != 5 {
		t.Errorf("Total = %d, want 5", resp.Total)
	}
	if resp.Groups["open"] != 3 {
		t.Errorf("Groups[open] = %d, want 3", resp.Groups["open"])
	}
	// Missing key returns 0.
	if resp.Groups["in_progress"] != 0 {
		t.Errorf("Groups[in_progress] = %d, want 0", resp.Groups["in_progress"])
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
			fleetIssueWire: fleetIssueWire{ID: "test-1", Title: "Ready 1", Status: string(types.StatusOpen), CreatedAt: now, UpdatedAt: now},
			Parent:         &parent,
		},
		{
			fleetIssueWire: fleetIssueWire{ID: "test-2", Title: "Ready 2", Status: string(types.StatusOpen), CreatedAt: now, UpdatedAt: now},
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
	issues := []*blockedIssueWire{
		{
			fleetIssueWire: fleetIssueWire{ID: "test-1", Title: "Blocked", Status: string(types.StatusBlocked), CreatedAt: now, UpdatedAt: now},
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

func TestFleetIssueWire_RepoProjection(t *testing.T) {
	w := fleetIssueWire{Repo: "org/repo"}
	if got := w.toIssue().SourceRepo; got != "org/repo" {
		t.Errorf("SourceRepo = %q, want %q", got, "org/repo")
	}
}

func TestFleetIssueWire_ExternalRefProjection(t *testing.T) {
	ref := "https://github.com/owner/repo/pull/42"
	got := fleetIssueWire{ExternalRef: ref}.toIssue().ExternalRef
	if got == nil || *got != ref {
		t.Errorf("ExternalRef = %v, want %q", got, ref)
	}
	// Empty wire value projects to nil (omitted), not a pointer to "".
	if got := (fleetIssueWire{}).toIssue().ExternalRef; got != nil {
		t.Errorf("empty ExternalRef = %v, want nil", got)
	}
}
