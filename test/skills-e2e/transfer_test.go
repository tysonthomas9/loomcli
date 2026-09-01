//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var corruptDownloadIsRejected = registry.Scenario{
	ID:        "corrupt-download-is-rejected",
	Behavior:  "materialization rejects object bytes changed in transit",
	Owner:     "loom",
	Seam:      "loom-fleet-e2e",
	Backends:  []string{"redis", "postgres"},
	Providers: []string{"minio"},
	Cases: []registry.EdgeCase{
		{ID: 34, Behavior: "download size and SHA-256 are verified before bytes are returned", Rationale: "a real proxy truncates provider bytes and the public materialize command rejects them"},
	},
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
