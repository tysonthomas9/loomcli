package fleet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// newTestServer creates a mock fleet server and returns a FleetBackend pointing at it.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*FleetBackend, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	fb, err := New(Config{
		BaseURL:     ts.URL,
		WorkspaceID: "test-ws",
		AuthToken:   "test-token",
	})
	if err != nil {
		ts.Close()
		t.Fatalf("New: %v", err)
	}
	return fb, ts
}

// respondOK writes a successful JSON envelope.
func respondOK(w http.ResponseWriter, data interface{}) {
	raw, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiResponse{Success: true, Data: raw}) //nolint:errcheck
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// respondErr writes an error JSON envelope with the given status code.
func respondErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(apiResponse{Success: false, Error: msg}) //nolint:errcheck
}

// --- Constructor tests ---

func TestNew_MissingBaseURL(t *testing.T) {
	_, err := New(Config{WorkspaceID: "ws1"})
	if err == nil || !strings.Contains(err.Error(), "BaseURL") {
		t.Fatalf("expected BaseURL error, got %v", err)
	}
}

func TestNew_MissingWorkspaceID(t *testing.T) {
	_, err := New(Config{BaseURL: "http://example.com"})
	if err == nil || !strings.Contains(err.Error(), "WorkspaceID") {
		t.Fatalf("expected WorkspaceID error, got %v", err)
	}
}

func TestNew_TrailingSlash(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://example.com/", WorkspaceID: "ws1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if strings.Contains(fb.baseWorkspaceURL, "//api") {
		t.Errorf("double slash in URL: %s", fb.baseWorkspaceURL)
	}
}

func TestBackendName(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if fb.BackendName() != "fleet" {
		t.Errorf("BackendName = %q, want %q", fb.BackendName(), "fleet")
	}
}

// --- Auth header tests ---

func TestAuthHeaders(t *testing.T) {
	var gotAuth, gotAPIKey string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("X-Fleet-API-Key")
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	fb.SetAPIKey("my-key")
	_, _ = fb.Stats(context.Background())

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
	if gotAPIKey != "my-key" {
		t.Errorf("X-Fleet-API-Key = %q, want %q", gotAPIKey, "my-key")
	}
}

func TestActorHeader(t *testing.T) {
	var gotActor string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActor = r.Header.Get("X-Actor")
		respondOK(w, json.RawMessage(`{}`))
	}))
	defer ts.Close()

	fb, err := New(Config{BaseURL: ts.URL, WorkspaceID: "ws", Actor: "alice"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = fb.Stats(context.Background())

	if gotActor != "alice" {
		t.Errorf("X-Actor = %q, want %q", gotActor, "alice")
	}
}

func TestActorHeader_OmittedWhenEmpty(t *testing.T) {
	var sawActor bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawActor = r.Header["X-Actor"]
		respondOK(w, json.RawMessage(`{}`))
	}))
	defer ts.Close()

	fb, err := New(Config{BaseURL: ts.URL, WorkspaceID: "ws"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = fb.Stats(context.Background())

	if sawActor {
		t.Errorf("X-Actor header should not be set when Actor is empty")
	}
}

func TestSetAuthToken(t *testing.T) {
	var gotAuth string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	fb.SetAuthToken("new-token")
	_, _ = fb.Stats(context.Background())

	if gotAuth != "Bearer new-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer new-token")
	}
}

// --- Get tests ---

func TestGet_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	details := testIssueDetails{
		testIssue: testIssue{
			ID:                 "issue-1",
			Title:              "Test Issue",
			Description:        "description",
			Design:             "design",
			AcceptanceCriteria: "acceptance",
			Notes:              "BLOCKED: missing origin remote",
			Status:             workitems.StatusOpen,
			Priority:           2,
			IssueType:          workitems.TypeTask,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		Labels:       []string{"label-1"},
		Dependencies: []*testIssueWithDependencyMetadata{},
		Dependents:   []*testIssueWithDependencyMetadata{},
		Comments:     []*workitems.Comment{},
	}

	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Get fires three requests: the primary /issues/{id} (returning
		// the slim record that fleet-db ships), then /deps and /comments
		// to populate the embedded relations that IssueDetails
		// carries inline but fleet-db doesn't. The deps/comments calls
		// are tolerated with empty-list responses — the test only
		// asserts that the primary issue fetch happened correctly.
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/issue-1"):
			respondOK(w, details)
		case strings.HasSuffix(r.URL.Path, "/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			respondOK(w, map[string]interface{}{"comments": []interface{}{}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	result, err := fb.Get(context.Background(), "issue-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if result.ID != "issue-1" {
		t.Errorf("ID = %q, want %q", result.ID, "issue-1")
	}
	if result.Title != "Test Issue" {
		t.Errorf("Title = %q, want %q", result.Title, "Test Issue")
	}
	if result.Description != "description" {
		t.Errorf("Description = %q, want description", result.Description)
	}
	if result.Design != "design" {
		t.Errorf("Design = %q, want design", result.Design)
	}
	if result.AcceptanceCriteria != "acceptance" {
		t.Errorf("AcceptanceCriteria = %q, want acceptance", result.AcceptanceCriteria)
	}
	if result.Notes != "BLOCKED: missing origin remote" {
		t.Errorf("Notes = %q, want blocker note", result.Notes)
	}
}

func TestGet_NotFound(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 404, "issue not found")
	})
	defer ts.Close()

	_, err := fb.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Fatalf("expected KindNotFound, got %v", err)
	}
}

func TestGet_ServerError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 500, "internal error")
	})
	defer ts.Close()

	_, err := fb.Get(context.Background(), "issue-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Fatalf("expected KindInternal, got %v", err)
	}
}

// --- List tests ---

func TestList_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issues := []*testIssueWithCounts{
		{
			testIssue:       &testIssue{ID: "a", Title: "A", Status: workitems.StatusOpen, CreatedAt: now, UpdatedAt: now},
			DependencyCount: 1,
		},
		{
			testIssue: &testIssue{ID: "b", Title: "B", Status: workitems.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now},
		},
	}

	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		respondOK(w, issues)
	})
	defer ts.Close()

	result, err := fb.List(context.Background(), backend.ListOpts{Status: "open", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].DependencyCount != 1 {
		t.Errorf("DependencyCount = %d, want 1", result[0].DependencyCount)
	}
}

