package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

type WorkingArea struct {
	Workspace, Lead, Repo, Path, Branch, BaseSHA, Mode string
}

func (s *SQLite) WorkingAreaByPath(ctx context.Context, path string) (WorkingArea, error) {
	var area WorkingArea
	err := s.db.QueryRowContext(ctx, `SELECT workspace,lead,repo,path,branch,base_sha,mode
		FROM working_areas WHERE path=?`, path).Scan(&area.Workspace, &area.Lead, &area.Repo,
		&area.Path, &area.Branch, &area.BaseSHA, &area.Mode)
	return area, err
}

func (s *SQLite) WorkingAreaForAppliedChange(ctx context.Context, workspace, change, repo string) (WorkingArea, error) {
	var area WorkingArea
	err := s.db.QueryRowContext(ctx, `SELECT w.workspace,w.lead,w.repo,w.path,w.branch,w.base_sha,w.mode
		FROM applied_layers a JOIN working_areas w ON w.workspace=a.workspace AND w.lead=a.lead
		WHERE a.workspace=? AND a.change_id=? AND w.repo=? AND a.phase='done'
		ORDER BY a.rowid DESC LIMIT 1`, workspace, change, repo).Scan(&area.Workspace, &area.Lead,
		&area.Repo, &area.Path, &area.Branch, &area.BaseSHA, &area.Mode)
	return area, err
}

func createWorkingAreaSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS working_areas (
		workspace TEXT NOT NULL, lead TEXT NOT NULL, repo TEXT NOT NULL,
		path TEXT NOT NULL, branch TEXT NOT NULL, base_sha TEXT NOT NULL,
		mode TEXT NOT NULL, PRIMARY KEY(workspace, lead, repo)
	)`)
	if err != nil {
		return err
	}
	return backfillWorkingAreas(db)
}

// Older v2 workspaces already have the default lead checkout in their
// completed creation plan. Install its row without changing that checkout.
func backfillWorkingAreas(db *sql.DB) error {
	rows, err := db.Query(`SELECT e.request_id, c.plan FROM workspace_creations c
		JOIN journal_entries e ON e.id=c.entry_id WHERE e.phase='done'`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var request string
		var data []byte
		if err := rows.Scan(&request, &data); err != nil {
			return err
		}
		workspace, ok := strings.CutPrefix(request, "workspace-create:")
		if !ok {
			continue
		}
		var plan WorkspaceCreation
		if err := json.Unmarshal(data, &plan); err != nil {
			return err
		}
		for _, repo := range plan.Repos {
			if repo.Path == "" || repo.BaseSHA == "" {
				continue
			}
			mode := repo.Mode
			if mode == "" {
				mode = "worktree"
			}
			if _, err := db.Exec(`INSERT OR IGNORE INTO working_areas(workspace,lead,repo,path,branch,base_sha,mode)
				VALUES (?,?,?,?,?,?,?)`, workspace, "lead", repo.Name, repo.Path, repo.Branch, repo.BaseSHA, mode); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func (s *SQLite) WorkingAreas(ctx context.Context, workspace, lead string) ([]WorkingArea, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,lead,repo,path,branch,base_sha,mode
		FROM working_areas WHERE workspace=? AND lead=? ORDER BY repo`, workspace, lead)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var areas []WorkingArea
	for rows.Next() {
		var a WorkingArea
		if err := rows.Scan(&a.Workspace, &a.Lead, &a.Repo, &a.Path, &a.Branch, &a.BaseSHA, &a.Mode); err != nil {
			return nil, err
		}
		areas = append(areas, a)
	}
	return areas, rows.Err()
}

func (s *SQLite) SaveWorkingAreas(ctx context.Context, areas []WorkingArea) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, a := range areas {
		if _, err := tx.ExecContext(ctx, `INSERT INTO working_areas(workspace,lead,repo,path,branch,base_sha,mode)
			VALUES (?,?,?,?,?,?,?)`, a.Workspace, a.Lead, a.Repo, a.Path, a.Branch, a.BaseSHA, a.Mode); err != nil {
			return err
		}
	}
	return tx.Commit()
}
