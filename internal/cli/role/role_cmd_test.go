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

// A disposition outside the closed vocabulary must fail locally, with the same
// wording the server would have returned, rather than round-tripping to find
// out. The failure must also leave the patch untouched — a partially-applied
// policy is worse than a rejected one.
func TestBuildRolePatchInputPolicy_RejectsUnknownDisposition(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{
			name:    "unknown disposition",
			value:   "trust_prompt=yes",
			wantErr: `role input_policy kind "trust_prompt" disposition "yes" must be one of deny, allow, ask`,
		},
		{
			name:    "unknown default disposition",
			value:   "default=always",
			wantErr: `role input_policy default "always" must be one of deny, allow, ask`,
		},
		{
			// Resolution has to read an empty disposition as deny, but a human
			// typing it at a terminal has made a mistake; accepting it would
			// hide which of the two they meant.
			name:    "empty disposition",
			value:   "trust_prompt=",
			wantErr: `disposition "" must be one of deny, allow, ask`,
		},
		{name: "missing separator", value: "trust_prompt", wantErr: "must be KIND=DISPOSITION"},
		{name: "empty kind", value: "=allow", wantErr: "role input_policy kind is required"},
		{name: "duplicate kind", value: "trust_prompt=allow,trust_prompt=deny", wantErr: `names kind "trust_prompt" twice`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := buildRolePatch("input_policy", tt.value, false)
			if err == nil {
				t.Fatalf("buildRolePatch(%q) error = nil, want a validation error", tt.value)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
			if patch.InputPolicy != nil {
				t.Errorf("a rejected spec must leave InputPolicy unset, got %+v", *patch.InputPolicy)
			}
		})
	}
}

func TestBuildRolePatchInputPolicy_SetAndClear(t *testing.T) {
	patch, err := buildRolePatch("input_policy", "default=deny, trust_prompt=allow , confirm=ask", false)
	if err != nil {
		t.Fatalf("buildRolePatch: %v", err)
	}
	if patch.InputPolicy == nil || *patch.InputPolicy == nil {
		t.Fatal("InputPolicy = nil, want the parsed policy")
	}
	policy := *patch.InputPolicy
	for kind, want := range map[string]string{
		"trust_prompt": domain.RoleInputAllow,
		"confirm":      domain.RoleInputAsk,
		"unnamed":      domain.RoleInputDeny,
	} {
		if got := policy.DispositionFor(kind); got != want {
			t.Errorf("DispositionFor(%q) = %q, want %q", kind, got, want)
		}
	}

	// Unset clears to nil, not to an empty-but-present policy: both resolve to
	// deny, but only nil says "this role has no policy".
	cleared, err := buildRolePatch("input_policy", "", true)
	if err != nil {
		t.Fatalf("buildRolePatch(unset): %v", err)
	}
	if cleared.InputPolicy == nil {
		t.Fatal("unset must produce a clear signal, got nil")
	}
	if *cleared.InputPolicy != nil {
		t.Fatalf("unset must clear to nil, got %+v", *cleared.InputPolicy)
	}
}

// --input-policy-default and --input-policy are the same knob; an explicit
// default= in the list is the later word and wins.
func TestBuildAddInputPolicy_FlagsMerge(t *testing.T) {
	policy, err := buildAddInputPolicy("deny", []string{"trust_prompt=allow"})
	if err != nil {
		t.Fatalf("buildAddInputPolicy: %v", err)
	}
	if policy.Default != domain.RoleInputDeny || policy.DispositionFor("trust_prompt") != domain.RoleInputAllow {
		t.Fatalf("policy = %+v, want default deny with trust_prompt allowed", policy)
	}

	policy, err = buildAddInputPolicy("deny", []string{"default=ask"})
	if err != nil {
		t.Fatalf("buildAddInputPolicy: %v", err)
	}
	if policy.Default != domain.RoleInputAsk {
		t.Errorf("Default = %q, want the list entry to win", policy.Default)
	}

	// No flags at all is nil, not an empty policy — "said nothing" and "denies
	// everything" stay the same value all the way down.
	policy, err = buildAddInputPolicy("", nil)
	if err != nil {
		t.Fatalf("buildAddInputPolicy: %v", err)
	}
	if policy != nil {
		t.Fatalf("policy = %+v, want nil when no policy flags were given", policy)
	}

	if _, err := buildAddInputPolicy("sure", nil); err == nil {
		t.Error("an invalid --input-policy-default must fail locally")
	}
}

func TestFormatInputPolicy_RoundTripsThroughSet(t *testing.T) {
	policy := &domain.RoleInputPolicy{
		Default: domain.RoleInputDeny,
		Kinds:   map[string]string{"trust_prompt": domain.RoleInputAllow, "confirm": domain.RoleInputAsk},
	}
	got := formatInputPolicy(policy)
	if got != "default=deny, confirm=ask, trust_prompt=allow" {
		t.Fatalf("formatInputPolicy = %q", got)
	}
	// What `role show` prints must be what `role set` accepts.
	patch, err := buildRolePatch("input_policy", got, false)
	if err != nil {
		t.Fatalf("displayed policy did not parse back: %v", err)
	}
	if (*patch.InputPolicy).DispositionFor("trust_prompt") != domain.RoleInputAllow {
		t.Error("re-parsed policy lost the allow")
	}

	if formatInputPolicy(nil) != "" {
		t.Error("a nil policy must render as nothing; the caller decides not to print the line")
	}
	if got := formatInputPolicy(&domain.RoleInputPolicy{}); got != "deny (empty policy)" {
		t.Errorf("empty policy rendered as %q, want it to say what it does", got)
	}
}