func TestList_QueryParams(t *testing.T) {
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, []*testIssueWithCounts{})
	})
	defer ts.Close()

	// Only fleet-db-supported fields: Status, IssueType, Assignee, Labels,
	// SourceRepos, ParentID, UpdatedAfter, UpdatedBefore, Limit.
	_, _ = fb.List(context.Background(), backend.ListOpts{
		Status:       "open",
		Limit:        5,
		Assignee:     "agent-1",
		Labels:       []string{"urgent"},
		UpdatedAfter: "2026-01-01",
		SourceRepos:  []string{"repo-a"},
	})

	for _, want := range []string{
		"status=open", "limit=5", "assignee=agent-1",
		"label=urgent", "updated_after=2026-01-01", "repo=repo-a",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestList_ClientFiltersMultipleReposWithoutServerLimit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, []*testIssueWithCounts{
			{testIssue: &testIssue{ID: "a", Title: "A", Status: workitems.StatusOpen, SourceRepo: "repo-a", CreatedAt: now, UpdatedAt: now}},
			{testIssue: &testIssue{ID: "b", Title: "B", Status: workitems.StatusOpen, SourceRepo: "repo-b", CreatedAt: now, UpdatedAt: now}},
			{testIssue: &testIssue{ID: "c", Title: "C", Status: workitems.StatusOpen, SourceRepo: "repo-c", CreatedAt: now, UpdatedAt: now}},
		})
	})
	defer ts.Close()

	result, err := fb.List(context.Background(), backend.ListOpts{
		SourceRepos: []string{"repo-b", "repo-c"},
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if strings.Contains(gotQuery, "source_repos=") || strings.Contains(gotQuery, "repo=") || strings.Contains(gotQuery, "limit=") {
		t.Fatalf("query = %q, want no unsupported repo filter or pre-filter limit", gotQuery)
	}
	if len(result) != 1 || result[0].ID != "b" {
		t.Fatalf("result = %+v, want first locally filtered repo item", result)
	}
}

func TestList_UnsupportedFilter_Single(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("RPC call should not be made when unsupported filter is set")
		respondOK(w, []*testIssueWithCounts{})
	})
	defer ts.Close()

	p := 2
	_, err := fb.List(context.Background(), backend.ListOpts{
		Status:   "open",
		Priority: &p,
	})
	if err == nil {
		t.Fatal("expected error for unsupported filter Priority")
	}
	if !errors.Is(err, backend.ErrFilterNotSupported) {
		t.Errorf("expected ErrFilterNotSupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "Priority") {
		t.Errorf("error should mention field name, got %q", err.Error())
	}
}

func TestList_UnsupportedFilter_Multiple(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("RPC call should not be made when unsupported filters are set")
		respondOK(w, []*testIssueWithCounts{})
	})
	defer ts.Close()

	p := 1
	_, err := fb.List(context.Background(), backend.ListOpts{
		Query:    "test",
		Priority: &p,
		Overdue:  true,
	})
	if err == nil {
		t.Fatal("expected error for unsupported filters")
	}
	if !errors.Is(err, backend.ErrFilterNotSupported) {
		t.Errorf("expected ErrFilterNotSupported, got %v", err)
	}
	errMsg := err.Error()
	for _, field := range []string{"Query", "Priority", "Overdue"} {
		if !strings.Contains(errMsg, field) {
			t.Errorf("error should mention %q, got %q", field, errMsg)
		}
	}
}

func TestList_EmptyOpts_NoError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []*testIssueWithCounts{})
	})
	defer ts.Close()

	_, err := fb.List(context.Background(), backend.ListOpts{})
	if err != nil {
		t.Errorf("empty ListOpts should not error, got %v", err)
	}
}

// --- List parent filter ---

func TestList_ParentFilter_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issues := []*testIssueWithCounts{
		{testIssue: &testIssue{ID: "c1", Title: "Child 1", Parent: "epic-1", Status: workitems.StatusOpen, CreatedAt: now, UpdatedAt: now}},
		{testIssue: &testIssue{ID: "c2", Title: "Child 2", Parent: "epic-1", Status: workitems.StatusOpen, CreatedAt: now, UpdatedAt: now}},
	}

	var gotPath string
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		if r.Method != "GET" {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		respondOK(w, issues)
	})
	defer ts.Close()

	result, err := fb.List(context.Background(), backend.ListOpts{ParentID: "epic-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues") {
		t.Errorf("path = %q, want suffix /issues", gotPath)
	}
	if !strings.Contains(gotQuery, "parent_id=epic-1") {
		t.Errorf("query = %q, want parent_id=epic-1", gotQuery)
	}
	if len(result) != 2 || result[0].ID != "c1" || result[1].ID != "c2" {
		t.Errorf("got %v, want [c1 c2]", result)
	}
}

