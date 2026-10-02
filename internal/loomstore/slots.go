package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Slot states (design v2 §4.9).
const (
	SlotWaiting   = "waiting"
	SlotHanded    = "handed"
	SlotDelivered = "delivered"
	SlotWithdrawn = "withdrawn"
)

// Slot is one agent_slots row: the latest message from one sender to one
// agent. The row is reused for the agent's life.
type Slot struct {
	AgentID, Sender, RequestID, Body, Source, State string
	NativeKey, QueuedAt                             *string
	First                                           bool
	UpdatedAt                                       string
}

const slotCols = `agent_id, sender, request_id, body, source, state, native_key, queued_at, first, updated_at`

func (s *Slot) fields() []any {
	return []any{&s.AgentID, &s.Sender, &s.RequestID, &s.Body, &s.Source, &s.State, &s.NativeKey,
		&s.QueuedAt, &s.First, &s.UpdatedAt}
}

// Receipt is one agent_send_receipts row: an accepted Send and its result.
type Receipt struct {
	AgentID, RequestID, Sender, ResultJSON, CreatedAt string
}

// SlotSend is one accepted Send's slot change.
type SlotSend struct {
	AgentID, Sender, RequestID, Body, Source string
	// Hand hands the message over at once (the agent is idle) with NativeKey.
	// Otherwise the message fills the sender's slot or replaces its waiting text.
	Hand      bool
	NativeKey string
	// First marks the slot to be handed over before older waiting slots (an interrupt).
	First bool
	// Reopen starts a new attempt of the finished agent: in the same
	// transaction it moves the agent finished -> active with the next attempt,
	// records the agent's last event seq as the attempt's start, clears its
	// outcome, and clears finished_at, which cancels its R29 history deadline. It fails with ErrHistoryPurged if the sweep already
	// purged the history, or ErrStateChanged if the agent is no longer
	// finished; then nothing is stored.
	Reopen bool
	// Result builds the Send's result JSON, stored as its receipt. replaced
	// reports that this Send replaced the sender's waiting text.
	Result func(replaced bool) (string, error)
}

// ErrSlotBusy means the sender's slot holds a message this Send would lose:
// a handed message not yet marked delivered, or, for Hand, a waiting one.
var ErrSlotBusy = errors.New("loomstore: sender's slot is busy")

