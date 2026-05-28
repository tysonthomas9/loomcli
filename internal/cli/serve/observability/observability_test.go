package observability

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

	"github.com/tysonthomas9/loomcli/internal/cli"
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

// newTestMetricsCache creates a CachedValue for observability metrics tests.
func newTestMetricsCache(dir string) *CachedValue[*events.MetricsSnapshot] {
	return NewCachedValue[*events.MetricsSnapshot](30*time.Second, func() *events.MetricsSnapshot {
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		_ = ReplayAllEvents(store, dir)
		snap := store.Snapshot()
		return &snap
	})
}

func TestResolveEventsDirUsesWorkspaceRuntimeDefault(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_WORKSPACE", "")
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	got := ResolveEventsDir()
	want := filepath.Join(runtimeDir, ".loom", "events")
	if got != want {
		t.Fatalf("ResolveEventsDir = %q, want %q", got, want)
	}
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

	handler := HandleMetrics(dir, newTestMetricsCache(dir))
	req := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MetricsResponse
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

func TestNewMetricsCacheReplaysDirectory(t *testing.T) {
	dir := t.TempDir()
	e, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "task-1"})
	e.Timestamp = time.Now()
	writeTestEvent(t, dir, e)

	cache := NewMetricsCache(dir)
	snap := cache.Get()
	if snap == nil {
		t.Fatal("NewMetricsCache returned nil snapshot")
	}
	if snap.TasksCompleted24h != 1 {
		t.Fatalf("TasksCompleted24h = %d, want 1", snap.TasksCompleted24h)
	}
}

func TestNewMetricsCacheHandlesReplayError(t *testing.T) {
	cache := NewMetricsCache(filepath.Join(t.TempDir(), "missing"))
	if snap := cache.Get(); snap == nil {
		t.Fatal("NewMetricsCache missing dir returned nil snapshot")
	}
}

func TestHandleObservabilityMetrics_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	handler := HandleMetrics(dir, newTestMetricsCache(dir))
	req := httptest.NewRequest("GET", "/api/observability/metrics", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp MetricsResponse
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
	handler := HandleMetrics("", newTestMetricsCache(""))
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

	handler := HandleEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp EventsResponse
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

	handler := HandleEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?page=2&per_page=10", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp EventsResponse
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

	handler := HandleEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?type=task.completed", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp EventsResponse
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

	handler := HandleEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?agent=alpha", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp EventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 event for alpha, got %d", resp.Total)
	}
}

func TestHandleObservabilityEvents_FilterBySince(t *testing.T) {
	dir := t.TempDir()
	// UTC throughout: time.RFC3339 emits a '+' for non-UTC offsets, and
	// inlining that string into a URL query lets net/url decode the '+'
	// as a space — the handler then rejects the "since" parameter as
	// non-RFC3339. Using UTC produces a trailing 'Z' that round-trips
	// through URL encoding cleanly.
	now := time.Now().UTC()

	e1, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t1"})
	e1.Timestamp = now
	writeTestEvent(t, dir, e1)

	e2, _ := events.NewEvent(events.TaskCompleted, "agent1", "coder", "", events.TaskCompletedData{TaskID: "t2"})
	e2.Timestamp = now.Add(-2 * time.Hour)
	writeTestEvent(t, dir, e2)

	since := now.Add(-time.Hour).Format(time.RFC3339)
	handler := HandleEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?since="+since, nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp EventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 event after since, got %d", resp.Total)
	}
}

func TestHandleObservabilityEvents_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	handler := HandleEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp EventsResponse
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
	handler := HandleEvents(dir)

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
	handler := HandleEvents(dir)
	req := httptest.NewRequest("GET", "/api/observability/events?per_page=500", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp EventsResponse
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

	evts, total, err := ReadEventsFromJSONL(dir, EventReadOpts{Page: 1, PerPage: 50})
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

	evts, total, err := ReadEventsFromJSONL(dir, EventReadOpts{Page: 1, PerPage: 50})
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
	evts, total, err := ReadEventsFromJSONL("", EventReadOpts{Page: 1, PerPage: 50})
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
	cache := NewCachedValue[*events.MetricsSnapshot](5*time.Second, func() *events.MetricsSnapshot {
		atomic.AddInt64(&callCount, 1)
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		_ = ReplayAllEvents(store, dir)
		snap := store.Snapshot()
		return &snap
	})

	handler := HandleMetrics(dir, cache)

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
	var resp1, resp2 MetricsResponse
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
	cache := NewCachedValue[*events.MetricsSnapshot](10*time.Millisecond, func() *events.MetricsSnapshot {
		atomic.AddInt64(&callCount, 1)
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		_ = ReplayAllEvents(store, dir)
		snap := store.Snapshot()
		return &snap
	})

	handler := HandleMetrics(dir, cache)

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

func TestCachedValueSharesInflightCollection(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var callCount int64
	cache := NewCachedValue[int](time.Hour, func() int {
		atomic.AddInt64(&callCount, 1)
		started <- struct{}{}
		<-release
		return 42
	})

	first := make(chan int, 1)
	go func() { first <- cache.Get() }()
	<-started

	second := make(chan int, 1)
	go func() { second <- cache.Get() }()
	time.Sleep(10 * time.Millisecond)
	close(release)

	if got := <-first; got != 42 {
		t.Fatalf("first Get = %d, want 42", got)
	}
	if got := <-second; got != 42 {
		t.Fatalf("second Get = %d, want 42", got)
	}
	if got := atomic.LoadInt64(&callCount); got != 1 {
		t.Fatalf("collect calls = %d, want 1", got)
	}
}

func TestHandleObservabilityEventsReadError(t *testing.T) {
	handler := HandleEvents("[")
	req := httptest.NewRequest("GET", "/api/observability/events", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestReadJSONLFileScannerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events-2025-03-01.jsonl")
	line := make([]byte, 1024*1024+1)
	for i := range line {
		line[i] = 'x'
	}
	if err := os.WriteFile(path, append(line, '\n'), 0600); err != nil {
		t.Fatalf("write long line: %v", err)
	}

	if _, err := ReadJSONLFile(path); err == nil {
		t.Fatal("ReadJSONLFile long line error = nil")
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
		got, err := ParseIntParam(tt.input, tt.defaultVal)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseIntParam(%q, %d) error = %v, wantErr %v", tt.input, tt.defaultVal, err, tt.wantErr)
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("ParseIntParam(%q, %d) = %d, want %d", tt.input, tt.defaultVal, got, tt.want)
		}
	}
}
