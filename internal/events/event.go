package events

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration wraps time.Duration with human-readable JSON serialization (e.g. "1m30s").
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

// EventType identifies the kind of event.
type EventType string

const (
	TaskClaimed      EventType = "task.claimed"
	TaskStarted      EventType = "task.started"
	TaskCompleted    EventType = "task.completed"
	TaskFailed       EventType = "task.failed"
	TaskStuck        EventType = "task.stuck"
	AgentStarted     EventType = "agent.started"
	AgentRestarted   EventType = "agent.restarted"
	AgentStopped     EventType = "agent.stopped"
	EpicAssigned     EventType = "epic.assigned"
	EpicExhausted    EventType = "epic.exhausted"
	PRCreated        EventType = "pr.created"
	ConflictResolved EventType = "conflict.resolved"
	HealthCheck      EventType = "system.health_check"
	DaemonDegraded   EventType = "system.daemon_degraded"
	ConfigReloaded   EventType = "system.config_reloaded"
	CircuitOpened    EventType = "circuit.opened"
	CircuitClosed    EventType = "circuit.closed"
)

// Event is the envelope written to JSONL files. Data is stored as json.RawMessage
// so that JSON round-trips preserve typed data without losing type information.
//
// Trace fields:
//   - TraceParent / TraceState: W3C trace-context captured at emit time. Empty
//     when the emitter had no active span (or tracing was disabled). The
//     otelexport consumer rebuilds the parent span context from these so
//     event-driven spans (loom.task, loom.agent.lifecycle) connect to the
//     originating request rather than fragmenting into their own traces.
//
// Older JSONL records without these fields decode normally — the otelexport
// consumer treats absent fields as "no parent, use the existing
// context.Background() path" so backward compatibility is preserved.
type Event struct {
	Type        EventType       `json:"type"`
	Timestamp   time.Time       `json:"timestamp"`
	Agent       string          `json:"agent,omitempty"`
	Role        string          `json:"role,omitempty"`
	EpicID      string          `json:"epic_id,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	TraceParent string          `json:"traceparent,omitempty"`
	TraceState  string          `json:"tracestate,omitempty"`
}

// NewEvent creates an Event, marshaling v into the Data field.
// Timestamp is set to Now(); callers may override it after construction.
// If v is nil, Data is left empty.
func NewEvent(eventType EventType, agent, role, epicID string, v interface{}) (Event, error) {
	e := Event{
		Type:      eventType,
		Timestamp: Now(),
		Agent:     agent,
		Role:      role,
		EpicID:    epicID,
	}
	if v != nil {
		raw, err := json.Marshal(v)
		if err != nil {
			return Event{}, fmt.Errorf("marshaling event data: %w", err)
		}
		e.Data = raw
	}
	return e, nil
}

// eventDataTargets maps each event type to a constructor for its payload
// struct. A table rather than a switch: the switch form crossed the repo's
// cyclomatic-complexity budget as the event set grew, and every arm was pure
// dispatch with no logic to lose in the translation.
var eventDataTargets = map[EventType]func() interface{}{
	TaskClaimed:      func() interface{} { return &TaskClaimedData{} },
	TaskStarted:      func() interface{} { return &TaskStartedData{} },
	TaskCompleted:    func() interface{} { return &TaskCompletedData{} },
	TaskFailed:       func() interface{} { return &TaskFailedData{} },
	TaskStuck:        func() interface{} { return &TaskStuckData{} },
	AgentStarted:     func() interface{} { return &AgentStartedData{} },
	AgentRestarted:   func() interface{} { return &AgentRestartedData{} },
	AgentStopped:     func() interface{} { return &AgentStoppedData{} },
	EpicAssigned:     func() interface{} { return &EpicAssignedData{} },
	EpicExhausted:    func() interface{} { return &EpicExhaustedData{} },
	PRCreated:        func() interface{} { return &PRCreatedData{} },
	ConflictResolved: func() interface{} { return &ConflictResolvedData{} },
	HealthCheck:      func() interface{} { return &HealthCheckData{} },
	DaemonDegraded:   func() interface{} { return &DaemonDegradedData{} },
	ConfigReloaded:   func() interface{} { return &ConfigReloadedData{} },
	CircuitOpened:    func() interface{} { return &CircuitOpenedData{} },
	CircuitClosed:    func() interface{} { return &CircuitClosedData{} },
}

// DecodeData unmarshals the Data field into the correct typed struct based on Event.Type.
// Returns nil if Data is empty.
func (e *Event) DecodeData() (interface{}, error) {
	if len(e.Data) == 0 {
		return nil, nil
	}
	newTarget, ok := eventDataTargets[e.Type]
	if !ok {
		return nil, fmt.Errorf("unknown event type: %s", e.Type)
	}
	target := newTarget()
	if err := json.Unmarshal(e.Data, target); err != nil {
		return nil, fmt.Errorf("unmarshaling %s data: %w", e.Type, err)
	}
	return target, nil
}

// Typed data structs for each event kind.

type TaskClaimedData struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
}

type TaskStartedData struct {
	TaskID string `json:"task_id"`
}

type TaskCompletedData struct {
	TaskID       string   `json:"task_id"`
	Duration     Duration `json:"duration"`
	FilesChanged int      `json:"files_changed"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
}

