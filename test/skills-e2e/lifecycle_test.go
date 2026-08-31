//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
)

func TestSkillUpdateSelectsAndMaterializesExactRevision(t *testing.T) {
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

func TestIdenticalSkillReimportKeepsContentRevision(t *testing.T) {
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

func TestSkillContentUpdatePreservesBundledFiles(t *testing.T) {
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

func TestSkillRematerializationRemovesStaleFiles(t *testing.T) {
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

func TestSkillDeletionPrunesExistingMaterialization(t *testing.T) {
	loom := harness.Open(t)
	source := loom.SkillFixture("deleted-skill/current")

	loom.SkillImport(source)
	materialized := loom.SkillMaterialize()
	materialized.RequireExactTree(source, "deleted-skill")

	loom.SkillDelete("deleted-skill")
	loom.SkillMaterializeInto(materialized)
	materialized.RequireSkillAbsent("deleted-skill")
}

func TestSkillListReportsSelectedRevision(t *testing.T) {
	loom := harness.Open(t)
	source := loom.SkillFixture("listed-skill/current")

	loom.SkillImport(source)
	selected := loom.SkillShow("listed-skill")
	listed := loom.SkillList()

	loom.RequireListedSkill(selected, listed)
}
