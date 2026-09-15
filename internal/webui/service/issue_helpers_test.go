package service

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

// --- issueDetailDataToWire tests ---

// TestIssueDetailDataToWire_EstimatedMinutes pins the omitempty semantics of
// the detail wire map: an explicit 0 is a real estimate and must be emitted,
// while nil must leave the key out entirely rather than emit 0 or null.
func TestIssueDetailDataToWire_EstimatedMinutes(t *testing.T) {
	tests := []struct {
		name      string
		est       *int
		wantKey   bool
		wantValue int
	}{
		{"populated", intPtr(30), true, 30},
		{"explicit zero", intPtr(0), true, 0},
		{"nil", nil, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := issueDetailDataToWire(&backend.IssueDetailData{
				IssueData:        backend.IssueData{ID: "issue-1", Title: "Estimated"},
				EstimatedMinutes: tt.est,
			})
			got, ok := out["estimated_minutes"]
			if ok != tt.wantKey {
				t.Fatalf("estimated_minutes present = %v, want %v (value %v)", ok, tt.wantKey, got)
			}
			if !tt.wantKey {
				return
			}
			// The helper dereferences, so the map value is int, not *int.
			if got != tt.wantValue {
				t.Errorf("estimated_minutes = %v (%T), want %d (int)", got, got, tt.wantValue)
			}
		})
	}
}
