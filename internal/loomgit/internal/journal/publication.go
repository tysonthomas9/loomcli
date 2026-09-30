package journal

import (
	"context"
	"database/sql"
	"errors"
)

type Publication struct {
	Workspace, Change, Repo, Branch, Trunk, Slug, Head string
	Phase                                              string
	PRNumber                                           int
	PRURL                                              string
}

func createPublicationSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS change_publications (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, repo TEXT NOT NULL,
		branch TEXT NOT NULL, trunk TEXT NOT NULL, slug TEXT NOT NULL,
		head_sha TEXT NOT NULL, phase TEXT NOT NULL,
		pr_number INTEGER NOT NULL DEFAULT 0, pr_url TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, change_id)
	)`)
	return err
}

func (s *SQLite) Publication(ctx context.Context, workspace, change string) (Publication, bool, error) {
	var p Publication
	err := s.db.QueryRowContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,phase,pr_number,pr_url
		FROM change_publications WHERE workspace=? AND change_id=?`, workspace, change).Scan(
		&p.Workspace, &p.Change, &p.Repo, &p.Branch, &p.Trunk, &p.Slug, &p.Head, &p.Phase, &p.PRNumber, &p.PRURL)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, false, nil
	}
	return p, err == nil, err
}

func (s *SQLite) BeginPublication(ctx context.Context, p Publication) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO change_publications
		(workspace,change_id,repo,branch,trunk,slug,head_sha,phase) VALUES (?,?,?,?,?,?,?,'started')
		ON CONFLICT(workspace,change_id) DO UPDATE SET repo=excluded.repo,branch=excluded.branch,
		trunk=excluded.trunk,slug=excluded.slug,head_sha=excluded.head_sha,phase='started'
		WHERE change_publications.head_sha <> excluded.head_sha`,
		p.Workspace, p.Change, p.Repo, p.Branch, p.Trunk, p.Slug, p.Head)
	return err
}

func (s *SQLite) AdvancePublication(ctx context.Context, p Publication) error {
	result, err := s.db.ExecContext(ctx, `UPDATE change_publications SET phase=?,pr_number=?,pr_url=?
		WHERE workspace=? AND change_id=? AND head_sha=?`,
		p.Phase, p.PRNumber, p.PRURL, p.Workspace, p.Change, p.Head)
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

func (s *SQLite) OpenPublications(ctx context.Context) ([]Publication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,phase,pr_number,pr_url
		FROM change_publications WHERE phase <> 'done' ORDER BY workspace,change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Publication
	for rows.Next() {
		var p Publication
		if err := rows.Scan(&p.Workspace, &p.Change, &p.Repo, &p.Branch, &p.Trunk, &p.Slug,
			&p.Head, &p.Phase, &p.PRNumber, &p.PRURL); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
