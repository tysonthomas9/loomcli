package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	HarnessSessionID, HarnessSessionRoot                          *string
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
	// HistoryPurgeFailedAt is set while a due history purge has failed (an
	// incomplete expiry); whatever ends the deadline clears it.
	HistoryPurgeFailedAt *string
	// Revision counts the agent's state changes; CommitState bumps it.
	Revision int64
	// LastSeq is the agent's latest committed event seq, read in the same
	// statement as the row. Only GetAgent and ListAgents set it.
	LastSeq int64
}

// insertCols are the columns InsertAgent writes; agentCols adds those only
// later changes set.
const insertCols = `agent_id, workspace_id, name, profile_key, preset, preset_version, mode, interaction_mode,
 role_kind, spec_json, spec_version, owner_kind, owner_id, created_by_kind, created_by_id,
 parent_agent_id, root_agent_id, subject_type, subject_id, subject_version, external_key,
 create_request_id, last_request_id, repo, base_ref, worktree_path, branch, harness,
 harness_session_id, host, model, state, state_reason, waiting_on, attempt, outcome,
 archive_reason, attention_reason, running_turn_id, create_step, delete_requested,
 delete_result_json, last_active_at, created_at, updated_at, archived_at, finished_at,
 history_purged_at, deleted_at, harness_session_root`

const agentCols = insertCols + `, history_purge_failed_at, revision`

// readCols adds LastSeq: one lookup on agent_events' (agent_id, seq) key.
const readCols = agentCols + `, (SELECT COALESCE(MAX(seq), 0) FROM agent_events e WHERE e.agent_id = agents.agent_id)`

func (a *Agent) readFields() []any { return append(a.fields(), &a.LastSeq) }

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
		&a.ArchivedAt, &a.FinishedAt, &a.HistoryPurgedAt, &a.DeletedAt, &a.HarnessSessionRoot, &a.HistoryPurgeFailedAt, &a.Revision}
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
	f := a.fields()
	f = f[:len(f)-2] // insertCols: a new agent has no failed purge and revision 0
	ph := strings.TrimSuffix(strings.Repeat("?,", len(f)), ",")
	_, err := s.db.ExecContext(ctx, "INSERT INTO agents ("+insertCols+") VALUES ("+ph+")", f...) //nolint:gosec // G202: constant column list and placeholders only.
	return err
}

// GetAgent returns the agent row by id, tombstoned or not.
func (s *Store) GetAgent(ctx context.Context, agentID string) (Agent, error) {
	var a Agent
	err := s.db.QueryRowContext(ctx, "SELECT "+readCols+" FROM agents WHERE agent_id = ?", agentID).Scan(a.readFields()...)
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
	return s.tx(ctx, func(tx *sql.Tx) error { return recordNative(ctx, tx, n) })
}

func recordNative(ctx context.Context, tx *sql.Tx, n NativeSession) error {
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
}

// RecordPurgePending records n as owned, as RecordNativeSession does, and
// as purge-pending, in one transaction: a failed Open left it behind.
func (s *Store) RecordPurgePending(ctx context.Context, n NativeSession) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := recordNative(ctx, tx, n); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO native_purge_pending (harness, native_root, native_id)
			VALUES (?,?,?) ON CONFLICT DO NOTHING`, n.Harness, n.NativeRoot, n.NativeID)
		return err
	})
}

// ClearPurgePending drops n's purge-pending mark: it was purged, or a later
// Open returned it as a working session.
func (s *Store) ClearPurgePending(ctx context.Context, n NativeSession) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM native_purge_pending WHERE harness = ? AND native_root = ? AND native_id = ?`,
		n.Harness, n.NativeRoot, n.NativeID)
	return err
}

