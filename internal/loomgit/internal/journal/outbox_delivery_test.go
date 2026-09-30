package journal

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func TestOutboxDeliverySchemaBackfillsExistingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE journal_entries (id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE,
		operation TEXT NOT NULL, phase TEXT NOT NULL, version INTEGER NOT NULL, fence INTEGER NOT NULL);
		CREATE TABLE event_outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, entry_id TEXT NOT NULL REFERENCES journal_entries(id),
		kind TEXT NOT NULL, payload BLOB, delivered INTEGER NOT NULL DEFAULT 0);
		INSERT INTO journal_entries VALUES ('old','old','integrate','done',1,1);
		INSERT INTO event_outbox(entry_id,kind) VALUES ('old','git.integrated');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	pending, err := store.PendingEvents(context.Background())
	if err != nil || len(pending) != 1 || pending[0].JSONLEmitted {
		t.Fatalf("existing event not backfilled: %+v, %v", pending, err)
	}
}

func TestPendingEventExpiresBeforeLateBroadcast(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	entry, _, err := store.Begin(ctx, "expired", "integrate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(ctx, entry, "done", nil, []loomgit.OutboxEvent{{Kind: "git.integrated"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE event_outbox_delivery SET created_at=unixepoch()-601`); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.PendingEvents(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("stale event replayed: %+v, %v", pending, err)
	}
	var expired int
	if err := store.db.QueryRowContext(ctx, `SELECT expired FROM event_outbox_delivery`).Scan(&expired); err != nil || expired != 1 {
		t.Fatalf("event not marked expired: %d, %v", expired, err)
	}
}
