package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	fleetbackend "github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

// fleetStatsServer answers the four sub-requests IssueBackend.Stats() makes
// against fleet-db. throttlePath (a path suffix) is answered with 429 instead;
// an empty throttlePath means every call succeeds.
func fleetStatsServer(t *testing.T, throttlePath, retryAfter string, throttleStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if throttlePath != "" && strings.HasSuffix(path, throttlePath) {
			w.Header().Set("Content-Type", "application/json")
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(throttleStatus)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(path, "/issues/count"):
			_, _ = w.Write([]byte(`{"total":0,"groups":{}}`))
		case strings.HasSuffix(path, "/issues/blocked"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(path, "/issues/deferred"), strings.HasSuffix(path, "/issues/ready"):
			_, _ = w.Write([]byte(`{"issues":[],"count":0}`))
		default:
			t.Errorf("unexpected fleet request: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}))
}

func newFleetStatsHandler(t *testing.T, baseURL string) http.HandlerFunc {
	t.Helper()
	be, err := fleetbackend.New(fleetbackend.Config{
		BaseURL:     baseURL,
		WorkspaceID: "test-ws",
	})
	if err != nil {
		t.Fatalf("fleet backend: %v", err)
	}
	return HandleStatsWithBackend(nil, func(context.Context) backend.IssueBackend { return be })
}

func TestServeStatsViaBackend_UpstreamRateLimitBecomes429(t *testing.T) {
	tests := []struct {
		name       string
		throttled  string
		retryAfter string
	}{
		{name: "ready sub-call", throttled: "/issues/ready", retryAfter: "12"},
		{name: "count sub-call", throttled: "/issues/count", retryAfter: "12"},
		{name: "no Retry-After", throttled: "/issues/ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := fleetStatsServer(t, tt.throttled, tt.retryAfter, http.StatusTooManyRequests)
			defer server.Close()

			resp := httptest.NewRecorder()
			newFleetStatsHandler(t, server.URL).ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/stats", nil))

			if resp.Code != http.StatusTooManyRequests {
				t.Errorf("status = %d, want 429; body: %s", resp.Code, resp.Body.String())
			}
			if got := resp.Header().Get("Retry-After"); got != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
			}
			var body StatsResponse
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Success {
				t.Error("success = true, want false")
			}
			if !strings.Contains(body.Error, "rate limit exceeded") {
				t.Errorf("error = %q, want it to mention the upstream message", body.Error)
			}
		})
	}
}

// The blanket 500 on /stats is deliberate for every kind except the throttle;
// widening it is a separate change. This pins that decision.
func TestServeStatsViaBackend_Upstream503Stays500(t *testing.T) {
	server := fleetStatsServer(t, "/issues/ready", "", http.StatusServiceUnavailable)
	defer server.Close()

	resp := httptest.NewRecorder()
	newFleetStatsHandler(t, server.URL).ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/stats", nil))

	if resp.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want none", got)
	}
}