// Send applies an accepted Send in one transaction with its receipt. A
// RequestID that already has a receipt on the agent is a retry: Send returns
// that receipt with retry true and changes nothing, whatever happened to the
// slot since. Replacing waiting text keeps the slot's queued_at.
func (s *Store) Send(ctx context.Context, in SlotSend) (r Receipt, retry bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		old, err := getReceipt(ctx, tx, in.AgentID, in.RequestID)
		if err == nil {
			r, retry = old, true
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var cur Slot
		err = tx.QueryRowContext(ctx, `SELECT `+slotCols+` FROM agent_slots WHERE agent_id = ? AND sender = ?`,
			in.AgentID, in.Sender).Scan(cur.fields()...)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if cur.State == SlotHanded || (in.Hand && cur.State == SlotWaiting) {
			return ErrSlotBusy
		}
		if err := reopen(ctx, tx, in); err != nil {
			return err
		}
		replaced, now, err := putSlot(ctx, tx, in, cur)
		if err != nil {
			return err
		}
		nativeKey := any(nil)
		if in.Hand {
			nativeKey = in.NativeKey
		}
		res, err := in.Result(replaced)
		if err != nil {
			return err
		}
		r = Receipt{AgentID: in.AgentID, RequestID: in.RequestID, Sender: in.Sender, ResultJSON: res, CreatedAt: now}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_send_receipts (agent_id, request_id, sender, result_json, created_at,
			body, native_key) VALUES (?,?,?,?,?,?,?)`, r.AgentID, r.RequestID, r.Sender, r.ResultJSON, r.CreatedAt, in.Body, nativeKey)
		return err
	})
	return r, retry, err
}

// putSlot writes in into the sender's slot, whose current row is cur: a
// waiting slot keeps its place in line (replaced), else the slot joins the
// end of the line. It returns the write's stamp.
func putSlot(ctx context.Context, tx *sql.Tx, in SlotSend, cur Slot) (replaced bool, now string, err error) {
	replaced = cur.State == SlotWaiting
	now = Stamp(time.Now())
	state, nativeKey, queuedAt := SlotWaiting, any(nil), any(nil)
	if in.Hand {
		state, nativeKey = SlotHanded, in.NativeKey
	}
	if replaced { // the slot keeps its place in line
		queuedAt = *cur.QueuedAt
	} else if queuedAt, err = nextQueuedAt(ctx, tx, in.AgentID); err != nil {
		return false, "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_slots (`+slotCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (agent_id, sender) DO UPDATE SET request_id = excluded.request_id, body = excluded.body,
		source = excluded.source, state = excluded.state, native_key = excluded.native_key,
		queued_at = excluded.queued_at, first = excluded.first, updated_at = excluded.updated_at`,
		in.AgentID, in.Sender, in.RequestID, in.Body, in.Source, state, nativeKey, queuedAt,
		in.First || (replaced && cur.First), now)
	return replaced, now, err
}

// Notice is one record a sender adds to its slot, keyed so it is added once.
type Notice struct{ Key, Text string }

// Notify adds notices, in order, to the sender's slot in one transaction
// (design v2 §10.3): a waiting slot keeps its text and place and gains the
// records on new lines; any other slot is filled with them. Each notice's
// Key is stored as a receipt, so a notice already added is skipped; the
// last added one becomes the slot's RequestID and its receipt keeps the
// whole slot text. A handed slot fails with ErrSlotBusy and changes nothing.
// added reports whether any notice was new.
func (s *Store) Notify(ctx context.Context, agentID, sender, source string, notices []Notice,
	result func(requestID string, replaced bool) (string, error)) (added bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		fresh, err := unreceipted(ctx, tx, agentID, notices)
		if err != nil || len(fresh) == 0 {
			return err
		}
		var cur Slot
		err = tx.QueryRowContext(ctx, `SELECT `+slotCols+` FROM agent_slots WHERE agent_id = ? AND sender = ?`,
			agentID, sender).Scan(cur.fields()...)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if cur.State == SlotHanded {
			return ErrSlotBusy
		}
		texts := make([]string, 0, len(fresh)+1)
		if cur.State == SlotWaiting {
			texts = append(texts, cur.Body)
		}
		for _, n := range fresh {
			texts = append(texts, n.Text)
		}
		in := SlotSend{AgentID: agentID, Sender: sender, RequestID: fresh[len(fresh)-1].Key,
			Body: strings.Join(texts, "\n"), Source: source}
		replaced, now, err := putSlot(ctx, tx, in, cur)
		if err != nil {
			return err
		}
		for _, n := range fresh {
			res, err := result(n.Key, replaced)
			if err != nil {
				return err
			}
			body := any(nil)
			if n.Key == in.RequestID {
				body = in.Body
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_send_receipts (agent_id, request_id, sender,
				result_json, created_at, body) VALUES (?,?,?,?,?,?)`, agentID, n.Key, sender, res, now, body); err != nil {
				return err
			}
		}
		added = true
		return nil
	})
	return added, err
}

// unreceipted returns the notices whose Key has no receipt on agentID.
func unreceipted(ctx context.Context, tx *sql.Tx, agentID string, notices []Notice) ([]Notice, error) {
	var fresh []Notice
	for _, n := range notices {
		if _, err := getReceipt(ctx, tx, agentID, n.Key); errors.Is(err, ErrNotFound) {
			fresh = append(fresh, n)
		} else if err != nil {
			return nil, err
		}
	}
	return fresh, nil
}

