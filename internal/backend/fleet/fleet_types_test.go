package fleet

import (
	"encoding/json"
	"testing"
)

func TestAPIResponse_Unmarshal_Success(t *testing.T) {
	raw := `{"success":true,"data":{"id":"test-1"}}`
	var resp APIResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("Success = false, want true")
	}
	if resp.Data == nil {
		t.Error("Data should not be nil")
	}
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty", resp.Error)
	}
}

func TestAPIResponse_Unmarshal_Error(t *testing.T) {
	raw := `{"success":false,"error":"not found"}`
	var resp APIResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.Success {
		t.Error("Success = true, want false")
	}
	if resp.Error != "not found" {
		t.Errorf("Error = %q, want %q", resp.Error, "not found")
	}
}

func TestClaimResult_Unmarshal(t *testing.T) {
	raw := `{
		"payload": {
			"issue": {
				"id": "task-1",
				"title": "Do work",
				"status": "open",
				"priority": 2,
				"issue_type": "task",
				"created_at": "2026-01-01T00:00:00Z",
				"updated_at": "2026-01-01T00:00:00Z"
			},
			"labels": ["urgent", "fleet"],
			"dependencies": [
				{
					"issue_id": "task-1",
					"depends_on_id": "task-0",
					"type": "blocks",
					"created_at": "2026-01-01T00:00:00Z"
				}
			],
			"reason": "load balancing",
			"priority_override": 1
		}
	}`
	var cr ClaimResult
	if err := json.Unmarshal([]byte(raw), &cr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cr.Payload == nil {
		t.Fatal("Payload should not be nil")
	}
	if cr.Payload.Issue == nil {
		t.Fatal("Payload.Issue should not be nil")
	}
	if cr.Payload.Issue.ID != "task-1" {
		t.Errorf("Issue.ID = %q, want %q", cr.Payload.Issue.ID, "task-1")
	}
	if len(cr.Payload.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(cr.Payload.Labels))
	}
	if len(cr.Payload.Dependencies) != 1 {
		t.Errorf("Dependencies len = %d, want 1", len(cr.Payload.Dependencies))
	}
	if cr.Payload.Reason != "load balancing" {
		t.Errorf("Reason = %q, want %q", cr.Payload.Reason, "load balancing")
	}
	if cr.Payload.PriorityOverride == nil || *cr.Payload.PriorityOverride != 1 {
		t.Errorf("PriorityOverride = %v, want 1", cr.Payload.PriorityOverride)
	}
}

func TestRegisterResult_Unmarshal(t *testing.T) {
	raw := `{"token":"abc-123-xyz"}`
	var rr RegisterResult
	if err := json.Unmarshal([]byte(raw), &rr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rr.Token != "abc-123-xyz" {
		t.Errorf("Token = %q, want %q", rr.Token, "abc-123-xyz")
	}
}

func TestDoneResult_Unmarshal(t *testing.T) {
	raw := `{"task_id":"task-1","worker_id":"worker-42"}`
	var dr DoneResult
	if err := json.Unmarshal([]byte(raw), &dr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dr.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", dr.TaskID, "task-1")
	}
	if dr.WorkerID != "worker-42" {
		t.Errorf("WorkerID = %q, want %q", dr.WorkerID, "worker-42")
	}
}

func TestHeartbeatResult_Unmarshal(t *testing.T) {
	raw := `{"last_heartbeat":"2026-01-15T10:30:00Z"}`
	var hr HeartbeatResult
	if err := json.Unmarshal([]byte(raw), &hr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if hr.LastHeartbeat.IsZero() {
		t.Error("LastHeartbeat should not be zero")
	}
	if hr.LastHeartbeat.Hour() != 10 {
		t.Errorf("LastHeartbeat hour = %d, want 10", hr.LastHeartbeat.Hour())
	}
}
