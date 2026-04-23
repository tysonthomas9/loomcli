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
		`"title"`,
		`"assignee"`,
		`"actor"`,
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

	// Verify required fields ARE present
	requiredFields := []string{
		`"type"`,
		`"issue_id"`,
		`"timestamp"`,
	}

	for _, field := range requiredFields {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("Required field %s should be present, got: %s", field, jsonStr)
		}
	}
}

func TestMutationEvent_JSONTagConsistency(t *testing.T) {
	t.Parallel()

	// Verify all fields use consistent snake_case JSON tags
	event := MutationEvent{
		Type:      MutationStatus,
		IssueID:   "bd-123",
		Title:     "Test Issue",
		Assignee:  "alice",
		Actor:     "bob",
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

	// Verify snake_case JSON tags are used for all fields
	expectedTags := []string{
		`"type"`,
		`"issue_id"`,
		`"title"`,
		`"assignee"`,
		`"actor"`,
		`"timestamp"`,
		`"old_status"`,
		`"new_status"`,
		`"parent_id"`,
		`"step_count"`,
	}

	for _, tag := range expectedTags {
		if !strings.Contains(jsonStr, tag) {
			t.Errorf("Expected snake_case JSON tag %s not found in: %s", tag, jsonStr)
		}
	}

	// Verify PascalCase keys are NOT present (regression test)
	unexpectedTags := []string{
		`"Type"`,
		`"IssueID"`,
		`"Title"`,
		`"Assignee"`,
		`"Actor"`,
		`"Timestamp"`,
	}

	for _, tag := range unexpectedTags {
		if strings.Contains(jsonStr, tag) {
			t.Errorf("Unexpected PascalCase JSON tag %s found in: %s", tag, jsonStr)
		}
	}
}

func TestMutationEvent_IssueField_RoundTrip(t *testing.T) {
	t.Parallel()

	// Populate with a representative lightweight issue payload (no description
	// / design / acceptance_criteria / notes, matching marshalLightweightIssue).
	issueJSON := json.RawMessage(`{"id":"bd-1","title":"T","priority":1,"status":"open","assignee":"alice"}`)

	original := MutationEvent{
		Type:      MutationCreate,
		IssueID:   "bd-1",
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
		Issue:     issueJSON,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored MutationEvent
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if len(restored.Issue) == 0 {
		t.Fatal("expected restored.Issue to be non-empty")
	}

	// Compare as decoded objects rather than bytes — whitespace/key-ordering in
	// RawMessage is not stable across Marshal/Unmarshal round-trips.
	var gotObj, wantObj map[string]any
	if err := json.Unmarshal(restored.Issue, &gotObj); err != nil {
		t.Fatalf("unmarshal restored.Issue: %v", err)
	}
	if err := json.Unmarshal(issueJSON, &wantObj); err != nil {
		t.Fatalf("unmarshal original issue: %v", err)
	}
	if len(gotObj) != len(wantObj) {
		t.Errorf("restored Issue has %d keys, want %d (got=%v want=%v)", len(gotObj), len(wantObj), gotObj, wantObj)
	}
	for k, v := range wantObj {
		if gv, ok := gotObj[k]; !ok || gv != v {
			t.Errorf("restored Issue[%q] = %v (present=%v), want %v", k, gv, ok, v)
		}
	}

	// The envelope must also contain an "issue" key (sanity-check omitempty
	// semantics aren't stripping a populated RawMessage).
	if !strings.Contains(string(data), `"issue"`) {
		t.Errorf("expected marshaled event to contain \"issue\" key, got: %s", data)
	}
}

func TestMutationEvent_IssueField_OmitEmpty(t *testing.T) {
	t.Parallel()

	// Case 1: Issue is nil — must be omitted.
	nilEvent := MutationEvent{
		Type:      MutationDelete,
		IssueID:   "bd-42",
		Timestamp: time.Now(),
	}
	nilData, err := json.Marshal(nilEvent)
	if err != nil {
		t.Fatalf("json.Marshal(nilEvent) error: %v", err)
	}
	if strings.Contains(string(nilData), `"issue"`) {
		t.Errorf("Issue=nil should not emit \"issue\" key, got: %s", nilData)
	}

	// Case 2: Issue is an empty []byte — treated by encoding/json as empty
	// RawMessage; documented behavior: omitempty triggers on len==0, so no key.
	emptyEvent := MutationEvent{
		Type:      MutationDelete,
		IssueID:   "bd-42",
		Timestamp: time.Now(),
		Issue:     json.RawMessage{},
	}
	emptyData, err := json.Marshal(emptyEvent)
	if err != nil {
		t.Fatalf("json.Marshal(emptyEvent) error: %v", err)
	}
	if strings.Contains(string(emptyData), `"issue"`) {
		t.Errorf("Issue=empty slice should not emit \"issue\" key, got: %s", emptyData)
	}

	// Case 3: Issue is explicit JSON "null" (non-empty bytes). omitempty does
	// NOT strip non-empty RawMessages, so "null" is emitted verbatim. Document
	// this so callers know to pass nil (not RawMessage("null")) to omit.
	nullEvent := MutationEvent{
		Type:      MutationDelete,
		IssueID:   "bd-42",
		Timestamp: time.Now(),
		Issue:     json.RawMessage("null"),
	}
	nullData, err := json.Marshal(nullEvent)
	if err != nil {
		t.Fatalf("json.Marshal(nullEvent) error: %v", err)
	}
	if !strings.Contains(string(nullData), `"issue":null`) {
		t.Errorf("Issue=RawMessage(\"null\") should emit \"issue\":null verbatim, got: %s", nullData)
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
