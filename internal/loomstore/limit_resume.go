package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// LimitResume is an agent's usage-limit resume owed (OR7): its turn TurnID
// ended on a usage limit, the Attempt-th in a row, and a resume is due at
// DueAt. DueAt is empty once the resume was sent; the row then only keeps
// the count for the resumed turn's end.
type LimitResume struct {
	AgentID, TurnID string
	Attempt         int64
	DueAt           string // a Stamp, or empty: sent
}

// LimitResumeOn reports whether workspace opted in to usage-limit
// auto-resume; it is off unless set.
func (s *Store) LimitResumeOn(ctx context.Context, workspace string) (bool, error) {
	var on bool
	err := s.db.QueryRowContext(ctx, `SELECT limit_resume FROM agent_workspace_settings WHERE workspace_id = ?`,
		workspace).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return on, err
}

// SetLimitResumeOn sets workspace's usage-limit auto-resume opt-in.
func (s *Store) SetLimitResumeOn(ctx context.Context, workspace string, on bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_workspace_settings (workspace_id, limit_resume) VALUES (?, ?)
		ON CONFLICT (workspace_id) DO UPDATE SET limit_resume = excluded.limit_resume`, workspace, on)
	return err
}

// PutLimitResume saves r as its agent's one resume owed, replacing any other.
func (s *Store) PutLimitResume(ctx context.Context, r LimitResume) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_limit_resumes (agent_id, turn_id, attempt, due_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (agent_id) DO UPDATE SET turn_id = excluded.turn_id, attempt = excluded.attempt, due_at = excluded.due_at`,
		r.AgentID, r.TurnID, r.Attempt, r.DueAt)
	return err
}

// DropLimitResume removes agentID's resume owed, if any.
func (s *Store) DropLimitResume(ctx context.Context, agentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_limit_resumes WHERE agent_id = ?`, agentID)
	return err
}

// GetLimitResume returns agentID's resume owed, or ErrNotFound.
func (s *Store) GetLimitResume(ctx context.Context, agentID string) (LimitResume, error) {
	r := LimitResume{AgentID: agentID}
	err := s.db.QueryRowContext(ctx, `SELECT turn_id, attempt, due_at FROM agent_limit_resumes WHERE agent_id = ?`,
		agentID).Scan(&r.TurnID, &r.Attempt, &r.DueAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// DueLimitResumes returns the resumes owed to workspace's agents that are
// due at now and not yet sent, oldest first.
func (s *Store) DueLimitResumes(ctx context.Context, workspace string, now time.Time) ([]LimitResume, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.agent_id, r.turn_id, r.attempt, r.due_at FROM agent_limit_resumes r
		JOIN agents a ON a.agent_id = r.agent_id WHERE a.workspace_id = ? AND r.due_at != '' AND r.due_at <= ? ORDER BY r.due_at, r.agent_id`,
		workspace, Stamp(now))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LimitResume
	for rows.Next() {
		var r LimitResume
		if err := rows.Scan(&r.AgentID, &r.TurnID, &r.Attempt, &r.DueAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
