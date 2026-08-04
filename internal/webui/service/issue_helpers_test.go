package service

import (
	"testing"
)

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
