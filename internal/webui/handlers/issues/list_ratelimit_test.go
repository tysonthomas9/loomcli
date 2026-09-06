package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	fleetbackend "github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// newFleetListHandler wires HandleListIssues onto a fleet backend pointed at
// baseURL with a nil pool, which forces the listIssuesViaBackend arm.
func newFleetListHandler(t *testing.T, baseURL string) http.HandlerFunc {
	t.Helper()
	be, err := fleetbackend.New(fleetbackend.Config{
		BaseURL:     baseURL,
		WorkspaceID: "test-ws",
	})
	if err != nil {
		t.Fatalf("fleet backend: %v", err)
	}
	svc := service.NewIssueServiceWithBackend(nil, nil, nil, func(context.Context) backend.IssueBackend {
		return be
	})
	return HandleListIssues(svc)
}

// writeFleetThrottle emits fleet-db's throttle response: 429 plus the
// Retry-After it wants the caller to honour.
func writeFleetThrottle(w http.ResponseWriter, retryAfter string) {
	w.Header().Set("Content-Type", "application/json")
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
}

func decodeErrorBody(t *testing.T, body string) map[string]string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return out
}

func TestHandleListIssues_UpstreamRateLimitBecomes429(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		query      string
	}{
		{name: "with Retry-After", retryAfter: "12"},
		{name: "without Retry-After"},
		{name: "include_blocked fan-out", retryAfter: "12", query: "?include_blocked=true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				// In the fan-out case the plain list succeeds and only the
				// /issues/blocked sub-request is throttled.
				if tt.query != "" && !strings.HasSuffix(path, "/issues/blocked") {
					writeFleetJSON(w, map[string]any{"issues": []any{}})
					return
				}
				writeFleetThrottle(w, tt.retryAfter)
			}))
			defer server.Close()

			handler := newFleetListHandler(t, server.URL)
			req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/issues"+tt.query, nil)
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			if resp.Code != http.StatusTooManyRequests {
				t.Errorf("status = %d, want 429; body: %s", resp.Code, resp.Body.String())
			}
			if got := resp.Header().Get("Retry-After"); got != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
			}
			if got := decodeErrorBody(t, resp.Body.String())["kind"]; got != "rate_limited" {
				t.Errorf("kind = %q, want rate_limited", got)
			}
		})
	}
}

func TestHandleListIssues_Upstream503StaysUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"backend down"}`))
	}))
	defer server.Close()

	handler := newFleetListHandler(t, server.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/issues", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want none", got)
	}
	if got := decodeErrorBody(t, resp.Body.String())["kind"]; got != "unavailable" {
		t.Errorf("kind = %q, want unavailable", got)
	}
}
