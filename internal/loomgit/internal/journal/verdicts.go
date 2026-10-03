package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

func createReviewSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS revision_authors (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, number INTEGER NOT NULL,
		kind TEXT NOT NULL, actor_id TEXT NOT NULL,
		PRIMARY KEY(workspace, change_id, number)
	);
	CREATE TABLE IF NOT EXISTS review_verdicts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, number INTEGER NOT NULL,
		head_sha TEXT NOT NULL, kind TEXT NOT NULL, actor_kind TEXT NOT NULL,
		actor_id TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', source_verdict_id INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS review_verdicts_revision ON review_verdicts(workspace, change_id, number, id);`)
	return err
}

func (s *SQLite) SetRevisionAuthor(ctx context.Context, r loomgit.Revision, kind, id string) error {
	if kind == "" || id == "" {
		return errors.New("revision author kind and ID are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO revision_authors(workspace, change_id, number, kind, actor_id)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, r.Workspace, r.Change, r.Number, kind, id)
	if err != nil {
		return err
	}
	var savedKind, savedID string
	err = s.db.QueryRowContext(ctx, `SELECT kind, actor_id FROM revision_authors WHERE workspace=? AND change_id=? AND number=?`, r.Workspace, r.Change, r.Number).Scan(&savedKind, &savedID)
	if err != nil {
		return err
	}
	if savedKind != kind || savedID != id {
		return errors.New("revision author differs from recorded author")
	}
	return nil
}

func (s *SQLite) RevisionAuthor(ctx context.Context, r loomgit.Revision) (string, string, error) {
	var kind, id string
	err := s.db.QueryRowContext(ctx, `SELECT kind, actor_id FROM revision_authors WHERE workspace=? AND change_id=? AND number=?`, r.Workspace, r.Change, r.Number).Scan(&kind, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return kind, id, err
}

func (s *SQLite) SetRevisionIncomplete(ctx context.Context, r loomgit.Revision) error {
	result, err := s.db.ExecContext(ctx, `UPDATE change_revisions SET incomplete=1, no_changes=0 WHERE workspace=? AND change_id=? AND number=?`, r.Workspace, r.Change, r.Number)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) LatestSourceNumber(ctx context.Context, workspace, change string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) FROM change_revisions
		WHERE workspace=? AND change_id=? AND kind='source' AND ready=1`, workspace, change).Scan(&n)
	return n, err
}

func scanVerdict(row interface{ Scan(...any) error }) (loomgit.Verdict, error) {
	var v loomgit.Verdict
	err := row.Scan(&v.ID, &v.Workspace, &v.Change, &v.Number, &v.HeadSHA, &v.Kind,
		&v.ActorKind, &v.ActorID, &v.Reason, &v.SourceVerdictID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

const verdictColumns = `id, workspace, change_id, number, head_sha, kind, actor_kind, actor_id, reason, source_verdict_id`

// RecordVerdict atomically rejects an incomplete or superseded source revision.
// heldRevision matches a done layer on the follow's lead for its revision or a
// revision derived from it.
const heldRevision = `SELECT 1 FROM applied_layers a WHERE a.workspace=excluded.workspace
	AND a.lead=excluded.lead AND a.change_id=excluded.change_id AND a.phase='done'
	AND (a.revision=excluded.revision OR a.revision IN (SELECT r.number FROM change_revisions r
		WHERE r.workspace=excluded.workspace AND r.change_id=excluded.change_id
		AND r.kind='derived' AND r.derived_from_number=excluded.revision))`

func (s *SQLite) RecordVerdict(ctx context.Context, v loomgit.Verdict) (loomgit.Verdict, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO review_verdicts
		(workspace,change_id,number,head_sha,kind,actor_kind,actor_id,reason,source_verdict_id)
		SELECT ?,?,?,?,?,?,?,?,? WHERE EXISTS (
			SELECT 1 FROM change_revisions r WHERE r.workspace=? AND r.change_id=? AND r.number=?
			AND r.head_sha=? AND r.ready=1 AND r.incomplete=0
			AND (r.kind!='source' OR NOT EXISTS (
				SELECT 1 FROM change_revisions newer WHERE newer.workspace=r.workspace
				AND newer.change_id=r.change_id AND newer.kind='source' AND newer.ready=1 AND newer.number>r.number)))`,
		v.Workspace, v.Change, v.Number, v.HeadSHA, v.Kind, v.ActorKind, v.ActorID, v.Reason, v.SourceVerdictID,
		v.Workspace, v.Change, v.Number, v.HeadSHA)
	if err != nil {
		return v, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return v, err
	}
	if n != 1 {
		return v, fmt.Errorf("revision changed before verdict was recorded")
	}
	id, err := result.LastInsertId()
	if err != nil {
		return v, err
	}
	v.ID = id
	if err := recordApprovalTargets(ctx, tx, v); err != nil {
		return v, err
	}
	if err := queueVerdictEvent(ctx, tx, v); err != nil {
		return v, err
	}
	if err := tx.Commit(); err != nil {
		return v, err
	}
	return v, nil
}

func queueVerdictEvent(ctx context.Context, tx *sql.Tx, v loomgit.Verdict) error {
	payload, err := json.Marshal(struct {
		Workspace string `json:"workspace"`
		ChangeID  string `json:"change_id"`
		Revision  int    `json:"revision"`
		HeadSHA   string `json:"head_sha"`
		Kind      string `json:"kind"`
		ActorKind string `json:"actor_kind"`
		ActorID   string `json:"actor_id"`
	}{v.Workspace, v.Change, v.Number, v.HeadSHA, v.Kind, v.ActorKind, v.ActorID})
	if err != nil {
		return err
	}
	return queueEvent(ctx, tx, fmt.Sprintf("review-event:%d", v.ID), "git.review_recorded", payload)
}

func (s *SQLite) LatestVerdict(ctx context.Context, r loomgit.Revision) (loomgit.Verdict, error) {
	return scanVerdict(s.db.QueryRowContext(ctx, `SELECT `+verdictColumns+` FROM review_verdicts
		WHERE workspace=? AND change_id=? AND number=? ORDER BY id DESC LIMIT 1`, r.Workspace, r.Change, r.Number))
}

func (s *SQLite) VerdictByID(ctx context.Context, id int64) (loomgit.Verdict, error) {
	return scanVerdict(s.db.QueryRowContext(ctx, `SELECT `+verdictColumns+` FROM review_verdicts WHERE id=?`, id))
}

func (s *SQLite) ListTaskRevisions(ctx context.Context, workspace, task string) ([]loomgit.Revision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+revisionColumns+` FROM change_revisions
		WHERE workspace=? AND change_id IN (SELECT change_id FROM driver_changes WHERE workspace=? AND task_id=?)
		AND ready=1 ORDER BY number DESC`, workspace, workspace, task)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []loomgit.Revision
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// recordApprovalTargets records, with the verdict, the lead an approval is
// followed into and, for Approve and create PR, the intent to open its PR.
func recordApprovalTargets(ctx context.Context, tx *sql.Tx, v loomgit.Verdict) error {
	if v.TargetLead == "" || (v.Kind != "approve" && v.Kind != "override" && v.Kind != "policy") {
		return nil
	}
	// The follow always points at the newest approval. It stays 'applied'
	// only while the lead still holds this revision (or one derived from
	// it); otherwise the newer approval re-arms it, whatever its status.
	if _, err := tx.ExecContext(ctx, `INSERT INTO approval_follow(workspace,lead,change_id,revision,verdict_id)
		VALUES (?,?,?,?,?) ON CONFLICT(workspace,lead,change_id,revision) DO UPDATE SET
		verdict_id=excluded.verdict_id,
		status=CASE WHEN approval_follow.status='applied' AND EXISTS (`+heldRevision+`) THEN 'applied' ELSE 'approved' END,
		paths=CASE WHEN approval_follow.status='applied' AND EXISTS (`+heldRevision+`) THEN approval_follow.paths ELSE '[]' END
		WHERE excluded.verdict_id > approval_follow.verdict_id`,
		v.Workspace, v.TargetLead, v.Change, v.Number, v.ID); err != nil {
		return err
	}
	if !v.Publish {
		return nil
	}
	// Approving the same revision again re-arms an intent that ended without
	// a PR (spent, superseded, unapplied, or a skip whose cause may be fixed);
	// one that already opened its PR stays as it is.
	_, err := tx.ExecContext(ctx, `INSERT INTO approval_publications(workspace,lead,change_id,revision,verdict_id)
		VALUES (?,?,?,?,?) ON CONFLICT(workspace,lead,change_id,revision) DO UPDATE SET
		verdict_id=excluded.verdict_id, status='pending', reason='', pr_url='', pr_number=0, attempted_at=0
		WHERE approval_publications.status <> 'published'`,
		v.Workspace, v.TargetLead, v.Change, v.Number, v.ID)
	return err
}
