package events

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func newTestStore() *MetricsStore {
	ms := NewMetricsStore(nil, time.Hour)
	ms.now = func() time.Time {
		return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	}
	return ms
}

func mustEvent(t *testing.T, et EventType, agent, role, epicID string, v interface{}) Event {
	t.Helper()
	e, err := NewEvent(et, agent, role, epicID, v)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	return e
}

func TestMetricsStore_HandleTaskCompleted(t *testing.T) {
	ms := newTestStore()
	e := mustEvent(t, TaskCompleted, "agent1", "dev", "epic1", TaskCompletedData{
		TaskID:       "t1",
		Duration:     Duration{5 * time.Minute},
		FilesChanged: 3,
		LinesAdded:   100,
		LinesRemoved: 20,
	})
	e.Timestamp = ms.now().Add(-10 * time.Minute)
	ms.handleEvent(e)

	snap := ms.Snapshot()
	if snap.TasksCompleted24h != 1 {
		t.Errorf("TasksCompleted24h = %d, want 1", snap.TasksCompleted24h)
	}
	if snap.AvgTaskDurationSec != 300 {
		t.Errorf("AvgTaskDurationSec = %f, want 300", snap.AvgTaskDurationSec)
	}
	if snap.TotalTasksCompleted != 1 {
		t.Errorf("TotalTasksCompleted = %d, want 1", snap.TotalTasksCompleted)
	}
	if snap.TasksCompletedLastHr != 1 {
		t.Errorf("TasksCompletedLastHr = %d, want 1", snap.TasksCompletedLastHr)
	}
	if snap.LinesChangedLastHr != 120 {
		t.Errorf("LinesChangedLastHr = %d, want 120", snap.LinesChangedLastHr)
	}
}

func TestMetricsStore_HandleTaskFailed(t *testing.T) {
	ms := newTestStore()
	e := mustEvent(t, TaskFailed, "agent1", "dev", "", TaskFailedData{
		TaskID: "t1",
		Error:  "compilation error",
	})
	e.Timestamp = ms.now()
	ms.handleEvent(e)

	snap := ms.Snapshot()
	if snap.ErrorRatePct != 100 {
		t.Errorf("ErrorRatePct = %f, want 100", snap.ErrorRatePct)
	}
	if snap.TotalTasksFailed != 1 {
		t.Errorf("TotalTasksFailed = %d, want 1", snap.TotalTasksFailed)
	}
}

func TestMetricsStore_HandleAgentLifecycle(t *testing.T) {
	ms := newTestStore()
	base := ms.now().Add(-30 * time.Minute)

	started := mustEvent(t, AgentStarted, "agent1", "", "", AgentStartedData{PID: 100})
	started.Timestamp = base
	ms.handleEvent(started)

	stopped := mustEvent(t, AgentStopped, "agent1", "", "", AgentStoppedData{PID: 100, ExitCode: 0})
	stopped.Timestamp = base.Add(20 * time.Minute)
	ms.handleEvent(stopped)

	snap := ms.Snapshot()
	util, ok := snap.AgentUtilization["agent1"]
	if !ok {
		t.Fatal("missing agent1 utilization")
	}
	// 20 minutes of work out of 1 hour retention = ~0.333
	if util < 0.3 || util > 0.4 {
		t.Errorf("AgentUtilization = %f, want ~0.333", util)
	}
}

func TestMetricsStore_HandleAgentRestarted(t *testing.T) {
	ms := newTestStore()
	e := mustEvent(t, AgentRestarted, "agent1", "", "", AgentRestartedData{PID: 101, RestartCount: 3})
	e.Timestamp = ms.now()
	ms.handleEvent(e)

	e2 := mustEvent(t, AgentRestarted, "agent2", "", "", AgentRestartedData{PID: 102, RestartCount: 1})
	e2.Timestamp = ms.now()
	ms.handleEvent(e2)

	snap := ms.Snapshot()
	if snap.RestartCount24h != 2 {
		t.Errorf("RestartCount24h = %d, want 2", snap.RestartCount24h)
	}
	if snap.RestartsByAgent["agent1"] != 1 {
		t.Errorf("RestartsByAgent[agent1] = %d, want 1", snap.RestartsByAgent["agent1"])
	}
	if snap.TotalRestarts != 2 {
		t.Errorf("TotalRestarts = %d, want 2", snap.TotalRestarts)
	}
}