func TestList_ParentFilter_Empty(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []*testIssueWithCounts{})
	})
	defer ts.Close()
	result, err := fb.List(context.Background(), backend.ListOpts{ParentID: "epic-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

// --- Search ---

func TestSearch_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issues := []*testIssueWithCounts{
		{testIssue: &testIssue{ID: "s1", Title: "Auth bug in login", Status: workitems.StatusOpen, CreatedAt: now, UpdatedAt: now}},
		{testIssue: &testIssue{ID: "s2", Title: "Auth bug: refresh token", Status: workitems.StatusOpen, CreatedAt: now, UpdatedAt: now}},
	}

	var gotPath string
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		if r.Method != "GET" {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		respondOK(w, issues)
	})
	defer ts.Close()

	result, err := fb.Search(context.Background(), workitems.SearchQuery{Query: "auth bug", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues/search") {
		t.Errorf("path = %q, want suffix /issues/search", gotPath)
	}
	if !strings.Contains(gotQuery, "q=auth+bug") {
		t.Errorf("query = %q, want to contain q=auth+bug", gotQuery)
	}
	if !strings.Contains(gotQuery, "limit=10") {
		t.Errorf("query = %q, want to contain limit=10", gotQuery)
	}
	if len(result) != 2 || result[0].ID != "s1" || result[1].ID != "s2" {
		t.Errorf("got %v, want [s1 s2]", result)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fb.Search(context.Background(), workitems.SearchQuery{Limit: 10})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestSearch_NegativeLimit(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fb.Search(context.Background(), workitems.SearchQuery{Query: "q", Limit: -1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestSearch_Empty(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []*testIssueWithCounts{})
	})
	defer ts.Close()
	result, err := fb.Search(context.Background(), workitems.SearchQuery{Query: "nothing", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

// TestCheckFleetUnsupportedFilters_EachField sets each unsupported ListOpts
// field individually and verifies that (a) the error wraps
// ErrFilterNotSupported and (b) the error message contains the field name.
// This ensures no field is accidentally omitted from checkFleetUnsupportedFilters.
func TestCheckFleetUnsupportedFilters_EachField(t *testing.T) {
	intVal := 1
	boolTrue := true

	tests := []struct {
		name string
		opts backend.ListOpts
	}{
		{"Query", backend.ListOpts{Query: "search"}},
		{"Priority", backend.ListOpts{Priority: &intVal}},
		{"LabelsAny", backend.ListOpts{LabelsAny: []string{"a"}}},
		{"IDs", backend.ListOpts{IDs: []string{"id-1"}}},
		{"TitleContains", backend.ListOpts{TitleContains: "title"}},
		{"DescriptionContains", backend.ListOpts{DescriptionContains: "desc"}},
		{"NotesContains", backend.ListOpts{NotesContains: "note"}},
		{"CreatedAfter", backend.ListOpts{CreatedAfter: "2026-01-01"}},
		{"CreatedBefore", backend.ListOpts{CreatedBefore: "2026-12-31"}},
		{"ClosedAfter", backend.ListOpts{ClosedAfter: "2026-01-01"}},
		{"ClosedBefore", backend.ListOpts{ClosedBefore: "2026-12-31"}},
		{"EmptyDescription", backend.ListOpts{EmptyDescription: true}},
		{"NoAssignee", backend.ListOpts{NoAssignee: true}},
		{"NoLabels", backend.ListOpts{NoLabels: true}},
		{"PriorityMin", backend.ListOpts{PriorityMin: &intVal}},
		{"PriorityMax", backend.ListOpts{PriorityMax: &intVal}},
		{"Pinned", backend.ListOpts{Pinned: &boolTrue}},
		{"IncludeTemplates", backend.ListOpts{IncludeTemplates: true}},
		{"Ephemeral", backend.ListOpts{Ephemeral: &boolTrue}},
		{"MolType", backend.ListOpts{MolType: "molecule"}},
		{"ExcludeStatus", backend.ListOpts{ExcludeStatus: []string{"closed"}}},
		{"ExcludeTypes", backend.ListOpts{ExcludeTypes: []string{"epic"}}},
		{"Deferred", backend.ListOpts{Deferred: true}},
		{"DeferAfter", backend.ListOpts{DeferAfter: "2026-01-01"}},
		{"DeferBefore", backend.ListOpts{DeferBefore: "2026-12-31"}},
		{"DueAfter", backend.ListOpts{DueAfter: "2026-01-01"}},
		{"DueBefore", backend.ListOpts{DueBefore: "2026-12-31"}},
		{"Overdue", backend.ListOpts{Overdue: true}},
		{"AllowStale", backend.ListOpts{AllowStale: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkFleetUnsupportedFilters(tt.opts)
			if err == nil {
				t.Fatalf("expected error for unsupported field %s, got nil", tt.name)
			}
			if !errors.Is(err, backend.ErrFilterNotSupported) {
				t.Errorf("expected error wrapping ErrFilterNotSupported, got %v", err)
			}
			if !strings.Contains(err.Error(), tt.name) {
				t.Errorf("error message should contain field name %q, got %q", tt.name, err.Error())
			}
		})
	}
}

// TestCheckFleetUnsupportedFilters_SupportedFieldsOnly verifies that setting
// only supported fields does not trigger an error.
func TestCheckFleetUnsupportedFilters_SupportedFieldsOnly(t *testing.T) {
	opts := backend.ListOpts{
		Status:        "open",
		IssueType:     "task",
		Assignee:      "agent-1",
		Labels:        []string{"urgent"},
		ParentID:      "parent-1",
		Limit:         50,
		UpdatedAfter:  "2026-01-01",
		UpdatedBefore: "2026-12-31",
		SourceRepos:   []string{"repo-a"},
	}
	if err := checkFleetUnsupportedFilters(opts); err != nil {
		t.Errorf("supported-only opts should not error, got %v", err)
	}
}

// --- Create tests ---

func TestCreate_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		respondOK(w, testIssue{
			ID:        "new-1",
			Title:     "New Issue",
			Status:    workitems.StatusOpen,
			Priority:  2,
			IssueType: workitems.TypeTask,
			CreatedAt: now,
			UpdatedAt: now,
		})
	})
	defer ts.Close()

	result, err := fb.Create(context.Background(), backend.CreateParams{
		Title:     "New Issue",
		Status:    "deferred",
		IssueType: "task",
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.ID != "new-1" {
		t.Errorf("ID = %q, want %q", result.ID, "new-1")
	}
	if gotBody["status"] != "deferred" {
		t.Errorf("body.status = %v, want deferred", gotBody["status"])
	}
}

func TestCreateParentNotFoundIsValidation(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "parent issue not found",
			},
		})
	})
	defer ts.Close()

	_, err := fb.Create(context.Background(), backend.CreateParams{Title: "Child", Parent: "missing"})
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("Create error = %v, want validation", err)
	}
}

// --- Update tests ---

func TestUpdate_HappyPath(t *testing.T) {
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("Method = %q, want PATCH", r.Method)
		}
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{ //nolint:errcheck
			Success: true,
			Data:    json.RawMessage(`{"id":"test-1","status":"updated"}`),
		})
	})
	defer ts.Close()

	title := "Updated Title"
	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		Title: &title,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotBody["title"] != "Updated Title" {
		t.Errorf("title = %v, want %q", gotBody["title"], "Updated Title")
	}
}

