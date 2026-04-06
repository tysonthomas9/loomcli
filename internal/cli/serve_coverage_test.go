//go:build ignore

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestGroupAgentsByWorkspace_Coverage(t *testing.T) {
	agents := []AgentStatus{
		{Name: "alpha", Workspace: "ws1"},
		{Name: "beta", Workspace: "ws1"},
		{Name: "gamma", Workspace: "ws2"},
		{Name: "delta", Workspace: ""},
	}

	groups := groupAgentsByWorkspace(agents)

	if len(groups["ws1"]) != 2 {
		t.Errorf("ws1 count = %d, want 2", len(groups["ws1"]))
	}
	if len(groups["ws2"]) != 1 {
		t.Errorf("ws2 count = %d, want 1", len(groups["ws2"]))
	}
	if len(groups["(legacy)"]) != 1 {
		t.Errorf("(legacy) count = %d, want 1", len(groups["(legacy)"]))
	}
}

func TestGroupAgentsByWorkspace_Empty(t *testing.T) {
	groups := groupAgentsByWorkspace([]AgentStatus{})
	if len(groups) != 0 {
		t.Errorf("expected empty map, got %d entries", len(groups))
	}
}

func TestGroupAgentsByWorkspace_AllSameWorkspace(t *testing.T) {
	agents := []AgentStatus{
		{Name: "a", Workspace: "ws"},
		{Name: "b", Workspace: "ws"},
		{Name: "c", Workspace: "ws"},
	}

	groups := groupAgentsByWorkspace(agents)
	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}
	if len(groups["ws"]) != 3 {
		t.Errorf("ws count = %d, want 3", len(groups["ws"]))
	}
}

func TestWriteJSON_Coverage(t *testing.T) {
	rr := httptest.NewRecorder()

	data := map[string]string{"key": "value"}
	writeJSON(rr, data)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("response body should not be empty")
	}
}

func TestHandleUsage_NoStore(t *testing.T) {
	orig := usageStoreInstance
	usageStoreInstance = nil
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

func TestHandleUsage_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.SessionCount != 0 {
		t.Errorf("expected 0 sessions, got %d", resp.SessionCount)
	}
	if len(resp.Sessions) != 0 {
		t.Errorf("expected empty sessions, got %d", len(resp.Sessions))
	}
}

func TestHandleUsage_WithData(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	rec := usage.SessionUsage{
		AgentName:        "nova",
		Backend:          "claude",
		TaskID:           "kv31p.4",
		InputTokens:      100000,
		OutputTokens:     50000,
		CacheReadTokens:  10000,
		CacheWriteTokens: 5000,
		EstimatedCostUSD: 1.50,
		StartedAt:        now.Add(-10 * time.Minute),
		EndedAt:          now,
		ExitCode:         0,
	}
	if err := store.Append(rec); err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp UsageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", resp.SessionCount)
	}
	if resp.TotalInputTokens != 100000 {
		t.Errorf("TotalInputTokens = %d, want 100000", resp.TotalInputTokens)
	}
	if resp.TotalCost != 1.50 {
		t.Errorf("TotalCost = %f, want 1.50", resp.TotalCost)
	}
	if len(resp.ByAgent) != 1 || resp.ByAgent[0].Name != "nova" {
		t.Errorf("ByAgent unexpected: %+v", resp.ByAgent)
	}
}

func TestHandleUsage_QueryFilters(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	recs := []usage.SessionUsage{
		{AgentName: "nova", Backend: "claude", EstimatedCostUSD: 1.0, StartedAt: now, EndedAt: now},
		{AgentName: "falcon", Backend: "codex", EstimatedCostUSD: 2.0, StartedAt: now, EndedAt: now},
	}
	for _, r := range recs {
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	// Filter by agent
	req := httptest.NewRequest("GET", "/api/usage?agent=nova", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	var resp UsageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if resp.SessionCount != 1 {
		t.Errorf("filtered SessionCount = %d, want 1", resp.SessionCount)
	}
	if resp.TotalCost != 1.0 {
		t.Errorf("filtered TotalCost = %f, want 1.0", resp.TotalCost)
	}
}

func TestHandleUsage_InvalidDate(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage?since=not-a-date", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid date, got %d", rr.Code)
	}
}

func TestHandleUsage_InvalidUntilDate(t *testing.T) {
	dir := t.TempDir()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	orig := usageStoreInstance
	usageStoreInstance = store
	t.Cleanup(func() { usageStoreInstance = orig })

	req := httptest.NewRequest("GET", "/api/usage?until=invalid", nil)
	rr := httptest.NewRecorder()
	handleUsage(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid until date, got %d", rr.Code)
	}
}

func TestHandleStaleDetector_NotEnabled(t *testing.T) {
	// Ensure staleDetectorInstance is nil
	origInstance := staleDetectorInstance
	staleDetectorInstance = nil
	t.Cleanup(func() { staleDetectorInstance = origInstance })

	req := httptest.NewRequest("GET", "/api/stale-detector", nil)
	rr := httptest.NewRecorder()

	handleStaleDetector(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("response body should not be empty")
	}
}
