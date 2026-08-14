package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ws builds a workspace-scoped skill. content distinguishes two skills that
// share a name so a shadowing assertion can say WHICH one survived.
func ws(name, content string) *Skill {
	return &Skill{Name: name, Scope: SkillScopeWorkspace, Content: content}
}

// rl builds a role-scoped skill.
func rl(role, name, content string) *Skill {
	return &Skill{Name: name, Scope: SkillScopeRole, RoleName: role, Content: content}
}

// resolvedNames renders a resolution as "name=content" pairs so a failure
// message says which copy won, not just that the count was wrong.
func resolvedNames(skills []*Skill) []string {
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.Name+"="+s.Content)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The scope chain is the rule ticket 03 materializes against: workspace skills
// plus the agent's own role's, role shadowing workspace on name. Every row here
// is a way that can go wrong.
func TestResolveSkillChain(t *testing.T) {
	tests := []struct {
		name   string
		skills []*Skill
		role   string
		want   []string
	}{
		{
			name:   "empty input resolves to nothing",
			skills: nil,
			role:   "lead",
			want:   []string{},
		},
		{
			name:   "workspace skills load into every role",
			skills: []*Skill{ws("alpha", "w"), ws("beta", "w")},
			role:   "lead",
			want:   []string{"alpha=w", "beta=w"},
		},
		{
			name:   "the agent's own role skills load",
			skills: []*Skill{rl("lead", "alpha", "r")},
			role:   "lead",
			want:   []string{"alpha=r"},
		},
		{
			name:   "another role's skills do not load",
			skills: []*Skill{rl("task", "alpha", "r")},
			role:   "lead",
			want:   []string{},
		},
		{
			name:   "both scopes combine when names differ",
			skills: []*Skill{ws("alpha", "w"), rl("lead", "beta", "r")},
			role:   "lead",
			want:   []string{"alpha=w", "beta=r"},
		},
		{
			name:   "role shadows workspace on the same name",
			skills: []*Skill{ws("alpha", "w"), rl("lead", "alpha", "r")},
			role:   "lead",
			want:   []string{"alpha=r"},
		},
		{
			name:   "shadowing does not depend on input order",
			skills: []*Skill{rl("lead", "alpha", "r"), ws("alpha", "w")},
			role:   "lead",
			want:   []string{"alpha=r"},
		},
		{
			name:   "another role's same-named skill does not shadow",
			skills: []*Skill{ws("alpha", "w"), rl("task", "alpha", "r")},
			role:   "lead",
			want:   []string{"alpha=w"},
		},
		{
			name:   "shadowing is per name, siblings are untouched",
			skills: []*Skill{ws("alpha", "w"), ws("beta", "w"), rl("lead", "alpha", "r")},
			role:   "lead",
			want:   []string{"alpha=r", "beta=w"},
		},
		{
			name:   "an agent with no role still gets the workspace set",
			skills: []*Skill{ws("alpha", "w"), rl("lead", "beta", "r")},
			role:   "",
			want:   []string{"alpha=w"},
		},
		{
			name:   "role name is compared exactly, not case-folded",
			skills: []*Skill{rl("Lead", "alpha", "r")},
			role:   "lead",
			want:   []string{},
		},
		{
			name:   "whitespace around the role name is trimmed on both sides",
			skills: []*Skill{rl("lead", "alpha", "r")},
			role:   "lead\n",
			want:   []string{"alpha=r"},
		},
		{
			name:   "an unknown scope is dropped rather than defaulted",
			skills: []*Skill{{Name: "alpha", Scope: SkillScope("global"), Content: "g"}},
			role:   "lead",
			want:   []string{},
		},
		{
			name:   "an unknown scope cannot shadow a real one",
			skills: []*Skill{{Name: "alpha", Scope: SkillScope(""), Content: "g"}, ws("alpha", "w")},
			role:   "lead",
			want:   []string{"alpha=w"},
		},
		{
			name:   "nil entries and unnamed skills are skipped",
			skills: []*Skill{nil, {Name: "", Scope: SkillScopeWorkspace}, ws("alpha", "w")},
			role:   "lead",
			want:   []string{"alpha=w"},
		},
		{
			name:   "duplicates within one scope resolve to the first, not to map order",
			skills: []*Skill{ws("alpha", "first"), ws("alpha", "second")},
			role:   "lead",
			want:   []string{"alpha=first"},
		},
		{
			name:   "output is sorted by name regardless of input order",
			skills: []*Skill{ws("gamma", "w"), rl("lead", "alpha", "r"), ws("beta", "w")},
			role:   "lead",
			want:   []string{"alpha=r", "beta=w", "gamma=w"},
		},
		{
			name:   "a role-scoped skill with no role name never loads",
			skills: []*Skill{{Name: "alpha", Scope: SkillScopeRole, Content: "r"}},
			role:   "lead",
			want:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvedNames(ResolveSkillChain(tt.skills, tt.role))
			if !equalStrings(got, tt.want) {
				t.Errorf("ResolveSkillChain(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// The detail form is what a CLI or a UI uses to say "the role overrides the
// workspace copy", so the shadowed skill has to survive resolution rather than
// simply disappear.
func TestResolveSkillChainDetail_ReportsWhatWasShadowed(t *testing.T) {
	workspaceAlpha := ws("alpha", "w")
	skills := []*Skill{workspaceAlpha, rl("lead", "alpha", "r"), ws("beta", "w"), rl("lead", "gamma", "r")}

	got := ResolveSkillChainDetail(skills, "lead")
	if len(got) != 3 {
		t.Fatalf("resolved %d skills, want 3: %v", len(got), got)
	}

	if got[0].Skill.Content != "r" {
		t.Errorf("alpha resolved to %q, want the role-scoped copy", got[0].Skill.Content)
	}
	if got[0].Shadowed != workspaceAlpha {
		t.Errorf("alpha shadowed = %v, want the workspace-scoped copy", got[0].Shadowed)
	}
	// beta has no role override, and gamma has no workspace copy to displace:
	// neither may claim to have shadowed anything.
	if got[1].Shadowed != nil {
		t.Errorf("beta shadowed = %v, want nil", got[1].Shadowed)
	}
	if got[2].Skill.Name != "gamma" || got[2].Shadowed != nil {
		t.Errorf("gamma entry = %+v, want gamma with nothing shadowed", got[2])
	}
}

// Resolution must not hand a caller a view onto the slice it was given, or
// ticket 03 mutating a materialization plan would edit the listing behind it.
func TestResolveSkillChain_DoesNotAliasTheInputSlice(t *testing.T) {
	skills := []*Skill{ws("beta", "w"), ws("alpha", "w")}
	got := ResolveSkillChain(skills, "lead")
	got[0] = nil
	if skills[0] == nil || skills[1] == nil {
		t.Fatalf("mutating the result mutated the input: %v", skills)
	}
}

func TestSkillRef(t *testing.T) {
	tests := []struct {
		name    string
		ref     SkillRef
		want    string
		wantErr bool
	}{
		{name: "workspace", ref: WorkspaceSkillRef("alpha"), want: "workspace:alpha"},
		{name: "role", ref: RoleSkillRef("lead", "alpha"), want: "role:lead:alpha"},
		{name: "role traversal", ref: RoleSkillRef("..", "alpha"), want: "role:..:alpha", wantErr: true},
		{name: "role slash", ref: RoleSkillRef("a/b", "alpha"), want: "role:a/b:alpha", wantErr: true},
		{name: "role with no role name", ref: SkillRef{Scope: SkillScopeRole, Name: "alpha"}, want: "role::alpha", wantErr: true},
		{name: "workspace carrying a role name", ref: SkillRef{Scope: SkillScopeWorkspace, RoleName: "lead", Name: "a"}, want: "workspace:a", wantErr: true},
		{name: "unknown scope renders empty", ref: SkillRef{Scope: "global", Name: "alpha"}, want: "", wantErr: true},
		{name: "no name", ref: SkillRef{Scope: SkillScopeWorkspace}, want: "workspace:", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
			err := tt.ref.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("Validate() = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate() = %v, want it to wrap ErrInvalid", err)
			}
		})
	}
}

// skill_ref comes back on every per-document response, so parsing it has to be
// exactly the inverse of rendering it.
func TestParseSkillRef(t *testing.T) {
	tests := []struct {
		in      string
		want    SkillRef
		wantErr bool
	}{
		{in: "workspace:alpha", want: WorkspaceSkillRef("alpha")},
		{in: "role:lead:alpha", want: RoleSkillRef("lead", "alpha")},
		{in: "alpha", wantErr: true},
		{in: "", wantErr: true},
		{in: "workspace:", wantErr: true},
		{in: "role::alpha", wantErr: true},
		{in: "role:lead:", wantErr: true},
		{in: "global:alpha", wantErr: true},
		{in: "workspace:lead:alpha", wantErr: true},
		{in: "role:lead:alpha:extra", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseSkillRef(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSkillRef(%q) = %v, want an error", tt.in, got)
				}
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("ParseSkillRef(%q) error = %v, want it to wrap ErrInvalid", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSkillRef(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseSkillRef(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			if round := got.String(); round != tt.in {
				t.Errorf("round trip = %q, want %q", round, tt.in)
			}
		})
	}
}

// The two failure modes must stay distinguishable: one is fixed by re-reading
// and merging, the other cannot be fixed by the caller at all.
func TestSkillErrorsAreDistinguishable(t *testing.T) {
	provenance := error(&SkillProvenanceConflictError{
		Ref:               RoleSkillRef("lead", "alpha"),
		ExistingCreatedBy: "alice",
		IncomingCreatedBy: "bob",
		ExistingUpdatedAt: time.Now().UTC(),
	})
	precondition := error(&SkillPreconditionError{
		Ref:      WorkspaceSkillRef("alpha"),
		Path:     "references/api.md",
		Expected: "0123456789abcdef",
		Stored:   "fedcba9876543210",
	})

	if !errors.Is(provenance, ErrSkillProvenanceConflict) {
		t.Errorf("provenance error does not match ErrSkillProvenanceConflict")
	}
	if errors.Is(provenance, ErrSkillPreconditionFailed) {
		t.Errorf("provenance error must not match ErrSkillPreconditionFailed")
	}
	if !errors.Is(precondition, ErrSkillPreconditionFailed) {
		t.Errorf("precondition error does not match ErrSkillPreconditionFailed")
	}
	if errors.Is(precondition, ErrSkillProvenanceConflict) {
		t.Errorf("precondition error must not match ErrSkillProvenanceConflict")
	}

	var conflict *SkillProvenanceConflictError
	if !errors.As(provenance, &conflict) || conflict.ExistingCreatedBy != "alice" {
		t.Errorf("errors.As did not recover the owner detail: %+v", conflict)
	}
	var stale *SkillPreconditionError
	if !errors.As(precondition, &stale) || stale.Stored != "fedcba9876543210" {
		t.Errorf("errors.As did not recover the revision detail: %+v", stale)
	}
}

func TestValidateSkillName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "pr-review", ok: true},
		{name: "a", ok: true},
		{name: "a1-b2-c3", ok: true},
		{name: "", ok: false},
		{name: "PR-Review", ok: false},
		{name: "pr_review", ok: false},
		{name: "-leading", ok: false},
		{name: "trailing-", ok: false},
		{name: "double--hyphen", ok: false},
		{name: "has space", ok: false},
		{name: "../escape", ok: false},
		{name: "claude", ok: false},
		{name: "anthropic", ok: false},
		{name: "con", ok: false},
		{name: "nul", ok: false},
		{name: "com1", ok: false},
		{name: "conference", ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillName(tt.name)
			if tt.ok && err != nil {
				t.Errorf("ValidateSkillName(%q) = %v, want nil", tt.name, err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatalf("ValidateSkillName(%q) = nil, want an error", tt.name)
				}
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("ValidateSkillName(%q) = %v, want it to wrap ErrInvalid", tt.name, err)
				}
			}
		})
	}

	long := make([]byte, MaxSkillNameLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidateSkillName(string(long)); err == nil {
		t.Errorf("a %d-character name was accepted", len(long))
	}
}

func TestValidateSkillFilePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "root file", path: "notes.md"},
		{name: "nested file", path: "references/api.md"},
		{name: "unicode file", path: "references/café.md"},
		{name: "empty", path: "", wantErr: true},
		{name: "absolute", path: "/etc/passwd", wantErr: true},
		{name: "backslash", path: `scripts\run.sh`, wantErr: true},
		{name: "traversal", path: "../outside", wantErr: true},
		{name: "nested traversal", path: "references/../outside", wantErr: true},
		{name: "dot segment", path: "references/./api.md", wantErr: true},
		{name: "empty segment", path: "references//api.md", wantErr: true},
		{name: "trailing slash", path: "references/", wantErr: true},
		{name: "home expansion", path: "~/.config", wantErr: true},
		{name: "drive prefix", path: "C:/config", wantErr: true},
		{name: "control", path: "references/api\n.md", wantErr: true},
		{name: "reserved body", path: SkillFileNameSKILLMD, wantErr: true},
		{name: "reserved body folded", path: "skill.MD/child", wantErr: true},
		{name: "nested skill name allowed", path: "docs/SKILL.md", wantErr: false},
		{name: "windows device", path: "scripts/CON.txt", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillFilePath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateSkillFilePath(%q) = nil, want error", tt.path)
				}
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("ValidateSkillFilePath(%q) = %v, want ErrInvalid", tt.path, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateSkillFilePath(%q) = %v, want nil", tt.path, err)
			}
		})
	}
}

