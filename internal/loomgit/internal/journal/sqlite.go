package journal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

type SQLite struct{ db *sql.DB }

func OpenSQLiteReadOnly(path string) (*SQLite, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	query := u.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLite{db: db}, nil
}

// OpenSQLite opens a host-local durable store. A separate SQLite connection
// is safe in each process; all fences are checked by row updates.
//
//nolint:funlen // Schema bootstrap is one atomic set of related table definitions.
func OpenSQLite(path string) (*SQLite, error) {
	if path == "" {
		return nil, errors.New("journal path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS journal_entries (
		id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE, operation TEXT NOT NULL,
		phase TEXT NOT NULL, version INTEGER NOT NULL, fence INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS journal_results (
		request_id TEXT PRIMARY KEY, result BLOB NOT NULL,
		FOREIGN KEY(request_id) REFERENCES journal_entries(request_id)
	);
	CREATE TABLE IF NOT EXISTS journal_leases (
		scope TEXT PRIMARY KEY, owner TEXT NOT NULL, fence INTEGER NOT NULL, expires_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS event_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT, entry_id TEXT NOT NULL REFERENCES journal_entries(id),
		kind TEXT NOT NULL, payload BLOB, delivered INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS change_revisions (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, number INTEGER NOT NULL,
		request_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, operation TEXT NOT NULL,
		outcome TEXT NOT NULL, base_sha TEXT NOT NULL, head_sha TEXT NOT NULL DEFAULT '',
		tree_hash TEXT NOT NULL, source_head_sha TEXT NOT NULL,
		derived_from_change TEXT NOT NULL DEFAULT '', derived_from_number INTEGER NOT NULL DEFAULT 0,
		ready INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(workspace, change_id, number)
	);
	CREATE TABLE IF NOT EXISTS workspace_repos (
		workspace TEXT NOT NULL, repo TEXT NOT NULL, trunk TEXT NOT NULL,
		workspace_branch TEXT NOT NULL, base_sha TEXT NOT NULL,
		PRIMARY KEY(workspace, repo)
	);
	CREATE TABLE IF NOT EXISTS workspace_settings (
		workspace TEXT PRIMARY KEY, auto_commit INTEGER NOT NULL DEFAULT 1,
		lead_may_approve_publish INTEGER NOT NULL DEFAULT 1,
		delivery_mode TEXT NOT NULL DEFAULT 'stack'
	);
	CREATE INDEX IF NOT EXISTS event_outbox_pending ON event_outbox(delivered, id);`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open journal: %w", err)
	}
	for _, step := range []struct {
		name   string
		create func(*sql.DB) error
	}{
		{"outbox delivery", createOutboxDeliverySchema},
		{"driver changes", createDriverChanges},
		{"local lineage", createLocalLineageSchema},
		{"revision completeness", createRevisionCompleteness},
		{"review journal", createReviewSchema},
		{"workspace creation journal", initWorkspaceCreationSchema},
		{"applied journal", createAppliedSchema},
		{"pull journal", createPullSchema},
		{"working areas", createWorkingAreaSchema},
		{"publication journal", createPublicationSchema},
		{"stack backend journal", createStackBackendSchema},
		{"provider journal", createProviderSchema},
		{"feedback journal", createFeedbackSchema},
		{"delivery mode", ensureDeliveryMode},
		{"lead merge policy", ensureLeadMayMerge},
		{"abandonment journal", createAbandonSchema},
		{"retention journal", createRetentionSchema},
	} {
		if err := step.create(db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("open %s: %w", step.name, err)
		}
	}
	return &SQLite{db: db}, nil
}
func (s *SQLite) Close() error { return s.db.Close() }

// LeadMayApprovePublish defaults on for workspaces without an explicit setting.
func (s *SQLite) LeadMayApprovePublish(ctx context.Context, workspace string) (bool, error) {
	if workspace == "" {
		return false, errors.New("workspace is required")
	}
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT lead_may_approve_publish FROM workspace_settings WHERE workspace = ?`, workspace).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return enabled != 0, err
}

func (s *SQLite) SetLeadMayApprovePublish(ctx context.Context, workspace string, enabled bool) error {
	if workspace == "" {
		return errors.New("workspace is required")
	}
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_settings(workspace, lead_may_approve_publish) VALUES (?, ?)
		ON CONFLICT(workspace) DO UPDATE SET lead_may_approve_publish = excluded.lead_may_approve_publish`, workspace, value)
	return err
}

// LeadMayMergeWarning is shown whenever lead_may_merge=when_green is on.
const LeadMayMergeWarning = "no required review: Loom adds no review requirement of its own, so on a repo " +
	"without a required-review rule an agent chain can land code no human reviewed"

// LeadMergePolicy is the human-set lead_may_merge setting for one workspace.
type LeadMergePolicy struct{ Workspace, Value, SetBy string }

// LeadMergeStack is a Loom-backend stack in a workspace whose lead may merge when green.
type LeadMergeStack struct{ Workspace, StackID, Lead, SetBy string }

func ensureLeadMayMerge(db *sql.DB) error {
	for _, column := range []string{"lead_may_merge TEXT NOT NULL DEFAULT 'off'", "lead_may_merge_set_by TEXT NOT NULL DEFAULT ''"} {
		if _, err := db.Exec(`ALTER TABLE workspace_settings ADD COLUMN ` + column); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

// LeadMayMerge defaults off for workspaces without an explicit setting.
func (s *SQLite) LeadMayMerge(ctx context.Context, workspace string) (LeadMergePolicy, error) {
	policy := LeadMergePolicy{Workspace: workspace, Value: "off"}
	if workspace == "" {
		return policy, errors.New("workspace is required")
	}
	err := s.db.QueryRowContext(ctx, `SELECT lead_may_merge,lead_may_merge_set_by FROM workspace_settings WHERE workspace=?`,
		workspace).Scan(&policy.Value, &policy.SetBy)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	return policy, err
}

func (s *SQLite) SetLeadMayMerge(ctx context.Context, workspace, value, setBy string) error {
	if workspace == "" || setBy == "" {
		return errors.New("workspace and setter are required")
	}
	if value != "off" && value != "when_green" {
		return fmt.Errorf("invalid lead_may_merge %q", value)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_settings(workspace,lead_may_merge,lead_may_merge_set_by) VALUES (?,?,?)
		ON CONFLICT(workspace) DO UPDATE SET lead_may_merge=excluded.lead_may_merge,lead_may_merge_set_by=excluded.lead_may_merge_set_by`,
		workspace, value, setBy)
	return err
}

// LeadMergePolicies lists workspaces with lead_may_merge=when_green. A read-only
// store from before the setting (or its table) existed has none.
func (s *SQLite) LeadMergePolicies(ctx context.Context) ([]LeadMergePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,lead_may_merge,lead_may_merge_set_by FROM workspace_settings
		WHERE lead_may_merge='when_green' ORDER BY workspace`)
	if err != nil && (strings.Contains(err.Error(), "no such column") || strings.Contains(err.Error(), "no such table")) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var policies []LeadMergePolicy
	for rows.Next() {
		var policy LeadMergePolicy
		if err := rows.Scan(&policy.Workspace, &policy.Value, &policy.SetBy); err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

// LeadMergeStacks lists Loom-backend stacks whose workspace lets the lead merge
// when green, with the lead whose working area applied the stack.
func (s *SQLite) LeadMergeStacks(ctx context.Context) ([]LeadMergeStack, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.workspace,b.stack_id,ws.lead_may_merge_set_by,
		COALESCE((SELECT a.lead FROM applied_layers a JOIN change_publications p
			ON p.workspace=a.workspace AND p.change_id=a.change_id
			WHERE p.workspace=b.workspace AND p.stack_id=b.stack_id AND a.phase='done'
			ORDER BY a.rowid DESC LIMIT 1),'')
		FROM stack_backends b JOIN workspace_settings ws ON ws.workspace=b.workspace
		WHERE b.backend='loom' AND ws.lead_may_merge='when_green' ORDER BY b.workspace,b.stack_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var stacks []LeadMergeStack
	for rows.Next() {
		var stack LeadMergeStack
		if err := rows.Scan(&stack.Workspace, &stack.StackID, &stack.SetBy, &stack.Lead); err != nil {
			return nil, err
		}
		stacks = append(stacks, stack)
	}
	return stacks, rows.Err()
}

// AutoCommit defaults on for workspaces without an explicit setting.
func (s *SQLite) AutoCommit(ctx context.Context, workspace string) (bool, error) {
	if workspace == "" {
		return false, errors.New("workspace is required")
	}
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT auto_commit FROM workspace_settings WHERE workspace = ?`, workspace).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return enabled != 0, err
}