func TestUpdate_ClaimRejected(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called when Claim=true")
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		Claim: true,
	})
	if err == nil {
		t.Fatal("expected error for Claim=true")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T", err)
	}
	if be.Kind != backend.KindValidation {
		t.Errorf("Kind = %v, want KindValidation", be.Kind)
	}
	if !strings.Contains(be.Message, "ClaimIssue") {
		t.Errorf("Message = %q, want it to mention ClaimIssue", be.Message)
	}
}

func TestUpdate_ClaimFalseAllowed(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	title := "Some Title"
	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		Claim: false,
		Title: &title,
	})
	if err != nil {
		t.Fatalf("Update with Claim=false should succeed, got: %v", err)
	}
}

func TestUpdate_StatusInProgressWithAssigneeClaimsAsAssignee(t *testing.T) {
	var gotActor string
	var gotClaim bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, testIssue{ID: "test-1", Title: "T", Status: workitems.StatusOpen, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/claim"):
			gotClaim = true
			gotActor = r.Header.Get("X-Actor")
			respondOK(w, json.RawMessage(`{}`))
		default:
			respondErr(w, http.StatusNotFound, "not found")
		}
	})
	defer ts.Close()

	status := "in_progress"
	assignee := "[H] Tyson"
	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{
		Status:   &status,
		Assignee: &assignee,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !gotClaim {
		t.Fatal("expected claim endpoint to be called")
	}
	if gotActor != "[H] Tyson" {
		t.Errorf("X-Actor = %q, want %q", gotActor, "[H] Tyson")
	}
}

