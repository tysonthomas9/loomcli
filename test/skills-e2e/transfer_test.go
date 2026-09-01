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
		{ID: 40, Behavior: "a truncated download is rejected", Rationale: "a real proxy shortens the successful provider response"},
		{ID: 41, Behavior: "invalid downloaded bytes are not materialized", Rationale: "the public materialize command fails and leaves no Skill tree"},
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
