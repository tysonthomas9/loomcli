package api

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
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// Compile-time interface assertion.
var _ backend.IssueBackend = (*APIBackend)(nil)

// newTestServer creates a mock API server and returns an APIBackend pointing at it.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*APIBackend, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	ab, err := New(Config{
		BaseURL:     ts.URL,
		WorkspaceID: "test-ws",
	})
	if err != nil {
		ts.Close()
		t.Fatalf("New: %v", err)
	}
	return ab, ts
}

// respondOK writes a successful JSON envelope.
func respondOK(w http.ResponseWriter, data interface{}) {
	raw, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apiResponse{Success: true, Data: raw})
}

// respondEnvelopeNullData writes success=true with explicit null data.
func respondEnvelopeNullData(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true,"data":null}`))
}

// respondErr writes an error JSON envelope with the given status code.
func respondErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(apiResponse{Success: false, Error: msg})
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

func TestNew_InvalidURL(t *testing.T) {
	_, err := New(Config{BaseURL: "not-a-url", WorkspaceID: "ws"})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error should mention invalid: %v", err)
	}
}

func TestNew_TrailingSlashStripped(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://example.com/", WorkspaceID: "ws1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ab.baseURL != "http://example.com" {
		t.Errorf("baseURL = %q, want http://example.com", ab.baseURL)
	}
}

func TestNew_CustomHTTPClient(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if ab.client != client {
		t.Errorf("custom HTTPClient not used")
	}
}

func TestNew_DefaultHTTPClient(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if ab.client == nil {
		t.Errorf("default HTTPClient should be set")
	}
}

func TestBackendName(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if ab.BackendName() != "api" {
		t.Errorf("BackendName = %q, want api", ab.BackendName())
	}
}

func TestWorkspaceURLConstruction(t *testing.T) {
	var gotPath string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		respondOK(w, gen.IssueResponse{
			Id: "loom-1", Title: "t", Status: "open", IssueType: "task",
			CreatedAt: time.Now(), UpdatedAt: time.Now(), Labels: []string{},
		})
	})
	defer ts.Close()

	_, _ = ab.Get(context.Background(), "loom-1")
	if !strings.HasPrefix(gotPath, "/api/workspaces/test-ws/") {
		t.Errorf("path = %q, want prefix /api/workspaces/test-ws/", gotPath)
	}
	if !strings.HasSuffix(gotPath, "/issues/loom-1") {
		t.Errorf("path = %q, want suffix /issues/loom-1", gotPath)
	}
}

// --- Get tests ---

func TestGet_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		respondOK(w, gen.IssueResponse{
			Id:        "loom-1",
			Title:     "Test Issue",
			Status:    gen.IssueResponseStatus("open"),
			IssueType: gen.IssueResponseIssueType("task"),
			Priority:  2,
			CreatedAt: now,
			UpdatedAt: now,
			Labels:    []string{"urgent"},
		})
	})
	defer ts.Close()

	result, err := ab.Get(context.Background(), "loom-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if result.ID != "loom-1" || result.Title != "Test Issue" {
		t.Errorf("result = %+v", result)
	}
	if result.Status != "open" {
		t.Errorf("Status = %q", result.Status)
	}
}

func TestGet_NotFound(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 404, "issue not found")
	})
	defer ts.Close()

	_, err := ab.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound, got %v", err)
	}
}

func TestGet_NullData(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondEnvelopeNullData(w)
	})
	defer ts.Close()

	_, err := ab.Get(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error for null data")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound for null data, got %v", err)
	}
}

func TestGet_InvalidJSON(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json at all`))
	})
	defer ts.Close()

	_, err := ab.Get(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error for non-JSON")
	}
}

// --- List tests ---