// SaveReceipt stores r as the receipt of a Send that changes no slot (an
// interrupt with no message). The caller holds the agent lock and has
// checked that r.RequestID has no receipt yet.
func (s *Store) SaveReceipt(ctx context.Context, r Receipt) (Receipt, error) {
	r.CreatedAt = Stamp(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_send_receipts (agent_id, request_id, sender, result_json, created_at)
		VALUES (?,?,?,?,?)`, r.AgentID, r.RequestID, r.Sender, r.ResultJSON, r.CreatedAt)
	return r, err
}

// ErrHistoryPurged means the agent's history was purged under R29.
var ErrHistoryPurged = errors.New("loomstore: agent history purged")

// reopen starts the agent's next attempt when in.Reopen is set.
func reopen(ctx context.Context, tx *sql.Tx, in SlotSend) error {
	if !in.Reopen {
		return nil
	}
	agentID := in.AgentID
	res, err := tx.ExecContext(ctx, `UPDATE agents SET state = 'active', attempt = attempt + 1, outcome = NULL,
		finished_at = NULL, updated_at = ?,
		attempt_after_seq = (SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = agents.agent_id)
		WHERE agent_id = ? AND state = 'finished' AND deleted_at IS NULL
		AND history_purged_at IS NULL`, Stamp(time.Now()), agentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var purged sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT history_purged_at FROM agents WHERE agent_id = ?`, agentID).Scan(&purged); err != nil {
		return err
	}
	if purged.Valid {
		return ErrHistoryPurged
	}
	return ErrStateChanged
}

// nextQueuedAt returns now, or just after the agent's latest queued_at if the
// clock has not moved past it, so delivery order is strict.
func nextQueuedAt(ctx context.Context, tx *sql.Tx, agentID string) (string, error) {
	now := time.Now().UTC()
	var last sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT MAX(queued_at) FROM agent_slots WHERE agent_id = ?`, agentID).Scan(&last); err != nil {
		return "", err
	}
	if last.Valid {
		t, err := time.Parse(stampLayout, last.String)
		if err != nil {
			return "", err
		}
		if !now.After(t) {
			now = t.Add(time.Nanosecond)
		}
	}
	return Stamp(now), nil
}

// HandNext hands over the agent's next waiting slot, a First slot before
// others and then the oldest by queued_at, setting its native key from
// nativeKey. It returns ErrNotFound when nothing waits.
func (s *Store) HandNext(ctx context.Context, agentID string, nativeKey func(Slot) string) (Slot, error) {
	var sl Slot
	err := s.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT `+slotCols+` FROM agent_slots WHERE agent_id = ? AND state = ?
			ORDER BY first DESC, queued_at, sender LIMIT 1`, agentID, SlotWaiting).Scan(sl.fields()...)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		k := nativeKey(sl)
		sl.State, sl.NativeKey, sl.UpdatedAt = SlotHanded, &k, Stamp(time.Now())
		if _, err = tx.ExecContext(ctx, `UPDATE agent_slots SET state = ?, native_key = ?, updated_at = ?
			WHERE agent_id = ? AND sender = ?`, sl.State, k, sl.UpdatedAt, agentID, sl.Sender); err != nil {
			return err
		}
		// The Send's receipt now reports the hand-over, so a later retry of it
		// returns state handed (design v2 §4.9), and records the input key.
		_, err = tx.ExecContext(ctx, `UPDATE agent_send_receipts SET native_key = ?,
			result_json = CASE WHEN json_valid(result_json) THEN json_set(result_json, '$.state', ?) ELSE result_json END
			WHERE agent_id = ? AND request_id = ?`, k, SlotHanded, agentID, sl.RequestID)
		return err
	})
	return sl, err
}

