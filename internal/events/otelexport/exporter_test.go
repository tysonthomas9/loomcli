package otelexport

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"

	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func makeEvent(t events.EventType, agent, role, epicID string, data interface{}) events.Event {
	ev := events.Event{
		Type:      t,
		Timestamp: time.Now(),
		Agent:     agent,
		Role:      role,
		EpicID:    epicID,
	}
	if data != nil {
		raw, _ := json.Marshal(data)
		ev.Data = raw
	}
	return ev
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}
	return rm
}

func findCounter(rm metricdata.ResourceMetrics, name string) int64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
					var total int64
					for _, dp := range sum.DataPoints {
						total += dp.Value
					}
					return total
				}
			}
		}
	}
	return 0
}

func findHistogramCount(rm metricdata.ResourceMetrics, name string) uint64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				if hist, ok := m.Data.(metricdata.Histogram[float64]); ok {
					var total uint64
					for _, dp := range hist.DataPoints {
						total += dp.Count
					}
					return total
				}
				if hist, ok := m.Data.(metricdata.Histogram[int64]); ok {
					var total uint64
					for _, dp := range hist.DataPoints {
						total += dp.Count
					}
					return total
				}
			}
		}
	}
	return 0
}

func TestNew_Defaults(t *testing.T) {
	exp, _, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	if exp.tracer == nil {
		t.Fatal("tracer should not be nil")
	}
	if exp.taskCompleted == nil {
		t.Fatal("taskCompleted counter should not be nil")
	}
}

func TestHandleEvent_TaskCompleted(t *testing.T) {
	exp, _, reader := newTestExporter(t)
	defer exp.Stop(context.Background())

	ev := makeEvent(events.TaskCompleted, "agent1", "task", "epic-1", events.TaskCompletedData{
		TaskID:       "task-123",
		Duration:     events.Duration{Duration: 5 * time.Second},
		FilesChanged: 3,
		LinesAdded:   100,
		LinesRemoved: 20,
	})
	exp.HandleEvent(ev)

	rm := collectMetrics(t, reader)
	if got := findCounter(rm, "loom.task.completed"); got != 1 {
		t.Errorf("loom.task.completed = %d, want 1", got)
	}
	if got := findHistogramCount(rm, "loom.task.duration_ms"); got != 1 {
		t.Errorf("loom.task.duration_ms count = %d, want 1", got)
	}
	if got := findHistogramCount(rm, "loom.task.lines_changed"); got != 1 {
		t.Errorf("loom.task.lines_changed count = %d, want 1", got)
	}
}

func TestHandleEvent_TaskFailed(t *testing.T) {
	exp, _, reader := newTestExporter(t)
	defer exp.Stop(context.Background())

	ev := makeEvent(events.TaskFailed, "agent1", "task", "epic-1", events.TaskFailedData{
		TaskID: "task-456",
		Error:  "connection refused to API",
	})
	exp.HandleEvent(ev)

	rm := collectMetrics(t, reader)
	if got := findCounter(rm, "loom.task.failed"); got != 1 {
		t.Errorf("loom.task.failed = %d, want 1", got)
	}
}

func TestHandleEvent_AgentLifecycle(t *testing.T) {
	exp, spanExp, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	exp.HandleEvent(makeEvent(events.AgentStarted, "agent1", "task", "", events.AgentStartedData{PID: 1234}))
	exp.HandleEvent(makeEvent(events.AgentStopped, "agent1", "task", "", events.AgentStoppedData{PID: 1234, ExitCode: 0}))

	spans := spanExp.GetSpans()
	found := false
	for _, s := range spans {
		if s.Name == "loom.agent.lifecycle" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected loom.agent.lifecycle span")
	}
}

func TestHandleEvent_TaskSpan(t *testing.T) {
	exp, spanExp, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "epic-1", events.TaskClaimedData{
		TaskID: "task-789",
		Title:  "Do something",
	}))
	exp.HandleEvent(makeEvent(events.TaskCompleted, "agent1", "task", "epic-1", events.TaskCompletedData{
		TaskID:   "task-789",
		Duration: events.Duration{Duration: 3 * time.Second},
	}))

	spans := spanExp.GetSpans()
	found := false
	for _, s := range spans {
		if s.Name == "loom.task" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected loom.task span")
	}
}

