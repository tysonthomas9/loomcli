//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var skillUpdateRoundTrip = registry.Scenario{
	ID:       "skill-update-roundtrip",
	Behavior: "an update selects and materializes the exact new revision",
	Cases: []registry.EdgeCase{
		{ID: 1},
		{ID: 2},
		{ID: 3},
		{ID: 12},
	},
}

func TestSkillUpdateSelectsAndMaterializesExactRevision(t *testing.T) {
	skillUpdateRoundTrip.Covers(t)
	loom := harness.Open(t)
	initialSource := loom.SkillFixture("exact-round-trip/initial")
	updatedSource := loom.SkillFixture("exact-round-trip/updated")

	loom.SkillImport(initialSource)
	initial := loom.SkillShow("exact-round-trip")

	loom.SkillImport(updatedSource)
	selected := loom.SkillShow("exact-round-trip")

	if initial.FileTreeRevision == selected.FileTreeRevision {
		t.Fatalf("updated Skill retained initial tree revision %q", initial.FileTreeRevision)
	}
	loom.RequireSkill(selected, "exact-round-trip/expected.json")

	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(updatedSource, "exact-round-trip")
}

var stableIdenticalReimport = registry.Scenario{
	ID:       "stable-identical-reimport",
	Behavior: "importing identical content retains the content revision",
}

func TestIdenticalSkillReimportKeepsContentRevision(t *testing.T) {
	stableIdenticalReimport.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("stable-reimport/current")

	loom.SkillImport(source)
	first := loom.SkillShow("stable-reimport")

	loom.SkillImport(source)
	second := loom.SkillShow("stable-reimport")

	if first.FileTreeRevision != second.FileTreeRevision {
		t.Fatalf("identical reimport changed revision: first=%q second=%q",
			first.FileTreeRevision, second.FileTreeRevision)
	}

	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(source, "stable-reimport")
}

var contentUpdatePreservesBundles = registry.Scenario{
	ID:       "content-update-preserves-bundles",
	Behavior: "updating only Skill content preserves every bundled file",
}

func TestSkillContentUpdatePreservesBundledFiles(t *testing.T) {
	contentUpdatePreservesBundles.Covers(t)
	loom := harness.Open(t)
	originalSource := loom.SkillFixture("content-update/original")
	expectedSource := loom.SkillFixture("content-update/expected")
	replacementBody := loom.FileFixture("content-update/replacement-body.md")

	loom.SkillImport(originalSource)
	before := loom.SkillShow("content-update")

	loom.SkillUpdateContent("content-update", replacementBody)
	after := loom.SkillShow("content-update")

	if before.FileTreeRevision == after.FileTreeRevision {
		t.Fatalf("content update retained original revision %q", before.FileTreeRevision)
	}
	if len(before.Files) != len(after.Files) {
		t.Fatalf("content update changed bundled file count: before=%d after=%d", len(before.Files), len(after.Files))
	}

	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(expectedSource, "content-update")
}

var rematerializationPrunesStaleFiles = registry.Scenario{
	ID:       "rematerialization-prunes-stale-files",
	Behavior: "rematerialization removes files absent from the selected revision",
}

func TestSkillRematerializationRemovesStaleFiles(t *testing.T) {
	rematerializationPrunesStaleFiles.Covers(t)
	loom := harness.Open(t)
	initialSource := loom.SkillFixture("shrinking-tree/initial")
	updatedSource := loom.SkillFixture("shrinking-tree/updated")

	loom.SkillImport(initialSource)
	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(initialSource, "shrinking-tree")

	loom.SkillImport(updatedSource)
	loom.SkillMaterializeInto(materialized)
	materialized.RequireExactTree(updatedSource, "shrinking-tree")
}

var deletionPrunesMaterialization = registry.Scenario{
	ID:       "deletion-prunes-materialization",
	Behavior: "deleting a Skill prunes it from an existing materialization",
}

func TestSkillDeletionPrunesExistingMaterialization(t *testing.T) {
	deletionPrunesMaterialization.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("deleted-skill/current")

	loom.SkillImport(source)
	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(source, "deleted-skill")

	loom.SkillDelete("deleted-skill")
	loom.SkillMaterializeInto(materialized)
	materialized.RequireSkillAbsent("deleted-skill")
}

var listShowRevisionAgreement = registry.Scenario{
	ID:       "list-show-revision-agreement",
	Behavior: "public list and show results report the same selected revision",
}

func TestSkillListReportsSelectedRevision(t *testing.T) {
	listShowRevisionAgreement.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("listed-skill/current")

	loom.SkillImport(source)
	selected := loom.SkillShow("listed-skill")
	listed := loom.SkillList()

	loom.RequireListedSkill(selected, listed)
}
