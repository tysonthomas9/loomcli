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
