package fleet

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- updateParamsToPatchRequest tests ---
//
// These exercise the loom→fleet-db field mapping. fleet-db's
// UpdateIssueRequest schema is intentionally narrower than loom's
// UpdateParams: status / claim / labels / agent_state / assignee /
// acceptance_criteria / estimated_minutes have dedicated endpoints
// (close, reopen, claim, label.add, etc.) and aren't accepted on PATCH.
// fleet-db enforces this with disallowUnknownFields, so loom must drop
// those keys here rather than silently shipping them.

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

// fleetUpdateIssueFields is every JSON key fleet-db's UpdateIssueRequest
// accepts on PATCH /issues/{id}. It mirrors the json tags on fleet-db's
// internal/api/request.go UpdateIssueRequest — the struct its strict decoder
// binds the body to, and the only faithful copy of that shape: fleet-db's
// own api/openapi.yaml and pkg/client both still omit external_ref, which
// the server does accept, so neither can be vendored as the source here.
//
// Nothing links the two repos at build time, so this list is hand-maintained.
// Adding a key here that fleet-db does not have is the design_format bug
// again: disallowUnknownFields rejects the *whole* body, so the PATCH 400s
// and every field traveling with the unknown one is lost with it — the
// design itself, in that outage.
//
// Loom deliberately forwards less than fleet-db accepts (status and claim go
// through dedicated endpoints), so the check below is subset, not equality.
var fleetUpdateIssueFields = map[string]bool{
	"title":         true,
	"description":   true,
	"status":        true,
	"priority":      true,
	"type":          true,
	"design":        true,
	"design_format": true,
	"notes":         true,
	"owner":         true,
	"due_at":        true,
	"external_ref":  true,
}

// TestUpdateParamsToPatchRequest_SendsOnlyServerFields is the guard the
// design_format outage needed. It populates every backend.UpdateParams field
// by reflection — so a field added to UpdateParams later is swept in without
// editing this test — and fails on any emitted key fleet-db's
// UpdateIssueRequest does not accept.
func TestUpdateParamsToPatchRequest_SendsOnlyServerFields(t *testing.T) {
	req := updateParamsToPatchRequest(populatedUpdateParams(t))
	if len(req) == 0 {
		t.Fatal("converter emitted nothing — populatedUpdateParams stopped filling UpdateParams")
	}
	for key := range req {
		if !fleetUpdateIssueFields[key] {
			t.Errorf("PATCH body carries %q, which fleet-db's UpdateIssueRequest does not accept — "+
				"disallowUnknownFields rejects the whole body, so every update sending it 400s", key)
		}
	}
}

// TestUpdateParamsToPatchRequest_ForwardsEachSupportedField covers the fields
// the converter does forward, one case each: the key fleet-db expects, the
// value, and — with the param left nil — no key at all. Omission is the whole
// point of the pointer fields: fleet-db applies every key present, so an
// unset Design shipped as "" would blank the issue's design rather than leave
// it alone.
func TestUpdateParamsToPatchRequest_ForwardsEachSupportedField(t *testing.T) {
	prio := 1
	cases := []struct {
		key    string
		params backend.UpdateParams
		want   interface{}
	}{
		{"title", backend.UpdateParams{Title: strPtr("new title")}, "new title"},
		{"description", backend.UpdateParams{Description: strPtr("what and why")}, "what and why"},
		{"priority", backend.UpdateParams{Priority: &prio}, 1},
		{"design", backend.UpdateParams{Design: strPtr("# Design")}, "# Design"},
		{"design_format", backend.UpdateParams{DesignFormat: strPtr("markdown")}, "markdown"},
		{"notes", backend.UpdateParams{Notes: strPtr("a note")}, "a note"},
		{"owner", backend.UpdateParams{Owner: strPtr("alice")}, "alice"},
		{"type", backend.UpdateParams{IssueType: strPtr("epic")}, "epic"},
		{"due_at", backend.UpdateParams{DueAt: strPtr("2026-06-01T00:00:00Z")}, "2026-06-01T00:00:00Z"},
		{"external_ref", backend.UpdateParams{
			ExternalRef: strPtr("https://github.com/owner/repo/pull/42"),
		}, "https://github.com/owner/repo/pull/42"},
	}

	unset := updateParamsToPatchRequest(backend.UpdateParams{})
	covered := make(map[string]bool, len(cases))
	for _, tc := range cases {
		covered[tc.key] = true
		t.Run(tc.key, func(t *testing.T) {
			req := updateParamsToPatchRequest(tc.params)
			if got, ok := req[tc.key]; !ok || got != tc.want {
				t.Errorf("%s = %v (present=%t), want %v", tc.key, got, ok, tc.want)
			}
			if len(req) != 1 {
				t.Errorf("one param set produced %d keys (%v), want only %q", len(req), req, tc.key)
			}
			if _, ok := unset[tc.key]; ok {
				t.Errorf("%s present for a nil param — nil must omit the key, not send a zero value", tc.key)
			}
		})
	}

	// Keeps the table honest: a field added to the converter without a case
	// above would otherwise ship with nothing asserting its name.
	for key := range updateParamsToPatchRequest(populatedUpdateParams(t)) {
		if !covered[key] {
			t.Errorf("converter emits %q with no case in this table", key)
		}
	}
}