func (s *SQLite) SetAutoCommit(ctx context.Context, workspace string, enabled bool) error {
	if workspace == "" {
		return errors.New("workspace is required")
	}
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_settings(workspace, auto_commit) VALUES (?, ?)
		ON CONFLICT(workspace) DO UPDATE SET auto_commit = excluded.auto_commit`, workspace, value)
	return err
}
func ensureDeliveryMode(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS delivery_holds (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, status TEXT NOT NULL,
		PRIMARY KEY(workspace,change_id))`); err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info(workspace_settings)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "delivery_mode" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE workspace_settings ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT 'stack'`)
	return err
}

func (s *SQLite) SetDeliveryHold(ctx context.Context, workspace, change, status string) error {
	if workspace == "" || change == "" {
		return errors.New("workspace and change are required")
	}
	if status == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM delivery_holds WHERE workspace=? AND change_id=?`, workspace, change)
		return err
	}
	if status != "waiting_on_dependency" && status != "waiting_on_verdict" {
		return fmt.Errorf("invalid delivery hold %q", status)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO delivery_holds(workspace,change_id,status) VALUES (?,?,?)
		ON CONFLICT(workspace,change_id) DO UPDATE SET status=excluded.status`, workspace, change, status)
	return err
}

func (s *SQLite) DeliveryHold(ctx context.Context, workspace, change string) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM delivery_holds WHERE workspace=? AND change_id=?`, workspace, change).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return status, err
}

