package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Agent is one agents row. Pointer fields are nullable columns; timestamps
// use Stamp's format.
type Agent struct {
	AgentID, WorkspaceID, Name, Preset, PresetVersion  string
	Mode, InteractionMode, RoleKind, SpecJSON          string
	SpecVersion                                        int64
	OwnerKind, OwnerID, CreatedByKind, CreatedByID     string
	ParentAgentID, RootAgentID                         *string
	SubjectType, SubjectID, SubjectVersion             *string
	ExternalKey                                        *string
	CreateRequestID                                    string
	LastRequestID                                      *string
	Repo                                               string
	BaseRef, WorktreePath, Branch                      *string
	Harness                                            string
	HarnessSessionID                                   *string
	Host                                               string
	Model                                              *string
	State                                              string
	StateReason, WaitingOn                             *string
	Attempt                                            int64
	Outcome, ArchiveReason, AttentionReason            *string
	RunningTurnID                                      *string
	CreateStep                                         int64
	DeleteRequested                                    bool
	DeleteResultJSON, LastActiveAt                     *string
	CreatedAt, UpdatedAt                               string
	ArchivedAt, FinishedAt, HistoryPurgedAt, DeletedAt *string
}

const agentCols = `agent_id, workspace_id, name, preset, preset_version, mode, interaction_mode,
 role_kind, spec_json, spec_version, owner_kind, owner_id, created_by_kind, created_by_id,
 parent_agent_id, root_agent_id, subject_type, subject_id, subject_version, external_key,
 create_request_id, last_request_id, repo, base_ref, worktree_path, branch, harness,
 harness_session_id, host, model, state, state_reason, waiting_on, attempt, outcome,
 archive_reason, attention_reason, running_turn_id, create_step, delete_requested,
 delete_result_json, last_active_at, created_at, updated_at, archived_at, finished_at,
 history_purged_at, deleted_at`

// fields lists a's fields in agentCols order; used both to bind and to scan.
func (a *Agent) fields() []any {
	return []any{&a.AgentID, &a.WorkspaceID, &a.Name, &a.Preset, &a.PresetVersion, &a.Mode,
		&a.InteractionMode, &a.RoleKind, &a.SpecJSON, &a.SpecVersion, &a.OwnerKind, &a.OwnerID,
		&a.CreatedByKind, &a.CreatedByID, &a.ParentAgentID, &a.RootAgentID, &a.SubjectType,
		&a.SubjectID, &a.SubjectVersion, &a.ExternalKey, &a.CreateRequestID, &a.LastRequestID,
		&a.Repo, &a.BaseRef, &a.WorktreePath, &a.Branch, &a.Harness, &a.HarnessSessionID,
		&a.Host, &a.Model, &a.State, &a.StateReason, &a.WaitingOn, &a.Attempt, &a.Outcome,
		&a.ArchiveReason, &a.AttentionReason, &a.RunningTurnID, &a.CreateStep,
		&a.DeleteRequested, &a.DeleteResultJSON, &a.LastActiveAt, &a.CreatedAt, &a.UpdatedAt,
		&a.ArchivedAt, &a.FinishedAt, &a.HistoryPurgedAt, &a.DeletedAt}
}

var (
	ErrNotFound     = errors.New("loomstore: not found")
	ErrSessionOwned = errors.New("loomstore: native session owned by another agent")
)

// InsertAgent inserts a and, if it already has a native session, records that
// session's ownership with origin "create", in one transaction.
func (s *Store) InsertAgent(ctx context.Context, a Agent) error {
	now := Stamp(time.Now())
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	if a.UpdatedAt == "" {
		a.UpdatedAt = now
	}
	if a.Host == "" {
		a.Host = "local"
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(a.fields())), ",")
		if _, err := tx.ExecContext(ctx, "INSERT INTO agents ("+agentCols+") VALUES ("+ph+")", a.fields()...); err != nil { //nolint:gosec // G202: constant column list and placeholders only.
			return err
		}
		if a.HarnessSessionID == nil {
			return nil
		}
		return recordNativeSession(ctx, tx, a.AgentID, a.Harness, *a.HarnessSessionID, "create")
	})
}

