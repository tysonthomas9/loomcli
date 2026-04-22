package metricscmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// TestScopedUsageStores_ReadsFromWorkspaceRoot guards the PR-46 P1 fix: the
// scoped usage endpoint must read from <wsPath>/usage.jsonl — the path
// automode writes through usage.NewStore(cli.GetBeadsDir()). A prior
// revision wrote into <wsPath>/.loom/usage.jsonl, which silently returned
// empty for every workspace.
func TestScopedUsageStores_ReadsFromWorkspaceRoot(t *testing.T) {
	wsPath := t.TempDir()

	// Seed a record via the same writer automode uses.
	writer, err := usage.NewStore(wsPath)
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	now := time.Now()
	seeded := usage.SessionUsage{
		AgentName:        "nova",
		Backend:          "claude",
		InputTokens:      4200,
		OutputTokens:     900,
		EstimatedCostUSD: 0.42,
		StartedAt:        now.Add(-5 * time.Minute),
		EndedAt:          now,
	}
	if err := writer.Append(seeded); err != nil {
		t.Fatalf("seed append: %v", err)
	}

	// Guards a future rename on either side of the writer/reader contract.
	expectedPath := filepath.Join(wsPath, "usage.jsonl")
	if _, statErr := os.Stat(expectedPath); statErr != nil {
		t.Fatalf("expected seeded file at %s: %v", expectedPath, statErr)
	}

	stores := newScopedUsageStores()
	pathFn := func(wsID string) string {
		if wsID == "ws-alpha" {
			return wsPath
		}
		return ""
	}
	handler := stores.handle(pathFn)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/monitor/usage", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-alpha/monitor/usage", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}

	var resp struct {
		SessionCount int                  `json:"session_count"`
		Sessions     []usage.SessionUsage `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rr.Body.String())
	}

	if resp.SessionCount != 1 {
		t.Fatalf("SessionCount = %d, want 1 — scoped reader likely looking at wrong path", resp.SessionCount)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].AgentName != "nova" {
		t.Fatalf("sessions = %+v, want single record for agent \"nova\"", resp.Sessions)
	}
}

// TestScopedUsageStores_CachesResponseWithinTTL confirms responses are
// memoized per (wsID, query) inside scopedCollectorTTL. A second request
// must not reflect file-level changes made after the first response was
// cached.
func TestScopedUsageStores_CachesResponseWithinTTL(t *testing.T) {
	wsPath := t.TempDir()
	writer, err := usage.NewStore(wsPath)
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	now := time.Now()
	if err := writer.Append(usage.SessionUsage{
		AgentName: "nova",
		Backend:   "claude",
		StartedAt: now.Add(-1 * time.Minute),
		EndedAt:   now,
	}); err != nil {
		t.Fatalf("seed append: %v", err)
	}

	stores := newScopedUsageStores()
	pathFn := func(string) string { return wsPath }
	handler := stores.handle(pathFn)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/monitor/usage", handler)

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-1/monitor/usage", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
		}
		var resp struct {
			SessionCount int `json:"session_count"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.SessionCount
	}

	if got := do(); got != 1 {
		t.Fatalf("first request: SessionCount = %d, want 1", got)
	}

	// Append another record; cached response should still report 1.
	if err := writer.Append(usage.SessionUsage{
		AgentName: "falcon",
		Backend:   "codex",
		StartedAt: now,
		EndedAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	if got := do(); got != 1 {
		t.Fatalf("second request (expected cached): SessionCount = %d, want 1", got)
	}
}

// TestScopedUsageStores_EmptyPathReturns503 confirms the wsID-missing branch
// still signals "not configured" rather than falling back to the launch dir.
func TestScopedUsageStores_EmptyPathReturns503(t *testing.T) {
	stores := newScopedUsageStores()
	handler := stores.handle(func(string) string { return "" })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/monitor/usage", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/unknown/monitor/usage", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}
