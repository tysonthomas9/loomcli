package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// End-to-end tests for FleetBackend.List against a stub that behaves like
// fleet-db's list route: it honors the parameters fleet-db actually implements
// (status, type, assignee, label, repo, parent_id, priority, updated_*, offset,
// limit) and IGNORES everything else.
//
// The ignoring is the point. It is exactly what the real server does, and it is
// what turns each assertion below into proof that the client-side pass works
// rather than proof that a cooperative stub filtered for us.

// listStub is the fake fleet-db list route.
type listStub struct {
	issues []fleetIssueWithCountsWire
	// queries records the query string of every request, so a test can assert
	// what was forwarded as well as what came back.
	queries []string
	// pageSize mirrors fleet-db's per-page cap. 0 means "no cap".
	pageSize int
}

func (s *listStub) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues") {
			t.Errorf("unexpected path %q", r.URL.Path)
			respondErr(w, http.StatusNotFound, "not found")
			return
		}
		q := r.URL.Query()
		s.queries = append(s.queries, r.URL.RawQuery)

		matched := make([]fleetIssueWithCountsWire, 0, len(s.issues))
		for _, iss := range s.issues {
			if stubMatches(t, iss, q) {
				matched = append(matched, iss)
			}
		}

		offset := stubInt(q.Get("offset"))
		if offset > len(matched) {
			offset = len(matched)
		}
		page := matched[offset:]

		pageCap := s.pageSize
		if limit := stubInt(q.Get("limit")); limit > 0 && (pageCap == 0 || limit < pageCap) {
			pageCap = limit
		}
		hasMore := false
		if pageCap > 0 && len(page) > pageCap {
			page = page[:pageCap]
			hasMore = true
		}
		if offset+len(page) < len(matched) {
			hasMore = true
		}

		raw, _ := json.Marshal(map[string]any{
			"issues":   page,
			"total":    len(matched),
			"has_more": hasMore,
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse{Success: true, Data: raw})
	}
}

// stubMatches implements only the filters fleet-db's parseListOptions
// implements. Anything else in the query string is deliberately ignored.
func stubMatches(t *testing.T, iss fleetIssueWithCountsWire, q url.Values) bool {
	t.Helper()
	if v := q.Get("status"); v != "" && iss.Status != v {
		return false
	}
	if v := q.Get("type"); v != "" && iss.Type != v {
		return false
	}
	if v := q.Get("assignee"); v != "" && iss.Assignee != v {
		return false
	}
	if v := q.Get("parent_id"); v != "" && iss.ParentID != v {
		return false
	}
	if v := q.Get("repo"); v != "" && iss.sourceRepo() != v {
		return false
	}
	if v := q.Get("label"); v != "" && !hasAnyString(iss.Labels, v) {
		return false
	}
	if v := q.Get("priority"); v != "" && strconv.Itoa(iss.Priority) != v {
		return false
	}
	// fleet-db parses these strictly and 400s on anything else. Failing the
	// test here is what pins the RFC3339 normalization in addListDateFilters.
	if v := q.Get("updated_after"); v != "" {
		after, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Errorf("updated_after=%q is not strict RFC3339; the real fleet-db returns 400 for this", v)
			return false
		}
		if !iss.UpdatedAt.After(after) {
			return false
		}
	}
	if v := q.Get("updated_before"); v != "" {
		before, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Errorf("updated_before=%q is not strict RFC3339; the real fleet-db returns 400 for this", v)
			return false
		}
		if !iss.UpdatedAt.Before(before) {
			return false
		}
	}
	return true
}

func stubInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// seedListFixture builds the fixture the filter assertions run against: known
// status, assignee, labels, priority, title, description, notes and created_at.
func seedListFixture() []fleetIssueWithCountsWire {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mk := func(w fleetIssueWire) fleetIssueWithCountsWire {
		if w.Status == "" {
			w.Status = "open"
		}
		if w.Type == "" {
			w.Type = "task"
		}
		if w.CreatedAt.IsZero() {
			w.CreatedAt = base
		}
		if w.UpdatedAt.IsZero() {
			w.UpdatedAt = w.CreatedAt
		}
		return fleetIssueWithCountsWire{fleetIssueWire: w}
	}
	return []fleetIssueWithCountsWire{
		mk(fleetIssueWire{
			ID: "F-1", Title: "Fix the Parser", Priority: 1,
			Assignee: "tyson", Labels: []string{"alpha"},
			Description: "The parser drops trailing commas",
			Notes:       "Blocked on upstream",
		}),
		mk(fleetIssueWire{
			ID: "F-2", Title: "Document the API", Priority: 2,
			Assignee: "tyson", Labels: []string{"beta"},
			Description: "Write the reference",
		}),
		// Unassigned, unlabeled, whitespace-only description.
		mk(fleetIssueWire{
			ID: "F-3", Title: "Triage inbox", Priority: 2,
			Description: "   ",
		}),
		// Unassigned, unlabeled, description absent entirely.
		mk(fleetIssueWire{ID: "F-4", Title: "Nameless chore", Priority: 3}),
		mk(fleetIssueWire{
			ID: "F-5", Title: "Pinned announcement", Status: pinnedStatus, Priority: 0,
			Labels: []string{"alpha", "beta"}, Description: "Read me first",
		}),
		mk(fleetIssueWire{
			ID: "F-6", Title: "Old bug", Priority: 4,
			Assignee: "someone-else", Labels: []string{"gamma"},
			Description: "Ancient", Notes: "stale",
			CreatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		}),
	}
}