func TestMetricsStore_Prune(t *testing.T) {
	ms := newTestStore()

	// Add an old event (before retention window)
	old := mustEvent(t, TaskCompleted, "agent1", "dev", "", TaskCompletedData{
		TaskID: "old", Duration: Duration{time.Minute},
	})
	old.Timestamp = ms.now().Add(-2 * time.Hour)
	ms.handleEvent(old)

	// Add a recent event
	recent := mustEvent(t, TaskCompleted, "agent1", "dev", "", TaskCompletedData{
		TaskID: "recent", Duration: Duration{time.Minute},
	})
	recent.Timestamp = ms.now().Add(-10 * time.Minute)
	ms.handleEvent(recent)

	ms.Prune()

	ms.mu.RLock()
	count := len(ms.tasks)
	ms.mu.RUnlock()

	if count != 1 {
		t.Errorf("after prune, tasks = %d, want 1", count)
	}
}

func TestMetricsStore_MonotonicCounters(t *testing.T) {
	ms := newTestStore()

	// Add events that will be pruned
	old := mustEvent(t, TaskCompleted, "agent1", "dev", "", TaskCompletedData{
		TaskID: "old", Duration: Duration{time.Minute},
	})
	old.Timestamp = ms.now().Add(-2 * time.Hour)
	ms.handleEvent(old)

	ms.Prune()

	snap := ms.Snapshot()
	if snap.TotalTasksCompleted != 1 {
		t.Errorf("TotalTasksCompleted = %d, want 1 (should survive pruning)", snap.TotalTasksCompleted)
	}
	if snap.TasksCompleted24h != 0 {
		t.Errorf("TasksCompleted24h = %d, want 0 (should be pruned)", snap.TasksCompleted24h)
	}
}

func TestMetricsStore_HourlyBuckets(t *testing.T) {
	ms := newTestStore()

	// Events at 10:30 and 11:30 (2 different hours)
	e1 := mustEvent(t, TaskCompleted, "agent1", "dev", "", TaskCompletedData{
		TaskID: "t1", Duration: Duration{2 * time.Minute},
	})
	e1.Timestamp = time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	ms.handleEvent(e1)

	e2 := mustEvent(t, TaskCompleted, "agent1", "dev", "", TaskCompletedData{
		TaskID: "t2", Duration: Duration{4 * time.Minute},
	})
	e2.Timestamp = time.Date(2026, 1, 1, 11, 45, 0, 0, time.UTC)
	ms.handleEvent(e2)

	e3 := mustEvent(t, TaskFailed, "agent1", "dev", "", TaskFailedData{
		TaskID: "t3", Error: "fail",
	})
	e3.Timestamp = time.Date(2026, 1, 1, 11, 50, 0, 0, time.UTC)
	ms.handleEvent(e3)

	snap := ms.Snapshot()
	if len(snap.HourlyCompletions) != 1 {
		t.Fatalf("HourlyCompletions = %d buckets, want 1", len(snap.HourlyCompletions))
	}
	bucket := snap.HourlyCompletions[0]
	if bucket.Completed != 2 || bucket.Failed != 1 {
		t.Errorf("bucket = {completed: %d, failed: %d}, want {2, 1}", bucket.Completed, bucket.Failed)
	}
	// avg duration = (120 + 240) / 2 = 180 seconds
	if bucket.AvgDuration != 180 {
		t.Errorf("AvgDuration = %f, want 180", bucket.AvgDuration)
	}
}

func TestMetricsStore_TasksByRole(t *testing.T) {
	ms := newTestStore()
	for _, role := range []string{"dev", "dev", "qa"} {
		e := mustEvent(t, TaskCompleted, "agent1", role, "", TaskCompletedData{
			TaskID: "t", Duration: Duration{time.Minute},
		})
		e.Timestamp = ms.now()
		ms.handleEvent(e)
	}
	snap := ms.Snapshot()
	if snap.TasksByRole["dev"] != 2 {
		t.Errorf("TasksByRole[dev] = %d, want 2", snap.TasksByRole["dev"])
	}
	if snap.TasksByRole["qa"] != 1 {
		t.Errorf("TasksByRole[qa] = %d, want 1", snap.TasksByRole["qa"])
	}
}

