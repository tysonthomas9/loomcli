package journal

import (
	"context"
	"database/sql"
)

// MirrorRecord is keyed by the local repository path so workspace deletion
// cannot discard a pending remote ref operation.
type MirrorRecord struct {
	Repo, Ref, Remote, SHA, State, Reason string
}

func (s *SQLite) EnsureMirrorSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mirror_refs (
		repo TEXT NOT NULL, ref TEXT NOT NULL, remote TEXT NOT NULL,
		sha TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(repo, ref)
	)`)
	return err
}

func (s *SQLite) PutMirrorRecord(ctx context.Context, row MirrorRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO mirror_refs(repo,ref,remote,sha,state,reason) VALUES(?,?,?,?,?,?)
		ON CONFLICT(repo,ref) DO UPDATE SET remote=excluded.remote,sha=excluded.sha,state=excluded.state,reason=excluded.reason`,
		row.Repo, row.Ref, row.Remote, row.SHA, row.State, row.Reason)
	return err
}

func (s *SQLite) MirrorRecords(ctx context.Context) ([]MirrorRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repo,ref,remote,sha,state,reason FROM mirror_refs ORDER BY repo,ref`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MirrorRecord
	for rows.Next() {
		var row MirrorRecord
		if err := rows.Scan(&row.Repo, &row.Ref, &row.Remote, &row.SHA, &row.State, &row.Reason); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *SQLite) ExistingMirrorRecords(ctx context.Context) ([]MirrorRecord, error) {
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='mirror_refs'`).Scan(&name); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return s.MirrorRecords(ctx)
}

func (s *SQLite) DeleteMirrorRecord(ctx context.Context, repo, ref string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mirror_refs WHERE repo=? AND ref=?`, repo, ref)
	return err
}

func (s *SQLite) MirrorState(ctx context.Context, repo, ref string) (MirrorRecord, bool, error) {
	var row MirrorRecord
	err := s.db.QueryRowContext(ctx, `SELECT repo,ref,remote,sha,state,reason FROM mirror_refs WHERE repo=? AND ref=?`, repo, ref).
		Scan(&row.Repo, &row.Ref, &row.Remote, &row.SHA, &row.State, &row.Reason)
	if err == sql.ErrNoRows {
		return MirrorRecord{}, false, nil
	}
	return row, err == nil, err
}
