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
func (s *Store) AppendEvent(ctx context.Context, e Event) (Event, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, e.Payload); err != nil {
		return Event{}, fmt.Errorf("loomstore: event payload: %w", err)
	}
	red, err := redact.JSONLContent(buf.String())
	if err != nil {
		return Event{}, err
	}
	e.Payload = json.RawMessage(red)
	e.CreatedAt = Stamp(time.Now())
	err = s.tx(ctx, func(tx *sql.Tx) error {
		got, err := getEvent(ctx, tx, e.AgentID, e.EventID)
		if err == nil {
			e = got
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM agent_events WHERE agent_id = ?`,
			e.AgentID).Scan(&e.Seq); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_events (agent_id, seq, event_id, kind, turn_id, redacted_payload, created_at)
			VALUES (?,?,?,?,NULLIF(?, ''),?,?)`, e.AgentID, e.Seq, e.EventID, e.Kind, e.TurnID, red, e.CreatedAt)
		return err
	})
	return e, err
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
