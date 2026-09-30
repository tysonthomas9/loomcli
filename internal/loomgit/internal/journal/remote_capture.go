package journal

import "context"

type RemoteCaptureRecord struct {
	Workspace, Attempt, Task, RepoURL string
	BaseSHA, CaptureSHA, TreeHash     string
	Outcome, State, Reason            string
	Complete                          bool
}

func (s *SQLite) EnsureRemoteCaptureSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS remote_captures (
		workspace TEXT NOT NULL, attempt TEXT NOT NULL, task TEXT NOT NULL, repo_url TEXT NOT NULL,
		base_sha TEXT NOT NULL, capture_sha TEXT NOT NULL, tree_hash TEXT NOT NULL,
		outcome TEXT NOT NULL, complete INTEGER NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(workspace, attempt)
	)`)
	return err
}

func (s *SQLite) PutRemoteCapture(ctx context.Context, row RemoteCaptureRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO remote_captures
		(workspace,attempt,task,repo_url,base_sha,capture_sha,tree_hash,outcome,complete,state,reason)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace,attempt) DO UPDATE SET
		task=excluded.task,repo_url=excluded.repo_url,base_sha=excluded.base_sha,
		capture_sha=excluded.capture_sha,tree_hash=excluded.tree_hash,outcome=excluded.outcome,
		complete=excluded.complete,state=excluded.state,reason=excluded.reason`,
		row.Workspace, row.Attempt, row.Task, row.RepoURL, row.BaseSHA, row.CaptureSHA,
		row.TreeHash, row.Outcome, row.Complete, row.State, row.Reason)
	return err
}

func (s *SQLite) PendingRemoteCaptures(ctx context.Context) ([]RemoteCaptureRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,attempt,task,repo_url,base_sha,capture_sha,
		tree_hash,outcome,complete,state,reason FROM remote_captures WHERE state='pending' ORDER BY workspace,attempt`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var captures []RemoteCaptureRecord
	for rows.Next() {
		var row RemoteCaptureRecord
		if err := rows.Scan(&row.Workspace, &row.Attempt, &row.Task, &row.RepoURL,
			&row.BaseSHA, &row.CaptureSHA, &row.TreeHash, &row.Outcome, &row.Complete,
			&row.State, &row.Reason); err != nil {
			return nil, err
		}
		captures = append(captures, row)
	}
	return captures, rows.Err()
}

func (s *SQLite) MarkRemoteCaptureFrozen(ctx context.Context, workspace, attempt, captureSHA string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_captures SET state='frozen',reason=''
		WHERE workspace=? AND attempt=? AND capture_sha=?`, workspace, attempt, captureSHA)
	return err
}