func (s *SQLite) DeliveryMode(ctx context.Context, workspace string) (string, error) {
	if workspace == "" {
		return "", errors.New("workspace is required")
	}
	var mode string
	err := s.db.QueryRowContext(ctx, `SELECT delivery_mode FROM workspace_settings WHERE workspace=?`, workspace).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "stack", nil
	}
	return mode, err
}

func (s *SQLite) SetDeliveryMode(ctx context.Context, workspace, mode string) error {
	if workspace == "" {
		return errors.New("workspace is required")
	}
	if mode != "stack" && mode != "trunk" {
		return fmt.Errorf("invalid delivery mode %q", mode)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspace_settings(workspace,delivery_mode) VALUES (?,?)
		ON CONFLICT(workspace) DO UPDATE SET delivery_mode=excluded.delivery_mode`, workspace, mode)
	return err
}

func scanEntry(row interface{ Scan(...any) error }) (loomgit.JournalEntry, error) {
	var e loomgit.JournalEntry
	err := row.Scan(&e.ID, &e.RequestID, &e.Operation, &e.Phase, &e.Version, &e.Fence, &e.Result)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

const entryColumns = `j.id, j.request_id, j.operation, j.phase, j.version, j.fence, r.result`
const entryFrom = ` FROM journal_entries j LEFT JOIN journal_results r ON r.request_id = j.request_id`

func (s *SQLite) Begin(ctx context.Context, requestID, operation string) (loomgit.JournalEntry, bool, error) {
	if requestID == "" || operation == "" {
		return loomgit.JournalEntry{}, false, errors.New("request ID and operation are required")
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO journal_entries (id, request_id, operation, phase, version, fence) VALUES (?, ?, ?, 'started', 1, 1) ON CONFLICT(request_id) DO NOTHING`, requestID, requestID, operation)
	if err != nil {
		return loomgit.JournalEntry{}, false, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return loomgit.JournalEntry{}, false, err
	}
	e, err := scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryColumns+entryFrom+` WHERE j.request_id = ?`, requestID))
	return e, n == 1, err
}
func (s *SQLite) Get(ctx context.Context, id string) (loomgit.JournalEntry, error) {
	return scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryColumns+entryFrom+` WHERE j.id = ?`, id))
}
func (s *SQLite) OpenEntries(ctx context.Context) ([]loomgit.JournalEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryColumns+entryFrom+` WHERE j.phase <> 'done' ORDER BY j.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []loomgit.JournalEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *SQLite) Advance(ctx context.Context, prior loomgit.JournalEntry, phase string, result []byte, events []loomgit.OutboxEvent) (loomgit.JournalEntry, error) {
	if phase == "" {
		return loomgit.JournalEntry{}, errors.New("phase is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return loomgit.JournalEntry{}, err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := tx.ExecContext(ctx, `UPDATE journal_entries SET phase = ?, version = version + 1 WHERE id = ? AND version = ? AND fence = ? AND phase <> 'done'`, phase, prior.ID, prior.Version, prior.Fence)
	if err != nil {
		return loomgit.JournalEntry{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return loomgit.JournalEntry{}, err
	}
	if n == 0 {
		return loomgit.JournalEntry{}, ErrStale
	}
	if phase == "done" {
		if result == nil {
			result = []byte{}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO journal_results(request_id, result) VALUES (?, ?)`, prior.RequestID, result); err != nil {
			return loomgit.JournalEntry{}, err
		}
	}
	for _, ev := range events {
		if ev.Kind == "" {
			return loomgit.JournalEntry{}, errors.New("event kind is required")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO event_outbox(entry_id, kind, payload) VALUES (?, ?, ?)`, prior.ID, ev.Kind, ev.Payload); err != nil {
			return loomgit.JournalEntry{}, err
		}
	}
	e, err := scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+entryFrom+` WHERE j.id = ?`, prior.ID))
	if err != nil {
		return e, err
	}
	if err = tx.Commit(); err != nil {
		return loomgit.JournalEntry{}, err
	}
	return e, nil
}
func (s *SQLite) Takeover(ctx context.Context, prior loomgit.JournalEntry) (loomgit.JournalEntry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return loomgit.JournalEntry{}, err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := tx.ExecContext(ctx, `UPDATE journal_entries SET fence = fence + 1, version = version + 1 WHERE id = ? AND version = ? AND fence = ? AND phase <> 'done'`, prior.ID, prior.Version, prior.Fence)
	if err != nil {
		return loomgit.JournalEntry{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return loomgit.JournalEntry{}, err
	}
	if n == 0 {
		return loomgit.JournalEntry{}, ErrStale
	}
	e, err := scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+entryFrom+` WHERE j.id = ?`, prior.ID))
	if err != nil {
		return e, err
	}
	if err := tx.Commit(); err != nil {
		return loomgit.JournalEntry{}, err
	}
	return e, nil
}
func (s *SQLite) PendingEvents(ctx context.Context) ([]loomgit.OutboxEvent, error) {
	if err := s.expirePendingEvents(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,o.entry_id,o.kind,o.payload,o.delivered,d.jsonl_emitted
		FROM event_outbox o JOIN event_outbox_delivery d ON d.event_id=o.id
		WHERE o.delivered=0 AND d.expired=0 ORDER BY o.id`)
	if err != nil {
		return nil, err
	}
	return scanOutboxRows(rows)
}
func (s *SQLite) PendingJSONLEvents(ctx context.Context) ([]loomgit.OutboxEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,o.entry_id,o.kind,o.payload,o.delivered,d.jsonl_emitted
		FROM event_outbox o JOIN event_outbox_delivery d ON d.event_id=o.id
		WHERE o.delivered=0 AND d.jsonl_emitted=0 ORDER BY o.id`)
	if err != nil {
		return nil, err
	}
	return scanOutboxRows(rows)
}
func scanOutboxRows(rows *sql.Rows) ([]loomgit.OutboxEvent, error) {
	defer func() { _ = rows.Close() }()
	var out []loomgit.OutboxEvent
	for rows.Next() {
		var e loomgit.OutboxEvent
		if err := rows.Scan(&e.ID, &e.EntryID, &e.Kind, &e.Payload, &e.Delivered, &e.JSONLEmitted); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *SQLite) MarkDelivered(ctx context.Context, id int64) error {
	r, err := s.db.ExecContext(ctx, `UPDATE event_outbox SET delivered = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

var _ loomgit.Store = (*SQLite)(nil)
