package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
)

func writeTestEvent(t *testing.T, dir string, e events.Event) {
	t.Helper()
	date := e.Timestamp.Format("2006-01-02")
	path := filepath.Join(dir, fmt.Sprintf("events-%s.jsonl", date))
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// newTestMetricsCache creates a cachedValue for observability metrics tests.
func newTestMetricsCache(dir string) *cachedValue[*events.MetricsSnapshot] {
	return newCachedValue[*events.MetricsSnapshot](30*time.Second, func() *events.MetricsSnapshot {
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		_ = replayAllEvents(store, dir)
		snap := store.Snapshot()
		return &snap
	})
}

func TestHandleObservabilityMetrics_Success(t *testing.T) {
	dir := t.TempDir()

	// Write a completed task event
	now := time.Now()
	e, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "epic-1", events.TaskCompletedData{
		TaskID:       "task-1",
		Duration:     events.Duration{Duration: 5 * time.Minute},
		FilesChanged: 3,
		LinesAdded:   100,
		LinesRemoved: 20,
	})
	e.Timestamp = now.Add(-30 * time.Minute)
	writeTestEvent(t, dir, e)

	handler := handleObservabilityMetrics(dir, newTestMetricsCache(dir))
	req := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ObservabilityMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if resp.Data.TasksCompleted24h != 1 {
		t.Errorf("expected 1 completed task, got %d", resp.Data.TasksCompleted24h)
	}
	if resp.Data.TasksByAgent["agent1"] != 1 {
		t.Errorf("expected 1 task for agent1, got %d", resp.Data.TasksByAgent["agent1"])
	}
}

func TestHandleObservabilityMetrics_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	handler := handleObservabilityMetrics(dir, newTestMetricsCache(dir))
	req := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ObservabilityMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatal("expected success")
	}
	if resp.Data.TasksCompleted24h != 0 {
		t.Errorf("expected 0, got %d", resp.Data.TasksCompleted24h)
	}
}

func TestHandleObservabilityMetrics_NotConfigured(t *testing.T) {
	handler := handleObservabilityMetrics("", newTestMetricsCache(""))
	req := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleObservabilityEvents_Success(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	for i := 0; i < 5; i++ {
		e, _ := events.NewEvent(events.TaskCompleted, fmt.Sprintf("agent%d", i), "coder", "", events.TaskCompletedData{
			TaskID: fmt.Sprintf("task-%d", i),
		})
		e.Timestamp = now.Add(-time.Duration(i) * time.Minute)
		writeTestEvent(t, dir, e)
	}

	handler := handleObservabilityEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ObservabilityEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("expected success, error: %s", resp.Error)
	}
	if resp.Total != 5 {
		t.Errorf("expected 5 total, got %d", resp.Total)
	}
	if len(resp.Data) != 5 {
		t.Errorf("expected 5 events, got %d", len(resp.Data))
	}
	// Verify reverse chronological order
	for i := 1; i < len(resp.Data); i++ {
		if resp.Data[i].Timestamp.After(resp.Data[i-1].Timestamp) {
			t.Errorf("events not in reverse chronological order at index %d", i)
		}
	}
}

func TestHandleObservabilityEvents_Pagination(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	for i := 0; i < 25; i++ {
		e, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{
			TaskID: fmt.Sprintf("task-%d", i),
		})
		e.Timestamp = now.Add(-time.Duration(i) * time.Minute)
		writeTestEvent(t, dir, e)
	}

	handler := handleObservabilityEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?page=2&per_page=10", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp ObservabilityEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 25 {
		t.Errorf("expected 25 total, got %d", resp.Total)
	}
	if len(resp.Data) != 10 {
		t.Errorf("expected 10 events on page 2, got %d", len(resp.Data))
	}
	if resp.Page != 2 {
		t.Errorf("expected page=2, got %d", resp.Page)
	}
}

