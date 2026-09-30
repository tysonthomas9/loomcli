package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// WorkspaceCreation records the intended owner and paths before any checkout
// is made. The journal entry remains open until all remote and local records
// have been reconciled.
type WorkspaceCreation struct {
	RequestID string                  `json:"request_id"`
	Kind      string                  `json:"kind"`
	Name      string                  `json:"name"`
	Path      string                  `json:"path"`
	Trunk     string                  `json:"trunk"`
	Repos     []WorkspaceCreationRepo `json:"repos"`
}

type WorkspaceCreationRepo struct {
	Name, Source, Path, Branch, BaseSHA string
}

func initWorkspaceCreationSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS workspace_creations (
		entry_id TEXT PRIMARY KEY REFERENCES journal_entries(id) ON DELETE CASCADE,
		plan BLOB NOT NULL
	)`)
	return err
}

func (s *SQLite) SaveWorkspaceCreation(ctx context.Context, entry loomgit.JournalEntry, plan WorkspaceCreation) error {
	if entry.Phase != "started" {
		return ErrStale
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workspace_creations(entry_id,plan) VALUES (?,?)`, entry.ID, data)
	return err
}

func (s *SQLite) ReplaceWorkspaceCreation(ctx context.Context, entry loomgit.JournalEntry, plan WorkspaceCreation) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	r, err := s.db.ExecContext(ctx, `UPDATE workspace_creations SET plan=? WHERE entry_id=? AND EXISTS (
		SELECT 1 FROM journal_entries WHERE id=? AND version=? AND fence=? AND phase='started'
	)`, data, entry.ID, entry.ID, entry.Version, entry.Fence)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStale
	}
	return nil
}

func (s *SQLite) WorkspaceCreation(ctx context.Context, entry loomgit.JournalEntry) (WorkspaceCreation, error) {
	var data []byte
	if err := s.db.QueryRowContext(ctx, `SELECT plan FROM workspace_creations WHERE entry_id=?`, entry.ID).Scan(&data); err != nil {
		return WorkspaceCreation{}, err
	}
	var plan WorkspaceCreation
	if err := json.Unmarshal(data, &plan); err != nil {
		return WorkspaceCreation{}, fmt.Errorf("decode workspace creation: %w", err)
	}
	return plan, nil
}