func newListStubBackend(t *testing.T, stub *listStub) *FleetBackend {
	t.Helper()
	fb, ts := newTestServer(t, stub.handler(t))
	t.Cleanup(ts.Close)
	return fb
}

func listIDs(rows []backend.IssueData) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// TestList_EveryDeclaredFilterNarrowsOrRejects is the acceptance criterion
// itself: no declared filter may return the unfiltered list. Each case names
// the exact rows it expects, so "narrower" cannot be satisfied by accident.
func TestList_EveryDeclaredFilterNarrowsOrRejects(t *testing.T) {
	p1, p9 := 1, 9
	pinnedTrue, pinnedFalse := true, false

	tests := []struct {
		name string
		opts backend.ListOpts
		want []string
	}{
		// --- the three now forwarded to fleet-db ---
		{"priority", backend.ListOpts{Priority: &p1}, []string{"F-1"}},
		{"priority with no match", backend.ListOpts{Priority: &p9}, nil},
		{"updated_after", backend.ListOpts{UpdatedAfter: "2026-01-01"}, []string{"F-1", "F-2", "F-3", "F-4", "F-5"}},
		{"updated_before", backend.ListOpts{UpdatedBefore: "2026-01-01"}, []string{"F-6"}},

		// --- the ten evaluated client-side ---
		{"created_after", backend.ListOpts{CreatedAfter: "2026-01-01"}, []string{"F-1", "F-2", "F-3", "F-4", "F-5"}},
		{"created_before", backend.ListOpts{CreatedBefore: "2026-01-01"}, []string{"F-6"}},
		{"created_after in the future returns nothing", backend.ListOpts{CreatedAfter: "2030-01-01"}, nil},
		{"created_before in the past returns nothing", backend.ListOpts{CreatedBefore: "2000-01-01"}, nil},

		{"title_contains is case-insensitive", backend.ListOpts{TitleContains: "PARSER"}, []string{"F-1"}},
		{"title_contains with no match", backend.ListOpts{TitleContains: "zzzznomatch"}, nil},

		{"description_contains", backend.ListOpts{DescriptionContains: "reference"}, []string{"F-2"}},
		{"description_contains with no match", backend.ListOpts{DescriptionContains: "zzzz"}, nil},

		{"notes_contains", backend.ListOpts{NotesContains: "UPSTREAM"}, []string{"F-1"}},
		{"notes_contains with no match", backend.ListOpts{NotesContains: "zzzz"}, nil},

		// q spans title + description + notes.
		{"q hits a title", backend.ListOpts{Query: "parser"}, []string{"F-1"}},
		{"q hits a description", backend.ListOpts{Query: "reference"}, []string{"F-2"}},
		{"q hits notes", backend.ListOpts{Query: "stale"}, []string{"F-6"}},
		{"q with no match", backend.ListOpts{Query: "zzzznomatch"}, nil},

		// Whitespace-only and absent descriptions both count as empty.
		{"empty_description", backend.ListOpts{EmptyDescription: true}, []string{"F-3", "F-4"}},

		{"no_assignee", backend.ListOpts{NoAssignee: true}, []string{"F-3", "F-4", "F-5"}},
		{"no_labels", backend.ListOpts{NoLabels: true}, []string{"F-3", "F-4"}},

		// pinned is a status, and pinned=false is an active filter.
		{"pinned=true", backend.ListOpts{Pinned: &pinnedTrue}, []string{"F-5"}},
		{"pinned=false", backend.ListOpts{Pinned: &pinnedFalse}, []string{"F-1", "F-2", "F-3", "F-4", "F-6"}},

		// --- the eight that already worked, as the ticket recorded them ---
		{"status", backend.ListOpts{Status: "pinned"}, []string{"F-5"}},
		{"type", backend.ListOpts{IssueType: "task"}, []string{"F-1", "F-2", "F-3", "F-4", "F-5", "F-6"}},
		{"assignee", backend.ListOpts{Assignee: "tyson"}, []string{"F-1", "F-2"}},
		{"labels", backend.ListOpts{Labels: []string{"alpha"}}, []string{"F-1", "F-5"}},
		{"limit", backend.ListOpts{Limit: 2}, []string{"F-1", "F-2"}},

		// --- composition: the whole reason q is a substring and not the
		// dedicated search route, which accepts no other filter ---
		{
			"q composes with status",
			backend.ListOpts{Query: "the", Status: "open"},
			[]string{"F-1", "F-2"},
		},
		{
			"no_assignee composes with priority",
			backend.ListOpts{NoAssignee: true, Priority: &[]int{2}[0]},
			[]string{"F-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &listStub{issues: seedListFixture()}
			fb := newListStubBackend(t, stub)

			got, err := fb.List(context.Background(), tt.opts)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if diff := strings.Join(listIDs(got), ","); diff != strings.Join(tt.want, ",") {
				t.Fatalf("ids = [%s], want [%s]", diff, strings.Join(tt.want, ","))
			}
			// The unfiltered list is 6 rows; nothing here may return all of
			// them except the filters that genuinely match everything.
			if len(got) == len(stub.issues) && tt.name != "type" {
				t.Errorf("filter returned the entire unfiltered list (%d rows)", len(got))
			}
			// List must never hand back a nil slice: the handler marshals it
			// and the frontend expects [].
			if got == nil {
				t.Error("List returned nil, want an empty slice")
			}
		})
	}
}

