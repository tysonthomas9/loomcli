package journal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

const revisionColumns = `workspace, change_id, request_id, number, kind, operation, outcome, base_sha, head_sha, tree_hash, source_head_sha, derived_from_change, derived_from_number, ready`

func scanRevision(row interface{ Scan(...any) error }) (loomgit.Revision, error) {
	var r loomgit.Revision
	err := row.Scan(&r.Workspace, &r.Change, &r.RequestID, &r.Number, &r.Kind,
		&r.Operation, &r.Outcome, &r.BaseSHA, &r.HeadSHA, &r.TreeHash,
		&r.SourceHeadSHA, &r.DerivedFromChange, &r.DerivedFromNumber, &r.Ready)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func sameRevisionIntent(a, b loomgit.Revision) bool {
	return a.Workspace == b.Workspace && a.Change == b.Change && a.RequestID == b.RequestID &&
		a.Kind == b.Kind && a.Operation == b.Operation && a.Outcome == b.Outcome &&
		a.BaseSHA == b.BaseSHA && a.TreeHash == b.TreeHash && a.SourceHeadSHA == b.SourceHeadSHA &&
		a.DerivedFromChange == b.DerivedFromChange && a.DerivedFromNumber == b.DerivedFromNumber
}

func (s *SQLite) ReserveRevision(ctx context.Context, r loomgit.Revision) (loomgit.Revision, error) {
	if r.RequestID == "" || r.Workspace == "" || r.Change == "" || r.Kind == "" ||
		r.Outcome == "" || r.BaseSHA == "" || r.TreeHash == "" || r.SourceHeadSHA == "" {
		return loomgit.Revision{}, errors.New("incomplete revision reservation")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO change_revisions
		(workspace, change_id, number, request_id, kind, operation, outcome, base_sha,
		 tree_hash, source_head_sha, derived_from_change, derived_from_number)
		SELECT ?, ?, COALESCE(MAX(number), 0)+1, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM change_revisions WHERE workspace = ? AND change_id = ?
		ON CONFLICT(request_id) DO NOTHING`, r.Workspace, r.Change, r.RequestID,
		r.Kind, r.Operation, r.Outcome, r.BaseSHA, r.TreeHash, r.SourceHeadSHA,
		r.DerivedFromChange, r.DerivedFromNumber, r.Workspace, r.Change)
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
	result, err := s.db.ExecContext(ctx, `UPDATE change_revisions SET head_sha = ?, ready = 1
		WHERE workspace = ? AND change_id = ? AND number = ? AND request_id = ?
		AND (head_sha = '' OR head_sha = ?)`, r.HeadSHA, r.Workspace, r.Change, r.Number, r.RequestID, r.HeadSHA)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStale
	}
	return nil
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
