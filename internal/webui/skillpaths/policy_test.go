package skillpaths

import "testing"

func TestPolicyHidden(t *testing.T) {
	tests := []struct {
		name   string
		roots  []string
		path   string
		hidden bool
	}{
		{name: "repo scope agents", roots: []string{""}, path: ".agents/skills/review/SKILL.md", hidden: true},
		{name: "repo scope claude", roots: []string{""}, path: ".claude/skills/review", hidden: true},
		{name: "case folded agents", roots: []string{""}, path: ".AGENTS/skills/review/SKILL.md", hidden: true},
		{name: "case folded claude", roots: []string{""}, path: ".CLAUDE/skills/review", hidden: true},
		{name: "case folded skills segment", roots: []string{""}, path: ".Agents/SKILLS/review", hidden: true},
		{name: "nested configured checkout", roots: []string{"services/api"}, path: "services/api/.agents/skills/review/SKILL.md", hidden: true},
		{name: "nested configured checkout root", roots: []string{"services/api"}, path: "services/api/.agents/skills", hidden: true},
		{name: "agent checkout", roots: []string{"worktrees/api/nova"}, path: "worktrees/api/nova/.claude/skills/review", hidden: true},
		{name: "non skill agents path", roots: []string{"services/api"}, path: "services/api/.agents/hooks/check.sh", hidden: false},
		{name: "same segments outside checkout", roots: []string{"services/api"}, path: ".agents/skills/review/SKILL.md", hidden: false},
		{name: "checkout prefix collision", roots: []string{"services/api"}, path: "services/api-v2/.agents/skills/review/SKILL.md", hidden: false},
		{name: "ordinary skill named directory", roots: []string{"services/api"}, path: "services/api/docs/skills/guide.md", hidden: false},
		{name: "unrelated folded prefix", roots: []string{""}, path: ".AGENTSTUFF/skills/review", hidden: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewPolicy(tt.roots...).Hidden(tt.path); got != tt.hidden {
				t.Fatalf("Hidden(%q) = %t, want %t", tt.path, got, tt.hidden)
			}
		})
	}
}

func TestPolicyIdentityTracksCheckoutTopology(t *testing.T) {
	first := NewPolicy("services/api", "services/web")
	reordered := NewPolicy("services/web", "services/api", "services/api")
	changed := NewPolicy("services/api", "packages/web")

	if first.Identity() != reordered.Identity() {
		t.Fatalf("equivalent topology identities differ: %q != %q", first.Identity(), reordered.Identity())
	}
	if first.Identity() == changed.Identity() {
		t.Fatalf("changed topology kept identity %q", first.Identity())
	}
}
