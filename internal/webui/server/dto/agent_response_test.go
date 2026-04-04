package dto

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentStatusResponse_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	lastActivity := time.Date(2026, 4, 3, 11, 30, 0, 0, time.UTC)

	resp := AgentStatusResponse{
		ID:           "agent-1",
		Title:        "falcon",
		Description:  "Primary implementation agent",
		Status:       "in_progress",
		State:        "running",
		RoleType:     "crew",
		Rig:          "standard",
		HookBead:     "hook-abc",
		RoleBead:     "role-xyz",
		LastActivity: &lastActivity,
		CreatedAt:    now,
		UpdatedAt:    now,
		Labels:       []string{"team-a", "priority"},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got AgentStatusResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != resp.ID {
		t.Errorf("ID = %q, want %q", got.ID, resp.ID)
	}
	if got.Title != resp.Title {
		t.Errorf("Title = %q, want %q", got.Title, resp.Title)
	}
	if got.Description != resp.Description {
		t.Errorf("Description = %q, want %q", got.Description, resp.Description)
	}
	if got.Status != resp.Status {
		t.Errorf("Status = %q, want %q", got.Status, resp.Status)
	}
	if got.State != resp.State {
		t.Errorf("State = %q, want %q", got.State, resp.State)
	}
	if got.RoleType != resp.RoleType {
		t.Errorf("RoleType = %q, want %q", got.RoleType, resp.RoleType)
	}
	if got.Rig != resp.Rig {
		t.Errorf("Rig = %q, want %q", got.Rig, resp.Rig)
	}
	if got.HookBead != resp.HookBead {
		t.Errorf("HookBead = %q, want %q", got.HookBead, resp.HookBead)
	}
	if got.RoleBead != resp.RoleBead {
		t.Errorf("RoleBead = %q, want %q", got.RoleBead, resp.RoleBead)
	}
	if got.LastActivity == nil || !got.LastActivity.Equal(lastActivity) {
		t.Errorf("LastActivity = %v, want %v", got.LastActivity, lastActivity)
	}
	if !got.CreatedAt.Equal(resp.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, resp.CreatedAt)
	}
	if !got.UpdatedAt.Equal(resp.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, resp.UpdatedAt)
	}
	if len(got.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(got.Labels))
	}
}

func TestAgentStatusResponse_EmptyLabels(t *testing.T) {
	resp := AgentStatusResponse{Labels: []string{}}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["labels"]
	if !ok {
		t.Fatal("labels should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("labels = %s, want []", val)
	}
}

// TestAgentStatusResponse_NilLabels documents raw Go serialization behavior.
// The mapping layer MUST initialize Labels to []string{} before constructing
// AgentStatusResponse. Sending null to the frontend is a bug.
func TestAgentStatusResponse_NilLabels(t *testing.T) {
	resp := AgentStatusResponse{Labels: nil}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["labels"]
	if !ok {
		t.Fatal("labels field omitted from JSON output")
	}
	if string(val) != "null" {
		t.Errorf("labels = %s, want null", val)
	}
}

func TestAgentStatusResponse_OptionalFieldsOmitted(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	resp := AgentStatusResponse{
		ID:        "agent-1",
		Title:     "falcon",
		CreatedAt: now,
		UpdatedAt: now,
		Labels:    []string{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{
		"description", "status", "agent_state",
		"role_type", "rig", "hook_bead", "role_bead",
		"last_activity",
	} {
		if _, ok := raw[field]; ok {
			t.Errorf("%s should be omitted when zero/nil, but present: %s", field, raw[field])
		}
	}
}

func TestAgentStatusResponse_StatusAsString(t *testing.T) {
	resp := AgentStatusResponse{
		Status: "open",
		Labels: []string{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got AgentStatusResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Status != "open" {
		t.Errorf("Status = %q, want %q", got.Status, "open")
	}
}

func TestAgentStatusResponse_StateAsString(t *testing.T) {
	resp := AgentStatusResponse{
		State:  "running",
		Labels: []string{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got AgentStatusResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.State != "running" {
		t.Errorf("State = %q, want %q", got.State, "running")
	}
}

func TestAgentStatusResponse_TimestampsPreserved(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 4, 3, 15, 30, 0, 0, time.UTC)

	resp := AgentStatusResponse{
		CreatedAt: created,
		UpdatedAt: updated,
		Labels:    []string{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got AgentStatusResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
	if !got.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, updated)
	}
}

func TestAgentStatusResponse_LastActivityPresent(t *testing.T) {
	activity := time.Date(2026, 4, 3, 14, 0, 0, 0, time.UTC)
	resp := AgentStatusResponse{
		LastActivity: &activity,
		Labels:       []string{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	if _, ok := raw["last_activity"]; !ok {
		t.Fatal("last_activity should be present when non-nil")
	}

	var got AgentStatusResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.LastActivity == nil || !got.LastActivity.Equal(activity) {
		t.Errorf("LastActivity = %v, want %v", got.LastActivity, activity)
	}
}