func TestList_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		status := gen.IssueStatus("open")
		issueType := gen.IssueIssueType("task")
		respondOK(w, []gen.Issue{
			{Id: "a", Title: "A", Status: &status, IssueType: &issueType, Priority: 1, CreatedAt: now, UpdatedAt: now},
			{Id: "b", Title: "B", Status: &status, IssueType: &issueType, Priority: 2, CreatedAt: now, UpdatedAt: now},
		})
	})
	defer ts.Close()

	result, err := ab.List(context.Background(), backend.ListOpts{Status: "open", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].ID != "a" || result[1].ID != "b" {
		t.Errorf("IDs: %q %q", result[0].ID, result[1].ID)
	}
}

func TestList_QueryParamsPropagated(t *testing.T) {
	var gotQuery string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, []gen.Issue{})
	})
	defer ts.Close()

	_, _ = ab.List(context.Background(), backend.ListOpts{
		Status:   "open",
		Assignee: "alice",
		Limit:    5,
	})
	for _, want := range []string{"status=open", "assignee=alice", "limit=5"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestList_EmptyResult(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, []gen.Issue{})
	})
	defer ts.Close()
	result, err := ab.List(context.Background(), backend.ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestList_NullDataEmpty(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondEnvelopeNullData(w)
	})
	defer ts.Close()
	result, err := ab.List(context.Background(), backend.ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if result == nil {
		t.Errorf("expected non-nil empty slice")
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestList_ServerError(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 500, "boom")
	})
	defer ts.Close()
	_, err := ab.List(context.Background(), backend.ListOpts{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("expected KindInternal, got %v", err)
	}
}

// --- Ready / Blocked ---

func TestReady_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotPath string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		respondOK(w, []gen.Issue{
			{Id: "r-1", Title: "Ready", Priority: 1, CreatedAt: now, UpdatedAt: now},
		})
	})
	defer ts.Close()

	result, err := ab.Ready(context.Background(), backend.ReadyOpts{Limit: 5})
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/ready") {
		t.Errorf("path = %q", gotPath)
	}
	if len(result) != 1 || result[0].ID != "r-1" {
		t.Errorf("result: %+v", result)
	}
}

func TestBlocked_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	seen := map[string]bool{}
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		switch {
		case strings.HasSuffix(r.URL.Path, "/blocked"):
			respondOK(w, []gen.BlockedIssue{
				{Id: "b-1", Title: "Blocked", Priority: 2, CreatedAt: now, UpdatedAt: now, BlockedBy: []string{"b-0"}},
			})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	defer ts.Close()

	result, err := ab.Blocked(context.Background(), workitems.AvailabilityQuery{Limit: 5})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if !seen["/api/workspaces/test-ws/blocked"] {
		t.Errorf("missing /blocked request; seen=%v", seen)
	}
	if len(seen) != 1 {
		t.Errorf("requests = %v, want only canonical /blocked request", seen)
	}
	if len(result) != 1 || result[0].ID != "b-1" {
		t.Errorf("result: %+v", result)
	}
}

func TestBlockedIncludesExplicitBlockedStatusIssues(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "epic-1"
	seen := map[string]bool{}
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path+"?"+r.URL.RawQuery] = true
		switch {
		case strings.HasSuffix(r.URL.Path, "/blocked"):
			respondOK(w, []gen.BlockedIssue{
				{Id: "dep-blocked", Title: "Dependency blocked", Priority: 2, CreatedAt: now, UpdatedAt: now, BlockedBy: []string{"dep-1"}},
				{Id: "explicit-blocked", Title: "Explicitly blocked", Priority: 2, CreatedAt: now, UpdatedAt: now},
			})
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})
	defer ts.Close()

	result, err := ab.Blocked(context.Background(), workitems.AvailabilityQuery{ParentID: parent, Limit: 5})
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d, want dependency-blocked plus explicit status blocked: %+v", len(result), result)
	}
	gotIDs := map[string]bool{}
	for _, issue := range result {
		gotIDs[issue.ID] = true
	}
	for _, want := range []string{"dep-blocked", "explicit-blocked"} {
		if !gotIDs[want] {
			t.Fatalf("Blocked result missing %q: %+v", want, result)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("requests = %#v, want only canonical /blocked", seen)
	}
}

