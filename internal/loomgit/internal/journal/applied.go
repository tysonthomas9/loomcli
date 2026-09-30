package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func createAppliedSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS applied_layers (
		request_id TEXT PRIMARY KEY, workspace TEXT NOT NULL, lead TEXT NOT NULL,
		change_id TEXT NOT NULL, revision INTEGER NOT NULL, old_tip TEXT NOT NULL,
		new_tip TEXT NOT NULL, commits BLOB NOT NULL, dropped BLOB NOT NULL,
		commit_details BLOB NOT NULL DEFAULT '[]',
		phase TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS applied_layers_lead ON applied_layers(workspace, lead);`)
	return err
}

// SaveApplied records the intended ref transition before the checkout is touched.
func (s *SQLite) SaveApplied(ctx context.Context, a loomgit.AppliedLayer) error {
	commits, err := json.Marshal(a.Commits)
	if err != nil {
		return err
	}
	dropped, err := json.Marshal(a.DroppedCommits)
	if err != nil {
		return err
	}
	details, err := json.Marshal(a.CommitDetails)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO applied_layers
		(request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,commit_details,phase)
		VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO NOTHING`,
		a.RequestID, a.Workspace, a.Lead, a.Change, a.Revision, a.OldTip, a.NewTip, commits, dropped, details, "prepared")
	return err
}

func (s *SQLite) AdvanceApplied(ctx context.Context, requestID, oldPhase, nextPhase string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE applied_layers SET phase=? WHERE request_id=? AND phase=?`, nextPhase, requestID, oldPhase)
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

func (s *SQLite) AppliedLog(ctx context.Context, workspace, lead string) ([]loomgit.AppliedLayer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,commit_details,phase
		FROM applied_layers WHERE workspace=? AND lead=? AND phase='done' ORDER BY rowid`, workspace, lead)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []loomgit.AppliedLayer
	for rows.Next() {
		var a loomgit.AppliedLayer
		var commits, dropped, details []byte
		if err := rows.Scan(&a.RequestID, &a.Workspace, &a.Lead, &a.Change, &a.Revision,
			&a.OldTip, &a.NewTip, &commits, &dropped, &details, &a.Phase); err != nil {
			return nil, err
		}
		if err := errors.Join(json.Unmarshal(commits, &a.Commits), json.Unmarshal(dropped, &a.DroppedCommits),
			json.Unmarshal(details, &a.CommitDetails)); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