type TaskFailedData struct {
	TaskID     string `json:"task_id"`
	Error      string `json:"error"`
	ErrorClass string `json:"error_class,omitempty"`
	RetryAfter string `json:"retry_after,omitempty"`
}

// TaskStuckData reports a task that failed repeatedly across consecutive
// auto-mode invocations and was skipped to allow the loop to make progress on
// other tasks.
type TaskStuckData struct {
	TaskID              string `json:"task_id"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastError           string `json:"last_error"`
}

type AgentStartedData struct {
	PID int `json:"pid"`
}

type AgentRestartedData struct {
	PID          int `json:"pid"`
	RestartCount int `json:"restart_count"`
}

type AgentStoppedData struct {
	PID        int    `json:"pid"`
	ExitCode   int    `json:"exit_code"`
	StopReason string `json:"stop_reason,omitempty"`
}

type EpicAssignedData struct {
	EpicID string `json:"epic_id"`
}

type EpicExhaustedData struct {
	EpicID string `json:"epic_id"`
}

type PRCreatedData struct {
	EpicID string `json:"epic_id"`
	URL    string `json:"url"`
}

type ConflictResolvedData struct {
	File     string `json:"file"`
	Strategy string `json:"strategy"`
}

type HealthCheckData struct {
	AgentCount   int `json:"agent_count"`
	HealthyCount int `json:"healthy_count"`
}

// DaemonDegradedData reports that the daemon has entered (Active true) or left
// (Active false) a self-reported degradation. It is published on the events bus
// specifically because the bus does not share a failure mode with the handles
// that degrade: the outage this exists for made the daemon's own state file
// unwritable, so a degradation recorded only there would have been unreadable
// exactly when it mattered.
//
// Since, Count and LastErr describe the episode while it is active. The
// recovery event (Active false) carries only Kind: it is published after the
// episode has been cleared, so there is no longer an episode to report. Its
// duration is recoverable by pairing it with the entry event that opened it.
type DaemonDegradedData struct {
	Kind    string    `json:"kind"`
	Active  bool      `json:"active"`
	Since   time.Time `json:"since"`
	Count   int       `json:"count"`
	LastErr string    `json:"last_err,omitempty"`
}

type ConfigReloadedData struct {
	Added    int    `json:"added"`
	Removed  int    `json:"removed"`
	Modified int    `json:"modified"`
	Error    string `json:"error,omitempty"`
}

// CircuitOpenedData reports that a rate-limit circuit breaker has tripped and
// work is being paused for a cooldown period.
type CircuitOpenedData struct {
	RateLimitCount   int      `json:"rate_limit_count"`
	WindowDuration   Duration `json:"window_duration"`
	CooldownDuration Duration `json:"cooldown_duration"`
}

// CircuitClosedData reports that a rate-limit circuit breaker has reset after
// a successful probe invocation.
type CircuitClosedData struct {
	Reason string `json:"reason,omitempty"`
}