func TestBlockedRejectsUnsupportedOwnerFiltersBeforeRequest(t *testing.T) {
	ab, ts := newTestServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsupported filter must not issue a request")
	})
	defer ts.Close()

	_, err := ab.Blocked(context.Background(), workitems.AvailabilityQuery{LabelsAny: []string{"urgent"}})
	if !errors.Is(err, backend.ErrFilterNotSupported) {
		t.Fatalf("Blocked error = %v, want unsupported filter", err)
	}
}

// --- Stats ---

func TestStats_HappyPath(t *testing.T) {
	// Stats returns raw Statistics, NOT the envelope.
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces/test-ws/stats" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gen.Statistics{
			TotalIssues:          100,
			OpenIssues:           40,
			InProgressIssues:     20,
			ClosedIssues:         30,
			BlockedIssues:        5,
			DeferredIssues:       3,
			ReadyIssues:          15,
			TombstoneIssues:      2,
			PinnedIssues:         1,
			AverageLeadTimeHours: 2.5,
		})
	})
	defer ts.Close()

	result, err := ab.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalIssues != 100 {
		t.Errorf("TotalIssues = %d", result.TotalIssues)
	}
	if result.OpenIssues != 40 {
		t.Errorf("OpenIssues = %d", result.OpenIssues)
	}
	if result.ReadyIssues != 15 {
		t.Errorf("ReadyIssues = %d", result.ReadyIssues)
	}
	if result.AverageLeadTime != 2.5 {
		t.Errorf("AverageLeadTime = %f", result.AverageLeadTime)
	}
}

func TestStats_ServerError(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	})
	defer ts.Close()
	_, err := ab.Stats(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("expected KindInternal, got %v", err)
	}
}

// --- Search ---

func TestSearch_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotPath string
	var gotQuery string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		status := gen.IssueStatus("open")
		issueType := gen.IssueIssueType("task")
		respondOK(w, []gen.Issue{
			{Id: "s1", Title: "Auth bug in login", Status: &status, IssueType: &issueType, Priority: 2, CreatedAt: now, UpdatedAt: now},
			{Id: "s2", Title: "Auth bug: refresh token", Status: &status, IssueType: &issueType, Priority: 1, CreatedAt: now, UpdatedAt: now},
		})
	})
	defer ts.Close()

	result, err := ab.Search(context.Background(), workitems.SearchQuery{Query: "auth bug", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues") {
		t.Errorf("path = %q, want suffix /issues", gotPath)
	}
	if !strings.Contains(gotQuery, "q=auth+bug") {
		t.Errorf("query = %q, want q=auth+bug", gotQuery)
	}
	if !strings.Contains(gotQuery, "limit=10") {
		t.Errorf("query = %q, want limit=10", gotQuery)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].ID != "s1" || result[1].ID != "s2" {
		t.Errorf("IDs: %q %q", result[0].ID, result[1].ID)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ab.Search(context.Background(), workitems.SearchQuery{Limit: 10})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestSearch_NegativeLimit(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ab.Search(context.Background(), workitems.SearchQuery{Query: "q", Limit: -1})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestSearch_ServerError(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 500, "search index unavailable")
	})
	defer ts.Close()
	_, err := ab.Search(context.Background(), workitems.SearchQuery{Query: "q"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("expected KindInternal, got %v", err)
	}
}

func TestSearch_UnmarshalError(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":"not-an-array"}`))
	})
	defer ts.Close()
	_, err := ab.Search(context.Background(), workitems.SearchQuery{Query: "q"})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("expected KindInternal, got %v", err)
	}
}

