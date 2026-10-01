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
	AgentID, WorkspaceID, Name, ProfileKey, Preset, PresetVersion string
	Mode, InteractionMode, RoleKind, SpecJSON                     string
	SpecVersion                                                   int64
	OwnerKind, OwnerID, CreatedByKind, CreatedByID                string
	ParentAgentID, RootAgentID                                    *string
	SubjectType, SubjectID, SubjectVersion                        *string
	ExternalKey                                                   *string
	CreateRequestID                                               string
	LastRequestID                                                 *string
	Repo                                                          string
	BaseRef, WorktreePath, Branch                                 *string
	Harness                                                       string
	HarnessSessionID                                              *string
	Host                                                          string
	Model                                                         *string
	State                                                         string
	StateReason, WaitingOn                                        *string
	Attempt                                                       int64
	Outcome, ArchiveReason, AttentionReason                       *string
	RunningTurnID                                                 *string
	CreateStep                                                    int64
	DeleteRequested                                               bool
	DeleteResultJSON, LastActiveAt                                *string
	CreatedAt, UpdatedAt                                          string
	ArchivedAt, FinishedAt, HistoryPurgedAt, DeletedAt            *string
}

const agentCols = `agent_id, workspace_id, name, profile_key, preset, preset_version, mode, interaction_mode,
 role_kind, spec_json, spec_version, owner_kind, owner_id, created_by_kind, created_by_id,
 parent_agent_id, root_agent_id, subject_type, subject_id, subject_version, external_key,
 create_request_id, last_request_id, repo, base_ref, worktree_path, branch, harness,
 harness_session_id, host, model, state, state_reason, waiting_on, attempt, outcome,
 archive_reason, attention_reason, running_turn_id, create_step, delete_requested,
 delete_result_json, last_active_at, created_at, updated_at, archived_at, finished_at,
 history_purged_at, deleted_at`