// TestList_PriorityIsForwardedNotPostFiltered distinguishes the three filters
// fleet-db evaluates from the ten this package evaluates. If priority were
// quietly post-filtered instead, the result would look identical while every
// list request pulled the whole board over the wire.
func TestList_PriorityIsForwardedNotPostFiltered(t *testing.T) {
	stub := &listStub{issues: seedListFixture()}
	fb := newListStubBackend(t, stub)

	p := 1
	got, err := fb.List(context.Background(), backend.ListOpts{Priority: &p})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Priority != 1 {
		t.Fatalf("result = %+v, want only priority-1 rows", listIDs(got))
	}
	if len(stub.queries) != 1 {
		t.Fatalf("made %d requests, want 1", len(stub.queries))
	}
	if !strings.Contains(stub.queries[0], "priority=1") {
		t.Errorf("query = %q, want it to carry priority=1", stub.queries[0])
	}
}

// TestList_ClientSideFiltersAreNotSentToTheServer is the other half: sending a
// parameter fleet-db ignores is what made the wire look filtered when it was
// not, and it is how this bug hid for so long.
func TestList_ClientSideFiltersAreNotSentToTheServer(t *testing.T) {
	stub := &listStub{issues: seedListFixture()}
	fb := newListStubBackend(t, stub)

	pinned := true
	_, err := fb.List(context.Background(), backend.ListOpts{
		Query: "x", TitleContains: "y", DescriptionContains: "z",
		NotesContains: "w", CreatedAfter: "2020-01-01",
		NoAssignee: true, NoLabels: true, EmptyDescription: true, Pinned: &pinned,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, key := range []string{
		"query=", "title_contains=", "description_contains=", "notes_contains=",
		"created_after=", "no_assignee=", "no_labels=", "empty_description=", "pinned=",
	} {
		for _, q := range stub.queries {
			if strings.Contains(q, key) {
				t.Errorf("query %q carries %q, which fleet-db ignores", q, key)
			}
		}
	}
}

// TestList_LimitAppliesAfterFiltering is the guard from the plan's edge cases,
// and the single most likely way to ship a subtler version of this bug:
// ?limit=10&no_assignee=true must return ten UNASSIGNED rows, not the
// unassigned subset of the first ten rows.
func TestList_LimitAppliesAfterFiltering(t *testing.T) {
	var issues []fleetIssueWithCountsWire
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// 50 assigned rows first, then 20 unassigned ones. A server-side limit of
	// 10 would return ten assigned rows and then filter them all away.
	for i := 0; i < 50; i++ {
		issues = append(issues, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{
			ID: fmt.Sprintf("A-%02d", i), Title: "assigned", Status: "open",
			Assignee: "tyson", CreatedAt: now, UpdatedAt: now,
		}})
	}
	for i := 0; i < 20; i++ {
		issues = append(issues, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{
			ID: fmt.Sprintf("U-%02d", i), Title: "unassigned", Status: "open",
			CreatedAt: now, UpdatedAt: now,
		}})
	}

	stub := &listStub{issues: issues, pageSize: 200}
	fb := newListStubBackend(t, stub)

	got, err := fb.List(context.Background(), backend.ListOpts{NoAssignee: true, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("got %d rows, want 10 unassigned rows", len(got))
	}
	for _, r := range got {
		if r.Assignee != "" {
			t.Fatalf("row %s has assignee %q; the limit was applied before filtering", r.ID, r.Assignee)
		}
	}
	for _, q := range stub.queries {
		if strings.Contains(q, "limit=") {
			t.Errorf("query %q carries a server-side limit; the client pass needs the complete set", q)
		}
	}
}

// TestList_ClientFilterSeesRowsBeyondTheFirstPage pins the paging interaction.
// fleet-db caps a page at 200 rows, so filtering a single page yields confident
// false negatives — the same failure PUPPET-576 fixed for --limit. The only
// matching row here is the last one, past three page boundaries.
func TestList_ClientFilterSeesRowsBeyondTheFirstPage(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	const total = 601

	var issues []fleetIssueWithCountsWire
	for i := 0; i < total-1; i++ {
		issues = append(issues, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{
			ID: fmt.Sprintf("N-%04d", i), Title: "ordinary", Status: "open",
			CreatedAt: now, UpdatedAt: now,
		}})
	}
	issues = append(issues, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{
		ID: "NEEDLE", Title: "the needle", Status: "open",
		CreatedAt: now, UpdatedAt: now,
	}})

	stub := &listStub{issues: issues, pageSize: 200}
	fb := newListStubBackend(t, stub)

	got, err := fb.List(context.Background(), backend.ListOpts{TitleContains: "needle"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "NEEDLE" {
		t.Fatalf("ids = %v, want [NEEDLE]; a single-page filter returns a confident false negative here", listIDs(got))
	}
	if len(stub.queries) < 4 {
		t.Errorf("made %d requests for %d rows at 200/page; the walk did not reach the end", len(stub.queries), total)
	}
}

// TestList_MalformedDateIsValidationNotInternal keeps a bad created_after out
// of the 500 bucket: the webui's translateBackendError maps KindValidation to
// 400, and the request must not reach the server at all.
func TestList_MalformedDateIsValidationNotInternal(t *testing.T) {
	for _, opts := range []backend.ListOpts{
		{CreatedAfter: "banana"},
		{CreatedBefore: "not-a-date"},
	} {
		stub := &listStub{issues: seedListFixture()}
		fb := newListStubBackend(t, stub)

		_, err := fb.List(context.Background(), opts)
		if err == nil {
			t.Fatalf("List(%+v): expected an error", opts)
		}
		var be *backend.BackendError
		if !errors.As(err, &be) || be.Kind != backend.KindValidation {
			t.Fatalf("List(%+v) error = %#v, want KindValidation", opts, err)
		}
		if len(stub.queries) != 0 {
			t.Errorf("List(%+v) issued %d requests before validating", opts, len(stub.queries))
		}
	}
}

// TestList_RowWithNoCreatedAtIsExcludedFromCreatedRange is the wire-level
// counterpart to the unit test: a row whose created_at fleet-db omitted must
// not sail through a created_before bound on the zero time.
func TestList_RowWithNoCreatedAtIsExcludedFromCreatedRange(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	stub := &listStub{issues: []fleetIssueWithCountsWire{
		{fleetIssueWire: fleetIssueWire{ID: "HAS-DATE", Title: "dated", Status: "open", CreatedAt: now, UpdatedAt: now}},
		{fleetIssueWire: fleetIssueWire{ID: "NO-DATE", Title: "undated", Status: "open"}},
	}}
	fb := newListStubBackend(t, stub)

	got, err := fb.List(context.Background(), backend.ListOpts{CreatedBefore: "2027-01-01"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "HAS-DATE" {
		t.Fatalf("ids = %v, want [HAS-DATE]", listIDs(got))
	}
}

// TestList_EmptyResultIsNonNil — the handler marshals this and the frontend
// expects [], not null.
func TestList_EmptyResultIsNonNil(t *testing.T) {
	stub := &listStub{issues: seedListFixture()}
	fb := newListStubBackend(t, stub)

	got, err := fb.List(context.Background(), backend.ListOpts{TitleContains: "zzzznomatch"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List returned nil for an empty result")
	}
	if len(got) != 0 {
		t.Fatalf("got %d rows, want 0", len(got))
	}
}

// TestList_UpdatedDateIsNormalizedForTheServer pins the forwarding half of the
// date contract. The stub parses updated_* strictly, exactly as fleet-db does,
// so a bare YYYY-MM-DD reaching the wire fails the test rather than silently
// becoming a 400 in production.
func TestList_UpdatedDateIsNormalizedForTheServer(t *testing.T) {
	stub := &listStub{issues: seedListFixture()}
	fb := newListStubBackend(t, stub)

	got, err := fb.List(context.Background(), backend.ListOpts{UpdatedAfter: "2026-01-01"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d rows, want 5", len(got))
	}
	if !strings.Contains(stub.queries[0], url.QueryEscape("2026-01-01T00:00:00Z")) {
		t.Errorf("query = %q, want an RFC3339-normalized updated_after", stub.queries[0])
	}
}