// populatedUpdateParams returns an UpdateParams with every field set to a
// non-zero value, so callers exercise the converter over its whole input
// surface. It fails on a field kind it cannot fill rather than skipping it —
// a silently unset field would hide exactly the drift these tests guard.
func populatedUpdateParams(t *testing.T) backend.UpdateParams {
	t.Helper()
	var params backend.UpdateParams
	v := reflect.ValueOf(&params).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		name := v.Type().Field(i).Name
		// Unexported fields are invisible to the converter (other package).
		if !field.CanSet() {
			continue
		}
		switch field.Kind() {
		case reflect.Pointer:
			field.Set(reflect.New(field.Type().Elem()))
			setNonZero(t, name, field.Elem())
		case reflect.Slice:
			elem := reflect.New(field.Type().Elem()).Elem()
			setNonZero(t, name, elem)
			field.Set(reflect.Append(field, elem))
		default:
			setNonZero(t, name, field)
		}
	}
	return params
}

func setNonZero(t *testing.T, name string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Int:
		v.SetInt(1)
	case reflect.Bool:
		v.SetBool(true)
	default:
		t.Fatalf("UpdateParams.%s: unhandled kind %s — extend setNonZero so the "+
			"sweep keeps covering every field", name, v.Kind())
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

func TestReadyOptsToQuery_UsesFleetLabelParams(t *testing.T) {
	q := readyOptsToQuery(backend.ReadyOpts{
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

// --- PUPPET-603: the thirteen filters that used to be parsed and discarded ---

// TestListOptsToQuery_ForwardsOnlyServerEvaluatedFilters is the wire half of the
// contract. Emitting a parameter fleet-db does not implement is what made the
// request look filtered when it was not, so absence is the assertion here.
func TestListOptsToQuery_ForwardsOnlyServerEvaluatedFilters(t *testing.T) {
	p, pinned := 1, true
	q := listOptsToQuery(backend.ListOpts{
		Priority:      &p,
		UpdatedAfter:  "2026-01-01",
		UpdatedBefore: "2026-12-31T23:59:59Z",

		CreatedAfter:        "2026-01-01",
		CreatedBefore:       "2026-12-31",
		Query:               "search",
		TitleContains:       "title",
		DescriptionContains: "desc",
		NotesContains:       "note",
		EmptyDescription:    true,
		NoAssignee:          true,
		NoLabels:            true,
		Pinned:              &pinned,
	})
	values, err := url.ParseQuery(q)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", q, err)
	}

	// Forwarded: fleet-db evaluates these itself.
	if got := values.Get("priority"); got != "1" {
		t.Errorf("priority = %q, want 1", got)
	}
	// Normalized: fleet-db parses updated_* with a strict RFC3339 and would 400
	// on the bare date this layer accepts.
	if got := values.Get("updated_after"); got != "2026-01-01T00:00:00Z" {
		t.Errorf("updated_after = %q, want RFC3339-normalized", got)
	}
	if got := values.Get("updated_before"); got != "2026-12-31T23:59:59Z" {
		t.Errorf("updated_before = %q", got)
	}

	// Not forwarded: evaluated client-side by List.
	for _, key := range []string{
		"created_after", "created_before", "query", "title_contains",
		"description_contains", "notes_contains", "empty_description",
		"no_assignee", "no_labels", "pinned", "priority_min", "priority_max",
	} {
		if values.Has(key) {
			t.Errorf("query carries %q = %q; fleet-db ignores it, so sending it makes the request look filtered when it is not",
				key, values.Get(key))
		}
	}
}

// TestNeedsListClientFilter_CoversEveryClientSideFilter matters more than it
// looks: listServerOpts zeroes the server limit off this predicate. Miss a
// filter here and `?limit=10&no_assignee=true` returns the unassigned subset of
// the first ten rows instead of ten unassigned rows — a subtler version of the
// same bug.
func TestNeedsListClientFilter_CoversEveryClientSideFilter(t *testing.T) {
	pinned := false
	tests := []struct {
		name string
		opts backend.ListOpts
	}{
		{"CreatedAfter", backend.ListOpts{CreatedAfter: "2026-01-01"}},
		{"CreatedBefore", backend.ListOpts{CreatedBefore: "2026-01-01"}},
		{"Query", backend.ListOpts{Query: "x"}},
		{"TitleContains", backend.ListOpts{TitleContains: "x"}},
		{"DescriptionContains", backend.ListOpts{DescriptionContains: "x"}},
		{"NotesContains", backend.ListOpts{NotesContains: "x"}},
		{"EmptyDescription", backend.ListOpts{EmptyDescription: true}},
		{"NoAssignee", backend.ListOpts{NoAssignee: true}},
		{"NoLabels", backend.ListOpts{NoLabels: true}},
		{"Pinned", backend.ListOpts{Pinned: &pinned}},
		{"multi-label (pre-existing)", backend.ListOpts{Labels: []string{"a", "b"}}},
		{"multi-repo (pre-existing)", backend.ListOpts{SourceRepos: []string{"a", "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !needsListClientFilter(tt.opts) {
				t.Fatal("needsListClientFilter = false; the server limit will not be zeroed")
			}
			tt.opts.Limit = 10
			if got := listServerOpts(tt.opts).Limit; got != 0 {
				t.Errorf("listServerOpts().Limit = %d, want 0", got)
			}
		})
	}

	// Server-only filters must NOT trigger the client pass; paying for a full
	// walk when fleet-db can answer directly is the cost of getting this wrong.
	p := 1
	for _, opts := range []backend.ListOpts{
		{Status: "open"}, {Assignee: "tyson"}, {Priority: &p},
		{Labels: []string{"one"}}, {UpdatedAfter: "2026-01-01"},
	} {
		if needsListClientFilter(opts) {
			t.Errorf("needsListClientFilter(%+v) = true, want false", opts)
		}
	}
}

// TestCheckFleetUnsupportedFilters_SupportedFields is the companion to
// TestCheckFleetUnsupportedFilters_EachField: these thirteen used to be
// rejected (CLI) or silently dropped (HTTP) and are now honored.
func TestCheckFleetUnsupportedFilters_SupportedFields(t *testing.T) {
	p, pinned := 1, true
	tests := []struct {
		name string
		opts backend.ListOpts
	}{
		{"Priority", backend.ListOpts{Priority: &p}},
		{"UpdatedAfter", backend.ListOpts{UpdatedAfter: "2026-01-01"}},
		{"UpdatedBefore", backend.ListOpts{UpdatedBefore: "2026-01-01"}},
		{"CreatedAfter", backend.ListOpts{CreatedAfter: "2026-01-01"}},
		{"CreatedBefore", backend.ListOpts{CreatedBefore: "2026-01-01"}},
		{"Query", backend.ListOpts{Query: "x"}},
		{"TitleContains", backend.ListOpts{TitleContains: "x"}},
		{"DescriptionContains", backend.ListOpts{DescriptionContains: "x"}},
		{"NotesContains", backend.ListOpts{NotesContains: "x"}},
		{"EmptyDescription", backend.ListOpts{EmptyDescription: true}},
		{"NoAssignee", backend.ListOpts{NoAssignee: true}},
		{"NoLabels", backend.ListOpts{NoLabels: true}},
		{"Pinned", backend.ListOpts{Pinned: &pinned}},
	}
	if len(tests) != 13 {
		t.Fatalf("expected 13 restored filters, table has %d", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkFleetUnsupportedFilters(tt.opts); err != nil {
				t.Fatalf("checkFleetUnsupportedFilters: %v", err)
			}
		})
	}
}