func TestHandleObservabilityEvents_FilterByType(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	e1, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t1"})
	e1.Timestamp = now
	writeTestEvent(t, dir, e1)

	e2, _ := events.NewEvent(events.AgentStarted, "agent1", "coder", "", events.AgentStartedData{PID: 123})
	e2.Timestamp = now.Add(-time.Minute)
	writeTestEvent(t, dir, e2)

	handler := handleObservabilityEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?type=task.completed", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp ObservabilityEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 filtered event, got %d", resp.Total)
	}
	if len(resp.Data) > 0 && resp.Data[0].Type != events.TaskCompleted {
		t.Errorf("expected task.completed, got %s", resp.Data[0].Type)
	}
}

func TestHandleObservabilityEvents_FilterByAgent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	e1, _ := events.NewEvent(events.TaskCompleted, "alpha", "coder", "", events.TaskCompletedData{TaskID: "t1"})
	e1.Timestamp = now
	writeTestEvent(t, dir, e1)

	e2, _ := events.NewEvent(events.TaskCompleted, "beta", "coder", "", events.TaskCompletedData{TaskID: "t2"})
	e2.Timestamp = now.Add(-time.Minute)
	writeTestEvent(t, dir, e2)

	handler := handleObservabilityEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?agent=alpha", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp ObservabilityEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 event for alpha, got %d", resp.Total)
	}
}

func TestHandleObservabilityEvents_FilterBySince(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	e1, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t1"})
	e1.Timestamp = now
	writeTestEvent(t, dir, e1)

	e2, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t2"})
	e2.Timestamp = now.Add(-2 * time.Hour)
	writeTestEvent(t, dir, e2)

	since := now.Add(-time.Hour).Format(time.RFC3339)
	handler := handleObservabilityEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?since="+since, nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp ObservabilityEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 event after since, got %d", resp.Total)
	}
}

func TestHandleObservabilityEvents_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	handler := handleObservabilityEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp ObservabilityEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Error("expected success for empty dir")
	}
	if resp.Total != 0 {
		t.Errorf("expected 0 total, got %d", resp.Total)
	}
}

func TestHandleObservabilityEvents_InvalidParams(t *testing.T) {
	dir := t.TempDir()
	handler := handleObservabilityEvents(dir)

	tests := []struct {
		name  string
		query string
	}{
		{"invalid page", "?page=abc"},
		{"negative page", "?page=-1"},
		{"invalid per_page", "?per_page=xyz"},
		{"zero per_page", "?per_page=0"},
		{"invalid since", "?since=not-a-date"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/observability/events"+tt.query, nil)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for %s, got %d", tt.query, rec.Code)
			}
		})
	}
}

func TestHandleObservabilityEvents_MaxPerPage(t *testing.T) {
	dir := t.TempDir()
	handler := handleObservabilityEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?per_page=500", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp ObservabilityEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.PerPage != 200 {
		t.Errorf("expected per_page clamped to 200, got %d", resp.PerPage)
	}
}

func TestReadEventsFromJSONL_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	day1 := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2025, 3, 2, 12, 0, 0, 0, time.UTC)

	e1, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t1"})
	e1.Timestamp = day1
	writeTestEvent(t, dir, e1)

	e2, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t2"})
	e2.Timestamp = day2
	writeTestEvent(t, dir, e2)

	evts, total, err := readEventsFromJSONL(dir, EventReadOpts{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("expected 2 events from 2 files, got %d", total)
	}
	if len(evts) != 2 {
		t.Errorf("expected 2 events returned, got %d", len(evts))
	}
	// Most recent first
	if evts[0].Timestamp.Before(evts[1].Timestamp) {
		t.Error("expected reverse chronological order")
	}
}

func TestReadEventsFromJSONL_MalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events-2025-03-01.jsonl")

	e, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t1"})
	e.Timestamp = time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	data, _ := json.Marshal(e)

	content := string(data) + "\n" + "not valid json\n" + string(data) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	evts, total, err := readEventsFromJSONL(dir, EventReadOpts{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("expected 2 valid events (skipping malformed), got %d", total)
	}
	if len(evts) != 2 {
		t.Errorf("expected 2 events, got %d", len(evts))
	}
}