func TestClaimIssue_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotMethod, gotPath string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if r.ContentLength > 0 {
			t.Errorf("zero TTL should omit request body; content length = %d", r.ContentLength)
		}
		respondOK(w, gen.IssueResponse{
			Id:        "abc",
			Title:     "claim test",
			Status:    gen.IssueResponseStatus("in_progress"),
			IssueType: gen.IssueResponseIssueType("task"),
			Priority:  2,
			CreatedAt: now,
			UpdatedAt: now,
			Labels:    []string{},
		})
	})
	defer ts.Close()

	if err := ab.ClaimIssue(context.Background(), "abc", 0); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/abc/claim") {
		t.Errorf("path = %q, want suffix /issues/abc/claim", gotPath)
	}
}

func TestClaimIssue_ForwardsLockTTL(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LockTTL int `json:"lock_ttl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.LockTTL != 300 {
			t.Errorf("lock_ttl = %d, want 300", body.LockTTL)
		}
		respondOK(w, gen.IssueResponse{Id: "abc"})
	})
	defer ts.Close()

	if err := ab.ClaimIssue(context.Background(), "abc", 5*time.Minute); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}

func TestClaimIssue_RoundsPositiveSubsecondTTL(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LockTTL int `json:"lock_ttl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.LockTTL != 1 {
			t.Errorf("lock_ttl = %d, want 1", body.LockTTL)
		}
		respondOK(w, gen.IssueResponse{Id: "abc"})
	})
	defer ts.Close()

	if err := ab.ClaimIssue(context.Background(), "abc", time.Millisecond); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
}

func TestClaimIssue_AlreadyClaimed(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, http.StatusConflict, "already claimed by other-agent")
	})
	defer ts.Close()

	err := ab.ClaimIssue(context.Background(), "abc", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Errorf("expected KindConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "already claimed") {
		t.Errorf("error should preserve daemon message, got %v", err)
	}
}

func TestClaimIssue_NotFound(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, http.StatusNotFound, "issue not found: abc")
	})
	defer ts.Close()

	err := ab.ClaimIssue(context.Background(), "abc", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound, got %v", err)
	}
}

func TestClaimIssue_EmptyID(t *testing.T) {
	ab, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be contacted when ID is empty")
	})
	defer ts.Close()

	err := ab.ClaimIssue(context.Background(), "", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestClaimIssue_NegativeTTL(t *testing.T) {
	ab, ts := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be contacted for negative TTL")
	})
	defer ts.Close()

	err := ab.ClaimIssue(context.Background(), "abc", -1*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

// --- Create ---

func TestCreate_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotMethod, gotPath string
	var gotBody map[string]json.RawMessage
	sourceRepo := "loomcli"
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode create request: %v", err)
		}
		respondOK(w, gen.IssueResponse{
			Id:         "new-1",
			Title:      "New",
			Status:     gen.IssueResponseStatus("open"),
			IssueType:  gen.IssueResponseIssueType("task"),
			Priority:   2,
			CreatedAt:  now,
			UpdatedAt:  now,
			Labels:     []string{},
			SourceRepo: &sourceRepo,
		})
	})
	defer ts.Close()

	result, err := ab.Create(context.Background(), backend.CreateParams{
		Title:      "New",
		IssueType:  "task",
		Priority:   2,
		SourceRepo: sourceRepo,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues") {
		t.Errorf("path = %q", gotPath)
	}
	var requestSourceRepo string
	if err := json.Unmarshal(gotBody["source_repo"], &requestSourceRepo); err != nil {
		t.Errorf("decode request source_repo: %v (body=%v)", err, gotBody)
	} else if requestSourceRepo != sourceRepo {
		t.Errorf("request source_repo = %q, want %q", requestSourceRepo, sourceRepo)
	}
	if result.ID != "new-1" {
		t.Errorf("ID = %q", result.ID)
	}
	if result.SourceRepo != sourceRepo {
		t.Errorf("result SourceRepo = %q, want %q", result.SourceRepo, sourceRepo)
	}
}

