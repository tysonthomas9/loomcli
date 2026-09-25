package stack

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func TestFormatMigrateReportConflict(t *testing.T) {
	out := formatMigrateReport(&stackstore.MigrateReport{
		Source: "/h/.loom/stacks.json", Workspace: "WS",
		Stacks: []stackstore.MigrateStackPlan{
			{StackID: "epic:E1", RepoName: "loomcli", Action: stackstore.MigrateConflict, Conflicts: []string{"node T1 differs"}},
			{StackID: "manual:m", RepoName: "loomcli", Action: stackstore.MigrateCreate, AddNodes: []string{"T2", "T3"}},
		},
		OtherWorkspaces: []string{"OTHER"},
	})
	assert.Contains(t, out, "conflict   epic:E1  repo=loomcli")
	assert.Contains(t, out, "! node T1 differs")
	assert.Contains(t, out, "add=T2,T3")
	assert.Contains(t, out, "not migrated by this run): OTHER")
	assert.Contains(t, out, "refused: resolve the conflicts above; nothing was written")
}

func TestFormatMigrateReportApplied(t *testing.T) {
	out := formatMigrateReport(&stackstore.MigrateReport{
		Source: "s", Workspace: "WS", Applied: true, Backup: "s.bak",
		Stacks: []stackstore.MigrateStackPlan{{StackID: "epic:E1", Action: stackstore.MigrateUnchanged}},
	})
	assert.Contains(t, out, "backup: s.bak")
	assert.Contains(t, out, "migrated\n")
	assert.Contains(t, formatMigrateReport(&stackstore.MigrateReport{SourceMissing: true, DryRun: true}), "(dry run)")
}
