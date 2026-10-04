package loomstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions/redact"
)

// Event is one agent_events row. Payload is JSON and is stored redacted.
type Event struct {
	AgentID   string
	Seq       int64
	EventID   string
	Kind      string
	TurnID    string
	Payload   json.RawMessage
	CreatedAt string
}

// AppendEvent redacts e.Payload and appends it with the agent's next seq; seq
// allocation and insert share one transaction. Appending an EventID the agent
// already has returns the stored event unchanged.
func (s *Store) AppendEvent(ctx context.Context, e Event) (got Event, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		got, err = appendEvent(ctx, tx, e)
		return err
	})
	return got, err
}

// AppendEvents appends events to agentID in order, as AppendEvent does, all
// in one transaction: none is saved unless every one is and the commit
// succeeds. before is the agent's last seq before them.
func (s *Store) AppendEvents(ctx context.Context, agentID string, events []Event) (before int64, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = ?`,
			agentID).Scan(&before); err != nil {
			return err
		}
		for _, e := range events {
			if e.AgentID != agentID {
				return fmt.Errorf("loomstore: event of %s in a batch of %s", e.AgentID, agentID)
			}
			if _, err := appendEvent(ctx, tx, e); err != nil {
				return err
			}
		}
		return nil
	})
	return before, err
}

func appendEvent(ctx context.Context, tx *sql.Tx, e Event) (Event, error) {
	red, err := redactPayload(e.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("loomstore: event payload: %w", err)
	}
	e.Payload = json.RawMessage(red)
	e.CreatedAt = Stamp(time.Now())
	got, err := getEvent(ctx, tx, e.AgentID, e.EventID)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return got, err
	}
	var purged bool
	err = tx.QueryRowContext(ctx, `SELECT history_purged_at IS NOT NULL FROM agents WHERE agent_id = ?`, e.AgentID).Scan(&purged)
	if purged || (err != nil && !errors.Is(err, sql.ErrNoRows)) {
		return e, err // purged history stays purged: a later event is live only (seq 0)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM agent_events WHERE agent_id = ?`,
		e.AgentID).Scan(&e.Seq); err != nil {
		return Event{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_events (agent_id, seq, event_id, kind, turn_id, redacted_payload, created_at)
		VALUES (?,?,?,?,NULLIF(?, ''),?,?)`, e.AgentID, e.Seq, e.EventID, e.Kind, e.TurnID, red, e.CreatedAt)
	return e, err
}

// redactPayload runs redact.String over every string value in a JSON
// payload. Unlike redact.JSONLContent it skips no key (path, signature, ID)
// and no object type (images), so metadata is covered too.
func redactPayload(p json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	if dec.More() {
		return "", errors.New("trailing data after JSON value")
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				x[k] = walk(c)
			}
		case []any:
			for i, c := range x {
				x[i] = walk(c)
			}
		case string:
			return redact.String(x)
		}
		return v
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(walk(v)); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func getEvent(ctx context.Context, tx *sql.Tx, agentID, eventID string) (Event, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+eventCols+` FROM agent_events WHERE agent_id = ? AND event_id = ?`, agentID, eventID)
	return scanEvent(row)
}

const eventCols = `agent_id, seq, event_id, kind, COALESCE(turn_id, ''), redacted_payload, created_at`

func scanEvent(r interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var p string
	err := r.Scan(&e.AgentID, &e.Seq, &e.EventID, &e.Kind, &e.TurnID, &p, &e.CreatedAt)
	e.Payload = json.RawMessage(p)
	return e, err
}

// EventQuery selects one page of an agent's events after the cursor seq After.
// Snapshot 0 starts a new read pinned at the current last seq; later pages
// pass the returned SnapshotSeq so events appended meanwhile are excluded.
type EventQuery struct {
	AgentID  string
	After    int64
	Snapshot int64
	Kinds    []string
	Limit    int
}

// EventPage is one page of events. Next is the cursor for the following page;
// More reports whether events up to SnapshotSeq remain.
type EventPage struct {
	Events      []Event
	SnapshotSeq int64
	Next        int64
	More        bool
}

// ListEvents returns one snapshot-pinned page of q.AgentID's events in seq order.
func (s *Store) ListEvents(ctx context.Context, q EventQuery) (EventPage, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	page := EventPage{SnapshotSeq: q.Snapshot, Next: q.After}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	if page.SnapshotSeq == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = ?`,
			q.AgentID).Scan(&page.SnapshotSeq); err != nil {
			return page, err
		}
	}
	where := `agent_id = ? AND seq > ? AND seq <= ?`
	args := []any{q.AgentID, q.After, page.SnapshotSeq}
	if len(q.Kinds) > 0 {
		where += ` AND kind IN (` + strings.TrimSuffix(strings.Repeat("?,", len(q.Kinds)), ",") + `)`
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+eventCols+` FROM agent_events WHERE `+where+` ORDER BY seq LIMIT ?`,
		append(args, q.Limit+1)...)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return page, err
		}
		if len(page.Events) == q.Limit {
			page.More = true
			break
		}
		page.Events = append(page.Events, e)
		page.Next = e.Seq
	}
	return page, rows.Err()
}

