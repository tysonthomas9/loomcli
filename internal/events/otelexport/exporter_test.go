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

func TestAgentStarted_EndsExistingSpan(t *testing.T) {
	exp, spanExp, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	// Send two AgentStarted events for the same agent without AgentStopped in between.
	exp.HandleEvent(makeEvent(events.AgentStarted, "agent1", "task", "", events.AgentStartedData{PID: 100}))
	exp.HandleEvent(makeEvent(events.AgentStarted, "agent1", "task", "", events.AgentStartedData{PID: 200}))

	// The first span should have been ended with status "superseded".
	spans := spanExp.GetSpans()
	var supersededCount int
	for _, s := range spans {
		if s.Name == "loom.agent.lifecycle" && s.Status.Code == codes.Error && s.Status.Description == "superseded" {
			supersededCount++
		}
	}
	if supersededCount != 1 {
		t.Errorf("expected 1 superseded agent span, got %d", supersededCount)
	}

	// Only one active agent span should remain in the map.
	exp.mu.Lock()
	agentSpans := len(exp.activeAgentSpans)
	exp.mu.Unlock()
	if agentSpans != 1 {
		t.Errorf("activeAgentSpans = %d, want 1", agentSpans)
	}
}

func TestTaskClaimed_EndsExistingSpan(t *testing.T) {
	exp, spanExp, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	// Send two TaskClaimed events for the same agent without TaskCompleted in between.
	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "epic-1", events.TaskClaimedData{TaskID: "t1", Title: "First task"}))
	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "epic-1", events.TaskClaimedData{TaskID: "t2", Title: "Second task"}))

	// The first task span should have been ended with status "superseded".
	spans := spanExp.GetSpans()
	var supersededCount int
	for _, s := range spans {
		if s.Name == "loom.task" && s.Status.Code == codes.Error && s.Status.Description == "superseded" {
			supersededCount++
		}
	}
	if supersededCount != 1 {
		t.Errorf("expected 1 superseded task span, got %d", supersededCount)
	}

	// Only one active task span should remain in the map.
	exp.mu.Lock()
	taskSpans := len(exp.activeTaskSpans)
	exp.mu.Unlock()
	if taskSpans != 1 {
		t.Errorf("activeTaskSpans = %d, want 1", taskSpans)
	}
}

func TestAgentRestarted_EndsTaskSpan(t *testing.T) {
	exp, spanExp, _ := newTestExporter(t)
	defer exp.Stop(context.Background())

	// Claim a task, then restart the agent.
	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "epic-1", events.TaskClaimedData{TaskID: "t1", Title: "Some task"}))
	exp.HandleEvent(makeEvent(events.AgentRestarted, "agent1", "task", "", events.AgentRestartedData{PID: 300, RestartCount: 1}))

	// The task span should have been ended with error status "agent_restarted".
	spans := spanExp.GetSpans()
	var found bool
	for _, s := range spans {
		if s.Name == "loom.task" && s.Status.Code == codes.Error && s.Status.Description == "agent_restarted" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected loom.task span with error status 'agent_restarted'")
	}

	// activeTaskSpans should be empty for that agent.
	exp.mu.Lock()
	taskSpans := len(exp.activeTaskSpans)
	exp.mu.Unlock()
	if taskSpans != 0 {
		t.Errorf("activeTaskSpans = %d, want 0", taskSpans)
	}
}

func TestFullRestartCycle_NoLeaks(t *testing.T) {
	exp, spanExp, _ := newTestExporter(t)

	// Simulate a full restart cycle:
	// AgentStarted -> TaskClaimed -> AgentRestarted -> AgentStarted -> TaskClaimed -> TaskCompleted -> AgentStopped
	exp.HandleEvent(makeEvent(events.AgentStarted, "agent1", "task", "", events.AgentStartedData{PID: 100}))
	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "epic-1", events.TaskClaimedData{TaskID: "t1", Title: "First task"}))
	exp.HandleEvent(makeEvent(events.AgentRestarted, "agent1", "task", "", events.AgentRestartedData{PID: 200, RestartCount: 1}))
	exp.HandleEvent(makeEvent(events.AgentStarted, "agent1", "task", "", events.AgentStartedData{PID: 200}))
	exp.HandleEvent(makeEvent(events.TaskClaimed, "agent1", "task", "epic-1", events.TaskClaimedData{TaskID: "t2", Title: "Second task"}))
	exp.HandleEvent(makeEvent(events.TaskCompleted, "agent1", "task", "epic-1", events.TaskCompletedData{TaskID: "t2", Duration: events.Duration{Duration: 2 * time.Second}}))
	exp.HandleEvent(makeEvent(events.AgentStopped, "agent1", "task", "", events.AgentStoppedData{PID: 200, ExitCode: 0}))

	// Check spans before Stop (which shuts down the tracer provider and may reset the exporter).
	spans := spanExp.GetSpans()

	// We expect 4 ended spans:
	// 1. loom.agent.lifecycle (first, superseded by second AgentStarted)
	// 2. loom.task (first task, ended by AgentRestarted with "agent_restarted")
	// 3. loom.agent.lifecycle (second, ended by AgentStopped)
	// 4. loom.task (second task, ended by TaskCompleted)
	var agentLifecycleCount, taskCount int
	for _, s := range spans {
		switch s.Name {
		case "loom.agent.lifecycle":
			agentLifecycleCount++
		case "loom.task":
			taskCount++
		}
	}
	if agentLifecycleCount != 2 {
		t.Errorf("expected 2 loom.agent.lifecycle spans, got %d", agentLifecycleCount)
	}
	if taskCount != 2 {
		t.Errorf("expected 2 loom.task spans, got %d", taskCount)
	}

	// Verify both maps are empty after the full cycle (before Stop).
	exp.mu.Lock()
	agentSpans := len(exp.activeAgentSpans)
	taskSpans := len(exp.activeTaskSpans)
	exp.mu.Unlock()

	if agentSpans != 0 {
		t.Errorf("after full cycle: activeAgentSpans = %d, want 0", agentSpans)
	}
	if taskSpans != 0 {
		t.Errorf("after full cycle: activeTaskSpans = %d, want 0", taskSpans)
	}

	// Clean up.
	if err := exp.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}
