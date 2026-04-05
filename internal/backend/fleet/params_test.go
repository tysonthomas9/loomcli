package fleet

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- updateParamsToPatchRequest tests ---

func TestUpdateParamsToPatchRequest_AgentState_Set(t *testing.T) {
	state := "running"
	params := backend.UpdateParams{
		AgentState: &state,
	}

	req := updateParamsToPatchRequest(params)

	got, ok := req["agent_state"]
	if !ok {
		t.Fatal("expected agent_state key in patch request map")
	}
	if got != "running" {
		t.Errorf("agent_state = %v, want %q", got, "running")
	}
}

func TestUpdateParamsToPatchRequest_AgentState_Nil(t *testing.T) {
	params := backend.UpdateParams{} // AgentState is nil

	req := updateParamsToPatchRequest(params)

	if _, ok := req["agent_state"]; ok {
		t.Error("expected agent_state to be omitted when nil")
	}
}

func TestUpdateParamsToPatchRequest_AgentState_EmptyString(t *testing.T) {
	empty := ""
	params := backend.UpdateParams{
		AgentState: &empty,
	}

	req := updateParamsToPatchRequest(params)

	got, ok := req["agent_state"]
	if !ok {
		t.Fatal("expected agent_state key for empty-string pointer")
	}
	if got != "" {
		t.Errorf("agent_state = %v, want empty string", got)
	}
}

func TestUpdateParamsToPatchRequest_AgentState_WithOtherFields(t *testing.T) {
	state := "stuck"
	status := "in_progress"
	params := backend.UpdateParams{
		Status:     &status,
		AgentState: &state,
	}

	req := updateParamsToPatchRequest(params)

	if req["status"] != "in_progress" {
		t.Errorf("status = %v, want %q", req["status"], "in_progress")
	}
	if req["agent_state"] != "stuck" {
		t.Errorf("agent_state = %v, want %q", req["agent_state"], "stuck")
	}
}
