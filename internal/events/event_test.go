package events

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestEventType_Constants(t *testing.T) {
	tests := []struct {
		et   EventType
		want string
	}{
		{TaskClaimed, "task_claimed"},
		{TaskStarted, "task_started"},
		{TaskCompleted, "task_completed"},
		{TaskFailed, "task_failed"},
		{AgentStarted, "agent_started"},
		{AgentRestarted, "agent_restarted"},
		{AgentStopped, "agent_stopped"},
		{EpicAssigned, "epic_assigned"},
		{EpicExhausted, "epic_exhausted"},
		{PRCreated, "pr_created"},
		{ConflictResolved, "conflict_resolved"},
		{HealthCheck, "health_check"},
	}
	for _, tt := range tests {
		if string(tt.et) != tt.want {
			t.Errorf("EventType %q != %q", tt.et, tt.want)
		}
	}
}

func TestNewEvent_And_DecodeData(t *testing.T) {
	tests := []struct {
		name    string
		et      EventType
		data    interface{}
		checkFn func(t *testing.T, decoded interface{})
	}{
		{
			name: "task_claimed",
			et:   TaskClaimed,
			data: TaskClaimedData{TaskID: "t1", Title: "Fix bug"},
			checkFn: func(t *testing.T, decoded interface{}) {
				d := decoded.(*TaskClaimedData)
				if d.TaskID != "t1" || d.Title != "Fix bug" {
					t.Errorf("unexpected: %+v", d)
				}
			},
		},
		{
			name: "task_completed",
			et:   TaskCompleted,
			data: TaskCompletedData{TaskID: "t2", Duration: Duration{5 * time.Minute}, FilesChanged: 3, LinesAdded: 100, LinesRemoved: 20},
			checkFn: func(t *testing.T, decoded interface{}) {
				d := decoded.(*TaskCompletedData)
				if d.TaskID != "t2" || d.Duration.Duration != 5*time.Minute || d.FilesChanged != 3 {
					t.Errorf("unexpected: %+v", d)
				}
			},
		},
		{
			name: "agent_started",
			et:   AgentStarted,
			data: AgentStartedData{PID: 1234},
			checkFn: func(t *testing.T, decoded interface{}) {
				d := decoded.(*AgentStartedData)
				if d.PID != 1234 {
					t.Errorf("unexpected PID: %d", d.PID)
				}
			},
		},
		{
			name: "health_check",
			et:   HealthCheck,
			data: HealthCheckData{AgentCount: 5, HealthyCount: 4},
			checkFn: func(t *testing.T, decoded interface{}) {
				d := decoded.(*HealthCheckData)
				if d.AgentCount != 5 || d.HealthyCount != 4 {
					t.Errorf("unexpected: %+v", d)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := NewEvent(tt.et, "agent1", "task", "epic1", tt.data)
			if err != nil {
				t.Fatalf("NewEvent: %v", err)
			}
			if e.Type != tt.et {
				t.Errorf("Type = %q, want %q", e.Type, tt.et)
			}

			// Round-trip through JSON
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var decoded Event
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}

			result, err := decoded.DecodeData()
			if err != nil {
				t.Fatalf("DecodeData: %v", err)
			}
			tt.checkFn(t, result)
		})
	}
}

func TestNewEvent_NilData(t *testing.T) {
	e, err := NewEvent(AgentStarted, "a", "r", "", nil)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if e.Data != nil {
		t.Errorf("expected nil Data, got %s", e.Data)
	}
	result, err := e.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestDecodeData_UnknownType(t *testing.T) {
	e := Event{Type: "unknown", Data: json.RawMessage(`{}`)}
	_, err := e.DecodeData()
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestTaskCompletedData_DurationJSON(t *testing.T) {
	d := TaskCompletedData{TaskID: "t1", Duration: Duration{90 * time.Second}}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	// Verify human-readable format in JSON
	if !json.Valid(raw) {
		t.Fatal("invalid JSON")
	}
	// Should contain "1m30s" not a raw nanosecond number
	if !bytes.Contains(raw, []byte("1m30s")) {
		t.Errorf("expected duration string in JSON, got: %s", raw)
	}
	var decoded TaskCompletedData
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Duration.Duration != 90*time.Second {
		t.Errorf("Duration = %v, want 90s", decoded.Duration)
	}
}
