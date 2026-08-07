package automation

import (
	"encoding/json"
	"testing"
)

// TestTriggerEventNormalizeProvenance covers zero-value back-compat for the
// fleet-db provenance mirror: records that round-trip without origin (written
// before the field existed) normalize to external/0; stamped provenance is
// left untouched.
func TestTriggerEventNormalizeProvenance(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantOrigin EventOrigin
		wantHop    int
	}{
		{
			name: "old record without provenance defaults to external",
			payload: `{
				"workspace_key": "WS",
				"event_id": "event-old",
				"source_kind": "webhook",
				"event_type": "push",
				"occurred_at": "2026-01-02T03:04:05Z",
				"received_at": "2026-01-02T03:04:05Z"
			}`,
			wantOrigin: EventOriginExternal,
			wantHop:    0,
		},
		{
			name: "workflow provenance passes through",
			payload: `{
				"workspace_key": "WS",
				"event_id": "event-wf",
				"source_kind": "workflow",
				"event_type": "loom.review_requested",
				"origin": "workflow",
				"hop_depth": 2,
				"occurred_at": "2026-01-02T03:04:05Z",
				"received_at": "2026-01-02T03:04:05Z"
			}`,
			wantOrigin: EventOriginWorkflow,
			wantHop:    2,
		},
		{
			name: "system provenance passes through",
			payload: `{
				"workspace_key": "WS",
				"event_id": "event-sys",
				"source_kind": "cron",
				"event_type": "schedule.tick",
				"origin": "system",
				"occurred_at": "2026-01-02T03:04:05Z",
				"received_at": "2026-01-02T03:04:05Z"
			}`,
			wantOrigin: EventOriginSystem,
			wantHop:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event Event
			if err := json.Unmarshal([]byte(tt.payload), &event); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			event.NormalizeProvenance()
			if event.Origin != tt.wantOrigin || event.HopDepth != tt.wantHop {
				t.Fatalf("provenance = %s/%d, want %s/%d", event.Origin, event.HopDepth, tt.wantOrigin, tt.wantHop)
			}
		})
	}
}
