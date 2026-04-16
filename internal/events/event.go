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
	AgentStarted     EventType = "agent.started"
	AgentRestarted   EventType = "agent.restarted"
	AgentStopped     EventType = "agent.stopped"
	EpicAssigned     EventType = "epic.assigned"
	EpicExhausted    EventType = "epic.exhausted"
	PRCreated        EventType = "pr.created"
	ConflictResolved EventType = "conflict.resolved"
	HealthCheck      EventType = "system.health_check"
	ConfigReloaded   EventType = "system.config_reloaded"
)

// Event is the envelope written to JSONL files. Data is stored as json.RawMessage
// so that JSON round-trips preserve typed data without losing type information.
type Event struct {
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Agent     string          `json:"agent,omitempty"`
	Role      string          `json:"role,omitempty"`
	EpicID    string          `json:"epic_id,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
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

// DecodeData unmarshals the Data field into the correct typed struct based on Event.Type.
// Returns nil if Data is empty.
func (e *Event) DecodeData() (interface{}, error) {
	if len(e.Data) == 0 {
		return nil, nil
	}
	var target interface{}
	switch e.Type {
	case TaskClaimed:
		target = &TaskClaimedData{}
	case TaskStarted:
		target = &TaskStartedData{}
	case TaskCompleted:
		target = &TaskCompletedData{}
	case TaskFailed:
		target = &TaskFailedData{}
	case AgentStarted:
		target = &AgentStartedData{}
	case AgentRestarted:
		target = &AgentRestartedData{}
	case AgentStopped:
		target = &AgentStoppedData{}
	case EpicAssigned:
		target = &EpicAssignedData{}
	case EpicExhausted:
		target = &EpicExhaustedData{}
	case PRCreated:
		target = &PRCreatedData{}
	case ConflictResolved:
		target = &ConflictResolvedData{}
	case HealthCheck:
		target = &HealthCheckData{}
	case ConfigReloaded:
		target = &ConfigReloadedData{}
	default:
		return nil, fmt.Errorf("unknown event type: %s", e.Type)
	}
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

type ConfigReloadedData struct {
	Added    int    `json:"added"`
	Removed  int    `json:"removed"`
	Modified int    `json:"modified"`
	Error    string `json:"error,omitempty"`
}
