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
	"github.com/tysonthomas9/loomcli/internal/types"
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
	details := types.IssueDetails{
		Issue: types.Issue{
			ID:        "issue-1",
			Title:     "Test Issue",
			Status:    types.StatusOpen,
			Priority:  2,
			IssueType: types.TypeTask,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Labels:       []string{"label-1"},
		Dependencies: []*types.IssueWithDependencyMetadata{},
		Dependents:   []*types.IssueWithDependencyMetadata{},
		Comments:     []*types.Comment{},
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
	issues := []*types.IssueWithCounts{
		{
			Issue:           &types.Issue{ID: "a", Title: "A", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
			DependencyCount: 1,
		},
		{
			Issue: &types.Issue{ID: "b", Title: "B", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now},
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
		respondOK(w, []*types.IssueWithCounts{})
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
		"labels=urgent", "updated_after=2026-01-01", "source_repos=repo-a",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestList_UnsupportedFilter_Single(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("RPC call should not be made when unsupported filter is set")
		respondOK(w, []*types.IssueWithCounts{})
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
		respondOK(w, []*types.IssueWithCounts{})
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
		respondOK(w, []*types.IssueWithCounts{})
	})
	defer ts.Close()

	_, err := fb.List(context.Background(), backend.ListOpts{})
	if err != nil {
		t.Errorf("empty ListOpts should not error, got %v", err)
	}
}

// --- GetChildren ---

func TestGetChildren_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issues := []*types.IssueWithCounts{
		{Issue: &types.Issue{ID: "c1", Title: "Child 1", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now}},
		{Issue: &types.Issue{ID: "c2", Title: "Child 2", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now}},
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

	result, err := fb.GetChildren(context.Background(), "epic-1")
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
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

func TestGetChildren_EmptyID(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fb.GetChildren(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestGetChildren_Empty(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []*types.IssueWithCounts{})
	})
	defer ts.Close()
	result, err := fb.GetChildren(context.Background(), "epic-1")
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

// --- SearchIssues ---

func TestSearchIssues_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	issues := []*types.IssueWithCounts{
		{Issue: &types.Issue{ID: "s1", Title: "Auth bug in login", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now}},
		{Issue: &types.Issue{ID: "s2", Title: "Auth bug: refresh token", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now}},
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

	result, err := fb.SearchIssues(context.Background(), "auth bug", 10)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues") {
		t.Errorf("path = %q, want suffix /issues", gotPath)
	}
	if !strings.Contains(gotQuery, "query=auth+bug") {
		t.Errorf("query = %q, want to contain query=auth+bug", gotQuery)
	}
	if !strings.Contains(gotQuery, "limit=10") {
		t.Errorf("query = %q, want to contain limit=10", gotQuery)
	}
	if len(result) != 2 || result[0].ID != "s1" || result[1].ID != "s2" {
		t.Errorf("got %v, want [s1 s2]", result)
	}
}

func TestSearchIssues_EmptyQuery(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fb.SearchIssues(context.Background(), "", 10)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestSearchIssues_NegativeLimit(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fb.SearchIssues(context.Background(), "q", -1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestSearchIssues_Empty(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []*types.IssueWithCounts{})
	})
	defer ts.Close()
	result, err := fb.SearchIssues(context.Background(), "nothing", 10)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
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

// --- Stats tests ---

func TestStats_HappyPath(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/test-ws/issues/count" {
			t.Errorf("path = %q, want issues/count", r.URL.Path)
		}
		if r.URL.Query().Get("group_by") != "status" {
			t.Errorf("group_by = %q, want status", r.URL.Query().Get("group_by"))
		}
		respondOK(w, countIssuesResponse{
			Total: 10,
			Groups: map[string]int64{
				"open":        3,
				"closed":      2,
				"in_progress": 2,
				"blocked":     1,
				"deferred":    1,
				"tombstone":   1,
			},
		})
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 10 {
		t.Errorf("TotalIssues = %d, want 10", result.TotalIssues)
	}
	if result.OpenIssues != 3 {
		t.Errorf("OpenIssues = %d, want 3", result.OpenIssues)
	}
	if result.ClosedIssues != 2 {
		t.Errorf("ClosedIssues = %d, want 2", result.ClosedIssues)
	}
	if result.InProgressIssues != 2 {
		t.Errorf("InProgressIssues = %d, want 2", result.InProgressIssues)
	}
	if result.BlockedIssues != 1 {
		t.Errorf("BlockedIssues = %d, want 1", result.BlockedIssues)
	}
	if result.DeferredIssues != 1 {
		t.Errorf("DeferredIssues = %d, want 1", result.DeferredIssues)
	}
	if result.TombstoneIssues != 1 {
		t.Errorf("TombstoneIssues = %d, want 1", result.TombstoneIssues)
	}
	if result.PinnedIssues != 0 {
		t.Errorf("PinnedIssues = %d, want 0 (not in groups)", result.PinnedIssues)
	}
	// ReadyIssues, EpicsEligibleForClosure, AverageLeadTime should be 0.
	if result.ReadyIssues != 0 {
		t.Errorf("ReadyIssues = %d, want 0 (fleet-08yg)", result.ReadyIssues)
	}
	if result.EpicsEligibleForClosure != 0 {
		t.Errorf("EpicsEligibleForClosure = %d, want 0 (fleet-08yg)", result.EpicsEligibleForClosure)
	}
	if result.AverageLeadTime != 0 {
		t.Errorf("AverageLeadTime = %f, want 0 (fleet-08yg)", result.AverageLeadTime)
	}
}

func TestStats_AllStatuses(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, countIssuesResponse{
			Total: 20,
			Groups: map[string]int64{
				"open":        3,
				"closed":      4,
				"in_progress": 2,
				"blocked":     1,
				"deferred":    2,
				"tombstone":   1,
				"pinned":      3,
				"review":      2,
				"hooked":      2,
			},
		})
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// Total comes from resp.Total, not sum of mapped fields.
	if result.TotalIssues != 20 {
		t.Errorf("TotalIssues = %d, want 20", result.TotalIssues)
	}
	if result.PinnedIssues != 3 {
		t.Errorf("PinnedIssues = %d, want 3", result.PinnedIssues)
	}
	// review and hooked have no extra stats field, so it is silently ignored.
}

func TestStats_EmptyWorkspace(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, countIssuesResponse{Total: 0, Groups: map[string]int64{}})
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 0 {
		t.Errorf("TotalIssues = %d, want 0", result.TotalIssues)
	}
	if result.OpenIssues != 0 {
		t.Errorf("OpenIssues = %d, want 0", result.OpenIssues)
	}
}

func TestStats_MissingStatusKeys(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, countIssuesResponse{
			Total:  5,
			Groups: map[string]int64{"open": 3, "closed": 2},
		})
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 5 {
		t.Errorf("TotalIssues = %d, want 5", result.TotalIssues)
	}
	if result.InProgressIssues != 0 {
		t.Errorf("InProgressIssues = %d, want 0", result.InProgressIssues)
	}
	if result.BlockedIssues != 0 {
		t.Errorf("BlockedIssues = %d, want 0", result.BlockedIssues)
	}
}

