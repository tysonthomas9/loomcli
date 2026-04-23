package metricscmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// TestScopedUsageStores_ReadsCentralStore confirms the scoped endpoint reads
// from ~/.loom/usage/<wsID>/usage.jsonl — the path the automode writer
// resolves through usage.NewStoreForWorkspace.
func TestScopedUsageStores_ReadsCentralStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const wsID = "ws-alpha"

	writer, err := usage.NewStoreForWorkspace(wsID)
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	now := time.Now()
	if err := writer.Append(usage.SessionUsage{
		AgentName:        "nova",
		Backend:          "claude",
		InputTokens:      4200,
		OutputTokens:     900,
		EstimatedCostUSD: 0.42,
		StartedAt:        now.Add(-5 * time.Minute),
		EndedAt:          now,
	}); err != nil {
		t.Fatalf("seed append: %v", err)
	}

	stores := newScopedUsageStores()
	handler := stores.handle()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/monitor/usage", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/monitor/usage", nil)
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
	t.Setenv("HOME", t.TempDir())
	const wsID = "ws-1"

	writer, err := usage.NewStoreForWorkspace(wsID)
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
	handler := stores.handle()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/monitor/usage", handler)

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+wsID+"/monitor/usage", nil)
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

// TestScopedUsageStores_EmptyWorkspaceIDReturnsNil confirms storeFor refuses
// to build a store when wsID is empty — callers surface this as 503 rather
// than falling back to a shared bucket.
func TestScopedUsageStores_EmptyWorkspaceIDReturnsNil(t *testing.T) {
	stores := newScopedUsageStores()
	if got := stores.storeFor(""); got != nil {
		t.Fatalf("storeFor(\"\") = %v, want nil", got)
	}
}