func TestReadEventsFromJSONL_EmptyDir(t *testing.T) {
	evts, total, err := readEventsFromJSONL("", EventReadOpts{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("expected 0, got %d", total)
	}
	if len(evts) != 0 {
		t.Errorf("expected empty slice, got %d", len(evts))
	}
}

func TestHandleObservabilityMetrics_CacheCoalescing(t *testing.T) {
	dir := t.TempDir()

	// Write a test event so metrics have something to report
	now := time.Now()
	e, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{
		TaskID:   "task-1",
		Duration: events.Duration{Duration: 2 * time.Minute},
	})
	e.Timestamp = now.Add(-10 * time.Minute)
	writeTestEvent(t, dir, e)

	// Track how many times the collection function is called
	var callCount int64
	cache := newCachedValue[*events.MetricsSnapshot](5*time.Second, func() *events.MetricsSnapshot {
		atomic.AddInt64(&callCount, 1)
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		_ = replayAllEvents(store, dir)
		snap := store.Snapshot()
		return &snap
	})

	handler := handleObservabilityMetrics(dir, cache)

	// First request: triggers collection
	req1 := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec1 := httptest.NewRecorder()
	handler(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("request 1: expected 200, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Second request within TTL: should use cached value
	req2 := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("request 2: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Verify only one collection happened
	count := atomic.LoadInt64(&callCount)
	if count != 1 {
		t.Errorf("expected 1 collection call, got %d", count)
	}

	// Verify both responses return the same data
	var resp1, resp2 ObservabilityMetricsResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &resp1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	if resp1.Data.TasksCompleted24h != resp2.Data.TasksCompleted24h {
		t.Errorf("cached response mismatch: %d vs %d",
			resp1.Data.TasksCompleted24h, resp2.Data.TasksCompleted24h)
	}
}

func TestHandleObservabilityMetrics_CacheExpiry(t *testing.T) {
	dir := t.TempDir()

	// Write a test event
	now := time.Now()
	e, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{
		TaskID:   "task-1",
		Duration: events.Duration{Duration: 1 * time.Minute},
	})
	e.Timestamp = now.Add(-5 * time.Minute)
	writeTestEvent(t, dir, e)

	// Use a very short TTL so it expires quickly
	var callCount int64
	cache := newCachedValue[*events.MetricsSnapshot](10*time.Millisecond, func() *events.MetricsSnapshot {
		atomic.AddInt64(&callCount, 1)
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		_ = replayAllEvents(store, dir)
		snap := store.Snapshot()
		return &snap
	})

	handler := handleObservabilityMetrics(dir, cache)

	// First request: triggers collection
	req1 := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec1 := httptest.NewRecorder()
	handler(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("request 1: expected 200, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Wait for the cache TTL to expire
	time.Sleep(50 * time.Millisecond)

	// Second request after TTL: should trigger a new collection
	req2 := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("request 2: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Verify two collections happened
	count := atomic.LoadInt64(&callCount)
	if count != 2 {
		t.Errorf("expected 2 collection calls after TTL expiry, got %d", count)
	}
}

func TestParseIntParam(t *testing.T) {
	tests := []struct {
		input      string
		defaultVal int
		want       int
		wantErr    bool
	}{
		{"", 10, 10, false},
		{"5", 10, 5, false},
		{"abc", 10, 0, true},
	}
	for _, tt := range tests {
		got, err := parseIntParam(tt.input, tt.defaultVal)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseIntParam(%q, %d) error = %v, wantErr %v", tt.input, tt.defaultVal, err, tt.wantErr)
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseIntParam(%q, %d) = %d, want %d", tt.input, tt.defaultVal, got, tt.want)
		}
	}
}
