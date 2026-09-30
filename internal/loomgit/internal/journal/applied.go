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
	result, err := s.db.ExecContext(ctx, `INSERT INTO applied_layers
		(request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,commit_details,phase)
		VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET
		workspace=excluded.workspace,lead=excluded.lead,change_id=excluded.change_id,
		revision=excluded.revision,old_tip=excluded.old_tip,new_tip=excluded.new_tip,
		commits=excluded.commits,dropped=excluded.dropped,commit_details=excluded.commit_details,phase='prepared'
		WHERE applied_layers.phase='not_applied' AND applied_layers.workspace=excluded.workspace
		AND applied_layers.lead=excluded.lead AND applied_layers.change_id=excluded.change_id`,
		a.RequestID, a.Workspace, a.Lead, a.Change, a.Revision, a.OldTip, a.NewTip, commits, dropped, details, "prepared")
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrStale
	}
	return nil
}

func (s *SQLite) AdvanceApplied(ctx context.Context, requestID, oldPhase, nextPhase string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := tx.ExecContext(ctx, `UPDATE applied_layers SET phase=? WHERE request_id=? AND phase=?`, nextPhase, requestID, oldPhase)
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
	if nextPhase == "done" {
		var workspace, lead, change, sha string
		var revision int
		err = tx.QueryRowContext(ctx, `SELECT workspace,lead,change_id,revision,new_tip FROM applied_layers WHERE request_id=?`, requestID).
			Scan(&workspace, &lead, &change, &revision, &sha)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(struct {
			Workspace    string `json:"workspace"`
			Lead         string `json:"lead"`
			ChangeID     string `json:"change_id"`
			Revision     int    `json:"revision"`
			WorkspaceSHA string `json:"workspace_sha"`
		}{workspace, lead, change, revision, sha})
		if err != nil {
			return err
		}
		entryID := "apply-event:" + requestID
		if err := queueEvent(ctx, tx, entryID, "git.integrated", payload); err != nil {
			return err
		}
	}
	return tx.Commit()
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

func (s *SQLite) OpenApplied(ctx context.Context, workspace, lead string) ([]loomgit.AppliedLayer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,commit_details,phase
		FROM applied_layers WHERE workspace=? AND lead=? AND phase NOT IN ('done','not_applied') ORDER BY rowid`, workspace, lead)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []loomgit.AppliedLayer
	for rows.Next() {
		var layer loomgit.AppliedLayer
		var commits, dropped, details []byte
		if err := rows.Scan(&layer.RequestID, &layer.Workspace, &layer.Lead, &layer.Change, &layer.Revision,
			&layer.OldTip, &layer.NewTip, &commits, &dropped, &details, &layer.Phase); err != nil {
			return nil, err
		}
		if err := errors.Join(json.Unmarshal(commits, &layer.Commits), json.Unmarshal(dropped, &layer.DroppedCommits),
			json.Unmarshal(details, &layer.CommitDetails)); err != nil {
			return nil, err
		}
		result = append(result, layer)
	}
	return result, rows.Err()
}

type AppliedTarget struct {
	Workspace string
	Lead      string
}

func (s *SQLite) OpenAppliedTargets(ctx context.Context) ([]AppliedTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace, lead FROM applied_layers
		WHERE phase NOT IN ('done','not_applied') ORDER BY workspace, lead`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var targets []AppliedTarget
	for rows.Next() {
		var target AppliedTarget
		if err := rows.Scan(&target.Workspace, &target.Lead); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}