// PurgePending lists the purge-pending native sessions of workspaceID's
// agents with their owners.
func (s *Store) PurgePending(ctx context.Context, workspaceID string) ([]NativeSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.agent_id, n.harness, n.native_root, n.native_id, n.recorded_at
		FROM native_purge_pending p JOIN agent_native_sessions n USING (harness, native_root, native_id)
		JOIN agents a ON a.agent_id = n.agent_id WHERE a.workspace_id = ? ORDER BY n.recorded_at`, workspaceID)
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
// terminal finish (background), per R29. Only AFT shortens it.
var HistoryRetention = 30 * 24 * time.Hour

// retentionDue is the sweep predicate; it binds the cutoff twice.
const retentionDue = `history_purged_at IS NULL AND (deleted_at IS NOT NULL
	OR (interaction_mode = 'interactive' AND archived_at <= ?)
	OR (interaction_mode = 'background' AND finished_at <= ?))`

// worktreeDue selects a live agent's working copy past retention, whether
// or not its history is purged yet; it binds the cutoff twice.
const worktreeDue = `worktree_path IS NOT NULL AND deleted_at IS NULL
	AND ((interaction_mode = 'interactive' AND archived_at <= ?)
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
	return s.dueIDs(ctx, retentionDue, now)
}

// Due reports whether agentID's history is still due for purge at now.
func (s *Store) Due(ctx context.Context, agentID string, now time.Time) (bool, error) {
	return s.isDue(ctx, retentionDue, agentID, now)
}

// WorktreesDue lists live agents whose working copy is past retention at
// now (the same deadline as their history), independent of the history purge.
func (s *Store) WorktreesDue(ctx context.Context, now time.Time) ([]string, error) {
	return s.dueIDs(ctx, worktreeDue, now)
}

// WorktreeDue reports whether agentID's working copy is still due at now.
func (s *Store) WorktreeDue(ctx context.Context, agentID string, now time.Time) (bool, error) {
	return s.isDue(ctx, worktreeDue, agentID, now)
}

// MarkPurgeFailed records that agentID's due history purge failed at now,
// so its expiry shows as incomplete until a retry succeeds.
func (s *Store) MarkPurgeFailed(ctx context.Context, agentID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET history_purge_failed_at = ?, updated_at = ? WHERE agent_id = ?`,
		Stamp(now), Stamp(time.Now()), agentID)
	return err
}

// ClearWorktree records that agentID's working copy was removed.
func (s *Store) ClearWorktree(ctx context.Context, agentID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET worktree_path = NULL, updated_at = ? WHERE agent_id = ?`,
		Stamp(time.Now()), agentID)
	return err
}

func (s *Store) dueIDs(ctx context.Context, pred string, now time.Time) ([]string, error) {
	cutoff := Stamp(now.Add(-HistoryRetention))
	rows, err := s.db.QueryContext(ctx, `SELECT agent_id FROM agents WHERE `+pred+` ORDER BY agent_id`, cutoff, cutoff) //nolint:gosec // G202: pred is a constant predicate.
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

func (s *Store) isDue(ctx context.Context, pred, agentID string, now time.Time) (bool, error) {
	cutoff := Stamp(now.Add(-HistoryRetention))
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM agents WHERE agent_id = ? AND `+pred, agentID, cutoff, cutoff).Scan(new(int))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// MarkHistoryPurged re-checks that agentID is still due at now, then sets
// history_purged_at and deletes its agent_events, in one transaction, so a
// sweep racing Unarchive or Send never purges early (ErrNotDue). Call it only
// after native content removal is verified.
func (s *Store) MarkHistoryPurged(ctx context.Context, agentID string, now time.Time) error {
	cutoff := Stamp(now.Add(-HistoryRetention))
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE agents SET history_purged_at = ?, history_purge_failed_at = NULL, updated_at = ?
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

// commitStateCrash runs inside the transactions of CommitState (and its
// CommitCreate and CommitArchive), CommitSpec, SendEvents and TombstoneEvents,
// after their writes and before their COMMIT; tests crash there.
var commitStateCrash = func() {}

// CommitState is the one write of an agent's state change. In one
// transaction it sets agentID's state columns to `to` and bumps its revision
// by one, only if the columns still equal `from`, the revision still equals
// rev and the agent is not deleted (otherwise ErrStateChanged), and appends
// events as AppendEvent does, each with no EventID named <agent>:<revision>:<kind>
// of the new revision. Both are saved or neither. It returns the saved events.
// A turn ending in finished (from active or waiting) also sets finished_at,
// the background R29 deadline, in the same statement.
func (s *Store) CommitState(ctx context.Context, agentID string, from, to AgentState, rev int64, events []Event) ([]Event, error) {
	for _, e := range events {
		if e.AgentID != agentID {
			return nil, fmt.Errorf("loomstore: event %s of %s in a state change of %s", e.Kind, e.AgentID, agentID)
		}
	}
	return s.commitState(ctx, agentID, from, to, rev, 0, events, nil)
}

// CommitArchive is CommitState that, in the same transaction, also records
// agentID's archive reason and R29 clock start as SetArchive does.
func (s *Store) CommitArchive(ctx context.Context, agentID string, from, to AgentState, rev int64, reason *string,
	at *time.Time, events []Event) ([]Event, error) {
	return s.commitState(ctx, agentID, from, to, rev, 0, events, func(tx *sql.Tx) error {
		return setArchive(ctx, tx, agentID, reason, at)
	})
}

// CommitCreate is CommitState for Create's last step: the same transaction
// also raises create_step to step, and saves events that keep their EventID
// (agent.created, a parent's child.created) as AppendEvent does. Another
// agent's event is saved only while that agent is not deleted; no lock of
// that agent is needed, as its seq is allocated in the transaction.
func (s *Store) CommitCreate(ctx context.Context, agentID string, from, to AgentState, rev, step int64, events []Event) ([]Event, error) {
	return s.commitState(ctx, agentID, from, to, rev, step, events, nil)
}

// commitState is the one state-change transaction; also, when set, writes
// more of agentID's row after the compare-and-set.
func (s *Store) commitState(ctx context.Context, agentID string, from, to AgentState, rev, step int64, events []Event,
	also func(*sql.Tx) error) (saved []Event, err error) {
	now := Stamp(time.Now())
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE agents SET state = ?, state_reason = ?, waiting_on = ?, outcome = ?,
		attention_reason = ?, running_turn_id = ?, attempt = ?, updated_at = ?, revision = revision + 1,
		create_step = MAX(create_step, ?),
		finished_at = CASE WHEN ? = 'finished' AND state IN ('active','waiting') THEN ? ELSE finished_at END
		WHERE agent_id = ? AND deleted_at IS NULL AND state = ? AND state_reason IS ? AND waiting_on IS ?
		AND outcome IS ? AND attention_reason IS ? AND running_turn_id IS ? AND attempt = ? AND revision = ?`,
			to.State, to.StateReason, to.WaitingOn, to.Outcome, to.AttentionReason, to.RunningTurn, to.Attempt,
			now, step, to.State, now, agentID, from.State, from.StateReason, from.WaitingOn, from.Outcome,
			from.AttentionReason, from.RunningTurn, from.Attempt, rev)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrStateChanged
		}
		if also != nil {
			if err := also(tx); err != nil {
				return err
			}
		}
		if saved, err = commitEvents(ctx, tx, agentID, rev+1, events); err != nil {
			return err
		}
		commitStateCrash()
		return nil
	})
	return saved, err
}

