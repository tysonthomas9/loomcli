// Package loomstore is the agent registry inside `loom serve`: a pure-Go
// SQLite database holding agents, their message slots, the native session IDs
// each agent has ever owned, and the append-only agent_events log (design v2
// §8.1.8). `loom serve` is its only writer.
package loomstore

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

// Store is an open loomstore database.
type Store struct{ db *sql.DB }

// Open opens (creating if needed) the database at path with WAL, a busy
// timeout and foreign keys, then applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	for {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var v int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			_ = tx.Rollback()
			return err
		}
		if v >= len(migrations) {
			return tx.Rollback()
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("loomstore: migration %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
}

const stampLayout = "2006-01-02T15:04:05.000000000Z"

// Stamp formats t as the fixed-width UTC text stored in timestamp columns, so
// they compare correctly as strings.
func Stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
