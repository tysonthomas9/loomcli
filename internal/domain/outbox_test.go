package domain

import "testing"

func TestTaskRunEventID(t *testing.T) {
	tests := []struct {
		name      string
		taskRunID string
		attempt   int
		eventType TaskRunEventType
		want      string
	}{
		{
			name:      "queued first attempt",
			taskRunID: "tr-123",
			attempt:   0,
			eventType: TaskRunEventQueued,
			want:      "tr-123#0#taskRunQueued",
		},
		{
			name:      "completed later attempt",
			taskRunID: "tr-abc",
			attempt:   3,
			eventType: TaskRunEventCompleted,
			want:      "tr-abc#3#taskRunCompleted",
		},
		{
			name:      "negative attempt is rendered verbatim",
			taskRunID: "tr-x",
			attempt:   -1,
			eventType: TaskRunEventFailed,
			want:      "tr-x#-1#taskRunFailed",
		},
		{
			name:      "empty task run id",
			taskRunID: "",
			attempt:   0,
			eventType: TaskRunEventCancelled,
			want:      "#0#taskRunCancelled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TaskRunEventID(tt.taskRunID, tt.attempt, tt.eventType)
			if got != tt.want {
				t.Errorf("TaskRunEventID(%q, %d, %q) = %q, want %q",
					tt.taskRunID, tt.attempt, tt.eventType, got, tt.want)
			}
		})
	}
}

func TestTaskRunEventIDDeterministic(t *testing.T) {
	a := TaskRunEventID("tr-1", 2, TaskRunEventRequeued)
	b := TaskRunEventID("tr-1", 2, TaskRunEventRequeued)
	if a != b {
		t.Errorf("same inputs produced different IDs: %q vs %q", a, b)
	}
	c := TaskRunEventID("tr-1", 3, TaskRunEventRequeued)
	if a == c {
		t.Errorf("different attempts produced the same ID: %q", a)
	}
}