func TestStats_ClientError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 500, "internal error")
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

func TestStats_NilResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// Return success with nil Data field (never set).
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{Success: true, Data: nil}) //nolint:errcheck
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error for nil response data, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

func TestStats_JSONNullResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// Return success with JSON null data (distinct from Go nil).
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":null}`)) //nolint:errcheck
	})
	defer ts.Close()

	result, err := fb.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error for JSON null data, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
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
		respondOK(w, types.Issue{
			ID:        "new-1",
			Title:     "New Issue",
			Status:    types.StatusOpen,
			Priority:  2,
			IssueType: types.TypeTask,
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
			respondOK(w, types.Issue{ID: "test-1", Title: "T", Status: types.StatusOpen, CreatedAt: time.Now(), UpdatedAt: time.Now()})
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
	var gotPath string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/labels") {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	err := fb.Update(context.Background(), "test-1", backend.UpdateParams{AddLabels: []string{"frontend"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues/test-1/labels") {
		t.Errorf("path = %q", gotPath)
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

func TestDeferIssue_UsesDedicatedEndpointWithoutDate(t *testing.T) {
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

	if err := fb.DeferIssue(context.Background(), "test-1", time.Time{}); err != nil {
		t.Fatalf("DeferIssue: %v", err)
	}
}

func TestRemoveLabel_UsesDedicatedEndpoint(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || !strings.HasSuffix(r.URL.Path, "/issues/test-1/labels/backend") {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		respondOK(w, json.RawMessage(`{}`))
	})
	defer ts.Close()

	if err := fb.RemoveLabel(context.Background(), "test-1", "backend"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
}

// --- Close tests ---

func TestClose_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
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
			Closed: &types.Issue{ID: "test-1", Title: "Done", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now},
			Unblocked: []*types.Issue{
				{ID: "freed-1", Title: "Free", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
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
}

func TestClose_NoUnblockedIssues(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// fleet-db currently returns closed issue but no unblocked issues (fleet-q6ox)
		respondOK(w, closeResultJSON{
			Closed: &types.Issue{ID: "T-10", Title: "Done", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now},
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
		respondOK(w, types.Issue{ID: "T-11", Title: "Done", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now})
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

// --- Dependency tests ---

func TestAddDependency(t *testing.T) {
	var gotBody map[string]string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{Success: true}) //nolint:errcheck
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
	if gotBody["depends_on_id"] != "b" {
		t.Errorf("depends_on_id = %q, want %q", gotBody["depends_on_id"], "b")
	}
}

func TestRemoveDependency(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("Method = %q, want DELETE", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/deps/") {
			t.Errorf("path missing /deps/: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{Success: true}) //nolint:errcheck
	})
	defer ts.Close()

	err := fb.RemoveDependency(context.Background(), backend.DepRemoveParams{FromID: "a", ToID: "b"})
	if err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
}

// --- Label tests ---

func TestAddLabel(t *testing.T) {
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

	err := fb.AddLabel(context.Background(), "test-1", "urgent")
	if err != nil {
		t.Fatalf("AddLabel: %v", err)
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

func TestRemoveLabel(t *testing.T) {
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

	err := fb.RemoveLabel(context.Background(), "test-1", "urgent")
	if err != nil {
		t.Fatalf("RemoveLabel: %v", err)
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
				fleetIssueWire: fleetIssueWire{ID: "r-1", Title: "Ready", Status: string(types.StatusOpen), CreatedAt: now, UpdatedAt: now},
				Parent:         &parent,
			},
		})
	})
	defer ts.Close()

	result, err := fb.Ready(context.Background(), backend.ReadyOpts{Limit: 10})
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

// --- Blocked tests ---

func TestBlocked_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/test-ws/issues/blocked"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		respondOK(w, []*types.BlockedIssue{
			{
				Issue:          types.Issue{ID: "b-1", Title: "Blocked", Status: types.StatusBlocked, CreatedAt: now, UpdatedAt: now},
				BlockedBy:      []string{"dep-1"},
				BlockedByCount: 1,
			},
		})
	})
	defer ts.Close()

	result, err := fb.Blocked(context.Background(), backend.BlockedOpts{Limit: 10})
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
	details := types.IssueDetails{
		Issue: types.Issue{
			ID:        "test-1",
			Title:     "Test",
			Status:    types.StatusOpen,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Labels:       []string{},
		Dependencies: []*types.IssueWithDependencyMetadata{},
		Dependents:   []*types.IssueWithDependencyMetadata{},
		Comments: []*types.Comment{
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
	details := types.IssueDetails{
		Issue: types.Issue{
			ID:        "test-1",
			Title:     "Test",
			Status:    types.StatusOpen,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Labels:       []string{},
		Dependencies: []*types.IssueWithDependencyMetadata{},
		Dependents:   []*types.IssueWithDependencyMetadata{},
		Comments:     []*types.Comment{},
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

// --- DeferIssue / UndeferIssue ---

func TestDeferIssue_WithUntil(t *testing.T) {
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

	if err := fb.DeferIssue(context.Background(), "loom-1", until); err != nil {
		t.Fatalf("DeferIssue: %v", err)
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

func TestDeferIssue_ZeroUntil(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondOK(w, map[string]interface{}{})
	})
	defer ts.Close()

	if err := fb.DeferIssue(context.Background(), "loom-1", time.Time{}); err != nil {
		t.Fatalf("DeferIssue: %v", err)
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

func TestDeferIssue_EmptyID(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	err = fb.DeferIssue(context.Background(), "", time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestUndeferIssue_Success(t *testing.T) {
	var gotPath, gotMethod string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		respondOK(w, map[string]interface{}{})
	})
	defer ts.Close()

	if err := fb.UndeferIssue(context.Background(), "loom-1"); err != nil {
		t.Fatalf("UndeferIssue: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/loom-1/undefer") {
		t.Errorf("path = %q, want suffix /issues/loom-1/undefer", gotPath)
	}
}

func TestUndeferIssue_EmptyID(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	err = fb.UndeferIssue(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

// --- Count tests ---

func TestCount_NoFilters(t *testing.T) {
	var gotPath, gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		respondOK(w, countIssuesResponse{Total: 42, Groups: map[string]int64{}})
	})
	defer ts.Close()

	n, err := fb.Count(context.Background(), backend.CountOpts{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 42 {
		t.Errorf("count = %d, want 42", n)
	}
	if !strings.HasSuffix(gotPath, "/issues/count") {
		t.Errorf("path = %q, want suffix /issues/count", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty", gotQuery)
	}
}

func TestCount_WithFilters(t *testing.T) {
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, countIssuesResponse{Total: 7, Groups: map[string]int64{}})
	})
	defer ts.Close()

	n, err := fb.Count(context.Background(), backend.CountOpts{
		Status:    "open",
		IssueType: "task",
		Assignee:  "agent-1",
		Labels:    []string{"urgent"},
	})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 7 {
		t.Errorf("count = %d, want 7", n)
	}
	for _, want := range []string{"status=open", "type=task", "assignee=agent-1", "label=urgent"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestCount_MultipleLabelsRejected(t *testing.T) {
	fb, err := New(Config{
		BaseURL:     "http://fleet.test",
		WorkspaceID: "test-ws",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			t.Fatal("server should not be called when multiple labels are unsupported")
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = fb.Count(context.Background(), backend.CountOpts{
		Labels: []string{"urgent", "bug"},
	})
	if err == nil {
		t.Fatal("expected error for multiple labels")
	}
	if !errors.Is(err, backend.ErrFilterNotSupported) {
		t.Fatalf("error = %v, want ErrFilterNotSupported", err)
	}
}

func TestCount_GroupByRejected(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called when GroupBy is set")
	})
	defer ts.Close()

	_, err := fb.Count(context.Background(), backend.CountOpts{GroupBy: "status"})
	if err == nil {
		t.Fatal("expected error for GroupBy")
	}
	if !backend.IsKind(err, backend.KindNotImplemented) {
		t.Errorf("expected KindNotImplemented, got %v", err)
	}
}

func TestCount_ServerError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 500, "boom")
	})
	defer ts.Close()

	_, err := fb.Count(context.Background(), backend.CountOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("expected KindInternal, got %v", err)
	}
}

func TestCount_NilData(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
	})
	defer ts.Close()

	_, err := fb.Count(context.Background(), backend.CountOpts{})
	if err == nil {
		t.Fatal("expected error for nil data")
	}
}

// --- GetMutations tests ---

func TestGetMutations_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotPath, gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		respondOK(w, fleetMutationsResponse{
			Events: []fleetMutationEvent{
				{
					ID:         "1708000000000-0",
					Timestamp:  now,
					Actor:      "agent-a",
					Action:     "issue.create",
					EntityType: "issue",
					EntityID:   "loom-1",
					After:      `{"title":"New issue","status":"open","parent":"ep-1","repo":"org/repo"}`,
				},
				{
					ID:         "1708000000001-0",
					Timestamp:  now.Add(time.Second),
					Actor:      "agent-b",
					Action:     "issue.close",
					EntityType: "issue",
					EntityID:   "loom-2",
					Before:     `{"status":"open"}`,
					After:      `{"status":"closed"}`,
				},
			},
			Cursor:  "1708000000001-0",
			HasMore: false,
		})
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 1700000000000)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/events/mutations") {
		t.Errorf("path = %q, want suffix /events/mutations", gotPath)
	}
	if !strings.Contains(gotQuery, "since=1700000000000") {
		t.Errorf("query %q missing since=1700000000000", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("got %d mutations, want 2", len(got))
	}
	if got[0].Type != backend.MutationCreate {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, backend.MutationCreate)
	}
	if got[0].IssueID != "loom-1" || got[0].Title != "New issue" || got[0].ParentID != "ep-1" || got[0].SourceRepo != "org/repo" {
		t.Errorf("got[0] = %+v, after-snapshot fields not extracted", got[0])
	}
	if got[1].Type != backend.MutationStatus {
		t.Errorf("got[1].Type = %q, want %q (issue.close -> status)", got[1].Type, backend.MutationStatus)
	}
	if got[1].OldStatus != "open" || got[1].NewStatus != "closed" {
		t.Errorf("got[1] old/new status = %q/%q, want open/closed", got[1].OldStatus, got[1].NewStatus)
	}
}

func TestGetMutations_EmptyResponse(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "0", HasMore: false})
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestGetMutations_NullDataReturnsEmpty(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":null}`))
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestGetMutations_ActionFolding(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{
			Events: []fleetMutationEvent{
				{Timestamp: now, Action: "issue.update", EntityType: "issue", EntityID: "a"},
				{Timestamp: now, Action: "issue.delete", EntityType: "issue", EntityID: "b"},
				{Timestamp: now, Action: "comment.add", EntityType: "comment", EntityID: "c"},
				{Timestamp: now, Action: "label.add", EntityType: "label", EntityID: "d"},
				{Timestamp: now, Action: "workspace.update", EntityType: "workspace", EntityID: "ws"},
				{Timestamp: now, Action: "unknown.weird", EntityType: "issue", EntityID: "e"},
			},
		})
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	wantTypes := []string{
		backend.MutationUpdate, backend.MutationDelete, backend.MutationComment,
		backend.MutationUpdate, backend.MutationRefresh, backend.MutationUpdate,
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("len = %d, want %d", len(got), len(wantTypes))
	}
	for i, wantT := range wantTypes {
		if got[i].Type != wantT {
			t.Errorf("got[%d].Type = %q, want %q", i, got[i].Type, wantT)
		}
	}
}

