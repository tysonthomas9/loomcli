package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

const revisionColumns = `workspace, change_id, request_id, number, kind, operation, outcome, base_sha, head_sha, tree_hash, source_head_sha, derived_from_change, derived_from_number, ready, incomplete`

func (s *SQLite) RevisionByHead(ctx context.Context, workspace, change, head string) (loomgit.Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM change_revisions
		WHERE workspace = ? AND change_id = ? AND head_sha = ? AND ready = 1
		ORDER BY number DESC LIMIT 1`, workspace, change, head))
}

func scanRevision(row interface{ Scan(...any) error }) (loomgit.Revision, error) {
	var r loomgit.Revision
	err := row.Scan(&r.Workspace, &r.Change, &r.RequestID, &r.Number, &r.Kind,
		&r.Operation, &r.Outcome, &r.BaseSHA, &r.HeadSHA, &r.TreeHash,
		&r.SourceHeadSHA, &r.DerivedFromChange, &r.DerivedFromNumber, &r.Ready, &r.Incomplete)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func sameRevisionIntent(a, b loomgit.Revision) bool {
	return a.Workspace == b.Workspace && a.Change == b.Change && a.RequestID == b.RequestID &&
		a.Kind == b.Kind && a.Operation == b.Operation && a.Outcome == b.Outcome &&
		a.BaseSHA == b.BaseSHA && a.TreeHash == b.TreeHash && a.SourceHeadSHA == b.SourceHeadSHA &&
		a.DerivedFromChange == b.DerivedFromChange && a.DerivedFromNumber == b.DerivedFromNumber && a.Incomplete == b.Incomplete
}

func (s *SQLite) ReserveRevision(ctx context.Context, r loomgit.Revision) (loomgit.Revision, error) {
	if r.RequestID == "" || r.Workspace == "" || r.Change == "" || r.Kind == "" ||
		r.Outcome == "" || r.BaseSHA == "" || r.TreeHash == "" || r.SourceHeadSHA == "" {
		return loomgit.Revision{}, errors.New("incomplete revision reservation")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO change_revisions
		(workspace, change_id, number, request_id, kind, operation, outcome, base_sha,
		 tree_hash, source_head_sha, derived_from_change, derived_from_number, incomplete)
		SELECT ?, ?, COALESCE(MAX(number), 0)+1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM change_revisions WHERE workspace = ? AND change_id = ?
		ON CONFLICT(request_id) DO NOTHING`, r.Workspace, r.Change, r.RequestID,
		r.Kind, r.Operation, r.Outcome, r.BaseSHA, r.TreeHash, r.SourceHeadSHA,
		r.DerivedFromChange, r.DerivedFromNumber, r.Incomplete, r.Workspace, r.Change)
	if err != nil {
		return loomgit.Revision{}, err
	}
	got, err := scanRevision(s.db.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM change_revisions WHERE request_id = ?`, r.RequestID))
	if err != nil {
		return got, err
	}
	if !sameRevisionIntent(got, r) {
		return loomgit.Revision{}, fmt.Errorf("request ID %q reused for different revision", r.RequestID)
	}
	return got, nil
}

func (s *SQLite) FinishRevision(ctx context.Context, r loomgit.Revision) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE change_revisions SET head_sha = ?, ready = 1
		WHERE workspace = ? AND change_id = ? AND number = ? AND request_id = ?
		AND ready = 0 AND (head_sha = '' OR head_sha = ?)`, r.HeadSHA, r.Workspace, r.Change, r.Number, r.RequestID, r.HeadSHA)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var head string
		var ready bool
		err := tx.QueryRowContext(ctx, `SELECT head_sha,ready FROM change_revisions
			WHERE workspace=? AND change_id=? AND number=? AND request_id=?`,
			r.Workspace, r.Change, r.Number, r.RequestID).Scan(&head, &ready)
		if err == nil && ready && head == r.HeadSHA {
			return nil
		}
		return ErrStale
	}
	if err := queueRevisionEvent(ctx, tx, r); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE feedback_requests SET revision = ?
		WHERE workspace = ? AND change_id = ? AND request_id = ? AND base_sha = ? AND revision = 0`,
		r.Number, r.Workspace, r.Change, r.RequestID, r.BaseSHA); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE change_feedback SET status = 'addressed'
		WHERE workspace = ? AND change_id = ? AND status = 'pending' AND delivery_id IN
		(SELECT delivery_id FROM feedback_requests WHERE workspace = ? AND change_id = ?
		AND request_id = ? AND base_sha = ? AND revision = ?)`, r.Workspace, r.Change,
		r.Workspace, r.Change, r.RequestID, r.BaseSHA, r.Number); err != nil {
		return err
	}
	return tx.Commit()
}

func queueRevisionEvent(ctx context.Context, tx *sql.Tx, revision loomgit.Revision) error {
	payload, err := json.Marshal(struct {
		Workspace string `json:"workspace"`
		ChangeID  string `json:"change_id"`
		Revision  int    `json:"revision"`
		HeadSHA   string `json:"head_sha"`
		Kind      string `json:"kind"`
	}{revision.Workspace, revision.Change, revision.Number, revision.HeadSHA, revision.Kind})
	if err != nil {
		return err
	}
	return queueEvent(ctx, tx, "revision-event:"+revision.RequestID, "git.revision_created", payload)
}

func (s *SQLite) GetRevision(ctx context.Context, workspace, change string, number int) (loomgit.Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM change_revisions
		WHERE workspace = ? AND change_id = ? AND number = ?`, workspace, change, number))
}

// RevisionByRequest lets a retried driver attempt reuse its recorded capture.
func (s *SQLite) RevisionByRequest(ctx context.Context, requestID string) (loomgit.Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM change_revisions
		WHERE request_id = ?`, requestID))
}

var _ loomgit.RevisionStore = (*SQLite)(nil)
