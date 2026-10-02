package journal

import (
	"context"
	"database/sql"
	"errors"
)

// LandingAttention is a published change that landing reconcile cannot track
// until someone repairs it. Reconcile keeps going for every other change.
type LandingAttention struct {
	Workspace, Change, Reason string
}

func createLandingAttentionSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS landing_attention (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, reason TEXT NOT NULL,
		PRIMARY KEY(workspace, change_id)
	)`)
	return err
}

// RecordLandingAttention stores reason for a change and reports whether it is new.
func (s *SQLite) RecordLandingAttention(ctx context.Context, workspace, change, reason string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO landing_attention(workspace,change_id,reason) VALUES (?,?,?)
		ON CONFLICT(workspace,change_id) DO UPDATE SET reason=excluded.reason WHERE reason<>excluded.reason`,
		workspace, change, reason)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// ClearLandingAttention removes a change's attention and reports whether it had one.
func (s *SQLite) ClearLandingAttention(ctx context.Context, workspace, change string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM landing_attention WHERE workspace=? AND change_id=?`, workspace, change)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// LandingAttentions lists every change needing repair. A journal written
// before landing attention existed has none.
func (s *SQLite) LandingAttentions(ctx context.Context) ([]LandingAttention, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='landing_attention'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,reason FROM landing_attention ORDER BY workspace,change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LandingAttention
	for rows.Next() {
		var item LandingAttention
		if err := rows.Scan(&item.Workspace, &item.Change, &item.Reason); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