// --- WaitForMutations tests ---

func TestWaitForMutations_TimeoutEmpty(t *testing.T) {
	var gotQuery string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, fleetMutationsResponse{Events: []fleetMutationEvent{}, Cursor: "0"})
	})
	defer ts.Close()

	got, err := fb.WaitForMutations(context.Background(), 1000, 5000)
	if err != nil {
		t.Fatalf("WaitForMutations: %v", err)
	}
	if !strings.Contains(gotQuery, "since=1000") || !strings.Contains(gotQuery, "timeout=5000") {
		t.Errorf("query %q missing since=1000 and/or timeout=5000", gotQuery)
	}
	if len(got) != 0 {
		t.Errorf("expected empty on timeout, got %d", len(got))
	}
}

func TestWaitForMutations_DeliversEvents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{
			Events: []fleetMutationEvent{
				{Timestamp: now, Action: "issue.update", EntityType: "issue", EntityID: "x"},
			},
		})
	})
	defer ts.Close()

	got, err := fb.WaitForMutations(context.Background(), 0, 2000)
	if err != nil {
		t.Fatalf("WaitForMutations: %v", err)
	}
	if len(got) != 1 || got[0].IssueID != "x" {
		t.Errorf("got %+v, want 1 mutation for x", got)
	}
}