// fields lists a's fields in agentCols order; used both to bind and to scan.
func (a *Agent) fields() []any {
	return []any{&a.AgentID, &a.WorkspaceID, &a.Name, &a.ProfileKey, &a.Preset, &a.PresetVersion, &a.Mode,
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

// InsertAgent inserts a. ProfileKey is fixed here and never changes. Native
// session ownership is recorded separately with RecordNativeSession, once the
// harness has returned the session's root.
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
	ph := strings.TrimSuffix(strings.Repeat("?,", len(a.fields())), ",")
	_, err := s.db.ExecContext(ctx, "INSERT INTO agents ("+agentCols+") VALUES ("+ph+")", a.fields()...) //nolint:gosec // G202: constant column list and placeholders only.
	return err
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

// NativeSession is one native session or thread an agent owned: the harness,
// the root the harness returned at launch, and the native ID under that root.
// Rows are immutable; an R29 purge uses NativeRoot as recorded.
type NativeSession struct {
	AgentID, Harness, NativeRoot, NativeID, RecordedAt string
}

// RecordNativeSession durably records that n.AgentID owns a native session
// (opened by Create, switch, Move or Resume). Ownership is append-only and
// comes only from these calls, never from scanning provider directories.
// Recording the same session for the same agent again is a no-op; a session
// already owned by another agent returns ErrSessionOwned.
func (s *Store) RecordNativeSession(ctx context.Context, n NativeSession) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_native_sessions (agent_id, harness, native_root, native_id, recorded_at)
			VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING`,
			n.AgentID, n.Harness, n.NativeRoot, n.NativeID, Stamp(time.Now())); err != nil {
			return err
		}
		owner, err := nativeSessionOwner(ctx, tx, n.Harness, n.NativeRoot, n.NativeID)
		if err != nil {
			return err
		}
		if owner != n.AgentID {
			return ErrSessionOwned
		}
		return nil
	})
}

// NativeSessions lists every native session agentID has owned, oldest first.
func (s *Store) NativeSessions(ctx context.Context, agentID string) ([]NativeSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT agent_id, harness, native_root, native_id, recorded_at
		FROM agent_native_sessions WHERE agent_id = ? ORDER BY recorded_at, rowid`, agentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NativeSession
	for rows.Next() {
		var n NativeSession
		if err := rows.Scan(&n.AgentID, &n.Harness, &n.NativeRoot, &n.NativeID, &n.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NativeSessionOwner returns the agent that owns a native session.
func (s *Store) NativeSessionOwner(ctx context.Context, harness, nativeRoot, nativeID string) (string, error) {
	owner, err := nativeSessionOwner(ctx, s.db, harness, nativeRoot, nativeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return owner, err
}

func nativeSessionOwner(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, harness, nativeRoot, nativeID string) (string, error) {
	var owner string
	err := q.QueryRowContext(ctx, `SELECT agent_id FROM agent_native_sessions
		WHERE harness = ? AND native_root = ? AND native_id = ?`, harness, nativeRoot, nativeID).Scan(&owner)
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

// AgentState is an agent's state columns (design v2 §5.1).
type AgentState struct {
	State                                                         string
	StateReason, WaitingOn, Outcome, AttentionReason, RunningTurn *string
	Attempt                                                       int64
}

// StateOf returns a's state columns.
func (a Agent) StateOf() AgentState {
	return AgentState{State: a.State, StateReason: a.StateReason, WaitingOn: a.WaitingOn, Outcome: a.Outcome,
		AttentionReason: a.AttentionReason, RunningTurn: a.RunningTurnID, Attempt: a.Attempt}
}

// ErrStateChanged means the agent's state columns no longer equal the expected ones.
var ErrStateChanged = errors.New("loomstore: agent state changed")

// CompareAndSetState sets agentID's state columns to `to` only if they still
// equal `from` and the agent is not deleted; otherwise it returns ErrStateChanged.
func (s *Store) CompareAndSetState(ctx context.Context, agentID string, from, to AgentState) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET state = ?, state_reason = ?, waiting_on = ?, outcome = ?,
		attention_reason = ?, running_turn_id = ?, attempt = ?, updated_at = ?
		WHERE agent_id = ? AND deleted_at IS NULL AND state = ? AND state_reason IS ? AND waiting_on IS ?
		AND outcome IS ? AND attention_reason IS ? AND running_turn_id IS ? AND attempt = ?`,
		to.State, to.StateReason, to.WaitingOn, to.Outcome, to.AttentionReason, to.RunningTurn, to.Attempt,
		Stamp(time.Now()), agentID, from.State, from.StateReason, from.WaitingOn, from.Outcome,
		from.AttentionReason, from.RunningTurn, from.Attempt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStateChanged
	}
	return nil
}

// AgentFilter selects agents for ListAgents. Empty fields match anything.
// Archived agents are left out unless included or filtered by
// state; deleted agents unless included.
type AgentFilter struct {
	WorkspaceID, OwnerKind, OwnerID, Parent, Root, Preset, Mode, Harness, RoleKind, State string
	SubjectType, SubjectID, ExternalKeyPrefix, Name                                       string
	IncludeArchived, IncludeDeleted                                                       bool
	After                                                                                 string // cursor: the last agent_id of the previous page
	Limit                                                                                 int    // 0 means no limit
}

// ListAgents returns one page of agents matching f in agent_id order, and the
// cursor of the next page ("" on the last page).
func (s *Store) ListAgents(ctx context.Context, f AgentFilter) ([]Agent, string, error) {
	where, args := []string{"agent_id > ?"}, []any{f.After}
	for col, v := range map[string]string{"workspace_id": f.WorkspaceID, "owner_kind": f.OwnerKind,
		"owner_id": f.OwnerID, "parent_agent_id": f.Parent, "root_agent_id": f.Root, "preset": f.Preset,
		"mode": f.Mode, "harness": f.Harness, "role_kind": f.RoleKind, "state": f.State,
		"subject_type": f.SubjectType, "subject_id": f.SubjectID, "name": f.Name} {
		if v != "" {
			where, args = append(where, col+" = ?"), append(args, v)
		}
	}
	if f.ExternalKeyPrefix != "" {
		where = append(where, "substr(external_key, 1, length(?)) = ?")
		args = append(args, f.ExternalKeyPrefix, f.ExternalKeyPrefix)
	}
	if !f.IncludeArchived && f.State == "" {
		where = append(where, "state <> 'archived'")
	}
	if !f.IncludeDeleted {
		where = append(where, "deleted_at IS NULL")
	}
	q := "SELECT " + agentCols + " FROM agents WHERE " + strings.Join(where, " AND ") + " ORDER BY agent_id" //nolint:gosec // G202: constant column names and placeholders only.
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit+1)
	}
	rows, err := s.db.QueryContext(ctx, q, args...) //nolint:gosec // G202: constant column names and placeholders only.
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	var out []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(a.fields()...); err != nil {
			return nil, "", err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
		return out, out[f.Limit-1].AgentID, nil
	}
	return out, "", nil
}

// SetArchive records agentID's archive reason and starts its R29 clock at
// `at`, keeping an earlier start so a retried Archive never moves it. A nil
// `at` clears both (Unarchive cancels the clock).
func (s *Store) SetArchive(ctx context.Context, agentID string, reason *string, at *time.Time) error {
	var stamp *string
	if at != nil {
		v := Stamp(*at)
		stamp = &v
	}
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET archive_reason = ?,
		archived_at = CASE WHEN ? IS NULL THEN NULL ELSE COALESCE(archived_at, ?) END, updated_at = ?
		WHERE agent_id = ?`, reason, stamp, stamp, Stamp(time.Now()), agentID)
	return err
}

// MarkDeleteRequested flags agentID as being deleted, so Reconcile finishes a
// Delete that crashed midway.
func (s *Store) MarkDeleteRequested(ctx context.Context, agentID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET delete_requested = 1, updated_at = ? WHERE agent_id = ?`,
		Stamp(time.Now()), agentID)
	return err
}

// Tombstone marks agentID deleted at now, keeping an earlier tombstone.
func (s *Store) Tombstone(ctx context.Context, agentID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET deleted_at = COALESCE(deleted_at, ?), updated_at = ?
		WHERE agent_id = ?`, Stamp(now), Stamp(now), agentID)
	return err
}

// AgentSpec is the agent columns Update changes: the spec, its version, the
// selected harness and its current native session, and the last applied
// Update RequestID.
type AgentSpec struct {
	Name, SpecJSON, Harness                string
	Model, HarnessSessionID, LastRequestID *string
	SpecVersion                            int64
}

// SpecOf returns a's spec columns.
func (a Agent) SpecOf() AgentSpec {
	return AgentSpec{Name: a.Name, SpecJSON: a.SpecJSON, Harness: a.Harness, Model: a.Model,
		HarnessSessionID: a.HarnessSessionID, LastRequestID: a.LastRequestID, SpecVersion: a.SpecVersion}
}

// ErrSpecChanged means the agent's spec_version no longer equals the expected one.
var ErrSpecChanged = errors.New("loomstore: agent spec version changed")

// CompareAndSetSpec sets agentID's spec columns to `to` in one statement, only
// if its spec_version still equals fromVersion and it is not deleted;
// otherwise it returns ErrSpecChanged.
func (s *Store) CompareAndSetSpec(ctx context.Context, agentID string, fromVersion int64, to AgentSpec) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET name = ?, spec_json = ?, harness = ?, model = ?,
		harness_session_id = ?, last_request_id = ?, spec_version = ?, updated_at = ?
		WHERE agent_id = ? AND deleted_at IS NULL AND spec_version = ?`,
		to.Name, to.SpecJSON, to.Harness, to.Model, to.HarnessSessionID, to.LastRequestID, to.SpecVersion,
		Stamp(time.Now()), agentID, fromVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSpecChanged
	}
	return nil
}

// FindCreated returns the agent a Create made in workspaceID: the live agent
// with externalKey when one is given, else the agent whose Create RequestID
// is requestID. It returns ErrNotFound when there is none.
func (s *Store) FindCreated(ctx context.Context, workspaceID, externalKey, requestID string) (Agent, error) {
	where, arg := "create_request_id = ?", requestID
	if externalKey != "" {
		where, arg = "external_key = ? AND deleted_at IS NULL", externalKey
	}
	var a Agent
	err := s.db.QueryRowContext(ctx, "SELECT "+agentCols+" FROM agents WHERE workspace_id = ? AND "+where, //nolint:gosec // G202: constant column list and placeholders only.
		workspaceID, arg).Scan(a.fields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// SetCreateStep records that Create finished step on agentID, with the
// columns that step filled; a nil column is kept.
func (s *Store) SetCreateStep(ctx context.Context, agentID string, step int64, worktreePath, branch, sessionID *string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET create_step = MAX(create_step, ?),
		worktree_path = COALESCE(?, worktree_path), branch = COALESCE(?, branch),
		harness_session_id = COALESCE(?, harness_session_id), updated_at = ? WHERE agent_id = ?`,
		step, worktreePath, branch, sessionID, Stamp(time.Now()), agentID)
	return err
}