// GetAgent returns the agent row by id, tombstoned or not.
func (s *Store) GetAgent(ctx context.Context, agentID string) (Agent, error) {
	var a Agent
	err := s.db.QueryRowContext(ctx, "SELECT "+agentCols+" FROM agents WHERE agent_id = ?", agentID).Scan(a.fields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// NativeSession is one native session or thread ID an agent has owned.
type NativeSession struct {
	Harness, SessionID, AgentID, Origin, CreatedAt string
}

// RecordNativeSession durably records that agentID owns a native session
// opened by origin (create, switch, move or resume). Ownership is append-only
// and comes only from these calls, never from scanning provider directories.
// Recording the same session for the same agent again is a no-op; a session
// already owned by another agent returns ErrSessionOwned.
func (s *Store) RecordNativeSession(ctx context.Context, agentID, harness, sessionID, origin string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		return recordNativeSession(ctx, tx, agentID, harness, sessionID, origin)
	})
}

func recordNativeSession(ctx context.Context, tx *sql.Tx, agentID, harness, sessionID, origin string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_native_sessions (harness, session_id, agent_id, origin, created_at)
		VALUES (?,?,?,?,?) ON CONFLICT (harness, session_id) DO NOTHING`,
		harness, sessionID, agentID, origin, Stamp(time.Now())); err != nil {
		return err
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_native_sessions WHERE harness = ? AND session_id = ?`,
		harness, sessionID).Scan(&owner); err != nil {
		return err
	}
	if owner != agentID {
		return ErrSessionOwned
	}
	return nil
}

// NativeSessions lists every native session agentID has owned, oldest first.
func (s *Store) NativeSessions(ctx context.Context, agentID string) ([]NativeSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT harness, session_id, agent_id, origin, created_at
		FROM agent_native_sessions WHERE agent_id = ? ORDER BY created_at, rowid`, agentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NativeSession
	for rows.Next() {
		var n NativeSession
		if err := rows.Scan(&n.Harness, &n.SessionID, &n.AgentID, &n.Origin, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NativeSessionOwner returns the agent that owns a native session.
func (s *Store) NativeSessionOwner(ctx context.Context, harness, sessionID string) (string, error) {
	var owner string
	err := s.db.QueryRowContext(ctx, `SELECT agent_id FROM agent_native_sessions WHERE harness = ? AND session_id = ?`,
		harness, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return owner, err
}

// HistoryRetention is how long history outlives Archive (interactive) or the
// terminal finish (background), per R29.
const HistoryRetention = 30 * 24 * time.Hour

// retentionDue is the sweep predicate; it binds the cutoff twice.
const retentionDue = `history_purged_at IS NULL AND (deleted_at IS NOT NULL
	OR (interaction_mode = 'interactive' AND archived_at <= ?)
	OR (interaction_mode = 'background' AND finished_at <= ?))`

// ErrNotDue means the agent is no longer due for purge (unarchived, sent to,
// or already purged).
var ErrNotDue = errors.New("loomstore: history not due for purge")

// RetentionDue lists agents whose history the sweep must purge at now:
// interactive agents archived at least HistoryRetention ago, background agents
// finished at least HistoryRetention ago, and deleted agents, all not yet
// purged. Unarchive (archived_at cleared) or a new background Send
// (finished_at cleared) drops an agent out of this list.
func (s *Store) RetentionDue(ctx context.Context, now time.Time) ([]string, error) {
	cutoff := Stamp(now.Add(-HistoryRetention))
	rows, err := s.db.QueryContext(ctx, `SELECT agent_id FROM agents WHERE `+retentionDue+` ORDER BY agent_id`, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkHistoryPurged re-checks that agentID is still due at now, then sets
// history_purged_at and deletes its agent_events, in one transaction, so a
// sweep racing Unarchive or Send never purges early (ErrNotDue). Call it only
// after native content removal is verified.
func (s *Store) MarkHistoryPurged(ctx context.Context, agentID string, now time.Time) error {
	cutoff := Stamp(now.Add(-HistoryRetention))
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE agents SET history_purged_at = ?, updated_at = ?
			WHERE agent_id = ? AND `+retentionDue, Stamp(now), Stamp(now), agentID, cutoff, cutoff)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotDue
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM agent_events WHERE agent_id = ?`, agentID)
		return err
	})
}