func TestWaitForMutations_ServerError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 400, "timeout out of range")
	})
	defer ts.Close()

	_, err := fb.WaitForMutations(context.Background(), 0, 500)
	if err == nil {
		t.Fatal("expected error for bad timeout")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

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

// --- Batch tests ---

func TestBatch_EmptyOpsReturnsEmpty(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for empty ops")
	})
	defer ts.Close()

	got, err := fb.Batch(context.Background(), nil)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestBatch_Creates_Aggregated(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotPath string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondOK(w, map[string]interface{}{
			"issues": []map[string]interface{}{
				{"id": "new-1", "title": "One", "status": "open", "type": "task", "priority": 2, "created_at": now, "updated_at": now},
				{"id": "new-2", "title": "Two", "status": "open", "type": "task", "priority": 2, "created_at": now, "updated_at": now},
			},
			"count": 2,
		})
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "create", Args: json.RawMessage(`{"title":"One","issue_type":"task","priority":2}`)},
		{Operation: "create", Args: json.RawMessage(`{"title":"Two","issue_type":"task","priority":2}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues/batch") {
		t.Errorf("path = %q, want suffix /issues/batch", gotPath)
	}
	issues, ok := gotBody["issues"].([]interface{})
	if !ok || len(issues) != 2 {
		t.Errorf("body.issues len = %d, want 2 (body=%+v)", len(issues), gotBody)
	}
	firstIssue, _ := issues[0].(map[string]interface{})
	if _, exists := firstIssue["issue_type"]; exists {
		t.Errorf("body.issues[0] contains issue_type; fleet-db batch API expects type (body=%+v)", firstIssue)
	}
	if firstIssue["type"] != "task" {
		t.Errorf("body.issues[0].type = %v, want task", firstIssue["type"])
	}
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("results[%d].Success = false, error = %q", i, r.Error)
		}
		if len(r.Data) == 0 {
			t.Errorf("results[%d].Data is empty", i)
		}
		var data backend.IssueData
		if err := json.Unmarshal(r.Data, &data); err != nil {
			t.Fatalf("results[%d].Data unmarshal: %v", i, err)
		}
		if data.IssueType != "task" {
			t.Errorf("results[%d].Data.issue_type = %q, want task", i, data.IssueType)
		}
	}
}

func TestBatch_Creates_AllOrNothingError(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 400, "validation failed on issue 0")
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "create", Args: json.RawMessage(`{"title":"Bad"}`)},
		{Operation: "create", Args: json.RawMessage(`{"title":"Good","issue_type":"task","priority":2}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v (transport error should not bubble for per-op failures)", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	for i, r := range results {
		if r.Success {
			t.Errorf("results[%d].Success = true, want false", i)
		}
		if r.Error == "" {
			t.Errorf("results[%d].Error should be non-empty", i)
		}
	}
}

