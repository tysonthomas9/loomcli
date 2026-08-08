package workitems

import (
	"errors"
	"strings"
	"testing"
)

func TestClassificationPolicy(t *testing.T) {
	t.Parallel()

	t.Run("statuses", func(t *testing.T) {
		t.Parallel()
		for _, status := range []Status{
			StatusOpen, StatusInProgress, StatusBlocked, StatusDeferred,
			StatusReview, StatusClosed, StatusTombstone, StatusPinned, StatusHooked,
		} {
			if !status.IsBuiltIn() {
				t.Fatalf("built-in status %q was rejected", status)
			}
		}
		if Status("").IsBuiltIn() || Status("OPEN").IsBuiltIn() {
			t.Fatal("omitted and case-drifted statuses must not be built in")
		}
		if !Status("waiting_qa").IsValidWithCustom([]string{"waiting_qa"}) {
			t.Fatal("configured custom status was rejected")
		}
		if Status("").IsValidWithCustom([]string{"waiting_qa"}) {
			t.Fatal("omitted status must be handled by its boundary, not custom policy")
		}
		if !Status("").IsCreateStatus() || !StatusOpen.IsCreateStatus() ||
			!StatusDeferred.IsCreateStatus() || StatusReview.IsCreateStatus() {
			t.Fatal("create-status policy drifted")
		}
		if !StatusReview.IsUserFacing() || StatusTombstone.IsUserFacing() || Status("").IsUserFacing() {
			t.Fatal("user-facing status policy drifted")
		}
	})

	t.Run("types", func(t *testing.T) {
		t.Parallel()
		for _, issueType := range []IssueType{TypeBug, TypeFeature, TypeTask, TypeEpic, TypeChore} {
			if !issueType.IsBuiltIn() {
				t.Fatalf("built-in type %q was rejected", issueType)
			}
		}
		if IssueType("").IsBuiltIn() || IssueType("BUG").IsBuiltIn() {
			t.Fatal("omitted and case-drifted types must not be built in")
		}
		if !IssueType("story").IsValidWithCustom([]string{"story"}) {
			t.Fatal("configured custom type was rejected")
		}
		if got := IssueType("ENHANCEMENT").Normalize(); got != TypeFeature {
			t.Fatalf("normalize enhancement = %q, want %q", got, TypeFeature)
		}
		if got := IssueType("custom").Normalize(); got != IssueType("custom") {
			t.Fatalf("normalize custom = %q, want custom", got)
		}
	})

	t.Run("titles", func(t *testing.T) {
		t.Parallel()
		if got := CanonicalTitle("  Proof  "); got != "Proof" {
			t.Fatalf("CanonicalTitle() = %q, want Proof", got)
		}
		for _, test := range []struct {
			title string
			kind  TitleValidationKind
		}{
			{title: "Proof"},
			{title: " " + strings.Repeat("a", MaxTitleLength) + " "},
			{title: "   ", kind: TitleRequired},
			{title: strings.Repeat("a", MaxTitleLength+1), kind: TitleTooLong},
		} {
			err := ValidateTitle(test.title)
			if test.kind == "" {
				if err != nil {
					t.Fatalf("ValidateTitle(%q) error = %v", test.title, err)
				}
				continue
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ValidateTitle(%q) error = %v, want ErrInvalid", test.title, err)
			}
			kind, ok := TitleValidationKindOf(err)
			if !ok || kind != test.kind {
				t.Fatalf("ValidateTitle(%q) kind = %q/%t, want %q", test.title, kind, ok, test.kind)
			}
		}
	})

	t.Run("agent states", func(t *testing.T) {
		t.Parallel()
		for _, state := range []AgentState{
			"", AgentStateIdle, AgentStateSpawning, AgentStateRunning, AgentStateWorking,
			AgentStateStuck, AgentStateDone, AgentStateStopped, AgentStateDead,
		} {
			if !state.IsValid() {
				t.Fatalf("valid agent state %q was rejected", state)
			}
		}
		if AgentState("unknown").IsValid() {
			t.Fatal("unknown agent state was accepted")
		}
	})
}
