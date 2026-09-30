package journal

import (
	"context"
	"database/sql"
)

func (s *SQLite) AbortRestack(ctx context.Context, requestID, workspace, lead string, ownRequestIDs []string) error {
	derivedPrefix := requestID + ":layer:"
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var phase string
	err = tx.QueryRowContext(ctx, `SELECT phase FROM applied_layers WHERE request_id=? AND workspace=? AND lead=? AND change_id='pull'`, requestID, workspace, lead).Scan(&phase)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && phase != "prepared" && phase != "not_applied" {
		return ErrStale
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM applied_layers WHERE request_id=? AND workspace=? AND lead=? AND change_id='pull' AND phase IN ('prepared','not_applied')`, requestID, workspace, lead); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pull_plans WHERE request_id=? AND workspace=? AND lead=?`, requestID, workspace, lead); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM review_verdicts WHERE (workspace,change_id,number) IN
		(SELECT workspace,change_id,number FROM change_revisions WHERE workspace=? AND substr(request_id,1,length(?))=?)`, workspace, derivedPrefix, derivedPrefix); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM revision_authors WHERE (workspace,change_id,number) IN
		(SELECT workspace,change_id,number FROM change_revisions WHERE workspace=? AND substr(request_id,1,length(?))=?)`, workspace, derivedPrefix, derivedPrefix); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM change_revisions WHERE workspace=? AND substr(request_id,1,length(?))=?`, workspace, derivedPrefix, derivedPrefix); err != nil {
		return err
	}
	for _, ownRequestID := range ownRequestIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM review_verdicts WHERE (workspace,change_id,number) IN
			(SELECT workspace,change_id,number FROM change_revisions WHERE workspace=? AND request_id=?)`, workspace, ownRequestID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM revision_authors WHERE (workspace,change_id,number) IN
			(SELECT workspace,change_id,number FROM change_revisions WHERE workspace=? AND request_id=?)`, workspace, ownRequestID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM change_revisions WHERE workspace=? AND request_id=?`, workspace, ownRequestID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