func TestBatch_Closes_Aggregated(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		// fleet-db returns 204; simulate a wrapped-envelope success with nil data.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "close", Args: json.RawMessage(`{"id":"loom-1","reason":"done"}`)},
		{Operation: "close", Args: json.RawMessage(`{"id":"loom-2"}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues/batch/close") {
		t.Errorf("path = %q, want suffix /issues/batch/close", gotPath)
	}
	ids, _ := gotBody["issue_ids"].([]interface{})
	if len(ids) != 2 {
		t.Errorf("issue_ids len = %d, want 2", len(ids))
	}
	if gotBody["reason"] != "done" {
		t.Errorf("reason = %v, want %q (first non-empty reason should be propagated)", gotBody["reason"], "done")
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("results[%d].Success = false, error = %q", i, r.Error)
		}
	}
}

func TestBatch_Mixed_FanOut(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var calls []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/batch"):
			respondOK(w, map[string]interface{}{
				"issues": []types.Issue{
					{ID: "new-1", Title: "Created", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
				},
				"count": 1,
			})
		case strings.HasSuffix(r.URL.Path, "/issues/batch/close"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(apiResponse{Success: true})
		case strings.Contains(r.URL.Path, "/issues/") && r.Method == "PATCH":
			respondOK(w, map[string]interface{}{"id": "loom-u"})
		case strings.Contains(r.URL.Path, "/issues/") && r.Method == "DELETE":
			respondOK(w, map[string]interface{}{})
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
			respondErr(w, 500, "unexpected path")
		}
	})
	defer ts.Close()

	newTitle := "New Title"
	updateArgs, _ := json.Marshal(map[string]interface{}{
		"id":    "loom-u",
		"title": newTitle,
	})
	ops := []backend.BatchOp{
		{Operation: "create", Args: json.RawMessage(`{"title":"Created","issue_type":"task","priority":2}`)},
		{Operation: "update", Args: updateArgs},
		{Operation: "close", Args: json.RawMessage(`{"id":"loom-c"}`)},
		{Operation: "delete", Args: json.RawMessage(`{"id":"loom-d","force":true}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("len = %d, want 4", len(results))
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("results[%d] not successful (err=%q)", i, r.Error)
		}
	}
	// Verify each endpoint was hit at least once.
	need := map[string]bool{"batch": false, "batch/close": false, "PATCH": false, "DELETE": false}
	for _, c := range calls {
		switch {
		case strings.Contains(c, "/issues/batch/close"):
			need["batch/close"] = true
		case strings.Contains(c, "/issues/batch"):
			need["batch"] = true
		case strings.HasPrefix(c, "PATCH"):
			need["PATCH"] = true
		case strings.HasPrefix(c, "DELETE"):
			need["DELETE"] = true
		}
	}
	for k, v := range need {
		if !v {
			t.Errorf("expected %s call, got none (calls=%v)", k, calls)
		}
	}
}

