package journal

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func TestWorkingAreaBackfillsCompletedDefaultLead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, _, err := st.Begin(ctx, "workspace-create:W", "ensure_workspace")
	if err != nil {
		t.Fatal(err)
	}
	plan := WorkspaceCreation{Repos: []WorkspaceCreationRepo{{Name: "app", Path: "/workspace/app", Branch: "loom/ws/W/interactive/lead", BaseSHA: "abc", Mode: "worktree"}}}
	if err := st.SaveWorkspaceCreation(ctx, entry, plan); err != nil {
		t.Fatal(err)
	}
	entry, err = st.Advance(ctx, entry, "checkouts_added", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err = st.Advance(ctx, entry, "rows_written", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{{Workspace: "W", Repo: "app", Trunk: "main", WorkspaceBranch: "loom/ws/W/interactive/lead", BaseSHA: "abc"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM working_areas`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	areas, err := st.WorkingAreas(ctx, "W", "lead")
	if err != nil || len(areas) != 1 || areas[0].Path != "/workspace/app" {
		t.Fatalf("backfill = %+v, %v", areas, err)
	}
}