func TestCreate_ValidationError(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 400, "title is required")
	})
	defer ts.Close()

	_, err := ab.Create(context.Background(), backend.CreateParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

// --- Update ---

func TestUpdate_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		respondOK(w, gen.IssueResponse{
			Id: "loom-1", Title: "t", Status: "open", IssueType: "task",
			CreatedAt: time.Now(), UpdatedAt: time.Now(), Labels: []string{},
		})
	})
	defer ts.Close()

	newTitle := "Updated"
	err := ab.Update(context.Background(), "loom-1", backend.UpdateParams{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/loom-1") {
		t.Errorf("path = %q", gotPath)
	}
}

func TestUpdate_ClaimRejected(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	err = ab.Update(context.Background(), "loom-1", backend.UpdateParams{Claim: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

// --- Close ---

func TestClose_HappyPath(t *testing.T) {
	var gotPath string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		respondOK(w, map[string]interface{}{"ok": true})
	})
	defer ts.Close()

	result, err := ab.Close(context.Background(), "loom-1", backend.CloseParams{Reason: "done"})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/issues/loom-1/close") {
		t.Errorf("path = %q", gotPath)
	}
	if result.Closed == nil || result.Closed.ID != "loom-1" {
		t.Errorf("Closed = %+v", result.Closed)
	}
}

// --- Reopen ---

func TestReopen_HappyPath(t *testing.T) {
	var patchCount int
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patchCount++
		}
		respondOK(w, gen.IssueResponse{
			Id: "loom-1", Title: "t", Status: "open", IssueType: "task",
			CreatedAt: time.Now(), UpdatedAt: time.Now(), Labels: []string{},
		})
	})
	defer ts.Close()

	err := ab.Reopen(context.Background(), "loom-1", backend.ReopenParams{})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if patchCount != 1 {
		t.Errorf("patchCount = %d, want 1", patchCount)
	}
}

func TestReopen_WithReason(t *testing.T) {
	var commentCount, patchCount int
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/comments") {
			commentCount++
		}
		if r.Method == http.MethodPatch {
			patchCount++
		}
		// Return envelope with data so Reopen's comment call is "successful"
		respondOK(w, gen.Comment{Id: 1, IssueId: "loom-1", Author: "a", Text: "t", CreatedAt: time.Now()})
	})
	defer ts.Close()

	err := ab.Reopen(context.Background(), "loom-1", backend.ReopenParams{Reason: "need more work"})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if patchCount != 1 {
		t.Errorf("patchCount = %d", patchCount)
	}
	if commentCount != 1 {
		t.Errorf("commentCount = %d, want 1", commentCount)
	}
}

func TestReopen_EmptyID(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	err = ab.Reopen(context.Background(), "", backend.ReopenParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

// --- Delete ---

func TestDelete_HappyPath(t *testing.T) {
	deleted := map[string]bool{}
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		parts := strings.Split(r.URL.Path, "/")
		deleted[parts[len(parts)-1]] = true
		respondOK(w, map[string]interface{}{"ok": true})
	})
	defer ts.Close()

	err := ab.Delete(context.Background(), backend.DeleteParams{IDs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted["a"] || !deleted["b"] {
		t.Errorf("deleted = %v", deleted)
	}
}

func TestDelete_EmptyIDs(t *testing.T) {
	ab, err := New(Config{BaseURL: "http://x", WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	err = ab.Delete(context.Background(), backend.DeleteParams{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("expected KindValidation, got %v", err)
	}
}

func TestDelete_ForceIgnores404(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 404, "issue not found")
	})
	defer ts.Close()

	err := ab.Delete(context.Background(), backend.DeleteParams{IDs: []string{"ghost"}, Force: true})
	if err != nil {
		t.Errorf("Force should ignore 404, got %v", err)
	}
}

func TestDelete_NoForceSurfaces404(t *testing.T) {
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondErr(w, 404, "issue not found")
	})
	defer ts.Close()

	err := ab.Delete(context.Background(), backend.DeleteParams{IDs: []string{"ghost"}, Force: false})
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("expected KindNotFound, got %v", err)
	}
}