func TestValidateRoleName(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		wantErr bool
	}{
		{name: "simple", role: "reviewer"},
		{name: "internal separators", role: "release.v2_review-team"},
		{name: "one character", role: "x"},
		{name: "maximum length", role: "r" + strings.Repeat("a", MaxRoleNameLength-1)},
		{name: "traversal", role: "..", wantErr: true},
		{name: "dot", role: ".", wantErr: true},
		{name: "slash", role: "a/b", wantErr: true},
		{name: "backslash", role: `a\b`, wantErr: true},
		{name: "uppercase", role: "Reviewer", wantErr: true},
		{name: "leading separator", role: "-reviewer", wantErr: true},
		{name: "trailing separator", role: "reviewer.", wantErr: true},
		{name: "control", role: "review\ner", wantErr: true},
		{name: "too long", role: "r" + strings.Repeat("a", MaxRoleNameLength), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRoleName(tt.role)
			if tt.wantErr {
				if err == nil || !errors.Is(err, ErrInvalid) {
					t.Fatalf("ValidateRoleName(%q) = %v, want ErrInvalid", tt.role, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateRoleName(%q) = %v, want nil", tt.role, err)
			}
		})
	}
}

func TestSkillCloneIsDeep(t *testing.T) {
	original := &Skill{Name: "alpha", Scope: SkillScopeWorkspace, Files: []SkillFile{{Path: "a.md", Content: "one"}}}
	clone := original.Clone()
	clone.Files[0].Content = "two"
	if original.Files[0].Content != "one" {
		t.Errorf("mutating the clone changed the original: %q", original.Files[0].Content)
	}
}

