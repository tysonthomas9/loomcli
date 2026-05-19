package usagecmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestBuildResponseAggregatesAndSorts(t *testing.T) {
	records := []usage.SessionUsage{
		{AgentName: "a", Backend: "codex", InputTokens: 10, OutputTokens: 20, CacheReadTokens: 3, CacheWriteTokens: 4, EstimatedCostUSD: 0.10, StartedAt: time.Date(2026, 5, 2, 1, 0, 0, 0, time.UTC)},
		{AgentName: "b", Backend: "claude", InputTokens: 5, OutputTokens: 7, EstimatedCostUSD: 0.50, StartedAt: time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)},
		{AgentName: "a", Backend: "codex", InputTokens: 1, OutputTokens: 2, EstimatedCostUSD: 0.20, StartedAt: time.Date(2026, 5, 2, 2, 0, 0, 0, time.UTC)},
	}

	resp := buildResponse(records)
	if resp.SessionCount != 3 || resp.TotalInputTokens != 16 || resp.TotalOutputTokens != 29 ||
		resp.TotalCacheReadTokens != 3 || resp.TotalCacheWriteTokens != 4 || resp.TotalCost != 0.80 {
		t.Fatalf("totals = %+v", resp)
	}
	if len(resp.ByAgent) != 2 || resp.ByAgent[0].Name != "b" || resp.ByAgent[1].Name != "a" {
		t.Fatalf("agent summaries = %+v", resp.ByAgent)
	}
	if len(resp.ByBackend) != 2 || resp.ByBackend[0].Name != "claude" || resp.ByBackend[1].Name != "codex" {
		t.Fatalf("backend summaries = %+v", resp.ByBackend)
	}
	if len(resp.DailyCosts) != 2 || resp.DailyCosts[0].Date != "2026-05-01" || resp.DailyCosts[1].Sessions != 2 {
		t.Fatalf("daily costs = %+v", resp.DailyCosts)
	}
}

func TestHandleUsageErrors(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	rec := httptest.NewRecorder()
	HandleUsage(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil store status = %d", rec.Code)
	}

	store := InitStore(t.TempDir())
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/usage?since=bad", nil)
	HandleUsage(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad since status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/usage?until=bad", nil)
	HandleUsage(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad until status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleUsageEmptyStore(t *testing.T) {
	store := InitStore(t.TempDir())
	if store == nil {
		t.Fatal("InitStore returned nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/usage?agent=a&backend=codex&epic=E&since=2026-05-01&until=2026-05-02", nil)
	HandleUsage(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SessionCount != 0 || resp.Sessions == nil {
		t.Fatalf("empty response = %+v", resp)
	}
}
