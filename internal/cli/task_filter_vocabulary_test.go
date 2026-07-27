package cli

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// `loom agent --task-filter` documents and validates "needs_design"; the
// daemon's router only ever matched "needs_plan". An agentdef created the
// documented way was therefore treated as "has_design", so a planner claimed
// tasks that already had designs and ran as a second worker (DOGFOOD-43).
func TestApplyTaskFilter_NeedsDesignIsAnAliasForNeedsPlan(t *testing.T) {
	// An issue that needs planning: no design yet.
	needsPlanning := backend.IssueData{
		ID:     "T-1",
		Status: "open",
	}

	needsPlan := applyTaskFilter(needsPlanning, "needs_plan")
	needsDesign := applyTaskFilter(needsPlanning, "needs_design")

	if needsPlan != needsDesign {
		t.Errorf("filters disagree on the same issue:\n  needs_plan   -> %q\n  needs_design -> %q",
			needsPlan, needsDesign)
	}
	if needsDesign != "" {
		t.Errorf("needs_design rejected an issue that needs planning: %q", needsDesign)
	}
}

func TestValidateTaskFilter(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "needs_design", want: "needs_plan"},
		{in: "needs_plan", want: "needs_plan"},
		{in: "has_design", want: "has_design"},
		{in: "any", want: "any"},
		{in: "", want: ""},
		{in: "needs-design", wantErr: true},
		{in: "needsdesign", wantErr: true},
		{in: "NEEDS_DESIGN", wantErr: true},
		{in: "plan", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ValidateTaskFilter(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateTaskFilter(%q) = %q, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateTaskFilter(%q) returned error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ValidateTaskFilter(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The error must name the accepted values; a bare "invalid" would leave the
// caller guessing between two vocabularies that both look plausible.
func TestValidateTaskFilter_ErrorNamesAcceptedValues(t *testing.T) {
	_, err := ValidateTaskFilter("nonsense")
	if err == nil {
		t.Fatal("expected an error for an unknown filter")
	}
	for _, want := range []string{"needs_design", "has_design", "any"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}
