package metricscmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

func TestHandleMetricsRendersPrometheusText(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	handler := HandleMetrics(func() *monitor.MonitorData {
		return &monitor.MonitorData{Tasks: monitor.TaskSummary{InProgress: 7}}
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"loom_ready_tasks",
		`loom_in_progress_tasks 7`,
		`loom_fleet_workers{status="active"} 0`,
		`loom_fleet_workers{status="idle"} 0`,
		`loom_fleet_workers{status="blocked"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestCollectorCachesAndCoalescesRequests(t *testing.T) {
	var calls atomic.Int32
	block := make(chan struct{})
	collector := NewCollectorFunc(time.Hour, func() *monitor.MonitorData {
		calls.Add(1)
		<-block
		return &monitor.MonitorData{Tasks: monitor.TaskSummary{InProgress: 3}}
	})

	var wg sync.WaitGroup
	results := make(chan *monitor.MonitorData, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- collector()
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(block)
	wg.Wait()
	close(results)
	if calls.Load() != 1 {
		t.Fatalf("collect calls = %d, want coalesced single call", calls.Load())
	}
	for data := range results {
		if data == nil || data.Tasks.InProgress != 3 {
			t.Fatalf("collector result = %+v, want cached data", data)
		}
	}
	if data := collector(); data == nil || data.Tasks.InProgress != 3 || calls.Load() != 1 {
		t.Fatalf("cached collector result=%+v calls=%d, want cache hit", data, calls.Load())
	}
}

func TestCollectorBackgroundRefreshAndNilCollectorFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	collector := NewCollectorWithBackgroundFunc(ctx, time.Hour, 10*time.Millisecond, func() *monitor.MonitorData {
		value := int(calls.Add(1))
		return &monitor.MonitorData{Tasks: monitor.TaskSummary{InProgress: value}}
	})

	deadline := time.Now().Add(250 * time.Millisecond)
	var data *monitor.MonitorData
	for time.Now().Before(deadline) {
		data = collector()
		if data != nil && data.Tasks.InProgress >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if data == nil || data.Tasks.InProgress < 2 {
		t.Fatalf("background collector returned %+v after %d calls", data, calls.Load())
	}
	cancel()

	fallback := NewCollectorFunc(time.Nanosecond, nil)
	if fallback == nil {
		t.Fatalf("NewCollectorFunc nil collectFn returned nil")
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	bgCancel()
	bgFallback := NewCollectorWithBackground(bgCtx, time.Nanosecond, time.Hour)
	if bgFallback == nil {
		t.Fatalf("NewCollectorWithBackground returned nil")
	}
}

func TestCachedCollectorBackgroundTickerRefreshes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	collector := &cachedCollector{
		ttl: time.Hour,
		collectFn: func() *monitor.MonitorData {
			value := int(calls.Add(1))
			return &monitor.MonitorData{Tasks: monitor.TaskSummary{InProgress: value}}
		},
	}
	collector.startBackground(ctx, time.Millisecond)

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatalf("background ticker did not refresh")
	}
	collector.mu.Lock()
	data := collector.cached
	collector.mu.Unlock()
	if data == nil || data.Tasks.InProgress < 2 {
		t.Fatalf("cached background data = %+v", data)
	}
}

func TestCollectWorkerStatusCountsWithoutDaemon(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	counts := collectWorkerStatusCounts()
	if counts["active"] != 0 || counts["idle"] != 0 || counts["blocked"] != 0 {
		t.Fatalf("worker counts = %#v, want zeros without daemon", counts)
	}
}
