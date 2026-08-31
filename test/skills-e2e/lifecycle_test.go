//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
)

func TestSkillLifecycle(t *testing.T) {
	t.Run("updates the selected revision and materializes the exact tree", func(t *testing.T) {
		env := harness.Open(t)

		initial := env.ImportSkill(t, "exact-round-trip/initial")
		selected := env.ImportSkill(t, "exact-round-trip/updated")

		if initial.FileTreeRevision == selected.FileTreeRevision {
			t.Fatalf("updated Skill retained initial tree revision %q", initial.FileTreeRevision)
		}

		env.RequireSkill(t, selected, "exact-round-trip/expected.json")
		env.MaterializeSkills(t).RequireExactTree(t, "exact-round-trip/updated", "exact-round-trip")

		t.Logf("verified initial_revision=%s selected_revision=%s", initial.FileTreeRevision, selected.FileTreeRevision)
	})
}