// commitEvents saves events in agentID's change to revision rev: one with
// no EventID is named <agent>:<rev>:<kind>; another agent's is skipped when
// that agent is deleted.
func commitEvents(ctx context.Context, tx *sql.Tx, agentID string, rev int64, events []Event) (saved []Event, err error) {
	for _, e := range events {
		if e.EventID == "" {
			e.EventID = fmt.Sprintf("%s:%d:%s", agentID, rev, e.Kind)
		}
		live := true
		if e.AgentID != agentID {
			err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE agent_id = ? AND deleted_at IS NULL`,
				e.AgentID).Scan(&live)
			if err != nil {
				return nil, err
			}
		}
		if !live {
			continue
		}
		got, err := appendEvent(ctx, tx, e)
		if err != nil {
			return nil, err
		}
		saved = append(saved, got)
	}
	return saved, nil
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
	q := "SELECT " + readCols + " FROM agents WHERE " + strings.Join(where, " AND ") + " ORDER BY agent_id" //nolint:gosec // G202: constant column names and placeholders only.
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
		if err := rows.Scan(a.readFields()...); err != nil {
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
	return setArchive(ctx, s.db, agentID, reason, at)
}

func setArchive(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, agentID string, reason *string, at *time.Time) error {
	var stamp *string
	if at != nil {
		v := Stamp(*at)
		stamp = &v
	}
	_, err := q.ExecContext(ctx, `UPDATE agents SET archive_reason = ?,
		archived_at = CASE WHEN ? IS NULL THEN NULL ELSE COALESCE(archived_at, ?) END,
		history_purge_failed_at = CASE WHEN ? IS NULL THEN NULL ELSE history_purge_failed_at END, updated_at = ?
		WHERE agent_id = ?`, reason, stamp, stamp, stamp, Stamp(time.Now()), agentID)
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
	_, err := s.TombstoneEvents(ctx, agentID, now, nil)
	return err
}

// TombstoneEvents is Tombstone that, in the same transaction that sets
// deleted_at and history_purged_at and deletes agentID's events, also saves
// events as AppendEvent does: after the purge they are live only (seq 0).
// It returns them.
func (s *Store) TombstoneEvents(ctx context.Context, agentID string, now time.Time, events []Event) (saved []Event, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE agents SET deleted_at = COALESCE(deleted_at, ?),
			history_purged_at = COALESCE(history_purged_at, ?), history_purge_failed_at = NULL, updated_at = ?
			WHERE agent_id = ?`, Stamp(now), Stamp(now), Stamp(now), agentID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_events WHERE agent_id = ?`, agentID); err != nil {
			return err
		}
		if saved, err = commitEvents(ctx, tx, agentID, 0, events); err != nil {
			return err
		}
		commitStateCrash()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// AgentSpec is the agent columns Update changes: the spec, its version, the
// selected harness and its current native session, and the last applied
// Update RequestID.
type AgentSpec struct {
	Name, SpecJSON, Harness                                    string
	Model, HarnessSessionID, HarnessSessionRoot, LastRequestID *string
	SpecVersion                                                int64
}

// SpecOf returns a's spec columns.
func (a Agent) SpecOf() AgentSpec {
	return AgentSpec{Name: a.Name, SpecJSON: a.SpecJSON, Harness: a.Harness, Model: a.Model,
		HarnessSessionID: a.HarnessSessionID, HarnessSessionRoot: a.HarnessSessionRoot, LastRequestID: a.LastRequestID,
		SpecVersion: a.SpecVersion}
}

// ErrSpecChanged means the agent's spec_version no longer equals the expected one.
var ErrSpecChanged = errors.New("loomstore: agent spec version changed")

// CompareAndSetSpec sets agentID's spec columns to `to` in one statement, only
// if its spec_version still equals fromVersion and it is not deleted;
// otherwise it returns ErrSpecChanged.
func (s *Store) CompareAndSetSpec(ctx context.Context, agentID string, fromVersion int64, to AgentSpec) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET name = ?, spec_json = ?, harness = ?, model = ?,
		harness_session_id = ?, harness_session_root = ?, last_request_id = ?, spec_version = ?, updated_at = ?
		WHERE agent_id = ? AND deleted_at IS NULL AND spec_version = ?`,
		to.Name, to.SpecJSON, to.Harness, to.Model, to.HarnessSessionID, to.HarnessSessionRoot, to.LastRequestID, to.SpecVersion,
		Stamp(time.Now()), agentID, fromVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSpecChanged
	}
	return nil
}

// CommitSpec is the one write of an Update's or harness switch's spec
// change. In one transaction it sets agentID's spec columns to `to` and
// bumps its revision by one, only if its spec_version still equals
// fromVersion, its revision still equals rev and it is not deleted
// (otherwise ErrSpecChanged), and saves events as CommitState does. Both are
// saved or neither. It returns the saved events.
func (s *Store) CommitSpec(ctx context.Context, agentID string, fromVersion, rev int64, to AgentSpec, events []Event) (saved []Event, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE agents SET name = ?, spec_json = ?, harness = ?, model = ?,
		harness_session_id = ?, harness_session_root = ?, last_request_id = ?, spec_version = ?, updated_at = ?,
		revision = revision + 1 WHERE agent_id = ? AND deleted_at IS NULL AND spec_version = ? AND revision = ?`,
			to.Name, to.SpecJSON, to.Harness, to.Model, to.HarnessSessionID, to.HarnessSessionRoot, to.LastRequestID,
			to.SpecVersion, Stamp(time.Now()), agentID, fromVersion, rev)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrSpecChanged
		}
		if saved, err = commitEvents(ctx, tx, agentID, rev+1, events); err != nil {
			return err
		}
		commitStateCrash()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
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
// columns that step filled; a nil column is kept. The session step saves the
// returned NativeRef whole: its id and its root.
func (s *Store) SetCreateStep(ctx context.Context, agentID string, step int64, worktreePath, sessionID, sessionRoot *string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET create_step = MAX(create_step, ?),
		worktree_path = COALESCE(?, worktree_path), harness_session_id = COALESCE(?, harness_session_id),
		harness_session_root = COALESCE(?, harness_session_root), updated_at = ? WHERE agent_id = ?`,
		step, worktreePath, sessionID, sessionRoot, Stamp(time.Now()), agentID)
	return err
}
