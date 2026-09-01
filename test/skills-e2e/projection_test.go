//go:build e2e

package skillse2e_test

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var delayedProjectionBecomesReadable = registry.Scenario{
	ID:       "delayed-projection-becomes-readable",
	Behavior: "Skill import waits for a durably published tree to become readable",
	Cases: []registry.EdgeCase{
		{ID: 64},
	},
}

func TestSkillImportWaitsForDelayedTreeVisibility(t *testing.T) {
	delayedProjectionBecomesReadable.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("delayed-visibility/current")

	loom.DelayNextTreeProjection(250 * time.Millisecond)
	loom.SkillImport(source)
	loom.RequireLastCommandActivated("workspace-file-inline-projection")
	loom.RequireLastCommandActivated("workspace-file-background-delay")

	selected := loom.SkillShow("delayed-visibility")
	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(source, selected.Name)
}
