package fleet

import (
	"net/url"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// --- updateParamsToPatchRequest tests ---
//
// These exercise the loom→fleet-db field mapping. fleet-db's
// UpdateIssueRequest schema is intentionally narrower than loom's
// UpdateParams: status / claim / labels / agent_state / assignee /
// acceptance_criteria / external_ref / estimated_minutes have dedicated
// endpoints (close, reopen, claim, label.add, etc.) and aren't accepted
// on PATCH. fleet-db enforces this with disallowUnknownFields, so loom
// must drop those keys here rather than silently shipping them.

func TestUpdateParamsToPatchRequest_DropsAgentState(t *testing.T) {
	state := "running"
	req := updateParamsToPatchRequest(backend.UpdateParams{AgentState: &state})
	if _, ok := req["agent_state"]; ok {
		t.Error("agent_state must be dropped — fleet-db rejects unknown fields")
	}
}

func TestUpdateParamsToPatchRequest_DropsStatus(t *testing.T) {
	status := "in_progress"
	req := updateParamsToPatchRequest(backend.UpdateParams{Status: &status})
	if _, ok := req["status"]; ok {
		t.Error("status must be dropped — use Close/Reopen/Claim instead")
	}
}

func TestUpdateParamsToPatchRequest_RenamesIssueTypeToType(t *testing.T) {
	it := "epic"
	req := updateParamsToPatchRequest(backend.UpdateParams{IssueType: &it})
	if _, ok := req["issue_type"]; ok {
		t.Error("issue_type key must be dropped — fleet-db expects 'type'")
	}
	if got, ok := req["type"]; !ok || got != "epic" {
		t.Errorf("type = %v, want %q", got, "epic")
	}
}

func TestUpdateParamsToPatchRequest_KeepsSupportedFields(t *testing.T) {
	title := "new title"
	owner := "alice"
	prio := 1
	req := updateParamsToPatchRequest(backend.UpdateParams{
		Title:    &title,
		Owner:    &owner,
		Priority: &prio,
	})
	if req["title"] != "new title" {
		t.Errorf("title = %v, want %q", req["title"], "new title")
	}
	if req["owner"] != "alice" {
		t.Errorf("owner = %v, want %q", req["owner"], "alice")
	}
	if req["priority"] != 1 {
		t.Errorf("priority = %v, want 1", req["priority"])
	}
}

func TestUpdateParamsToPatchRequest_KeepsExternalRef(t *testing.T) {
	ref := "https://github.com/owner/repo/pull/42"
	req := updateParamsToPatchRequest(backend.UpdateParams{ExternalRef: &ref})
	if got, ok := req["external_ref"]; !ok || got != ref {
		t.Errorf("external_ref = %v, want %q", got, ref)
	}
}

// --- createParamsToBody tests ---

func TestCreateParamsToBody_RenamesFields(t *testing.T) {
	req := createParamsToBody(backend.CreateParams{
		Title:      "T",
		IssueType:  "task",
		Status:     "deferred",
		Parent:     "loom-1",
		SourceRepo: "repo-a",
		Priority:   3,
		DeferUntil: "2026-05-01T00:00:00Z",
		DueAt:      "2026-06-01T00:00:00Z",
	})
	// Renames: issue_type → type, parent → parent_id, source_repo → repo.
	if _, ok := req["issue_type"]; ok {
		t.Error("issue_type must be renamed to 'type'")
	}
	if req["type"] != "task" {
		t.Errorf("type = %v, want %q", req["type"], "task")
	}
	if _, ok := req["parent"]; ok {
		t.Error("parent must be renamed to 'parent_id'")
	}
	if req["parent_id"] != "loom-1" {
		t.Errorf("parent_id = %v, want %q", req["parent_id"], "loom-1")
	}
	if _, ok := req["source_repo"]; ok {
		t.Error("source_repo must be renamed to 'repo'")
	}
	if req["repo"] != "repo-a" {
		t.Errorf("repo = %v, want %q", req["repo"], "repo-a")
	}
	if req["priority"] != 3 {
		t.Errorf("priority = %v, want 3", req["priority"])
	}
	if req["status"] != "deferred" {
		t.Errorf("status = %v, want deferred", req["status"])
	}
	if req["defer_until"] != "2026-05-01T00:00:00Z" {
		t.Errorf("defer_until = %v, want RFC3339", req["defer_until"])
	}
	if req["due_at"] != "2026-06-01T00:00:00Z" {
		t.Errorf("due_at = %v, want RFC3339", req["due_at"])
	}
}

func TestCreateParamsToBody_DropsLoomOnlyFields(t *testing.T) {
	estim := 30
	req := createParamsToBody(backend.CreateParams{
		Title:              "T",
		IssueType:          "task",
		ID:                 "explicit-id",
		AcceptanceCriteria: "AC",
		CreatedBy:          "bob",
		EstimatedMinutes:   &estim,
		Dependencies:       []string{"loom-2"},
	})
	for _, k := range []string{
		"id", "acceptance_criteria", "created_by",
		"estimated_minutes", "dependencies",
	} {
		if _, ok := req[k]; ok {
			t.Errorf("field %q must be dropped — not on fleet-db CreateIssueRequest", k)
		}
	}
}

func TestCreateParamsToBody_KeepsExternalRef(t *testing.T) {
	ref := "https://github.com/owner/repo/pull/42"
	req := createParamsToBody(backend.CreateParams{
		Title:       "T",
		IssueType:   "task",
		ExternalRef: ref,
	})
	if got, ok := req["external_ref"]; !ok || got != ref {
		t.Errorf("external_ref = %v, want %q", got, ref)
	}
}

func TestCreateParamsToBody_OmitsZeroValues(t *testing.T) {
	req := createParamsToBody(backend.CreateParams{Title: "only"})
	for _, k := range []string{"description", "status", "type", "assignee", "owner", "labels", "parent_id", "repo", "design", "notes", "defer_until", "due_at", "priority"} {
		if _, ok := req[k]; ok {
			t.Errorf("zero-value field %q should not appear in body", k)
		}
	}
}

// --- listOptsToQuery tests ---
//
// fleet-db's listIssues endpoint expects ?type=<kind>, not ?issue_type=<kind>
// (see fleet-db/api/openapi.yaml). loomcli sending "issue_type" caused the
// filter to be silently ignored, so a query for --type=epic also returned
// tasks.

func TestListOptsToQuery_UsesTypeNotIssueType(t *testing.T) {
	q := listOptsToQuery(backend.ListOpts{IssueType: "epic"})
	v := parseQueryValues(t, q)
	if _, has := v["issue_type"]; has {
		t.Errorf("query contains issue_type=%q; fleet-db's listIssues silently ignores it", v.Get("issue_type"))
	}
	if got := v.Get("type"); got != "epic" {
		t.Errorf("type = %q, want %q", got, "epic")
	}
}

func TestListOptsToQuery_UsesFleetLabelAndRepoParams(t *testing.T) {
	q := listOptsToQuery(backend.ListOpts{
		Labels:      []string{"urgent"},
		SourceRepos: []string{"repo-a"},
	})
	v := parseQueryValues(t, q)
	if got := v.Get("label"); got != "urgent" {
		t.Errorf("label = %q, want urgent", got)
	}
	if got := v.Get("repo"); got != "repo-a" {
		t.Errorf("repo = %q, want repo-a", got)
	}
	if v.Get("labels") != "" {
		t.Errorf("labels = %q, want absent", v.Get("labels"))
	}
	if v.Get("source_repos") != "" {
		t.Errorf("source_repos = %q, want absent", v.Get("source_repos"))
	}
}

func TestReadyQueryToQuery_UsesFleetLabelParams(t *testing.T) {
	q := readyQueryToQuery(workitems.AvailabilityQuery{
		Labels:    []string{"urgent", "frontend"},
		LabelsAny: []string{"bug", "design"},
	})
	v := parseQueryValues(t, q)
	if got := v.Get("label"); got != "urgent,frontend" {
		t.Errorf("label = %q, want comma-separated labels", got)
	}
	if got := v.Get("label_any"); got != "bug,design" {
		t.Errorf("label_any = %q, want comma-separated labels_any", got)
	}
	if v.Get("labels") != "" || v.Get("labels_any") != "" {
		t.Errorf("legacy labels params leaked: %q", q)
	}
}

func parseQueryValues(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	return v
}