func TestUpdate_StatusOpenClearsAssigneeOnAlreadyOpenIssue(t *testing.T) {
	var gotAssign bool
	var gotBody struct {
		Assignee string `json:"assignee"`
	}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusOpen,
				Assignee:  "old-agent",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/assign"):
			gotAssign = true
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	status := "open"
	if err := fb.Update(context.Background(), "test-1", backend.UpdateParams{Status: &status}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !gotAssign {
		t.Fatal("expected assign endpoint to be called")
	}
	if gotBody.Assignee != "" {
		t.Errorf("assignee = %q, want empty", gotBody.Assignee)
	}
}

func TestUpdate_StatusOpenAfterReopenClearsAssignee(t *testing.T) {
	var sawReopen bool
	var sawClearAssign bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, testIssue{
				ID:        "test-1",
				Title:     "T",
				Status:    workitems.StatusClosed,
				Assignee:  "old-agent",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, map[string]interface{}{"dependencies": []interface{}{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, []interface{}{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/reopen"):
			sawReopen = true
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/assign"):
			var body struct {
				Assignee string `json:"assignee"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			sawClearAssign = body.Assignee == ""
			respondOK(w, json.RawMessage(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	status := "open"
	if err := fb.Update(context.Background(), "test-1", backend.UpdateParams{Status: &status}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !sawReopen {
		t.Fatal("expected reopen endpoint to be called")
	}
	if !sawClearAssign {
		t.Fatal("expected assignment to be cleared after reopen")
	}
}

func TestUpdate_AssigneeOnlyUsesAssignEndpoint(t *testing.T) {
	var gotBody struct {
		Assignee string `json:"assignee"`
	}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/assign") {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	assignee := "alice"
	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{Assignee: &assignee})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotBody.Assignee != "alice" {
		t.Errorf("assignee = %q, want alice", gotBody.Assignee)
	}
}

func TestUpdate_LabelOnlyUsesLabelEndpoint(t *testing.T) {
	var sawLabelPost bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/test-1/labels"):
			sawLabelPost = true
			respondOK(w, json.RawMessage(`{}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1"):
			respondOK(w, fleetIssueWithCountsWire{
				fleetIssueWire: fleetIssueWire{
					ID:     "test-1",
					Labels: []string{"frontend"},
				},
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/deps"):
			respondOK(w, struct {
				Dependencies []struct{} `json:"dependencies"`
			}{})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/test-1/comments"):
			respondOK(w, struct {
				Comments []fleetCommentWire `json:"comments"`
			}{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{AddLabels: []string{"frontend"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !sawLabelPost {
		t.Fatal("label POST was not called")
	}
}

// --- ClaimIssue tests ---

func TestClaimIssue_HappyPath(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// fleet-db's claim is per-issue: POST /api/v1/{ws}/issues/{id}/claim
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/claim") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength > 0 {
			t.Errorf("zero TTL should omit request body; content length = %d", r.ContentLength)
		}
		w.Header().Set("Content-Type", "application/json")
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	err := fb.ClaimIssue(context.Background(), "test-1", 0)
	if err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}

func TestClaimIssueAsActor_OverridesActorHeader(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/claim") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Actor"); got != "desktopqa" {
			t.Fatalf("X-Actor = %q, want desktopqa", got)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.ClaimIssueAsActor(context.Background(), "test-1", 0, "desktopqa"); err != nil {
		t.Fatalf("ClaimIssueAsActor: %v", err)
	}
}

func TestClaimIssueAsActor_UsesDelegationForServiceCredential(t *testing.T) {
	var gotActor, gotDelegated string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActor = r.Header.Get("X-Actor")
		gotDelegated = r.Header.Get("X-Fleet-Delegated-Actor")
		respondOK(w, json.RawMessage(`{}`))
	}))
	defer ts.Close()

	fb, err := New(Config{
		BaseURL: ts.URL, WorkspaceID: "ws", APIKey: "service-key", Actor: "loom-local-service",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fb.ClaimIssueAsActor(context.Background(), "test-1", 0, "planner"); err != nil {
		t.Fatalf("ClaimIssueAsActor: %v", err)
	}
	if gotActor != "loom-local-service" || gotDelegated != "planner" {
		t.Fatalf("headers actor=%q delegated=%q, want loom-local-service/planner", gotActor, gotDelegated)
	}
}

func TestRenewIssueClaimAsActor_SendsRenewOnly(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/claim") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Actor"); got != "desktopqa" {
			t.Fatalf("X-Actor = %q, want desktopqa", got)
		}
		var body struct {
			RenewOnly bool `json:"renew_only"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if !body.RenewOnly {
			t.Error("renew_only = false, want true")
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.RenewIssueClaimAsActor(context.Background(), "test-1", 0, "desktopqa"); err != nil {
		t.Fatalf("RenewIssueClaimAsActor: %v", err)
	}
}

func TestClaimIssue_ForwardsLockTTL(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/claim") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			LockTTL int `json:"lock_ttl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.LockTTL != 300 {
			t.Errorf("lock_ttl = %d, want 300", body.LockTTL)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.ClaimIssue(context.Background(), "test-1", 5*time.Minute); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}

func TestClaimIssue_RoundsPositiveSubsecondTTL(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LockTTL int `json:"lock_ttl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.LockTTL != 1 {
			t.Errorf("lock_ttl = %d, want 1", body.LockTTL)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.ClaimIssue(context.Background(), "test-1", time.Millisecond); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}

func TestClaimIssue_EmptyID(t *testing.T) {
	fb, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := fb.ClaimIssue(context.Background(), "", 0)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestClaimIssue_NegativeTTL(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be contacted for negative TTL")
	})
	defer ts.Close()

	err := fb.ClaimIssue(context.Background(), "test-1", -time.Second)
	if err == nil {
		t.Fatal("expected error for negative TTL")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestClaimIssue_Conflict(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 409, "task already claimed by another worker")
	})
	defer ts.Close()

	err := fb.ClaimIssue(context.Background(), "test-1", 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("expected KindConflict, got %v", err)
	}
}

func TestDeferWorkflow_UsesDedicatedEndpointWithoutDate(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/defer") {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength > 0 {
			t.Fatalf("zero defer date should omit body, content length = %d", r.ContentLength)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.deferIssue(context.Background(), "test-1", time.Time{}); err != nil {
		t.Fatalf("deferIssue: %v", err)
	}
}

func TestRemoveLabelRequest_UsesDedicatedEndpoint(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/labels/backend") {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.removeLabel(context.Background(), "test-1", "backend"); err != nil {
		t.Fatalf("removeLabel: %v", err)
	}
}

// --- Close tests ---

func TestClose_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var claimReleased bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		// Close releases the agent claim before closing (a closed issue is
		// terminal and can no longer be unassigned).
		if strings.HasSuffix(r.URL.Path, "/assign") {
			var body struct {
				Assignee string `json:"assignee"`
			}
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			if body.Assignee != "" {
				t.Errorf("close assign assignee = %q, want empty (claim released)", body.Assignee)
			}
			claimReleased = true
			respondOK(w, json.RawMessage(`{}`))
			return
		}
		var gotBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if gotBody["reason"] != "done" {
			t.Errorf("reason = %v, want done", gotBody["reason"])
		}
		for _, unsupported := range []string{"session", "suggest_next", "force"} {
			if _, ok := gotBody[unsupported]; ok {
				t.Errorf("close body included unsupported fleet-db field %q", unsupported)
			}
		}
		respondOK(w, closeResultJSON{
			Closed: &fleetIssueWire{ID: "test-1", Title: "Done", Status: string(workitems.StatusClosed), CreatedAt: now, UpdatedAt: now, ClosedAt: &now},
			Unblocked: []*fleetIssueWire{
				{ID: "freed-1", Title: "Free", Status: string(workitems.StatusOpen), CreatedAt: now, UpdatedAt: now},
			},
		})
	})
	defer ts.Close()

	result, err := fb.Close(context.Background(), "test-1", backend.CloseParams{Reason: "done", Session: "session-1", SuggestNext: true, Force: true})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if result.Closed == nil || result.Closed.ID != "test-1" {
		t.Errorf("Closed = %v, want test-1", result.Closed)
	}
	if len(result.Unblocked) != 1 {
		t.Fatalf("Unblocked len = %d, want 1", len(result.Unblocked))
	}
	if !claimReleased {
		t.Error("expected close to release the agent claim before closing")
	}
}

func TestClose_NoUnblockedIssues(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// fleet-db currently returns closed issue but no unblocked issues (fleet-q6ox)
		respondOK(w, closeResultJSON{
			Closed: &fleetIssueWire{ID: "T-10", Title: "Done", Status: string(workitems.StatusClosed), CreatedAt: now, UpdatedAt: now, ClosedAt: &now},
		})
	})
	defer ts.Close()

	result, err := fb.Close(context.Background(), "T-10", backend.CloseParams{Reason: "done"})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if result.Closed == nil || result.Closed.ID != "T-10" {
		t.Errorf("Closed.ID = %v, want T-10", result.Closed)
	}
	if result.Closed.Status != "closed" {
		t.Errorf("Closed.Status = %q, want %q", result.Closed.Status, "closed")
	}
	if result.Unblocked == nil {
		t.Fatal("Unblocked must be non-nil (empty slice, not nil)")
	}
	if len(result.Unblocked) != 0 {
		t.Errorf("Unblocked len = %d, want 0", len(result.Unblocked))
	}
}

func TestClose_BareIssueResponse(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, testIssue{ID: "T-11", Title: "Done", Status: workitems.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now})
	})
	defer ts.Close()

	result, err := fb.Close(context.Background(), "T-11", backend.CloseParams{Reason: "done"})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if result.Closed == nil || result.Closed.ID != "T-11" {
		t.Errorf("Closed.ID = %v, want T-11", result.Closed)
	}
	if result.Unblocked == nil {
		t.Fatal("Unblocked must be non-nil (empty slice, not nil)")
	}
}

func TestClose_ServerError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 409, "issue already closed")
	})
	defer ts.Close()

	result, err := fb.Close(context.Background(), "test-1", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if result != nil {
		t.Errorf("result = %v, want nil on error", result)
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Errorf("expected KindConflict, got %v", err)
	}
}

func TestClose_EmptyResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{Success: true}) //nolint:errcheck
	})
	defer ts.Close()

	result, err := fb.Close(context.Background(), "test-1", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected error for empty response data, got nil")
	}
	if result != nil {
		t.Errorf("result = %v, want nil on error", result)
	}
}

func TestClose_UnmarshalError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{ //nolint:errcheck
			Success: true,
			Data:    json.RawMessage(`"not a close result"`),
		})
	})
	defer ts.Close()

	result, err := fb.Close(context.Background(), "test-1", backend.CloseParams{})
	if err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
	if result != nil {
		t.Errorf("result = %v, want nil on error", result)
	}
}

// --- Delete tests ---

func TestDelete_HappyPath(t *testing.T) {
	var deletedIDs []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("Method = %q, want DELETE", r.Method)
		}
		parts := strings.Split(r.URL.Path, "/")
		deletedIDs = append(deletedIDs, parts[len(parts)-1])
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{Success: true}) //nolint:errcheck
	})
	defer ts.Close()

	err := fb.Delete(context.Background(), backend.DeleteParams{IDs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deletedIDs) != 2 {
		t.Errorf("deletedIDs = %v, want [a b]", deletedIDs)
	}
}

func TestDelete_EmptyIDs(t *testing.T) {
	fb, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := fb.Delete(context.Background(), backend.DeleteParams{})
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

func TestDelete_ForceSkipsNotFound(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 404, "not found")
	})
	defer ts.Close()

	err := fb.Delete(context.Background(), backend.DeleteParams{IDs: []string{"gone"}, Force: true})
	if err != nil {
		t.Fatalf("expected nil with Force, got %v", err)
	}
}

func TestDelete_ActiveClaimConflict(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, http.StatusConflict, "issue is already claimed")
	})
	defer ts.Close()

	err := fb.Delete(context.Background(), backend.DeleteParams{IDs: []string{"active"}})
	if !backend.IsKind(err, backend.KindConflict) {
		t.Fatalf("Delete() error = %v, want KindConflict", err)
	}
}

// --- Dependency tests ---

func TestAddDependency(t *testing.T) {
	var gotBody map[string]string
	var sawPost bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/a/deps"):
			sawPost = true
			json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(apiResponse{Success: true}) //nolint:errcheck
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/a"):
			respondOK(w, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{ID: "a"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/a/deps"):
			respondOK(w, struct {
				Dependencies []struct {
					IssueID     string `json:"issue_id"`
					DependsOnID string `json:"depends_on_id"`
					Type        string `json:"type"`
				} `json:"dependencies"`
			}{
				Dependencies: []struct {
					IssueID     string `json:"issue_id"`
					DependsOnID string `json:"depends_on_id"`
					Type        string `json:"type"`
				}{
					{IssueID: "a", DependsOnID: "b", Type: "blocks"},
				},
			})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/b"):
			respondOK(w, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{ID: "b", Title: "Blocker"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/a/comments"):
			respondOK(w, struct {
				Comments []fleetCommentWire `json:"comments"`
			}{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	err := fb.AddDependency(context.Background(), backend.DepAddParams{
		FromID:  "a",
		ToID:    "b",
		DepType: "blocks",
	})
	if err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if !sawPost {
		t.Fatal("dependency POST was not called")
	}
	if gotBody["depends_on_id"] != "b" {
		t.Errorf("depends_on_id = %q, want %q", gotBody["depends_on_id"], "b")
	}
}

func TestRemoveDependency(t *testing.T) {
	var sawDelete bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/issues/a/deps/b"):
			sawDelete = true
			if got := r.URL.Query().Get("type"); got != "blocks" {
				t.Fatalf("dependency type query = %q, want %q", got, "blocks")
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(apiResponse{Success: true}) //nolint:errcheck
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/a"):
			respondOK(w, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{ID: "a"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/a/deps"):
			respondOK(w, struct {
				Dependencies []struct{} `json:"dependencies"`
			}{})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/a/comments"):
			respondOK(w, struct {
				Comments []fleetCommentWire `json:"comments"`
			}{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	err := fb.RemoveDependency(context.Background(), backend.DepRemoveParams{FromID: "a", ToID: "b"})
	if err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
	if !sawDelete {
		t.Fatal("dependency DELETE was not called")
	}
}

// --- Label tests ---

func TestAddLabelRequest(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{ //nolint:errcheck
			Success: true,
			Data:    json.RawMessage(`{"id":"test-1","status":"updated"}`),
		})
	})
	defer ts.Close()

	err := fb.addLabel(context.Background(), "test-1", "urgent")
	if err != nil {
		t.Fatalf("addLabel: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("Method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/test-1/labels") {
		t.Errorf("path = %q, want suffix /issues/test-1/labels", gotPath)
	}
	if gotBody["label"] != "urgent" {
		t.Errorf("label = %v, want urgent", gotBody["label"])
	}
}

func TestRemoveLabelRequest(t *testing.T) {
	var gotPath, gotMethod string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{ //nolint:errcheck
			Success: true,
			Data:    json.RawMessage(`{"id":"test-1","status":"updated"}`),
		})
	})
	defer ts.Close()

	err := fb.removeLabel(context.Background(), "test-1", "urgent")
	if err != nil {
		t.Fatalf("removeLabel: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("Method = %q, want DELETE", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/test-1/labels/urgent") {
		t.Errorf("path = %q, want suffix /issues/test-1/labels/urgent", gotPath)
	}
}

// --- Comment tests ---

func TestAddComment_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		respondOK(w, map[string]interface{}{
			"id":         1,
			"issue_id":   "test-1",
			"author":     "user",
			"body":       "hello",
			"created_at": now,
		})
	})
	defer ts.Close()

	result, err := fb.AddComment(context.Background(), backend.CommentAddParams{
		IssueID: "test-1",
		Author:  "user",
		Text:    "hello",
	})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
}

// --- Events tests ---

func TestListEvents_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues/test-1/history") {
			t.Errorf("path = %q, want suffix /issues/test-1/history", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit = %q, want %q", r.URL.Query().Get("limit"), "10")
		}
		respondOK(w, map[string]any{
			"history": []map[string]any{
				{
					"id":        "1",
					"timestamp": now,
					"actor":     "user",
					"action":    "issue.created",
				},
			},
		})
	})
	defer ts.Close()

	result, err := fb.ListEvents(context.Background(), "test-1", 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].Kind != "issue.created" {
		t.Errorf("Kind = %q, want %q", result[0].Kind, "issue.created")
	}
}

// --- Not implemented surfaces (partial — implemented methods moved out) ---
//
// Count, Batch, GetMutations, and WaitForMutations used to live here as
// ErrNotImplemented stubs; they are now wired against fleet-db endpoints
// (see tests above). The only remaining KindNotImplemented paths are:
//
//   - Count with GroupBy set — Count cannot return grouped data through its
//     int return value; callers must use Stats or (future) a GroupedCount API.
//     This is exercised by TestCount_GroupByRejected.
//
// If future refactors reintroduce unimplemented stubs, add cases here.

// --- Connection refused test ---

func TestConnectionRefused(t *testing.T) {
	fb, err := New(Config{
		BaseURL:     "http://127.0.0.1:1", // port 1 is unlikely to be open
		WorkspaceID: "ws",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = fb.Stats(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !backend.IsKind(err, backend.KindUnavailable) && !backend.IsKind(err, backend.KindTimeout) {
		t.Fatalf("expected KindUnavailable or KindTimeout, got %v", err)
	}
}

// --- Non-JSON response test ---

func TestNonJSONResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		w.Write([]byte("<html>Bad Gateway</html>")) //nolint:errcheck
	})
	defer ts.Close()

	_, err := fb.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Non-JSON response is classified as a transport error (parse failure).
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("expected KindUnavailable, got %v", err)
	}
}

// --- Reopen test ---

func TestReopen_HappyPath(t *testing.T) {
	var reopenPosted bool
	var commentPosted bool
	var claimReleased bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// fleet-db reopen is POST /issues/{id}/reopen (dedicated endpoint);
		// the previous PATCH status=open approach was rejected by
		// fleet-db's strict JSON validation. Comment body uses "body"
		// to match CreateCommentRequest.
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/reopen") {
			reopenPosted = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(apiResponse{ //nolint:errcheck
				Success: true,
				Data:    json.RawMessage(`{"id":"test-1","status":"open"}`),
			})
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments") {
			commentPosted = true
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			if body["body"] != "need more work" {
				t.Errorf("comment body = %q, want %q", body["body"], "need more work")
			}
			respondOK(w, map[string]interface{}{"id": 1, "body": body["body"]})
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/assign") {
			var body struct {
				Assignee string `json:"assignee"`
			}
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			if body.Assignee != "" {
				t.Errorf("reopen assign assignee = %q, want empty (claim released)", body.Assignee)
			}
			claimReleased = true
			respondOK(w, json.RawMessage(`{}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()

	err := fb.Reopen(context.Background(), "test-1", backend.ReopenParams{Reason: "need more work"})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if !reopenPosted {
		t.Error("expected POST /issues/{id}/reopen to fire")
	}
	if !commentPosted {
		t.Error("expected comment to be posted with reason")
	}
	if !claimReleased {
		t.Error("expected reopen to release the stale assignee claim")
	}
}

func TestReopen_EmptyID(t *testing.T) {
	fb, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	err := fb.Reopen(context.Background(), "", backend.ReopenParams{})
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("expected KindValidation, got %v", err)
	}
}

// --- Ready tests ---

func TestReady_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "epic-1"
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/test-ws/issues/ready"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		respondOK(w, []*readyIssueWithParent{
			{
				fleetIssueWire: fleetIssueWire{ID: "r-1", Title: "Ready", Status: string(workitems.StatusOpen), CreatedAt: now, UpdatedAt: now},
				Parent:         &parent,
			},
		})
	})
	defer ts.Close()

	result, err := fb.Ready(context.Background(), workitems.AvailabilityQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].Parent != "epic-1" {
		t.Errorf("Parent = %q, want %q", result[0].Parent, "epic-1")
	}
}

func TestReady_ClientFiltersSourceReposWithoutServerLimit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotQuery string
	repoA := "repo-a"
	repoB := "repo-b"
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, []*readyIssueWithParent{
			{fleetIssueWire: fleetIssueWire{ID: "repo-a", Title: "A", Status: string(workitems.StatusOpen), CreatedAt: now, UpdatedAt: now}, Repo: &repoA},
			{fleetIssueWire: fleetIssueWire{ID: "repo-b", Title: "B", Status: string(workitems.StatusOpen), CreatedAt: now, UpdatedAt: now}, Repo: &repoB},
		})
	})
	defer ts.Close()

	result, err := fb.Ready(context.Background(), workitems.AvailabilityQuery{
		SourceRepos: []string{"repo-b"},
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if strings.Contains(gotQuery, "source_repos=") || strings.Contains(gotQuery, "limit=") {
		t.Fatalf("query = %q, want no unsupported repo filter or pre-filter limit", gotQuery)
	}
	if len(result) != 1 || result[0].ID != "repo-b" {
		t.Fatalf("result = %+v, want repo-b", result)
	}
}

func TestDeferred_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "epic-1"
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/test-ws/issues/deferred"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		respondOK(w, struct {
			Issues []*readyIssueWithParent `json:"issues"`
			Count  int                     `json:"count"`
		}{Issues: []*readyIssueWithParent{
			{
				fleetIssueWire: fleetIssueWire{ID: "d-1", Title: "Deferred", Status: string(workitems.StatusOpen), Type: "task", ParentID: parent, CreatedAt: now, UpdatedAt: now},
			},
		}, Count: 1})
	})
	defer ts.Close()

	result, err := fb.Deferred(context.Background(), workitems.AvailabilityQuery{ParentID: parent, IssueType: "task"})
	if err != nil {
		t.Fatalf("Deferred: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].ID != "d-1" || result[0].Parent != parent || result[0].Status != string(workitems.StatusOpen) {
		t.Fatalf("deferred result = %+v", result[0])
	}
}

