package journal

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type RetainedCopy struct {
	Workspace, Change, Attempt, Path, SourceRepo string
	Complete, Removed, CaptureRefRemoved         bool
	EligibleAt                                   time.Time
}

type RetentionPolicy struct {
	Kept, Landed, Abandoned, CaptureRefs time.Duration
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{Kept: 14 * 24 * time.Hour, Landed: 7 * 24 * time.Hour,
		Abandoned: 30 * 24 * time.Hour, CaptureRefs: 90 * 24 * time.Hour}
}

func createRetentionSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS retained_task_copies (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, attempt TEXT NOT NULL,
		path TEXT NOT NULL, source_repo TEXT NOT NULL, complete INTEGER NOT NULL,
		removed INTEGER NOT NULL DEFAULT 0, capture_ref_removed INTEGER NOT NULL DEFAULT 0,
		eligible_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, attempt)
	);
	CREATE TABLE IF NOT EXISTS workspace_retention (
		workspace TEXT PRIMARY KEY, kept_days INTEGER NOT NULL, landed_days INTEGER NOT NULL,
		abandoned_days INTEGER NOT NULL, capture_days INTEGER NOT NULL
	)`)
	return err
}

func (s *SQLite) RecordRetainedCopy(ctx context.Context, row RetainedCopy) error {
	if row.Workspace == "" || row.Change == "" || row.Attempt == "" || row.Path == "" {
		return errors.New("retained copy requires workspace, change, attempt and path")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO retained_task_copies
		(workspace,change_id,attempt,path,source_repo,complete) VALUES(?,?,?,?,?,?)
		ON CONFLICT(workspace,attempt) DO UPDATE SET complete=retained_task_copies.complete OR excluded.complete
		WHERE change_id=excluded.change_id AND path=excluded.path AND source_repo=excluded.source_repo`,
		row.Workspace, row.Change, row.Attempt, row.Path, row.SourceRepo, row.Complete)
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

func (s *SQLite) RetainedCopies(ctx context.Context) ([]RetainedCopy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,change_id,attempt,path,source_repo,complete,removed,capture_ref_removed,eligible_at
		FROM retained_task_copies ORDER BY workspace,attempt`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []RetainedCopy
	for rows.Next() {
		var row RetainedCopy
		var eligible string
		if err := rows.Scan(&row.Workspace, &row.Change, &row.Attempt, &row.Path,
			&row.SourceRepo, &row.Complete, &row.Removed, &row.CaptureRefRemoved, &eligible); err != nil {
			return nil, err
		}
		if eligible != "" {
			row.EligibleAt, err = time.Parse(time.RFC3339Nano, eligible)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *SQLite) ObserveRetention(ctx context.Context, row RetainedCopy, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE retained_task_copies SET eligible_at=?
		WHERE workspace=? AND attempt=? AND eligible_at=''`, at.UTC().Format(time.RFC3339Nano), row.Workspace, row.Attempt)
	return err
}

func (s *SQLite) MarkRetainedCopyRemoved(ctx context.Context, row RetainedCopy) error {
	_, err := s.db.ExecContext(ctx, `UPDATE retained_task_copies SET removed=1 WHERE workspace=? AND attempt=?`, row.Workspace, row.Attempt)
	return err
}

func (s *SQLite) MarkCaptureRefRemoved(ctx context.Context, row RetainedCopy) error {
	_, err := s.db.ExecContext(ctx, `UPDATE retained_task_copies SET capture_ref_removed=1
		WHERE workspace=? AND attempt=? AND removed=1`, row.Workspace, row.Attempt)
	return err
}

func (s *SQLite) RetentionState(ctx context.Context, row RetainedCopy) (string, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM landed_changes WHERE workspace=? AND change_id=?`,
		row.Workspace, row.Change).Scan(&found)
	if err == nil {
		return "landed", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var schema int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='change_abandonments'`).Scan(&schema)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM change_abandonments
		WHERE workspace=? AND change_id=? AND retention_eligible=1 AND claim_released=1`,
		row.Workspace, row.Change).Scan(&found)
	if err == nil {
		return "abandoned", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return "", err
}

func (s *SQLite) HasCompleteCapture(ctx context.Context, row RetainedCopy) (bool, error) {
	var ready, incomplete int
	err := s.db.QueryRowContext(ctx, `SELECT ready,incomplete FROM change_revisions
		WHERE workspace=? AND change_id=? AND request_id=?`,
		row.Workspace, row.Change, "driver:"+row.Attempt).Scan(&ready, &incomplete)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && ready == 1 && incomplete == 0, err
}

func (s *SQLite) CaptureSHA(ctx context.Context, row RetainedCopy) (string, error) {
	var sha string
	err := s.db.QueryRowContext(ctx, `SELECT source_head_sha FROM change_revisions
		WHERE workspace=? AND change_id=? AND request_id=? AND ready=1 AND incomplete=0`,
		row.Workspace, row.Change, "driver:"+row.Attempt).Scan(&sha)
	return sha, err
}

func (s *SQLite) RetentionPolicy(ctx context.Context, workspace string) (RetentionPolicy, error) {
	var kept, landed, abandoned, capture int
	err := s.db.QueryRowContext(ctx, `SELECT kept_days,landed_days,abandoned_days,capture_days
		FROM workspace_retention WHERE workspace=?`, workspace).Scan(&kept, &landed, &abandoned, &capture)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultRetentionPolicy(), nil
	}
	if err != nil {
		return RetentionPolicy{}, err
	}
	return RetentionPolicy{time.Duration(kept) * 24 * time.Hour, time.Duration(landed) * 24 * time.Hour,
		time.Duration(abandoned) * 24 * time.Hour, time.Duration(capture) * 24 * time.Hour}, nil
}

func (s *SQLite) SetRetentionPolicy(ctx context.Context, workspace string, policy RetentionPolicy) error {
	if workspace == "" || policy.Kept < 0 || policy.Landed < 0 || policy.Abandoned < 0 || policy.CaptureRefs < 0 ||
		policy.Kept%(24*time.Hour) != 0 || policy.Landed%(24*time.Hour) != 0 ||
		policy.Abandoned%(24*time.Hour) != 0 || policy.CaptureRefs%(24*time.Hour) != 0 {
		return errors.New("retention policy requires a workspace and nonnegative whole days")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_retention(workspace,kept_days,landed_days,abandoned_days,capture_days)
		VALUES(?,?,?,?,?) ON CONFLICT(workspace) DO UPDATE SET kept_days=excluded.kept_days,
		landed_days=excluded.landed_days,abandoned_days=excluded.abandoned_days,capture_days=excluded.capture_days`,
		workspace, int(policy.Kept/(24*time.Hour)), int(policy.Landed/(24*time.Hour)),
		int(policy.Abandoned/(24*time.Hour)), int(policy.CaptureRefs/(24*time.Hour)))
	return err
}
