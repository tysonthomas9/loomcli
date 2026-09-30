package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type Publication struct {
	Workspace, Change, Repo, Branch, Trunk, Slug, Head, StackID, Prior string
	Phase                                                              string
	PRNumber                                                           int
	PRURL                                                              string
}

func createPublicationSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS change_publications (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, repo TEXT NOT NULL,
		branch TEXT NOT NULL, trunk TEXT NOT NULL, slug TEXT NOT NULL,
		head_sha TEXT NOT NULL, phase TEXT NOT NULL,
		pr_number INTEGER NOT NULL DEFAULT 0, pr_url TEXT NOT NULL DEFAULT '',
		stack_id TEXT NOT NULL DEFAULT '', prior_sha TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, change_id)
	)`)
	if err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info(change_publications)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, column := range []string{"stack_id", "prior_sha"} {
		if columns[column] {
			continue
		}
		_, err = db.Exec(`ALTER TABLE change_publications ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`)
		if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

func createStackBackendSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS stack_backends (
		workspace TEXT NOT NULL, stack_id TEXT NOT NULL, backend TEXT NOT NULL,
		PRIMARY KEY(workspace, stack_id)
	)`)
	return err
}

func (s *SQLite) RecordStackBackend(ctx context.Context, workspace, stackID, backend string) error {
	if workspace == "" || stackID == "" || backend == "" {
		return errors.New("workspace, stack ID and backend are required")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO stack_backends(workspace,stack_id,backend) VALUES (?,?,?)`, workspace, stackID, backend); err != nil {
		return err
	}
	var recorded string
	if err := s.db.QueryRowContext(ctx, `SELECT backend FROM stack_backends WHERE workspace=? AND stack_id=?`, workspace, stackID).Scan(&recorded); err != nil {
		return err
	}
	if recorded != backend {
		return errors.New("stack backend differs from its recorded selection")
	}
	return nil
}

func (s *SQLite) StackBackend(ctx context.Context, workspace, stackID string) (string, error) {
	var backend string
	err := s.db.QueryRowContext(ctx, `SELECT backend FROM stack_backends WHERE workspace=? AND stack_id=?`, workspace, stackID).Scan(&backend)
	return backend, err
}

func (s *SQLite) Publication(ctx context.Context, workspace, change string) (Publication, bool, error) {
	var p Publication
	err := s.db.QueryRowContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,phase,pr_number,pr_url,stack_id,prior_sha
		FROM change_publications WHERE workspace=? AND change_id=?`, workspace, change).Scan(
		&p.Workspace, &p.Change, &p.Repo, &p.Branch, &p.Trunk, &p.Slug, &p.Head, &p.Phase, &p.PRNumber, &p.PRURL, &p.StackID, &p.Prior)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, false, nil
	}
	return p, err == nil, err
}

func (s *SQLite) BeginPublication(ctx context.Context, p Publication) error {
	return beginPublication(ctx, s.db, p)
}

type publicationExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func beginPublication(ctx context.Context, database publicationExecer, p Publication) error {
	_, err := database.ExecContext(ctx, `INSERT INTO change_publications
		(workspace,change_id,repo,branch,trunk,slug,head_sha,stack_id,prior_sha,phase) VALUES (?,?,?,?,?,?,?,?,?,'started')
		ON CONFLICT(workspace,change_id) DO UPDATE SET repo=excluded.repo,branch=excluded.branch,
		trunk=excluded.trunk,slug=excluded.slug,head_sha=excluded.head_sha,stack_id=excluded.stack_id,prior_sha=excluded.prior_sha,phase='started'
		WHERE change_publications.head_sha <> excluded.head_sha OR change_publications.trunk <> excluded.trunk
		OR change_publications.stack_id <> excluded.stack_id`,
		p.Workspace, p.Change, p.Repo, p.Branch, p.Trunk, p.Slug, p.Head, p.StackID, p.Prior)
	return err
}

func (s *SQLite) BeginStackPublications(ctx context.Context, publications []Publication) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, publication := range publications {
		if err := beginPublication(ctx, tx, publication); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) AdvancePublication(ctx context.Context, p Publication) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE change_publications SET phase=?,pr_number=?,pr_url=?
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
	if p.Phase == "done" {
		payload, err := json.Marshal(struct {
			Workspace string `json:"workspace"`
			ChangeID  string `json:"change_id"`
			HeadSHA   string `json:"head_sha"`
			PRNumber  int    `json:"pr_number"`
			PRURL     string `json:"pr_url"`
		}{p.Workspace, p.Change, p.Head, p.PRNumber, p.PRURL})
		if err != nil {
			return err
		}
		key := "publication-event:" + p.Workspace + ":" + p.Change + ":" + p.Head
		if err := queueEvent(ctx, tx, key, "git.published", payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) OpenPublications(ctx context.Context) ([]Publication, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,repo,branch,trunk,slug,head_sha,phase,pr_number,pr_url,stack_id,prior_sha
		FROM change_publications WHERE phase <> 'done' ORDER BY workspace,change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Publication
	for rows.Next() {
		var p Publication
		if err := rows.Scan(&p.Workspace, &p.Change, &p.Repo, &p.Branch, &p.Trunk, &p.Slug,
			&p.Head, &p.Phase, &p.PRNumber, &p.PRURL, &p.StackID, &p.Prior); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