func TestDeferred_AppliesOwnerProjectionFilters(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repoA, repoB := "repo-a", "repo-b"
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, struct {
			Issues []*readyIssueWithParent `json:"issues"`
		}{Issues: []*readyIssueWithParent{
			{fleetIssueWire: fleetIssueWire{ID: "d-a", Type: "task", Assignee: "agent", Labels: []string{"other"}, CreatedAt: now, UpdatedAt: now}, Repo: &repoA},
			{fleetIssueWire: fleetIssueWire{ID: "d-b", Type: "task", Labels: []string{"urgent"}, CreatedAt: now, UpdatedAt: now}, Repo: &repoB},
		}})
	})
	defer ts.Close()

	result, err := fb.Deferred(context.Background(), workitems.AvailabilityQuery{
		Unassigned: true, LabelsAny: []string{"urgent"}, SourceRepos: []string{"repo-b"}, Limit: 1,
	})
	if err != nil {
		t.Fatalf("Deferred: %v", err)
	}
	if len(result) != 1 || result[0].ID != "d-b" || result[0].SourceRepo != repoB || result[0].Repo != repoB {
		t.Fatalf("deferred result = %#v, want owner projection d-b", result)
	}
}

func TestDeferred_RejectsUnprojectableFilters(t *testing.T) {
	fb, ts := newTestServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsupported filter must not issue a request")
	})
	defer ts.Close()

	_, err := fb.Deferred(context.Background(), workitems.AvailabilityQuery{MolType: "work"})
	if !errors.Is(err, backend.ErrFilterNotSupported) {
		t.Fatalf("Deferred error = %v, want unsupported filter", err)
	}
}

