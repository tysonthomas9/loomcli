package entity

import (
	"fmt"
	"strings"
	"testing"
)

func TestDependencyType_IsValid(t *testing.T) {
	tests := []struct {
		name    string
		depType DependencyType
		want    bool
	}{
		{"non-empty short string", "blocks", true},
		{"single char", "a", true},
		{"exactly 50 chars", DependencyType(strings.Repeat("x", 50)), true},
		{"51 chars is invalid", DependencyType(strings.Repeat("x", 51)), false},
		{"empty is invalid", "", false},
		{"typical dependency type", "depends_on", true},
		{"another type", "blocks", true},
		{"type with spaces", "some type", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.depType.IsValid(); got != tt.want {
				t.Errorf("DependencyType(%q).IsValid() = %v, want %v", tt.depType, got, tt.want)
			}
		})
	}
}

func TestDependencyType_IsWellKnown(t *testing.T) {
	// All 19 well-known constants should return true.
	wellKnown := []struct {
		name    string
		depType DependencyType
	}{
		{"DepBlocks", DepBlocks},
		{"DepParentChild", DepParentChild},
		{"DepConditionalBlocks", DepConditionalBlocks},
		{"DepWaitsFor", DepWaitsFor},
		{"DepRelated", DepRelated},
		{"DepDiscoveredFrom", DepDiscoveredFrom},
		{"DepRepliesTo", DepRepliesTo},
		{"DepRelatesTo", DepRelatesTo},
		{"DepDuplicates", DepDuplicates},
		{"DepSupersedes", DepSupersedes},
		{"DepAuthoredBy", DepAuthoredBy},
		{"DepAssignedTo", DepAssignedTo},
		{"DepApprovedBy", DepApprovedBy},
		{"DepAttests", DepAttests},
		{"DepTracks", DepTracks},
		{"DepUntil", DepUntil},
		{"DepCausedBy", DepCausedBy},
		{"DepValidates", DepValidates},
		{"DepDelegatedFrom", DepDelegatedFrom},
	}
	for _, tt := range wellKnown {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.depType.IsWellKnown() {
				t.Errorf("DependencyType(%q).IsWellKnown() = false, want true", tt.depType)
			}
		})
	}

	// Non-well-known values.
	notWellKnown := []struct {
		name    string
		depType DependencyType
	}{
		{"empty string", ""},
		{"custom string", "my-custom-dep"},
		{"partial match", "block"},
	}
	for _, tt := range notWellKnown {
		t.Run(tt.name, func(t *testing.T) {
			if tt.depType.IsWellKnown() {
				t.Errorf("DependencyType(%q).IsWellKnown() = true, want false", tt.depType)
			}
		})
	}
}

func TestDependencyType_AffectsReadyWork(t *testing.T) {
	tests := []struct {
		name    string
		depType DependencyType
		want    bool
	}{
		// Types that affect ready work.
		{"DepBlocks", DepBlocks, true},
		{"DepParentChild", DepParentChild, true},
		{"DepConditionalBlocks", DepConditionalBlocks, true},
		{"DepWaitsFor", DepWaitsFor, true},
		// Types that do NOT affect ready work.
		{"DepRelated", DepRelated, false},
		{"DepRepliesTo", DepRepliesTo, false},
		{"DepAuthoredBy", DepAuthoredBy, false},
		{"DepTracks", DepTracks, false},
		{"DepDelegatedFrom", DepDelegatedFrom, false},
		{"empty string", "", false},
		{"custom string", "my-custom-dep", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.depType.AffectsReadyWork(); got != tt.want {
				t.Errorf("DependencyType(%q).AffectsReadyWork() = %v, want %v", tt.depType, got, tt.want)
			}
		})
	}
}

