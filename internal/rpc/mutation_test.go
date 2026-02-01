package rpc

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMutationConstants(t *testing.T) {
	t.Parallel()

	expectedMutations := map[string]string{
		"create":   MutationCreate,
		"update":   MutationUpdate,
		"delete":   MutationDelete,
		"comment":  MutationComment,
		"bonded":   MutationBonded,
		"squashed": MutationSquashed,
		"burned":   MutationBurned,
		"status":   MutationStatus,
	}

	for want, got := range expectedMutations {
		if got != want {
			t.Errorf("Mutation constant %q = %q, want %q", want, got, want)
		}
	}
}

func TestMutationEvent_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Millisecond)

	original := MutationEvent{
		Type:      MutationUpdate,
		IssueID:   "bd-123",
		Title:     "Test Issue",
		Assignee:  "alice",
		Actor:     "bob",
		Timestamp: now,
		OldStatus: "open",
		NewStatus: "in_progress",
		ParentID:  "bd-parent",
		StepCount: 5,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored MutationEvent
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if restored.Type != original.Type {
		t.Errorf("Type = %q, want %q", restored.Type, original.Type)
	}
	if restored.IssueID != original.IssueID {
		t.Errorf("IssueID = %q, want %q", restored.IssueID, original.IssueID)
	}
	if restored.Title != original.Title {
		t.Errorf("Title = %q, want %q", restored.Title, original.Title)
	}
	if restored.Assignee != original.Assignee {
		t.Errorf("Assignee = %q, want %q", restored.Assignee, original.Assignee)
	}
	if restored.Actor != original.Actor {
		t.Errorf("Actor = %q, want %q", restored.Actor, original.Actor)
	}
	if !restored.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", restored.Timestamp, original.Timestamp)
	}
	if restored.OldStatus != original.OldStatus {
		t.Errorf("OldStatus = %q, want %q", restored.OldStatus, original.OldStatus)
	}
	if restored.NewStatus != original.NewStatus {
		t.Errorf("NewStatus = %q, want %q", restored.NewStatus, original.NewStatus)
	}
	if restored.ParentID != original.ParentID {
		t.Errorf("ParentID = %q, want %q", restored.ParentID, original.ParentID)
	}
	if restored.StepCount != original.StepCount {
		t.Errorf("StepCount = %d, want %d", restored.StepCount, original.StepCount)
	}
}

func TestMutationEvent_OmitEmpty(t *testing.T) {
	t.Parallel()

	// Minimal event - optional fields should be omitted
	event := MutationEvent{
		Type:      MutationCreate,
		IssueID:   "bd-123",
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	jsonStr := string(data)

	// These fields have omitempty and should not appear when empty
	omitEmptyFields := []string{
		`"old_status"`,
		`"new_status"`,
		`"parent_id"`,
		`"step_count"`,
	}

	for _, field := range omitEmptyFields {
		if strings.Contains(jsonStr, field) {
			t.Errorf("Empty field %s should be omitted, got: %s", field, jsonStr)
		}
	}
}

func TestMutationEvent_JSONTagConsistency(t *testing.T) {
	t.Parallel()

	// Document the mixed JSON tag format
	// The struct uses snake_case for optional metadata fields
	event := MutationEvent{
		Type:      MutationStatus,
		IssueID:   "bd-123",
		Timestamp: time.Now(),
		OldStatus: "open",
		NewStatus: "closed",
		ParentID:  "bd-parent",
		StepCount: 3,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	jsonStr := string(data)

	// Verify snake_case JSON tags are used for optional fields
	expectedTags := []string{
		`"old_status"`,
		`"new_status"`,
		`"parent_id"`,
		`"step_count"`,
	}

	for _, tag := range expectedTags {
		if !strings.Contains(jsonStr, tag) {
			t.Errorf("Expected JSON tag %s not found in: %s", tag, jsonStr)
		}
	}
}

func TestMutationEvent_AllTypes(t *testing.T) {
	t.Parallel()

	allTypes := []string{
		MutationCreate,
		MutationUpdate,
		MutationDelete,
		MutationComment,
		MutationBonded,
		MutationSquashed,
		MutationBurned,
		MutationStatus,
	}

	for _, mutationType := range allTypes {
		t.Run(mutationType, func(t *testing.T) {
			event := MutationEvent{
				Type:      mutationType,
				IssueID:   "bd-test",
				Timestamp: time.Now(),
			}

			data, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("json.Marshal() error for type %s: %v", mutationType, err)
			}

			var restored MutationEvent
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatalf("json.Unmarshal() error for type %s: %v", mutationType, err)
			}

			if restored.Type != mutationType {
				t.Errorf("Type = %q, want %q", restored.Type, mutationType)
			}
		})
	}
}