// --- Blocked tests ---

func TestBlocked_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/test-ws/issues/blocked"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		respondOK(w, []blockedIssueResponseWire{
			{
				Issue:    fleetIssueWire{ID: "b-1", Title: "Blocked", Status: string(workitems.StatusBlocked), CreatedAt: now, UpdatedAt: now},
				Blockers: []blockedBlockerWire{{ID: "dep-1"}},
			},
		})
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), workitems.AvailabilityQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].Status != "blocked" {
		t.Errorf("Status = %q, want %q", result[0].Status, "blocked")
	}
}

// --- ListComments tests ---

func TestListComments_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	details := testIssueDetails{
		testIssue: testIssue{
			ID:        "test-1",
			Title:     "Test",
			Status:    workitems.StatusOpen,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Labels:       []string{},
		Dependencies: []*testIssueWithDependencyMetadata{},
		Dependents:   []*testIssueWithDependencyMetadata{},
		Comments: []*workitems.Comment{
			{ID: 2, IssueID: "test-1", Author: "user2", Text: "c2", CreatedAt: now.Add(time.Second)},
			{ID: 1, IssueID: "test-1", Author: "user", Text: "c1", CreatedAt: now},
		},
	}

	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, details)
	})
	defer ts.Close()

	result, err := fb.ListComments(context.Background(), "test-1")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].ID != 1 || result[1].ID != 2 {
		t.Fatalf("comment order = [%d %d], want oldest-first [1 2]", result[0].ID, result[1].ID)
	}
}

