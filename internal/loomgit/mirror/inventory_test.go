package mirror

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestInventoryStatusRefreshesAfterPublication(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	before, err := InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO change_publications(workspace,change_id,repo,branch,trunk,slug,head_sha,phase) VALUES ('W1','C1','repo','pr','trunk','c1','abc123','done')`); err != nil {
		t.Fatal(err)
	}
	after, err := InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) != len(before.Entries)+1 || after.Entries[len(after.Entries)-1].Kind != "publication" {
		t.Fatalf("publication mutation not visible: before=%+v after=%+v", before.Entries, after.Entries)
	}
}