// --- Dependencies ---

func TestAddDependency_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	var body gen.AddDependencyRequest
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		respondOK(w, map[string]interface{}{"ok": true})
	})
	defer ts.Close()

	err := ab.AddDependency(context.Background(), backend.DepAddParams{
		FromID: "a", ToID: "b", DepType: "blocks",
	})
	if err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/a/dependencies") {
		t.Errorf("path = %q", gotPath)
	}
	if body.DependsOnId != "b" {
		t.Errorf("DependsOnId = %q", body.DependsOnId)
	}
	if body.DepType == nil || *body.DepType != "blocks" {
		t.Errorf("DepType = %v", body.DepType)
	}
}

func TestRemoveDependency_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		respondOK(w, map[string]interface{}{"ok": true})
	})
	defer ts.Close()

	err := ab.RemoveDependency(context.Background(), backend.DepRemoveParams{FromID: "a", ToID: "b"})
	if err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/issues/a/dependencies/b") {
		t.Errorf("path = %q", gotPath)
	}
}

// --- Comments ---

func TestListComments_ViaGet(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ab, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, gen.IssueResponse{
			Id: "loom-1", Title: "t", Status: "open", IssueType: "task",
			CreatedAt: now, UpdatedAt: now, Labels: []string{},
			Comments: []gen.CommentResponse{
				{Id: 1, Author: "alice", Text: "first", CreatedAt: now},
				{Id: 2, Author: "bob", Text: "second", CreatedAt: now},
			},
		})
	})
	defer ts.Close()

	result, err := ab.ListComments(context.Background(), "loom-1")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d", len(result))
	}
	if result[0].Text != "first" {
		t.Errorf("Text = %q", result[0].Text)
	}
}

func TestAddComment_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		respondOK(w, gen.Comment{
			Id: 42, IssueId: "", Author: "alice", Text: "hello", CreatedAt: now,
		})
	})
	defer ts.Close()

	result, err := ab.AddComment(context.Background(), backend.CommentAddParams{
		IssueID: "loom-1", Text: "hello",
	})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if result.ID != 42 {
		t.Errorf("ID = %d", result.ID)
	}
	// Server returned empty IssueId; should be backfilled from params.
	if result.IssueID != "loom-1" {
		t.Errorf("IssueID = %q, want loom-1", result.IssueID)
	}
}

// --- Events ---

func TestListEvents_HappyPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var gotPath string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		respondOK(w, []gen.IssueEvent{
			{Id: 1, IssueId: "loom-1", EventType: "create", Actor: "alice", CreatedAt: now},
			{Id: 2, IssueId: "loom-1", EventType: "update", Actor: "bob", CreatedAt: now},
		})
	})
	defer ts.Close()

	result, err := ab.ListEvents(context.Background(), "loom-1", 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d", len(result))
	}
	if result[0].ID != "1" {
		t.Errorf("ID = %q", result[0].ID)
	}
	if strings.Contains(gotPath, "limit=") {
		t.Errorf("limit should not appear when 0: %q", gotPath)
	}
}

func TestListEvents_WithLimit(t *testing.T) {
	var gotQuery string
	ab, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respondOK(w, []gen.IssueEvent{})
	})
	defer ts.Close()

	_, err := ab.ListEvents(context.Background(), "loom-1", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if !strings.Contains(gotQuery, "limit=50") {
		t.Errorf("query = %q, want limit=50", gotQuery)
	}
}

// --- Transport error ---

func TestTransportError_ConnectionRefused(t *testing.T) {
	// Create a server, then close it immediately to get a closed port.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	closedURL := ts.URL
	ts.Close()

	ab, err := New(Config{BaseURL: closedURL, WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = ab.Get(ctx, "loom-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Errorf("expected KindUnavailable, got %v", err)
	}
}