func TestDependencyType_IsDirectBlocker(t *testing.T) {
	tests := []struct {
		name    string
		depType DependencyType
		want    bool
	}{
		// Direct blockers.
		{"DepBlocks", DepBlocks, true},
		{"DepConditionalBlocks", DepConditionalBlocks, true},
		{"DepWaitsFor", DepWaitsFor, true},
		// parent-child is NOT a direct blocker (key distinction from AffectsReadyWork).
		{"DepParentChild", DepParentChild, false},
		// Other non-blocking types.
		{"DepRelated", DepRelated, false},
		{"DepAuthoredBy", DepAuthoredBy, false},
		{"empty string", "", false},
		{"custom string", "my-custom-dep", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.depType.IsDirectBlocker(); got != tt.want {
				t.Errorf("DependencyType(%q).IsDirectBlocker() = %v, want %v", tt.depType, got, tt.want)
			}
		})
	}
}

func TestIsFailureClose(t *testing.T) {
	tests := []struct {
		name        string
		closeReason string
		want        bool
	}{
		{"empty string", "", false},
		{"failed lowercase", "failed", true},
		{"FAILED uppercase", "FAILED", true},
		{"rejected in sentence", "The task was rejected by reviewer", true},
		{"cancelled british", "cancelled by user", true},
		{"canceled american", "canceled", true},
		{"completed successfully", "completed successfully", false},
		{"closed", "closed", false},
		{"error in sentence", "error during processing", true},
		{"timeout in sentence", "timeout exceeded", true},
		{"abandoned in sentence", "abandoned due to priority change", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsFailureClose(tt.closeReason); got != tt.want {
				t.Errorf("IsFailureClose(%q) = %v, want %v", tt.closeReason, got, tt.want)
			}
		})
	}
}

func TestDependencyType_ConstantValues(t *testing.T) {
	constants := []struct {
		name  string
		value DependencyType
		want  string
	}{
		{"DepBlocks", DepBlocks, "blocks"},
		{"DepParentChild", DepParentChild, "parent-child"},
		{"DepConditionalBlocks", DepConditionalBlocks, "conditional-blocks"},
		{"DepWaitsFor", DepWaitsFor, "waits-for"},
		{"DepRelated", DepRelated, "related"},
		{"DepDiscoveredFrom", DepDiscoveredFrom, "discovered-from"},
		{"DepRepliesTo", DepRepliesTo, "replies-to"},
		{"DepRelatesTo", DepRelatesTo, "relates-to"},
		{"DepDuplicates", DepDuplicates, "duplicates"},
		{"DepSupersedes", DepSupersedes, "supersedes"},
		{"DepAuthoredBy", DepAuthoredBy, "authored-by"},
		{"DepAssignedTo", DepAssignedTo, "assigned-to"},
		{"DepApprovedBy", DepApprovedBy, "approved-by"},
		{"DepAttests", DepAttests, "attests"},
		{"DepTracks", DepTracks, "tracks"},
		{"DepUntil", DepUntil, "until"},
		{"DepCausedBy", DepCausedBy, "caused-by"},
		{"DepValidates", DepValidates, "validates"},
		{"DepDelegatedFrom", DepDelegatedFrom, "delegated-from"},
	}
	for _, tt := range constants {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(tt.value); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, got, tt.want)
			}
		})
	}

	// Verify we tested all 19 constants.
	if len(constants) != 19 {
		t.Errorf("expected 19 constants, tested %d", len(constants))
	}
}

// TestDependencyType_IsDirectBlocker_vs_AffectsReadyWork verifies the key
// distinction: DepParentChild affects ready work but is NOT a direct blocker.
func TestDependencyType_IsDirectBlocker_vs_AffectsReadyWork(t *testing.T) {
	if !DepParentChild.AffectsReadyWork() {
		t.Error("DepParentChild.AffectsReadyWork() should be true")
	}
	if DepParentChild.IsDirectBlocker() {
		t.Error("DepParentChild.IsDirectBlocker() should be false")
	}

	// All direct blockers must also affect ready work.
	directBlockers := []DependencyType{DepBlocks, DepConditionalBlocks, DepWaitsFor}
	for _, d := range directBlockers {
		t.Run(fmt.Sprintf("%s_implies_AffectsReadyWork", d), func(t *testing.T) {
			if !d.IsDirectBlocker() {
				t.Errorf("%q.IsDirectBlocker() = false, want true", d)
			}
			if !d.AffectsReadyWork() {
				t.Errorf("%q.AffectsReadyWork() = false, want true (direct blockers must affect ready work)", d)
			}
		})
	}
}
