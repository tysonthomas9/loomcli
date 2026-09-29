package journal

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func TestFinishWorkspaceDeletionLeavesTombstoneAndClearsCreationRecords(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	entry, _, err := st.Begin(ctx, "workspace-create:TEST", "ensure_workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{{Workspace: "TEST", Repo: "repo", Trunk: "main", WorkspaceBranch: "lead", BaseSHA: "abc"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE workspace_settings (workspace TEXT PRIMARY KEY); INSERT INTO workspace_settings(workspace) VALUES ('TEST')`); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishWorkspaceDeletion(ctx, "TEST"); err != nil {
		t.Fatal(err)
	}
	repos, err := st.WorkspaceRepos(ctx, "TEST")
	if err != nil || len(repos) != 0 {
		t.Fatalf("repos=%v err=%v", repos, err)
	}
	var prefix string
	if err := st.db.QueryRowContext(ctx, `SELECT ref_prefix FROM workspace_ref_tombstones WHERE workspace='TEST'`).Scan(&prefix); err != nil {
		t.Fatal(err)
	}
	if prefix != "refs/loom/ws/TEST/" {
		t.Fatalf("prefix=%q", prefix)
	}
	var settings int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM workspace_settings WHERE workspace='TEST'`).Scan(&settings); err != nil || settings != 0 {
		t.Fatalf("settings=%d err=%v", settings, err)
	}
	_, created, err := st.Begin(ctx, "workspace-create:TEST", "ensure_workspace")
	if err != nil || !created {
		t.Fatalf("recreate journal: created=%v err=%v", created, err)
	}
}
