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

// The role's task_filter is the value the daemon router actually reads, so an
// unrecognized spelling must fail at input time rather than degrade into
// has_design at dispatch. The documented "needs_design" is stored canonically.
func TestBuildRolePatchTaskFilter(t *testing.T) {
	tests := []struct {
		name  string
		value string
		unset bool
		want  string
	}{
		{name: "needs_design canonicalized", value: "needs_design", want: "needs_plan"},
		{name: "needs_plan", value: "needs_plan", want: "needs_plan"},
		{name: "has_design", value: "has_design", want: "has_design"},
		{name: "any", value: "any", want: "any"},
		{name: "unset", unset: true, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := buildRolePatch("task_filter", tt.value, tt.unset)
			if err != nil {
				t.Fatalf("buildRolePatch() error = %v", err)
			}
			if patch.TaskFilter == nil {
				t.Fatalf("buildRolePatch() TaskFilter = nil")
			}
			if got := *patch.TaskFilter; got != tt.want {
				t.Fatalf("buildRolePatch() TaskFilter = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildRolePatchTaskFilterInvalid(t *testing.T) {
	patch, err := buildRolePatch("task_filter", "needs-design", false)
	if err == nil {
		t.Fatalf("buildRolePatch() error = nil, want validation error")
	}
	if patch.TaskFilter != nil {
		t.Fatalf("buildRolePatch() TaskFilter = %q, want nil", *patch.TaskFilter)
	}
}