// Requeue puts the sender's handed message requestID back to waiting,
// keeping its place, after the harness said it never landed. It returns
// ErrNotFound if that message is no longer handed.
func (s *Store) Requeue(ctx context.Context, agentID, sender, requestID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agent_slots SET state = ?, native_key = NULL, updated_at = ?
		WHERE agent_id = ? AND sender = ? AND request_id = ? AND state = ?`,
		SlotWaiting, Stamp(time.Now()), agentID, sender, requestID, SlotHanded)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingAgents lists workspaceID's live agents with a waiting or handed
// slot, or a task_completed notice no slot has taken, for the dispatcher's
// sweep at start.
func (s *Store) PendingAgents(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.agent_id FROM agent_slots s JOIN agents a USING (agent_id)
		WHERE s.state IN (?, ?) AND a.deleted_at IS NULL AND a.workspace_id = ?
		UNION SELECT e.agent_id FROM agent_events e JOIN agents a USING (agent_id)
		WHERE e.kind = 'task_completed' AND a.deleted_at IS NULL AND a.workspace_id = ?
		AND NOT EXISTS (SELECT 1 FROM agent_send_receipts r WHERE r.agent_id = e.agent_id AND r.request_id = e.event_id)
		ORDER BY 1`, SlotWaiting, SlotHanded, workspaceID, workspaceID)
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

// MarkDelivered marks the sender's handed message requestID delivered and
// clears First. It returns ErrNotFound if that message is no longer handed.
func (s *Store) MarkDelivered(ctx context.Context, agentID, sender, requestID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agent_slots SET state = ?, first = 0, updated_at = ?
		WHERE agent_id = ? AND sender = ? AND request_id = ? AND state = ?`,
		SlotDelivered, Stamp(time.Now()), agentID, sender, requestID, SlotHanded)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Withdraw results.
const (
	Withdrawn      = "withdrawn"
	NothingWaiting = "nothing_waiting"
	AlreadyHanded  = "already_handed"
)

// ClearSlot withdraws the sender's waiting message (Withdraw, or Archive
// cancelled and Delete). It returns Withdrawn, NothingWaiting or AlreadyHanded.
func (s *Store) ClearSlot(ctx context.Context, agentID, sender string) (string, error) {
	out := NothingWaiting
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM agent_slots WHERE agent_id = ? AND sender = ?`,
			agentID, sender).Scan(&state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		case state == SlotHanded:
			out = AlreadyHanded
			return nil
		case state != SlotWaiting:
			return nil
		}
		out = Withdrawn
		_, err = tx.ExecContext(ctx, `UPDATE agent_slots SET state = ?, first = 0, updated_at = ?
			WHERE agent_id = ? AND sender = ?`, SlotWithdrawn, Stamp(time.Now()), agentID, sender)
		return err
	})
	return out, err
}

// Slots lists the agent's slots in delivery order: First, then oldest.
func (s *Store) Slots(ctx context.Context, agentID string) ([]Slot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+slotCols+` FROM agent_slots WHERE agent_id = ?
		ORDER BY first DESC, queued_at, sender`, agentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Slot
	for rows.Next() {
		var sl Slot
		if err := rows.Scan(sl.fields()...); err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

// HandedText returns the text of the agent's message that was handed over
// with input key nativeKey, kept on its Send's receipt; ok is false when no
// receipt records that key (a legacy row, or a key Loom never handed).
func (s *Store) HandedText(ctx context.Context, agentID, nativeKey string) (text string, ok bool, err error) {
	var body sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT body FROM agent_send_receipts WHERE agent_id = ? AND native_key = ?
		ORDER BY created_at DESC LIMIT 1`, agentID, nativeKey).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return body.String, err == nil && body.Valid, err
}

// GetReceipt returns the receipt of the agent's Send requestID, or ErrNotFound.
func (s *Store) GetReceipt(ctx context.Context, agentID, requestID string) (Receipt, error) {
	return getReceipt(ctx, s.db, agentID, requestID)
}

func getReceipt(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, agentID, requestID string) (Receipt, error) {
	var r Receipt
	err := q.QueryRowContext(ctx, `SELECT agent_id, request_id, sender, result_json, created_at
		FROM agent_send_receipts WHERE agent_id = ? AND request_id = ?`, agentID, requestID).
		Scan(&r.AgentID, &r.RequestID, &r.Sender, &r.ResultJSON, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}
