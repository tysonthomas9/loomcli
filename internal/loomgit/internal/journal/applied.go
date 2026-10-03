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
	CREATE INDEX IF NOT EXISTS applied_layers_lead ON applied_layers(workspace, lead);
	CREATE TABLE IF NOT EXISTS lead_following (
		workspace TEXT NOT NULL, lead TEXT NOT NULL, paused INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(workspace, lead)
	);
	CREATE TABLE IF NOT EXISTS approval_follow (
		workspace TEXT NOT NULL, lead TEXT NOT NULL, change_id TEXT NOT NULL,
		revision INTEGER NOT NULL, verdict_id INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'approved',
		paths BLOB NOT NULL DEFAULT '[]', PRIMARY KEY(workspace, lead, change_id, revision)
	);`)
	if err != nil {
		return err
	}
	return createEpicPublicationSchema(db)
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

const appliedLayersWhere = `SELECT request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,commit_details,phase
		FROM applied_layers WHERE workspace=? AND lead=? AND `

func (s *SQLite) AppliedLog(ctx context.Context, workspace, lead string) ([]loomgit.AppliedLayer, error) {
	return s.appliedLayers(ctx, appliedLayersWhere+`phase='done' ORDER BY rowid`, workspace, lead)
}

func (s *SQLite) OpenApplied(ctx context.Context, workspace, lead string) ([]loomgit.AppliedLayer, error) {
	return s.appliedLayers(ctx, appliedLayersWhere+`phase NOT IN ('done','not_applied','unapplied') ORDER BY rowid`, workspace, lead)
}

func (s *SQLite) appliedLayers(ctx context.Context, query, workspace, lead string) ([]loomgit.AppliedLayer, error) {
	rows, err := s.db.QueryContext(ctx, query, workspace, lead)
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
		WHERE phase NOT IN ('done','not_applied','unapplied') ORDER BY workspace, lead`)
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

type PendingApproval struct {
	Workspace, Lead, Change, Repo, Predecessor, Status string
	Revision, VerdictID                                int
}

type ApprovalTarget struct {
	Workspace, Lead string
}

func (s *SQLite) PendingApprovalTargets(ctx context.Context) ([]ApprovalTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace,lead FROM approval_follow
		WHERE status NOT IN ('applied','superseded') ORDER BY workspace,lead`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var targets []ApprovalTarget
	for rows.Next() {
		var target ApprovalTarget
		if err := rows.Scan(&target.Workspace, &target.Lead); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *SQLite) FollowingPaused(ctx context.Context, workspace, lead string) (bool, error) {
	var paused int
	err := s.db.QueryRowContext(ctx, `SELECT paused FROM lead_following WHERE workspace=? AND lead=?`, workspace, lead).Scan(&paused)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return paused != 0, err
}

func (s *SQLite) SetFollowingPaused(ctx context.Context, workspace, lead string, paused bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_following(workspace,lead,paused) VALUES (?,?,?)
		ON CONFLICT(workspace,lead) DO UPDATE SET paused=excluded.paused`, workspace, lead, paused)
	return err
}