func TestListComments_NoComments(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	details := testIssueDetails{
		testIssue: testIssue{
			ID:        "test-1",
			Title:     "Test",
			Status:    workitems.StatusOpen,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Labels:       []string{},
		Dependencies: []*testIssueWithDependencyMetadata{},
		Dependents:   []*testIssueWithDependencyMetadata{},
		Comments:     []*workitems.Comment{},
	}

	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, details)
	})
	defer ts.Close()

	result, err := fb.ListComments(context.Background(), "test-1")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if result == nil {
		t.Fatal("expected empty slice, not nil")
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

// --- Deferred workflow ---

func TestDeferWorkflow_WithUntil(t *testing.T) {
	until := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	wantUntil := until.Format(time.RFC3339)

	var gotPath, gotMethod string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondOK(w, map[string]interface{}{})
	})
	defer ts.Close()

	if err := fb.deferIssue(context.Background(), "loom-1", until); err != nil {
		t.Fatalf("deferIssue: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/loom-1/defer") {
		t.Errorf("path = %q, want suffix /issues/loom-1/defer", gotPath)
	}
	if gotBody["defer_until"] != wantUntil {
		t.Errorf("defer_until = %v, want %q", gotBody["defer_until"], wantUntil)
	}
}

func TestDeferWorkflow_ZeroUntil(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondOK(w, map[string]interface{}{})
	})
	defer ts.Close()

	if err := fb.deferIssue(context.Background(), "loom-1", time.Time{}); err != nil {
		t.Fatalf("deferIssue: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/loom-1/defer") {
		t.Errorf("path = %q, want suffix /issues/loom-1/defer", gotPath)
	}
	if gotBody != nil {
		t.Errorf("defer_until should not be set for zero until, got %v", gotBody["defer_until"])
	}
}

// --- GetMutations tests ---

func TestGetMutationsAfter_PreservesRedisStreamCursor(t *testing.T) {
	var gotSince string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "1700000000000-1"})
	})
	defer ts.Close()

	if _, err := fb.GetMutationsAfter(context.Background(), "1700000000000-0"); err != nil {
		t.Fatalf("GetMutationsAfter: %v", err)
	}
	want := fleetOpaqueCursorPrefix + base64.RawURLEncoding.EncodeToString([]byte("1700000000000-0"))
	if gotSince != want {
		t.Fatalf("since = %q, want opaque cursor %q", gotSince, want)
	}
}

func TestGetMutationsAfter_PreservesOpaqueCursor(t *testing.T) {
	var gotSince string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "1700000000000-1"})
	})
	defer ts.Close()

	want := fleetOpaqueCursorPrefix + base64.RawURLEncoding.EncodeToString([]byte("1700000000000-0"))
	if _, err := fb.GetMutationsAfter(context.Background(), want); err != nil {
		t.Fatalf("GetMutationsAfter: %v", err)
	}
	if gotSince != want {
		t.Fatalf("since = %q, want opaque cursor preserved", gotSince)
	}
}

func TestWaitForMutationsAfter_IntegerCursorAddsRedisSequence(t *testing.T) {
	var gotSince string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "1700000000000-1"})
	})
	defer ts.Close()

	if _, err := fb.WaitForMutationsAfter(context.Background(), "1700000000000", 1000); err != nil {
		t.Fatalf("WaitForMutationsAfter: %v", err)
	}
	want := fleetOpaqueCursorPrefix + base64.RawURLEncoding.EncodeToString([]byte("1700000000000-0"))
	if gotSince != want {
		t.Fatalf("since = %q, want integer cursor normalized to opaque stream ID %q", gotSince, want)
	}
}