// HasEvent reports whether agentID has saved event eventID.
func (s *Store) HasEvent(ctx context.Context, agentID, eventID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_events WHERE agent_id = ? AND event_id = ?`,
		agentID, eventID).Scan(&n)
	return n > 0, err
}

// Unreceipted lists agentID's events of kind, in order, whose EventID has
// no Send receipt on the agent yet: notices not yet put in a slot.
func (s *Store) Unreceipted(ctx context.Context, agentID, kind string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventCols+` FROM agent_events e WHERE agent_id = ? AND kind = ?
		AND NOT EXISTS (SELECT 1 FROM agent_send_receipts r WHERE r.agent_id = e.agent_id AND r.request_id = e.event_id)
		ORDER BY seq`, agentID, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastCostTotal returns the costTotalUsd of agentID's newest usage row for
// native session that has one, or 0 when none does. Rows are appended in seq
// order, so the newest is the highest rowid: the lookup walks the kind index
// back and stops at the first match.
func (s *Store) LastCostTotal(ctx context.Context, agentID, session string) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT json_extract(redacted_payload, '$.costTotalUsd') FROM agent_events
		WHERE agent_id = ? AND kind = 'usage' AND json_extract(redacted_payload, '$.session') = ?
		AND json_extract(redacted_payload, '$.costTotalUsd') > 0
		ORDER BY rowid DESC LIMIT 1`, agentID, session).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return total, err
}

// LastReply returns the texts of the completed message items of the turn
// that holds agentID's last message item in its current attempt, in order:
// the whole final reply, which a harness may save as several message items
// (OpenCode saves one per text part, so the last alone can be a one-line
// stub). The turn is that message's turn_id: every message of the attempt
// tagged with it (a codex history replay saves items with their turn_id but
// no turn.started). An untagged message's turn starts after the last
// turn.started before it. Never before the attempt (attempt_after_seq).
// It is empty when the attempt has no message.
func (s *Store) LastReply(ctx context.Context, agentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `WITH
		attempt AS (SELECT attempt_after_seq AS after FROM agents WHERE agent_id = ?1),
		msgs AS (SELECT e.seq, COALESCE(e.turn_id, '') AS turn, json_extract(e.redacted_payload, '$.text') AS text
			FROM agent_events e, attempt
			WHERE e.agent_id = ?1 AND e.kind = 'item.completed' AND json_extract(e.redacted_payload, '$.itemKind') = 'message'
			AND e.seq > attempt.after),
		last AS (SELECT seq, turn FROM msgs ORDER BY seq DESC LIMIT 1),
		start AS (SELECT MAX((SELECT after FROM attempt), COALESCE((SELECT MAX(t.seq) FROM agent_events t, last
			WHERE t.agent_id = ?1 AND t.kind = 'turn.started' AND t.seq < last.seq), 0)) AS seq)
		SELECT COALESCE(msgs.text, '') FROM msgs, last, start
		WHERE CASE WHEN last.turn <> '' THEN msgs.turn = last.turn ELSE msgs.seq > start.seq END ORDER BY msgs.seq`, agentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, rows.Err()
}

// LastMessage returns the text of the last completed message item of
// agentID's current attempt, or "" when that attempt has none. The attempt
// starts after attempt_after_seq, which the reopen sets in its own
// transaction, so an earlier attempt's message is never returned, even after
// a crash right after the reopen.
func (s *Store) LastMessage(ctx context.Context, agentID string) (string, error) {
	var text sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT json_extract(e.redacted_payload, '$.text') FROM agent_events e
		JOIN agents a USING (agent_id)
		WHERE e.agent_id = ? AND e.kind = 'item.completed' AND json_extract(e.redacted_payload, '$.itemKind') = 'message'
		AND e.seq > a.attempt_after_seq
		ORDER BY e.seq DESC LIMIT 1`, agentID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text.String, err
}
