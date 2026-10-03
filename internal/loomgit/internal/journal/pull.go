package journal

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func (s *SQLite) CompletePull(ctx context.Context, requestID, workspace, lead, repo, base string, layers []loomgit.AppliedLayer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM applied_layers WHERE request_id=? AND change_id='pull'`, requestID).Scan(&phase); err != nil {
		return err
	}
	if phase != "done" {
		return ErrStale
	}
	var removed string
	if err := tx.QueryRowContext(ctx, `SELECT remove_change FROM pull_plans WHERE request_id=? AND workspace=? AND lead=? AND repo=?`,
		requestID, workspace, lead, repo).Scan(&removed); err != nil {
		return err
	}
	if removed != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE applied_layers SET phase='unapplied'
			WHERE workspace=? AND lead=? AND change_id=? AND phase='done'`, workspace, lead, removed); err != nil {
			return err
		}
	}
	for _, layer := range layers {
		if err := insertPulledLayer(ctx, tx, layer); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE working_areas SET base_sha=? WHERE workspace=? AND lead=? AND repo=?`, base, workspace, lead, repo)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrStale
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM applied_layers WHERE request_id=? AND phase='done' AND change_id='pull'`, requestID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pull_plans WHERE request_id=?`, requestID); err != nil {
		return err
	}
	return tx.Commit()
}

func insertPulledLayer(ctx context.Context, tx *sql.Tx, layer loomgit.AppliedLayer) error {
	commits, err := json.Marshal(layer.Commits)
	if err != nil {
		return err
	}
	dropped, err := json.Marshal(layer.DroppedCommits)
	if err != nil {
		return err
	}
	details, err := json.Marshal(layer.CommitDetails)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO applied_layers
		(request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,commit_details,phase)
		VALUES (?,?,?,?,?,?,?,?,?,?,'done')`, layer.RequestID, layer.Workspace, layer.Lead,
		layer.Change, layer.Revision, layer.OldTip, layer.NewTip, commits, dropped, details)
	return err
}

type PullPlan struct {
	RequestID, Workspace, Lead, Repo, BaseSHA, Phase, RemoveChange string
	Layers                                                         []loomgit.AppliedLayer
}

func (s *SQLite) SavePullPlan(ctx context.Context, plan PullPlan) error {
	layers, err := json.Marshal(plan.Layers)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO pull_plans(request_id,workspace,lead,repo,base_sha,layers,remove_change)
		VALUES (?,?,?,?,?,?,?)`, plan.RequestID, plan.Workspace, plan.Lead, plan.Repo, plan.BaseSHA, layers, plan.RemoveChange)
	return err
}

func (s *SQLite) PendingPullPlans(ctx context.Context, workspace, lead string) ([]PullPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.request_id,p.workspace,p.lead,p.repo,p.base_sha,p.layers,p.remove_change,
		COALESCE(a.phase,'') FROM pull_plans p LEFT JOIN applied_layers a ON a.request_id=p.request_id
		WHERE p.workspace=? AND p.lead=? ORDER BY p.rowid`, workspace, lead)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPullPlans(rows)
}

func (s *SQLite) OpenPullPlans(ctx context.Context) ([]PullPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.request_id,p.workspace,p.lead,p.repo,p.base_sha,p.layers,p.remove_change,
		COALESCE(a.phase,'') FROM pull_plans p LEFT JOIN applied_layers a ON a.request_id=p.request_id
		ORDER BY p.rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPullPlans(rows)
}

func scanPullPlans(rows *sql.Rows) ([]PullPlan, error) {
	var plans []PullPlan
	for rows.Next() {
		var plan PullPlan
		var data []byte
		if err := rows.Scan(&plan.RequestID, &plan.Workspace, &plan.Lead, &plan.Repo, &plan.BaseSHA, &data, &plan.RemoveChange, &plan.Phase); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &plan.Layers); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *SQLite) DiscardPullPlan(ctx context.Context, requestID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pull_plans WHERE request_id=?`, requestID)
	return err
}
