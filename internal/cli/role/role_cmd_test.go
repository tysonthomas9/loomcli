package role

import "testing"

func TestBuildRolePatchKind(t *testing.T) {
	tests := []struct {
		name  string
		value string
		unset bool
		want  string
	}{
		{name: "interactive", value: "interactive", want: "interactive"},
		{name: "worker", value: "worker", want: "worker"},
		{name: "normalized", value: " Interactive ", want: "interactive"},
		{name: "unset", unset: true, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := buildRolePatch("kind", tt.value, tt.unset)
			if err != nil {
				t.Fatalf("buildRolePatch() error = %v", err)
			}
			if patch.Kind == nil {
				t.Fatalf("buildRolePatch() Kind = nil")
			}
			if got := *patch.Kind; got != tt.want {
				t.Fatalf("buildRolePatch() Kind = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildRolePatchKindInvalid(t *testing.T) {
	patch, err := buildRolePatch("kind", "batch", false)
	if err == nil {
		t.Fatalf("buildRolePatch() error = nil, want validation error")
	}
	if patch.Kind != nil {
		t.Fatalf("buildRolePatch() Kind = %q, want nil", *patch.Kind)
	}
}

func TestBuildRolePatchLabels(t *testing.T) {
	patch, err := buildRolePatch("labels", "plan-ready, approved", false)
	if err != nil {
		t.Fatalf("buildRolePatch() error = %v", err)
	}
	if patch.Labels == nil {
		t.Fatal("buildRolePatch() Labels = nil")
	}
	got := *patch.Labels
	want := []string{"plan-ready", "approved"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("buildRolePatch() Labels = %v, want %v", got, want)
	}
}

func TestBuildRolePatchLabelsUnset(t *testing.T) {
	// unset form: empty value produces a non-nil pointer to an empty slice,
	// which fleet-db treats as "clear the field".
	patch, err := buildRolePatch("labels", "", true)
	if err != nil {
		t.Fatalf("buildRolePatch() error = %v", err)
	}
	if patch.Labels == nil {
		t.Fatal("buildRolePatch() Labels = nil, want non-nil pointer to empty slice")
	}
	if len(*patch.Labels) != 0 {
		t.Fatalf("buildRolePatch() Labels = %v, want empty slice", *patch.Labels)
	}
}

func TestBuildRolePatchExcludeLabels(t *testing.T) {
	patch, err := buildRolePatch("exclude_labels", "plan-reviewed", false)
	if err != nil {
		t.Fatalf("buildRolePatch() error = %v", err)
	}
	if patch.ExcludeLabels == nil {
		t.Fatal("buildRolePatch() ExcludeLabels = nil")
	}
	got := *patch.ExcludeLabels
	if len(got) != 1 || got[0] != "plan-reviewed" {
		t.Fatalf("buildRolePatch() ExcludeLabels = %v, want [plan-reviewed]", got)
	}
}

func TestBuildRolePatchExcludeLabelsUnset(t *testing.T) {
	patch, err := buildRolePatch("exclude_labels", "", true)
	if err != nil {
		t.Fatalf("buildRolePatch() error = %v", err)
	}
	if patch.ExcludeLabels == nil {
		t.Fatal("buildRolePatch() ExcludeLabels = nil, want non-nil pointer to empty slice")
	}
	if len(*patch.ExcludeLabels) != 0 {
		t.Fatalf("buildRolePatch() ExcludeLabels = %v, want empty slice", *patch.ExcludeLabels)
	}
}

func TestTrimFilterLabels(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "no whitespace", in: []string{"a", "b"}, want: []string{"a", "b"}},
		{name: "trims surrounding whitespace", in: []string{" a ", "b"}, want: []string{"a", "b"}},
		{name: "drops whitespace-only elements", in: []string{" ", "a", ""}, want: []string{"a"}},
		{name: "all whitespace collapses to empty slice", in: []string{" ", "\t"}, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimFilterLabels(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("trimFilterLabels(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("trimFilterLabels(%v) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

// TestRunRoleAdd_WhitespaceOnlyLabelDoesNotReintroduceGuardBug pins the fix
// for a whitespace-only --labels/--exclude-labels value: without filtering,
// a role with only a whitespace label would store a non-empty Labels slice
// (so HasRoutingConstraints sees it as configured), but the env round-trip's
// splitLabelCSV would decode it back to nil — silently falling through the
// routing-check activation guard, the exact bug this constraint exists to
// prevent. Filtering at loom role add time keeps what's stored consistent
// with what splitLabelCSV will later decode.
func TestRunRoleAdd_WhitespaceOnlyLabelDoesNotReintroduceGuardBug(t *testing.T) {
	got := trimFilterLabels([]string{" "})
	if len(got) != 0 {
		t.Fatalf("trimFilterLabels([\" \"]) = %v, want empty slice so a whitespace-only "+
			"--labels value is never stored as a non-empty constraint", got)
	}
}
