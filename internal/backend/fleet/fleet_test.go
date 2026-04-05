package fleet

import (
	"context"
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
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/issues/issue-1") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		respondOK(w, details)
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
		if r.URL.Path != "/api/workspaces/test-ws/issues/count" {
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
	// review and hooked have no BdStats field — silently ignored.
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
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
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
		IssueType: "task",
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.ID != "new-1" {
		t.Errorf("ID = %q, want %q", result.ID, "new-1")
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

// --- ClaimIssue tests ---

func TestClaimIssue_HappyPath(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/fleet/claim") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if body["issue_id"] != "test-1" {
			t.Errorf("issue_id = %q, want %q", body["issue_id"], "test-1")
		}
		w.Header().Set("Content-Type", "application/json")
		// Fleet claim uses "payload" field but we parse with generic apiResponse
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"success": true,
			"payload": map[string]interface{}{"issue": map[string]string{"id": "test-1"}},
		})
	})
	defer ts.Close()

	err := fb.ClaimIssue(context.Background(), "test-1", 0)
	if err != nil {
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

// --- Close tests ---

func TestClose_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		respondOK(w, closeResultJSON{
			Closed: &types.Issue{ID: "test-1", Title: "Done", Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now, ClosedAt: &now},
			Unblocked: []*types.Issue{
				{ID: "freed-1", Title: "Free", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
			},
		})
	})
	defer ts.Close()

	result, err := fb.Close(context.Background(), "test-1", backend.CloseParams{Reason: "done"})
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
		if !strings.Contains(r.URL.Path, "/dependencies/") {
			t.Errorf("path missing /dependencies/: %s", r.URL.Path)
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

	err := fb.AddLabel(context.Background(), "test-1", "urgent")
	if err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	labels, ok := gotBody["add_labels"].([]interface{})
	if !ok || len(labels) != 1 || labels[0] != "urgent" {
		t.Errorf("add_labels = %v, want [urgent]", gotBody["add_labels"])
	}
}

func TestRemoveLabel(t *testing.T) {
	var gotBody map[string]interface{}
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
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
	labels, ok := gotBody["remove_labels"].([]interface{})
	if !ok || len(labels) != 1 || labels[0] != "urgent" {
		t.Errorf("remove_labels = %v, want [urgent]", gotBody["remove_labels"])
	}
}

// --- Comment tests ---

func TestAddComment_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		respondOK(w, types.Comment{
			ID:        1,
			IssueID:   "test-1",
			Author:    "user",
			Text:      "hello",
			CreatedAt: now,
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
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit = %q, want %q", r.URL.Query().Get("limit"), "10")
		}
		respondOK(w, []*types.Event{
			{ID: 1, IssueID: "test-1", EventType: types.EventCreated, Actor: "user", CreatedAt: now},
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

// --- Not implemented methods ---

func TestNotImplementedMethods(t *testing.T) {
	fb, _ := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Count", func() error { _, err := fb.Count(ctx, backend.CountOpts{}); return err }},
		{"Batch", func() error { _, err := fb.Batch(ctx, nil); return err }},
		{"GetMutations", func() error { _, err := fb.GetMutations(ctx, 0); return err }},
		{"WaitForMutations", func() error { _, err := fb.WaitForMutations(ctx, 0, 0); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !backend.IsKind(err, backend.KindNotImplemented) {
				t.Fatalf("expected KindNotImplemented, got %v", err)
			}
		})
	}
}

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
	var patchBody map[string]interface{}
	var commentPosted bool
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			json.NewDecoder(r.Body).Decode(&patchBody) //nolint:errcheck
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(apiResponse{ //nolint:errcheck
				Success: true,
				Data:    json.RawMessage(`{"id":"test-1","status":"updated"}`),
			})
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments") {
			commentPosted = true
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			if body["text"] != "need more work" {
				t.Errorf("comment text = %q, want %q", body["text"], "need more work")
			}
			respondOK(w, map[string]interface{}{"id": 1, "text": body["text"]})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	})
	defer ts.Close()

	err := fb.Reopen(context.Background(), "test-1", backend.ReopenParams{Reason: "need more work"})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if patchBody["status"] != "open" {
		t.Errorf("status = %v, want %q", patchBody["status"], "open")
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
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []*readyIssueWithParent{
			{
				Issue:  &types.Issue{ID: "r-1", Title: "Ready", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
				Parent: &parent,
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
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
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
			{ID: 1, IssueID: "test-1", Author: "user", Text: "c1", CreatedAt: now},
			{ID: 2, IssueID: "test-1", Author: "user2", Text: "c2", CreatedAt: now},
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