func TestHandleEvent_TaskSpanOnFailure(t *testing.T) {
	exp, spanExp, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "", events.TaskClaimedData{TaskID: "t1"}))
	exp.HandleEvent(makeEvent(events.TaskFailed, "agent1", "task", "", events.TaskFailedData{TaskID: "t1", Error: "timeout exceeded"}))

	spans := spanExp.GetSpans()
	for _, s := range spans {
		if s.Name == "loom.task" {
			if s.Status.Code != codes.Error {
				t.Errorf("task span status = %v, want Error", s.Status.Code)
			}
			return
		}
	}
	t.Error("expected loom.task span with error status")
}

func TestHandleEvent_AgentRestart(t *testing.T) {
	exp, _, reader := newTestExporter(t)
	defer exp.Stop(context.Background())

	exp.HandleEvent(makeEvent(events.AgentRestarted, "agent1", "task", "", events.AgentRestartedData{PID: 5678, RestartCount: 2}))

	rm := collectMetrics(t, reader)
	if got := findCounter(rm, "loom.agent.restart"); got != 1 {
		t.Errorf("loom.agent.restart = %d, want 1", got)
	}
}

func TestHandleEvent_UnknownEvent(t *testing.T) {
	exp, _, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	// Should not panic
	exp.HandleEvent(events.Event{Type: "unknown_type"})
}

func TestStop_FlushesPending(t *testing.T) {
	exp, _, _ := newTestExporter(t)

	exp.HandleEvent(makeEvent(events.TaskCompleted, "agent1", "task", "", events.TaskCompletedData{
		TaskID:   "t1",
		Duration: events.Duration{Duration: time.Second},
	}))

	if err := exp.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestStop_EndsActiveSpans(t *testing.T) {
	exp, _, _ := newTestExporter(t)

	exp.HandleEvent(makeEvent(events.AgentStarted, "agent1", "task", "", events.AgentStartedData{PID: 111}))
	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "", events.TaskClaimedData{TaskID: "t1"}))

	// Verify spans are active before stop
	exp.mu.Lock()
	agentSpans := len(exp.activeAgentSpans)
	taskSpans := len(exp.activeTaskSpans)
	exp.mu.Unlock()

	if agentSpans != 1 {
		t.Errorf("activeAgentSpans = %d, want 1", agentSpans)
	}
	if taskSpans != 1 {
		t.Errorf("activeTaskSpans = %d, want 1", taskSpans)
	}

	// Stop should end all active spans and clear the maps
	if err := exp.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	exp.mu.Lock()
	agentSpans = len(exp.activeAgentSpans)
	taskSpans = len(exp.activeTaskSpans)
	exp.mu.Unlock()

	if agentSpans != 0 {
		t.Errorf("after Stop: activeAgentSpans = %d, want 0", agentSpans)
	}
	if taskSpans != 0 {
		t.Errorf("after Stop: activeTaskSpans = %d, want 0", taskSpans)
	}
}

func TestStop_Idempotent(t *testing.T) {
	exp, _, _ := newTestExporter(t)

	if err := exp.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop() error: %v", err)
	}
	if err := exp.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error: %v", err)
	}
}

func TestCategorizeError(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"context deadline exceeded", "timeout"},
		{"request timeout after 30s", "timeout"},
		{"out of memory", "oom"},
		{"OOM killed", "oom"},
		{"permission denied", "permission"},
		{"access denied for resource", "permission"},
		{"connection refused", "network"},
		{"DNS resolution failed", "network"},
		{"host unreachable", "network"},
		{"process crash detected", "crash"},
		{"panic: runtime error", "crash"},
		{"segfault in worker", "crash"},
		{"something unexpected happened", "unknown"},
		{"", "unknown"},
	}

	for _, tt := range tests {
		got := categorizeError(tt.input)
		if got != tt.want {
			t.Errorf("categorizeError(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConcurrentHandleEvent(t *testing.T) {
	exp, _, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			agent := "agent"
			if i%2 == 0 {
				exp.HandleEvent(makeEvent(events.AgentStarted, agent, "task", "", events.AgentStartedData{PID: i}))
			} else {
				exp.HandleEvent(makeEvent(events.TaskCompleted, agent, "task", "", events.TaskCompletedData{
					TaskID:   "t1",
					Duration: events.Duration{Duration: time.Second},
				}))
			}
		}(i)
	}
	wg.Wait()
}