func (s *SQLite) PendingApprovals(ctx context.Context, workspace, lead string) ([]PendingApproval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.workspace,a.lead,a.change_id,a.revision,a.verdict_id,a.status,
		COALESCE(d.repo,''),COALESCE(l.predecessor_change,'')
		FROM approval_follow a LEFT JOIN driver_changes d ON d.workspace=a.workspace AND d.change_id=a.change_id
		LEFT JOIN local_lineage l ON l.workspace=d.workspace AND l.task_id=d.task_id AND l.repo=d.repo
		WHERE a.workspace=? AND a.lead=? AND a.status NOT IN ('applied','superseded') ORDER BY a.verdict_id`, workspace, lead)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var pending []PendingApproval
	for rows.Next() {
		var approval PendingApproval
		if err := rows.Scan(&approval.Workspace, &approval.Lead, &approval.Change, &approval.Revision,
			&approval.VerdictID, &approval.Status, &approval.Repo, &approval.Predecessor); err != nil {
			return nil, err
		}
		pending = append(pending, approval)
	}
	return pending, rows.Err()
}

func (s *SQLite) ApprovalApplied(ctx context.Context, requestID string) (bool, error) {
	var phase string
	err := s.db.QueryRowContext(ctx, `SELECT phase FROM applied_layers WHERE request_id=?`, requestID).Scan(&phase)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return phase == "done", err
}

// AppliedRequest returns the layer recorded for requestID, if any.
func (s *SQLite) AppliedRequest(ctx context.Context, requestID string) (loomgit.AppliedLayer, bool, error) {
	layer := loomgit.AppliedLayer{RequestID: requestID}
	err := s.db.QueryRowContext(ctx, `SELECT workspace,lead,change_id,revision,old_tip,new_tip,phase
		FROM applied_layers WHERE request_id=?`, requestID).
		Scan(&layer.Workspace, &layer.Lead, &layer.Change, &layer.Revision, &layer.OldTip, &layer.NewTip, &layer.Phase)
	if errors.Is(err, sql.ErrNoRows) {
		return loomgit.AppliedLayer{}, false, nil
	}
	return layer, err == nil, err
}

func (s *SQLite) PredecessorApplied(ctx context.Context, workspace, lead, change string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM applied_layers WHERE workspace=? AND lead=?
		AND change_id=? AND phase='done'`, workspace, lead, change).Scan(&count)
	return count > 0, err
}

// RevisionApplied reports whether lead currently has this exact change
// revision applied; an empty lead matches any lead in the workspace. Unapply
// moves the layer out of 'done'.
func (s *SQLite) RevisionApplied(ctx context.Context, workspace, lead, change string, revision int) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM applied_layers WHERE workspace=?
		AND (?='' OR lead=?) AND change_id=? AND revision=? AND phase='done'`,
		workspace, lead, lead, change, revision).Scan(&count)
	return count > 0, err
}

// ApprovalLeads lists the leads an approving verdict on this revision targets.
func (s *SQLite) ApprovalLeads(ctx context.Context, workspace, change string, revision int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lead FROM approval_follow
		WHERE workspace=? AND change_id=? AND revision=? ORDER BY lead`, workspace, change, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var leads []string
	for rows.Next() {
		var lead string
		if err := rows.Scan(&lead); err != nil {
			return nil, err
		}
		leads = append(leads, lead)
	}
	return leads, rows.Err()
}

func (s *SQLite) SetApprovalFollow(ctx context.Context, approval PendingApproval, status string, paths []string) error {
	data, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE approval_follow SET status=?,paths=?
		WHERE workspace=? AND lead=? AND change_id=? AND revision=? AND verdict_id=?`,
		status, data, approval.Workspace, approval.Lead, approval.Change, approval.Revision, approval.VerdictID)
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

type EpicPublication struct {
	Workspace string
	RunID     string
	Lead      string
	Changes   []string
}

func createEpicPublicationSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS epic_publications (
		workspace TEXT NOT NULL, run_id TEXT NOT NULL, lead TEXT NOT NULL,
		changes BLOB NOT NULL, done INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(workspace,run_id)
	);
	CREATE INDEX IF NOT EXISTS epic_publications_pending ON epic_publications(done,workspace,lead);`)
	return err
}

func (s *SQLite) RecordEpicPublication(ctx context.Context, intent EpicPublication) error {
	if intent.Workspace == "" || intent.RunID == "" || intent.Lead == "" || len(intent.Changes) == 0 {
		return errors.New("incomplete epic publication")
	}
	changes, err := json.Marshal(intent.Changes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO epic_publications(workspace,run_id,lead,changes)
		VALUES (?,?,?,?) ON CONFLICT(workspace,run_id) DO NOTHING`,
		intent.Workspace, intent.RunID, intent.Lead, changes)
	return err
}

func (s *SQLite) PendingEpicPublications(ctx context.Context) ([]EpicPublication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,run_id,lead,changes FROM epic_publications
		WHERE done=0 ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var intents []EpicPublication
	for rows.Next() {
		var intent EpicPublication
		var changes []byte
		if err := rows.Scan(&intent.Workspace, &intent.RunID, &intent.Lead, &changes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(changes, &intent.Changes); err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (s *SQLite) CompleteEpicPublication(ctx context.Context, workspace, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE epic_publications SET done=1 WHERE workspace=? AND run_id=?`, workspace, runID)
	return err
}
