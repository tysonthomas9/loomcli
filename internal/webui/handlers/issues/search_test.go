package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// TestHandleSearchIssues is a table-driven test for the /issues/search handler
// covering the common success and error paths: empty query validation,
// limit parsing and clamping, backend passthrough, service error translation,
// and the JSON envelope shape the FE expects.
func TestHandleSearchIssues(t *testing.T) {
	tests := []struct {
		name             string
		url              string
		searchResult     json.RawMessage
		searchErr        error
		wantStatus       int
		wantQuery        string
		wantLimit        int
		wantEnvSuccess   bool
		wantErrorSubstr  string
		wantDataContains string
	}{
		{
			name:             "success — forwards query and default limit",
			url:              "/api/workspaces/ws1/issues/search?q=checkout",
			searchResult:     json.RawMessage(`[{"id":"A-1","title":"Fix checkout NPE"}]`),
			wantStatus:       http.StatusOK,
			wantQuery:        "checkout",
			wantLimit:        searchDefaultLimit,
			wantEnvSuccess:   true,
			wantDataContains: "Fix checkout NPE",
		},
		{
			name:             "success — empty result set",
			url:              "/api/workspaces/ws1/issues/search?q=nomatches",
			searchResult:     json.RawMessage(`[]`),
			wantStatus:       http.StatusOK,
			wantQuery:        "nomatches",
			wantLimit:        searchDefaultLimit,
			wantEnvSuccess:   true,
			wantDataContains: "[]",
		},
		{
			name:           "success — explicit limit respected",
			url:            "/api/workspaces/ws1/issues/search?q=foo&limit=25",
			searchResult:   json.RawMessage(`[]`),
			wantStatus:     http.StatusOK,
			wantQuery:      "foo",
			wantLimit:      25,
			wantEnvSuccess: true,
		},
		{
			name:           "success — limit clamped to searchMaxLimit",
			url:            "/api/workspaces/ws1/issues/search?q=foo&limit=9999",
			searchResult:   json.RawMessage(`[]`),
			wantStatus:     http.StatusOK,
			wantQuery:      "foo",
			wantLimit:      searchMaxLimit,
			wantEnvSuccess: true,
		},
		{
			name:           "success — invalid limit falls back to default",
			url:            "/api/workspaces/ws1/issues/search?q=foo&limit=abc",
			searchResult:   json.RawMessage(`[]`),
			wantStatus:     http.StatusOK,
			wantQuery:      "foo",
			wantLimit:      searchDefaultLimit,
			wantEnvSuccess: true,
		},
		{
			name:            "missing q — 400",
			url:             "/api/workspaces/ws1/issues/search",
			wantStatus:      http.StatusBadRequest,
			wantErrorSubstr: "missing search query",
		},
		{
			name:            "backend unavailable — 503",
			url:             "/api/workspaces/ws1/issues/search?q=x",
			searchErr:       service.ErrUnavailable("issue backend not available"),
			wantStatus:      http.StatusServiceUnavailable,
			wantQuery:       "x",
			wantLimit:       searchDefaultLimit,
			wantErrorSubstr: "issue backend not available",
		},
		{
			name:            "backend not implemented — 501",
			url:             "/api/workspaces/ws1/issues/search?q=x",
			searchErr:       service.ErrNotImplemented("search not supported on this backend"),
			wantStatus:      http.StatusNotImplemented,
			wantQuery:       "x",
			wantLimit:       searchDefaultLimit,
			wantErrorSubstr: "search not supported",
		},
		{
			name:            "service validation error — 400",
			url:             "/api/workspaces/ws1/issues/search?q=x",
			searchErr:       service.ErrValidation("limit must be non-negative"),
			wantStatus:      http.StatusBadRequest,
			wantQuery:       "x",
			wantLimit:       searchDefaultLimit,
			wantErrorSubstr: "limit must be non-negative",
		},
		{
			name:            "backend internal error — 500",
			url:             "/api/workspaces/ws1/issues/search?q=x",
			searchErr:       service.ErrInternal("something broke", nil),
			wantStatus:      http.StatusInternalServerError,
			wantQuery:       "x",
			wantLimit:       searchDefaultLimit,
			wantErrorSubstr: "something broke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery string
			var gotLimit int

			svc := &mockIssueService{
				searchIssuesFunc: func(_ context.Context, params service.SearchIssuesParams) (json.RawMessage, error) {
					gotQuery = params.Query
					gotLimit = params.Limit
					if tt.searchErr != nil {
						return nil, tt.searchErr
					}
					return tt.searchResult, nil
				},
			}

			h := handleSearchIssues(svc)

			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			req.SetPathValue("ws", "ws1")
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if tt.wantQuery != "" && gotQuery != tt.wantQuery {
				t.Errorf("query forwarded = %q, want %q", gotQuery, tt.wantQuery)
			}
			if tt.wantLimit != 0 && gotLimit != tt.wantLimit {
				t.Errorf("limit forwarded = %d, want %d", gotLimit, tt.wantLimit)
			}

			body := rec.Body.String()
			if tt.wantEnvSuccess {
				var envelope struct {
					Success bool            `json:"success"`
					Data    json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal([]byte(body), &envelope); err != nil {
					t.Fatalf("decode success envelope: %v (body=%s)", err, body)
				}
				if !envelope.Success {
					t.Errorf("envelope.success = false, want true (body=%s)", body)
				}
				if tt.wantDataContains != "" && !contains(string(envelope.Data), tt.wantDataContains) {
					t.Errorf("envelope.data = %s, want to contain %q", envelope.Data, tt.wantDataContains)
				}
			}

			if tt.wantErrorSubstr != "" && !contains(body, tt.wantErrorSubstr) {
				t.Errorf("body = %s, want to contain error substring %q", body, tt.wantErrorSubstr)
			}
		})
	}
}

// TestHandleSearchIssues_EnvelopeShape asserts the concrete wire shape the
// FleetDB UI regression test (test/fleetdb/ui/13-search.spec.ts) consumes:
//
//	{ "success": true, "data": [ { "id": ..., "title": ... }, ... ] }
func TestHandleSearchIssues_EnvelopeShape(t *testing.T) {
	svc := &mockIssueService{
		searchIssuesFunc: func(_ context.Context, _ service.SearchIssuesParams) (json.RawMessage, error) {
			return json.RawMessage(`[{"id":"A-1","title":"Fix checkout NPE"},{"id":"B-2","title":"Checkout redesign"}]`), nil
		},
	}

	h := handleSearchIssues(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws1/issues/search?q=checkout", nil)
	req.SetPathValue("ws", "ws1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body=%s", rec.Code, rec.Body.String())
	}

	var envelope struct {
		Success bool `json:"success"`
		Data    []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope.Success {
		t.Errorf("success = false, want true")
	}
	if len(envelope.Data) != 2 {
		t.Fatalf("data len = %d, want 2", len(envelope.Data))
	}
	if envelope.Data[0].Title != "Fix checkout NPE" {
		t.Errorf("data[0].title = %q, want %q", envelope.Data[0].Title, "Fix checkout NPE")
	}
}

// contains is a small helper to avoid importing the strings package only
// for substring assertions in tests.
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