func TestBatch_UnknownOperation(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be hit for unknown op")
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "teleport", Args: json.RawMessage(`{}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Errorf("results = %+v, want single failed result", results)
	}
	if !strings.Contains(results[0].Error, "unsupported batch operation") {
		t.Errorf("error = %q, want to mention unsupported batch operation", results[0].Error)
	}
}

func TestBatch_Update_MissingID(t *testing.T) {
	fb, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be hit for missing id")
	})
	defer ts.Close()

	ops := []backend.BatchOp{
		{Operation: "update", Args: json.RawMessage(`{"title":"x"}`)},
	}
	results, err := fb.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 1 || results[0].Success {
		t.Fatalf("results = %+v, want single failure", results)
	}
	if !strings.Contains(results[0].Error, "missing id") {
		t.Errorf("error = %q, want to mention missing id", results[0].Error)
	}
}

func TestBatch_Update_NestedParamsShape(t *testing.T) {
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("method = %q, want PATCH", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respondOK(w, map[string]interface{}{})
	})
	defer ts.Close()

	// Nested params shape: {"id": "loom-1", "params": {...UpdateParams...}}
	args, _ := json.Marshal(map[string]interface{}{
		"id":     "loom-1",
		"params": map[string]interface{}{"title": "Nested Title"},
	})
	results, err := fb.Batch(context.Background(), []backend.BatchOp{
		{Operation: "update", Args: args},
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !results[0].Success {
		t.Fatalf("result not successful: %q", results[0].Error)
	}
	if gotBody["title"] != "Nested Title" {
		t.Errorf("title = %v, want Nested Title (PATCH body should carry the nested params)", gotBody["title"])
	}
}
