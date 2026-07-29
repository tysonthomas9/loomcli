package role

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

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

// warnDroppedLabelConstraints is the only signal an operator gets when the
// backend predates label constraints: the write returns 200, the fields are
// dropped, and the role keeps claiming everything. The warning must fire in
// exactly that case and stay quiet otherwise.
func TestWarnDroppedLabelConstraints(t *testing.T) {
	tests := []struct {
		name        string
		stored      *domain.Role
		wantLabels  []string
		wantExclude []string
		want        []string // substrings the warning must contain; nil = silent
	}{
		{
			name:       "backend dropped labels",
			stored:     &domain.Role{Name: "reviewer"},
			wantLabels: []string{"plan-ready"},
			want:       []string{"labels", "NOT active"},
		},
		{
			name:        "backend dropped exclude_labels",
			stored:      &domain.Role{Name: "reviewer"},
			wantExclude: []string{"plan-reviewed"},
			want:        []string{"exclude_labels", "NOT active"},
		},
		{
			name:        "backend dropped both",
			stored:      &domain.Role{Name: "reviewer"},
			wantLabels:  []string{"plan-ready"},
			wantExclude: []string{"plan-reviewed"},
			want:        []string{"labels and exclude_labels"},
		},
		{
			name:        "backend persisted both",
			stored:      &domain.Role{Name: "reviewer", Labels: []string{"plan-ready"}, ExcludeLabels: []string{"plan-reviewed"}},
			wantLabels:  []string{"plan-ready"},
			wantExclude: []string{"plan-reviewed"},
		},
		{
			name:   "nothing configured, nothing stored",
			stored: &domain.Role{Name: "reviewer"},
		},
		{
			name:       "nil role cannot confirm the write",
			stored:     nil,
			wantLabels: []string{"plan-ready"},
			want:       []string{"labels"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := captureStderr(t, func() {
				warnDroppedLabelConstraints(tt.stored, tt.wantLabels, tt.wantExclude)
			})
			if len(tt.want) == 0 {
				if got != "" {
					t.Fatalf("warned when the constraints were stored: %q", got)
				}
				return
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("warning %q does not mention %q", got, want)
				}
			}
		})
	}
}

// captureStderr swaps os.Stderr for a pipe and returns what fn wrote to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

func TestDerefSlice(t *testing.T) {
	if got := derefSlice(nil); got != nil {
		t.Errorf("derefSlice(nil) = %v, want nil (patch leaves the field alone)", got)
	}
	empty := []string{}
	if got := derefSlice(&empty); len(got) != 0 {
		t.Errorf("derefSlice(&[]) = %v, want empty", got)
	}
	set := []string{"plan-ready"}
	if got := derefSlice(&set); len(got) != 1 || got[0] != "plan-ready" {
		t.Errorf("derefSlice(&[plan-ready]) = %v", got)
	}
}