// The revision has two written forms — the bare token our types carry and the
// quoted entity-tag an HTTP layer holds — and every boundary that accepts one
// has to accept the other. Getting this wrong is what made fleet-db's
// conditional writes 412 forever before it learned to parse entity-tags.
func TestNormalizeSkillRevision(t *testing.T) {
	const bare = "0123456789abcdef"
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "bare revision passes through", in: bare, want: bare},
		{name: "quoted entity-tag", in: `"` + bare + `"`, want: bare},
		{name: "weak entity-tag", in: `W/"` + bare + `"`, want: bare},
		{name: "surrounding whitespace", in: "  " + bare + "  ", want: bare},
		{name: "empty stays empty", in: "", want: ""},
		{name: "wildcard passes through unquoted", in: "*", want: "*"},
		{name: "a multi-tag list is refused, not reduced", in: `"a", "b"`, wantErr: true},
		{name: "a stray quote is refused", in: `"unterminated`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSkillRevision(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeSkillRevision(%q) = %q, want an error", tt.in, got)
				}
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("error = %v, want it to wrap ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeSkillRevision(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeSkillRevision(%q) = %q, want %q", tt.in, got, tt.want)
			}
			// Idempotent, or a value that passed through two boundaries would
			// come out different from one that passed through one.
			again, err := NormalizeSkillRevision(got)
			if err != nil || again != got {
				t.Errorf("second pass = %q, %v; want %q", again, err, got)
			}
		})
	}
}
