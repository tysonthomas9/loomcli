package retention

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	loomretention "github.com/tysonthomas9/loomcli/internal/loomgit/retention"
)

func TestRetentionCLIReportsIncompleteCopyWithoutRemovingIt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	if _, err := loomretention.RunAt(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO retained_task_copies
		(workspace,change_id,attempt,path,source_repo,complete)
		VALUES('W','C','A','/missing/copy','/missing/source',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO landed_changes(workspace,change_id) VALUES('W','C')`); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := *retentionCmd
	cmd.SetOut(&output)
	if err := runRetention(ctx, path, true, &cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "keep W C /missing/copy: capture incomplete") {
		t.Fatalf("unexpected report: %q", output.String())
	}
}
