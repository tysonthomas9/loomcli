package rpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestMarshalLightweightIssue_PreservesLightFields verifies that the light,
// display-oriented fields (id/title/priority/status/assignee) survive
// marshaling unchanged and that the heavy text fields are stripped.
func TestMarshalLightweightIssue_PreservesLightFields(t *testing.T) {
	t.Parallel()

	issue := &types.Issue{
		ID:                 "bd-42",
		Title:              "Important Task",
		Priority:           1,
		Status:             types.StatusOpen,
		Assignee:           "alice",
		Description:        "long description that should be stripped",
		Design:             "design doc body",
		AcceptanceCriteria: "AC list",
		Notes:              "side notes",
	}

	raw := marshalLightweightIssue(issue)
	if len(raw) == 0 {
		t.Fatal("expected non-empty RawMessage for non-nil issue")
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(raw): %v", err)
	}

	if got, _ := decoded["id"].(string); got != "bd-42" {
		t.Errorf("id = %q, want %q", got, "bd-42")
	}
	if got, _ := decoded["title"].(string); got != "Important Task" {
		t.Errorf("title = %q, want %q", got, "Important Task")
	}
	// JSON numbers decode to float64 in map[string]any.
	if got, _ := decoded["priority"].(float64); got != 1 {
		t.Errorf("priority = %v, want 1", decoded["priority"])
	}
	if got, _ := decoded["status"].(string); got != string(types.StatusOpen) {
		t.Errorf("status = %q, want %q", got, types.StatusOpen)
	}
	if got, _ := decoded["assignee"].(string); got != "alice" {
		t.Errorf("assignee = %q, want %q", got, "alice")
	}

	// Heavy fields: must be absent (omitempty) or empty string.
	heavyFields := []string{"description", "design", "acceptance_criteria", "notes"}
	for _, field := range heavyFields {
		v, present := decoded[field]
		if present {
			if s, ok := v.(string); !ok || s != "" {
				t.Errorf("heavy field %q should be absent or empty string, got %v", field, v)
			}
		}
		// Double-check: JSON text must not carry the heavy payload.
		if strings.Contains(string(raw), "long description that should be stripped") ||
			strings.Contains(string(raw), "design doc body") ||
			strings.Contains(string(raw), "AC list") ||
			strings.Contains(string(raw), "side notes") {
			t.Errorf("heavy field content leaked into lightweight JSON: %s", raw)
			return
		}
	}
}

// TestMarshalLightweightIssue_NilInput confirms nil-safe behavior.
func TestMarshalLightweightIssue_NilInput(t *testing.T) {
	t.Parallel()

	if got := marshalLightweightIssue(nil); got != nil {
		t.Errorf("marshalLightweightIssue(nil) = %v, want nil", got)
	}
}

// TestMarshalLightweightIssue_DoesNotMutateCaller verifies the helper copies
// the issue before stripping heavy fields — callers pass in live pointers and
// must not observe side effects (emit sites re-use the issue pointer after
// calling the helper).
func TestMarshalLightweightIssue_DoesNotMutateCaller(t *testing.T) {
	t.Parallel()

	issue := &types.Issue{
		ID:                 "bd-7",
		Title:              "Keep Me",
		Priority:           2,
		Status:             types.StatusInProgress,
		Assignee:           "bob",
		Description:        "preserved-description",
		Design:             "preserved-design",
		AcceptanceCriteria: "preserved-ac",
		Notes:              "preserved-notes",
	}

	raw := marshalLightweightIssue(issue)
	if len(raw) == 0 {
		t.Fatal("expected non-empty RawMessage")
	}

	// Caller's heavy fields must survive unchanged.
	if issue.Description != "preserved-description" {
		t.Errorf("Description mutated: %q", issue.Description)
	}
	if issue.Design != "preserved-design" {
		t.Errorf("Design mutated: %q", issue.Design)
	}
	if issue.AcceptanceCriteria != "preserved-ac" {
		t.Errorf("AcceptanceCriteria mutated: %q", issue.AcceptanceCriteria)
	}
	if issue.Notes != "preserved-notes" {
		t.Errorf("Notes mutated: %q", issue.Notes)
	}

	// Other caller fields also intact.
	if issue.ID != "bd-7" || issue.Title != "Keep Me" || issue.Assignee != "bob" {
		t.Errorf("caller issue was mutated: %+v", issue)
	}

	// Mutating the returned RawMessage must not reach back into the caller. We
	// overwrite the bytes in place; any aliasing would visibly corrupt the
	// issue struct (it wouldn't, but this asserts the invariant explicitly).
	for i := range raw {
		raw[i] = 'x'
	}
	if issue.Title != "Keep Me" {
		t.Errorf("caller Title mutated after overwriting returned RawMessage: %q", issue.Title)
	}
}