func TestMetricsStore_TasksByEpic(t *testing.T) {
	ms := newTestStore()
	for _, epic := range []string{"e1", "e1", "e2"} {
		e := mustEvent(t, TaskCompleted, "agent1", "dev", epic, TaskCompletedData{
			TaskID: "t", Duration: Duration{time.Minute},
		})
		e.Timestamp = ms.now()
		ms.handleEvent(e)
	}
	snap := ms.Snapshot()
	if snap.TasksByEpic["e1"] != 2 {
		t.Errorf("TasksByEpic[e1] = %d, want 2", snap.TasksByEpic["e1"])
	}
}

func TestMetricsStore_AgentUtilization_OngoingWork(t *testing.T) {
	ms := newTestStore()
	// Agent started 30 minutes ago but never stopped
	started := mustEvent(t, AgentStarted, "agent1", "", "", AgentStartedData{PID: 100})
	started.Timestamp = ms.now().Add(-30 * time.Minute)
	ms.handleEvent(started)

	snap := ms.Snapshot()
	util := snap.AgentUtilization["agent1"]
	// 30 min of work out of 1 hour retention = 0.5
	if util < 0.45 || util > 0.55 {
		t.Errorf("AgentUtilization = %f, want ~0.5", util)
	}
}

func TestMetricsStore_ConcurrentAccess(t *testing.T) {
	ms := newTestStore()
	ms.now = time.Now

	var wg sync.WaitGroup
	// 50 writers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := mustEvent(t, TaskCompleted, "agent1", "dev", "", TaskCompletedData{
				TaskID: "t", Duration: Duration{time.Minute},
			})
			e.Timestamp = time.Now()
			ms.handleEvent(e)
		}()
	}
	// 10 readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ms.Snapshot()
		}()
	}
	wg.Wait()

	snap := ms.Snapshot()
	if snap.TotalTasksCompleted != 50 {
		t.Errorf("TotalTasksCompleted = %d, want 50", snap.TotalTasksCompleted)
	}
}

func TestMetricsStore_NilBus(t *testing.T) {
	ms := NewMetricsStore(nil, 0)
	if ms.retention != DefaultRetention {
		t.Errorf("retention = %v, want %v", ms.retention, DefaultRetention)
	}
	snap := ms.Snapshot()
	if snap.TasksCompleted24h != 0 {
		t.Errorf("expected empty snapshot")
	}
}

func TestMetricsStore_ErrorRateCalculation(t *testing.T) {
	ms := newTestStore()

	// 3 successes
	for i := 0; i < 3; i++ {
		e := mustEvent(t, TaskCompleted, "agent1", "dev", "", TaskCompletedData{
			TaskID: "t", Duration: Duration{time.Minute},
		})
		e.Timestamp = ms.now()
		ms.handleEvent(e)
	}
	// 1 failure
	e := mustEvent(t, TaskFailed, "agent1", "dev", "", TaskFailedData{
		TaskID: "t", Error: "err",
	})
	e.Timestamp = ms.now()
	ms.handleEvent(e)

	snap := ms.Snapshot()
	if snap.ErrorRatePct != 25 {
		t.Errorf("ErrorRatePct = %f, want 25", snap.ErrorRatePct)
	}
}

func TestMetricsStore_EmptySnapshot(t *testing.T) {
	ms := newTestStore()
	snap := ms.Snapshot()
	if snap.TasksCompleted24h != 0 || snap.ErrorRatePct != 0 || snap.AvgTaskDurationSec != 0 {
		t.Error("expected all zeros for empty snapshot")
	}
	if snap.RestartsByAgent == nil || snap.AgentUtilization == nil || snap.TasksByRole == nil {
		t.Error("expected non-nil maps in empty snapshot")
	}
	if snap.HourlyCompletions == nil {
		t.Error("expected non-nil HourlyCompletions slice")
	}
}

func TestMetricsStore_SnapshotJSON(t *testing.T) {
	ms := newTestStore()
	snap := ms.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Error("invalid JSON")
	}
}

func TestMetricsStore_StoppedWithoutStart(t *testing.T) {
	ms := newTestStore()
	stopped := mustEvent(t, AgentStopped, "agent1", "", "", AgentStoppedData{PID: 100, ExitCode: 1})
	stopped.Timestamp = ms.now()
	ms.handleEvent(stopped)

	snap := ms.Snapshot()
	// Should not panic, utilization should be 0
	util := snap.AgentUtilization["agent1"]
	if util != 0 {
		t.Errorf("expected 0 utilization for agent stopped without start, got %f", util)
	}
}
