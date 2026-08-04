package events

import (
	"sync"
	"time"
)

const DefaultRetention = 24 * time.Hour

// TaskMetric records a completed or failed task.
type TaskMetric struct {
	Timestamp    time.Time
	Agent        string
	Role         string
	EpicID       string
	Duration     time.Duration
	Success      bool
	LinesAdded   int
	LinesRemoved int
	FilesChanged int
	TaskID       string
}

// AgentMetric records an agent lifecycle event.
type AgentMetric struct {
	Timestamp    time.Time
	Agent        string
	EventType    EventType
	RestartCount int
	ExitCode     int
}

// agentUtilState tracks work/idle transitions for utilization calculation.
type agentUtilState struct {
	lastStart    time.Time
	workDuration time.Duration
	isWorking    bool
}

// MetricsStore subscribes to the event Bus and maintains rolling aggregates.
type MetricsStore struct {
	mu        sync.RWMutex
	retention time.Duration
	now       func() time.Time

	tasks  []TaskMetric
	agents []AgentMetric

	// Monotonic counters (never reset, survive pruning)
	totalTasksCompleted int64
	totalTasksFailed    int64
	totalRestarts       int64

	utilization map[string]*agentUtilState
}

// NewMetricsStore creates a MetricsStore and subscribes it to the Bus.
// If bus is nil, returns a standalone store (useful for testing or replay-only).
func NewMetricsStore(bus *Bus, retention time.Duration) *MetricsStore {
	if retention <= 0 {
		retention = DefaultRetention
	}
	ms := &MetricsStore{
		retention:   retention,
		now:         time.Now,
		utilization: make(map[string]*agentUtilState),
	}
	if bus != nil {
		bus.Subscribe(ms.handleEvent)
	}
	return ms
}

// handleEvent is the Listener callback that records events into the store.
func (ms *MetricsStore) handleEvent(e Event) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	switch e.Type {
	case TaskCompleted:
		ms.handleTaskCompleted(e)
	case TaskFailed:
		ms.handleTaskFailed(e)
	case TaskStuck:
		ms.handleTaskStuck(e)
	case AgentStarted:
		ms.handleAgentStarted(e)
	case AgentStopped:
		ms.handleAgentStopped(e)
	case AgentRestarted:
		ms.handleAgentRestarted(e)
	}
}

func (ms *MetricsStore) handleTaskCompleted(e Event) {
	raw, err := e.DecodeData()
	if err != nil {
		return
	}
	data, ok := raw.(*TaskCompletedData)
	if !ok {
		return
	}
	ms.tasks = append(ms.tasks, TaskMetric{
		Timestamp:    e.Timestamp,
		Agent:        e.Agent,
		Role:         e.Role,
		EpicID:       e.EpicID,
		Duration:     data.Duration.Duration,
		Success:      true,
		LinesAdded:   data.LinesAdded,
		LinesRemoved: data.LinesRemoved,
		FilesChanged: data.FilesChanged,
		TaskID:       data.TaskID,
	})
	ms.totalTasksCompleted++
}

func (ms *MetricsStore) handleTaskFailed(e Event) {
	raw, err := e.DecodeData()
	if err != nil {
		return
	}
	data, ok := raw.(*TaskFailedData)
	if !ok {
		return
	}
	ms.tasks = append(ms.tasks, TaskMetric{
		Timestamp: e.Timestamp,
		Agent:     e.Agent,
		Role:      e.Role,
		EpicID:    e.EpicID,
		Success:   false,
		TaskID:    data.TaskID,
	})
	ms.totalTasksFailed++
}

// handleTaskStuck is intentionally a no-op for totalTasksFailed / ms.tasks.
// A task.stuck event is always preceded by the task.failed event that pushed
// sameTaskFailures over the threshold — the underlying failure is already
// recorded by handleTaskFailed, so double-counting here would inflate the
// error rate. We still accept the event via the switch in handleEvent so
// downstream listeners (web UI, replay, otelexport) can observe stuck
// classifications separately.
func (ms *MetricsStore) handleTaskStuck(e Event) {
	_, _ = e.DecodeData()
}

func (ms *MetricsStore) handleAgentStarted(e Event) {
	if _, err := e.DecodeData(); err != nil {
		return
	}
	ms.agents = append(ms.agents, AgentMetric{
		Timestamp: e.Timestamp,
		Agent:     e.Agent,
		EventType: e.Type,
	})
	state := ms.getOrCreateUtil(e.Agent)
	if state.isWorking && !state.lastStart.IsZero() {
		state.workDuration += e.Timestamp.Sub(state.lastStart)
	}
	state.lastStart = e.Timestamp
	state.isWorking = true
}

func (ms *MetricsStore) handleAgentStopped(e Event) {
	raw, err := e.DecodeData()
	if err != nil {
		return
	}
	data, ok := raw.(*AgentStoppedData)
	if !ok {
		return
	}
	ms.agents = append(ms.agents, AgentMetric{
		Timestamp: e.Timestamp,
		Agent:     e.Agent,
		EventType: e.Type,
		ExitCode:  data.ExitCode,
	})
	state := ms.getOrCreateUtil(e.Agent)
	if state.isWorking && !state.lastStart.IsZero() {
		state.workDuration += e.Timestamp.Sub(state.lastStart)
	}
	state.isWorking = false
}

func (ms *MetricsStore) handleAgentRestarted(e Event) {
	raw, err := e.DecodeData()
	if err != nil {
		return
	}
	data, ok := raw.(*AgentRestartedData)
	if !ok {
		return
	}
	ms.agents = append(ms.agents, AgentMetric{
		Timestamp:    e.Timestamp,
		Agent:        e.Agent,
		EventType:    e.Type,
		RestartCount: data.RestartCount,
	})
	ms.totalRestarts++
	state := ms.getOrCreateUtil(e.Agent)
	if state.isWorking && !state.lastStart.IsZero() {
		state.workDuration += e.Timestamp.Sub(state.lastStart)
	}
	state.lastStart = e.Timestamp
	state.isWorking = true
}

func (ms *MetricsStore) getOrCreateUtil(agent string) *agentUtilState {
	state, ok := ms.utilization[agent]
	if !ok {
		state = &agentUtilState{}
		ms.utilization[agent] = state
	}
	return state
}

// Prune removes entries older than the retention window.
func (ms *MetricsStore) Prune() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.pruneLocked()
}

func (ms *MetricsStore) pruneLocked() {
	cutoff := ms.now().Add(-ms.retention)

	n := 0
	for _, t := range ms.tasks {
		if !t.Timestamp.Before(cutoff) {
			ms.tasks[n] = t
			n++
		}
	}
	ms.tasks = ms.tasks[:n]

	n = 0
	for _, a := range ms.agents {
		if !a.Timestamp.Before(cutoff) {
			ms.agents[n] = a
			n++
		}
	}
	ms.agents = ms.agents[:n]
}
