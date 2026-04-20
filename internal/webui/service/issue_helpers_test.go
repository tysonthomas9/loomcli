package service

import (
	"testing"
)

// --- validatePatchParams tests ---

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func TestValidatePatchParams_ValidPriority(t *testing.T) {
	for _, p := range []int{0, 1, 2, 3, 4} {
		params := &PatchIssueParams{Priority: intPtr(p)}
		if err := validatePatchParams(params); err != nil {
			t.Errorf("priority=%d: expected nil, got %v", p, err)
		}
	}
}

func TestValidatePatchParams_InvalidPriority(t *testing.T) {
	for _, p := range []int{-1, 5, 100, -100} {
		params := &PatchIssueParams{Priority: intPtr(p)}
		err := validatePatchParams(params)
		if err == nil {
			t.Errorf("priority=%d: expected error, got nil", p)
			continue
		}
		if err.Kind != KindValidation {
			t.Errorf("priority=%d: Kind = %q, want %q", p, err.Kind, KindValidation)
		}
	}
}

func TestValidatePatchParams_NilPriority(t *testing.T) {
	params := &PatchIssueParams{}
	if err := validatePatchParams(params); err != nil {
		t.Errorf("nil priority: expected nil, got %v", err)
	}
}

func TestValidatePatchParams_ValidStatus(t *testing.T) {
	for _, s := range []string{"open", "in_progress", "blocked", "deferred", "review", "closed", "tombstone", "pinned", "hooked"} {
		params := &PatchIssueParams{Status: strPtr(s)}
		if err := validatePatchParams(params); err != nil {
			t.Errorf("status=%q: expected nil, got %v", s, err)
		}
	}
}

func TestValidatePatchParams_InvalidStatus(t *testing.T) {
	for _, s := range []string{"bogus", "nonexistent", "OPEN", "open "} {
		params := &PatchIssueParams{Status: strPtr(s)}
		err := validatePatchParams(params)
		if err == nil {
			t.Errorf("status=%q: expected error, got nil", s)
			continue
		}
		if err.Kind != KindValidation {
			t.Errorf("status=%q: Kind = %q, want %q", s, err.Kind, KindValidation)
		}
	}
}

func TestValidatePatchParams_EmptyStatus(t *testing.T) {
	// Empty string should pass — treated as no change requested.
	params := &PatchIssueParams{Status: strPtr("")}
	if err := validatePatchParams(params); err != nil {
		t.Errorf("empty status: expected nil, got %v", err)
	}
}

func TestValidatePatchParams_NilStatus(t *testing.T) {
	params := &PatchIssueParams{}
	if err := validatePatchParams(params); err != nil {
		t.Errorf("nil status: expected nil, got %v", err)
	}
}

func TestValidatePatchParams_CombinedValid(t *testing.T) {
	params := &PatchIssueParams{
		Priority: intPtr(2),
		Status:   strPtr("in_progress"),
	}
	if err := validatePatchParams(params); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidatePatchParams_CombinedInvalidPriorityValidStatus(t *testing.T) {
	params := &PatchIssueParams{
		Priority: intPtr(99),
		Status:   strPtr("open"),
	}
	err := validatePatchParams(params)
	if err == nil || err.Kind != KindValidation {
		t.Errorf("expected validation error, got %v", err)
	}
}

// --- patchParamsToUpdateArgs tests ---

func TestPatchParamsToUpdateArgs_AgentState_Set(t *testing.T) {
	state := "running"
	params := &PatchIssueParams{
		IssueID:    "issue-1",
		AgentState: &state,
	}

	args := patchParamsToUpdateArgs(params)

	if args.AgentState == nil {
		t.Fatal("expected AgentState to be non-nil in UpdateArgs")
	}
	if *args.AgentState != "running" {
		t.Errorf("AgentState = %q, want %q", *args.AgentState, "running")
	}
}

func TestPatchParamsToUpdateArgs_AgentState_Nil(t *testing.T) {
	params := &PatchIssueParams{
		IssueID: "issue-2",
		// AgentState not set (nil)
	}

	args := patchParamsToUpdateArgs(params)

	if args.AgentState != nil {
		t.Errorf("expected AgentState to be nil, got %q", *args.AgentState)
	}
}

func TestPatchParamsToUpdateArgs_AgentState_WithOtherFields(t *testing.T) {
	state := "idle"
	status := "open"
	title := "Updated title"
	params := &PatchIssueParams{
		IssueID:    "issue-3",
		Title:      &title,
		Status:     &status,
		AgentState: &state,
	}

	args := patchParamsToUpdateArgs(params)

	if args.ID != "issue-3" {
		t.Errorf("ID = %q, want %q", args.ID, "issue-3")
	}
	if args.Title == nil || *args.Title != "Updated title" {
		t.Errorf("Title = %v, want %q", args.Title, "Updated title")
	}
	if args.Status == nil || *args.Status != "open" {
		t.Errorf("Status = %v, want %q", args.Status, "open")
	}
	if args.AgentState == nil || *args.AgentState != "idle" {
		t.Errorf("AgentState = %v, want %q", args.AgentState, "idle")
	}
}
