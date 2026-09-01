//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var corruptDownloadIsRejected = registry.Scenario{
	ID:       "corrupt-download-is-rejected",
	Behavior: "materialization rejects object bytes changed in transit",
}

func TestCorruptSkillDownloadIsNotMaterialized(t *testing.T) {
	corruptDownloadIsRejected.Covers(t)
	loom := harness.Open(t)
	source := loom.SkillFixture("corrupt-download/current")

	loom.SkillImport(source)
	selected := loom.SkillShow("corrupt-download")
	corruption := loom.CorruptNextFileDownload(selected.FileTreeRevision)
	materialized := loom.SkillMaterializeFails()
	corruption.RequireActivated()
	materialized.RequireSkillAbsent("corrupt-download")
}
