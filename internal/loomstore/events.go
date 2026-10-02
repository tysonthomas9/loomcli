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

// LastMessage returns the text of agentID's last saved completed message
// item, or "" when it has none.
func (s *Store) LastMessage(ctx context.Context, agentID string) (string, error) {
	var text sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT json_extract(redacted_payload, '$.text') FROM agent_events
		WHERE agent_id = ? AND kind = 'item.completed' AND json_extract(redacted_payload, '$.itemKind') = 'message'
		ORDER BY seq DESC LIMIT 1`, agentID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text.String, err
}
