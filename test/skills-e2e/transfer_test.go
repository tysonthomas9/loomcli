//go:build e2e

package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/harness"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var corruptDownloadIsRejected = registry.Scenario{
	ID:       "corrupt-download-is-rejected",
	Behavior: "materialization bounds downloaded bytes and verifies SHA before returning them",
	Cases: []registry.EdgeCase{
		{ID: 34},
	},
}

func TestCorruptSkillDownloadIsNotMaterialized(t *testing.T) {
	corruptDownloadIsRejected.Covers(t)
	for _, tc := range []struct {
		name    string
		corrupt func(*harness.Environment, string) *harness.CorruptDownload
	}{
		{name: "declared size mismatch", corrupt: (*harness.Environment).CorruptNextFileDownload},
		{name: "same-length SHA mismatch", corrupt: (*harness.Environment).CorruptNextFileDownloadSameLength},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loom := harness.Open(t)
			source := loom.SkillFixture("corrupt-download/current")

			loom.SkillImport(source)
			selected := loom.SkillShow("corrupt-download")
			corruption := tc.corrupt(loom, selected.FileTreeRevision)
			materialized := loom.SkillMaterializeFails()
			corruption.RequireActivated()
			materialized.RequireSkillAbsent("corrupt-download")
		})
	}
}
