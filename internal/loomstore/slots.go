package loomstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	// Events are saved with the Send, in its transaction. One with no EventID
	// is named <agent>:<revision>:<kind> of the revision Reopen bumps to.
	Events []Event
	// Result builds the Send's result JSON, stored as its receipt. replaced
	// reports that this Send replaced the sender's waiting text.
	Result func(replaced bool) (string, error)
	// LimitResume marks this Send a usage-limit resume (OR7). In the Send's
	// transaction a resume marks the agent's resume owed sent, keeping its
	// attempt count, so its receipt and the consumed eligibility commit
	// together; any other Send drops it, ending the episode. A retry changes
	// neither.
	LimitResume bool
}

// ErrSlotBusy means the sender's slot holds a message this Send would lose:
// a handed message not yet marked delivered, or, for Hand, a waiting one.
var ErrSlotBusy = errors.New("loomstore: sender's slot is busy")

// Send applies an accepted Send in one transaction with its receipt. A
// RequestID that already has a receipt on the agent is a retry: Send returns
// that receipt with retry true and changes nothing, whatever happened to the
// slot since. Replacing waiting text keeps the slot's queued_at.
func (s *Store) Send(ctx context.Context, in SlotSend) (r Receipt, retry bool, err error) {
	r, _, retry, err = s.SendEvents(ctx, in)
	return r, retry, err
}

// SendEvents is Send that also returns in.Events as saved. A retry saves none.
func (s *Store) SendEvents(ctx context.Context, in SlotSend) (r Receipt, saved []Event, retry bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if r, saved, retry, err = sendTx(ctx, tx, in); err != nil {
			return err
		}
		commitStateCrash()
		return nil
	})
	return r, saved, retry, err
}

// sendTx is SendEvents in tx.
func sendTx(ctx context.Context, tx *sql.Tx, in SlotSend) (r Receipt, saved []Event, retry bool, err error) {
	err = func() error {
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
		rev, err := reopen(ctx, tx, in)
		if err != nil {
			return err
		}
		replaced, now, err := keepRecords(ctx, tx, &in, cur)
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
			body, native_key, notices) VALUES (?,?,?,?,?,?,?,'{}')`, r.AgentID, r.RequestID, r.Sender, r.ResultJSON, r.CreatedAt, in.Body, nativeKey)
		if err != nil {
			return err
		}
		q := `DELETE FROM agent_limit_resumes WHERE agent_id = ?`
		if in.LimitResume {
			q = `UPDATE agent_limit_resumes SET due_at = '' WHERE agent_id = ?`
		}
		if _, err = tx.ExecContext(ctx, q, in.AgentID); err != nil {
			return err
		}
		saved, err = sendEvents(ctx, tx, in, rev)
		return err
	}()
	return r, saved, retry, err
}

// sendEvents appends in.Events in tx, naming one with no EventID by rev, the
// revision in.Reopen bumped to.
func sendEvents(ctx context.Context, tx *sql.Tx, in SlotSend, rev int64) (saved []Event, err error) {
	for _, e := range in.Events {
		if e.AgentID != in.AgentID || (e.EventID == "" && !in.Reopen) {
			return nil, fmt.Errorf("loomstore: event %s of %s in a Send to %s", e.Kind, e.AgentID, in.AgentID)
		}
		if e.EventID == "" {
			e.EventID = fmt.Sprintf("%s:%d:%s", in.AgentID, rev, e.Kind)
		}
		got, err := appendEvent(ctx, tx, e)
		if err != nil {
			return nil, err
		}
		saved = append(saved, got)
	}
	return saved, nil
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
	// A new body drops the slot's notices; Notify sets them after.
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_slots (`+slotCols+`, notices) VALUES (?,?,?,?,?,?,?,?,?,?,'{}')
		ON CONFLICT (agent_id, sender) DO UPDATE SET request_id = excluded.request_id, body = excluded.body, notices = '{}',
		source = excluded.source, state = excluded.state, native_key = excluded.native_key,
		queued_at = excluded.queued_at, first = excluded.first, updated_at = excluded.updated_at`,
		in.AgentID, in.Sender, in.RequestID, in.Body, in.Source, state, nativeKey, queuedAt,
		in.First || (replaced && cur.First), now)
	return replaced, now, err
}

// Notice is one record a sender adds to its slot, keyed so it is added once.
type Notice struct{ Key, Text string }

// SlotNotices are the notices at the end of a slot's body: their keys in
// the order added, and the byte offset in the body where the first begins
// (the text before it, less its line break, is the sender's own message).
//
// Legacy is set instead on a slot saved before notices were kept (migration
// 9) whose last addition was a task_completed notice: that notice's key,
// for the reader to rebuild the rest from the records it saved.
type SlotNotices struct {
	Keys   []string `json:"keys"`
	At     int      `json:"at"`
	Legacy string   `json:"-"`
}

// legacyNotices reports whether a slot or receipt with notices raw and
// request id requestID predates notices and last took a task_completed notice.
// Every row written since stores notices ('{}' when it has none), so NULL
// means a row from before the upgrade, and only such a row is read by text.
func legacyNotices(raw sql.NullString, requestID string) bool {
	return !raw.Valid && strings.HasPrefix(requestID, "task_completed:")
}

func readNotices(raw sql.NullString) (SlotNotices, error) {
	var n SlotNotices
	if !raw.Valid || raw.String == "" {
		return n, nil
	}
	err := json.Unmarshal([]byte(raw.String), &n)
	return n, err
}

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
		var notes SlotNotices
		if cur.State == SlotWaiting {
			texts = append(texts, cur.Body)
			if notes, err = waitingNotices(ctx, tx, cur); err != nil {
				return err
			}
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
		if err := saveNotices(ctx, tx, in, notes, fresh); err != nil {
			return err
		}
		for _, n := range fresh {
			res, err := result(n.Key, replaced)
			if err != nil {
				return err
			}
			if err := noticeReceipt(ctx, tx, in, n.Key, res, now, notes.Legacy != ""); err != nil {
				return err
			}
		}
		added = true
		return nil
	})
	return added, err
}

// waitingNotices are the notices of cur, a waiting slot, as Notify extends
// them: legacy (cur saved before notices, ending in a record), or with At
// past cur's text when it has none yet.
func waitingNotices(ctx context.Context, tx *sql.Tx, cur Slot) (SlotNotices, error) {
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT notices FROM agent_slots WHERE agent_id = ? AND sender = ?`,
		cur.AgentID, cur.Sender).Scan(&raw); err != nil {
		return SlotNotices{}, err
	}
	notes, err := readNotices(raw)
	if err != nil {
		return notes, err
	}
	if legacyNotices(raw, cur.RequestID) {
		notes.Legacy = cur.RequestID
	} else if len(notes.Keys) == 0 { // the records start after the waiting text and its line break
		notes.At = len(cur.Body) + 1
	}
	return notes, nil
}

// keepRecords is putSlot for a Send: the records waiting in cur, the
// sender's slot, stay after in's message (in.Body becomes the whole slot
// text), so a new message replaces only the sender's own text. A legacy
// slot's records are not kept (migration 9).
func keepRecords(ctx context.Context, tx *sql.Tx, in *SlotSend, cur Slot) (replaced bool, now string, err error) {
	var notes SlotNotices
	if cur.State == SlotWaiting {
		if notes, err = waitingNotices(ctx, tx, cur); err != nil {
			return false, "", err
		}
	}
	if notes.Legacy != "" || len(notes.Keys) == 0 {
		return putSlot(ctx, tx, *in, cur)
	}
	in.Body, notes.At = in.Body+"\n"+cur.Body[notes.At:], len(in.Body)+1
	if replaced, now, err = putSlot(ctx, tx, *in, cur); err != nil {
		return false, "", err
	}
	return replaced, now, saveNotices(ctx, tx, *in, notes, nil)
}

// saveNotices stores notes plus fresh's keys on in's slot. A legacy slot
// stays legacy, its notices NULL as putSlot's '{}' would otherwise drop: the
// reader rebuilds them all from the receipts, this batch's included.
func saveNotices(ctx context.Context, tx *sql.Tx, in SlotSend, notes SlotNotices, fresh []Notice) error {
	if notes.Legacy != "" {
		_, err := tx.ExecContext(ctx, `UPDATE agent_slots SET notices = NULL WHERE agent_id = ? AND sender = ?`,
			in.AgentID, in.Sender)
		return err
	}
	for _, n := range fresh {
		notes.Keys = append(notes.Keys, n.Key)
	}
	b, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_slots SET notices = ? WHERE agent_id = ? AND sender = ?`,
		string(b), in.AgentID, in.Sender)
	return err
}

// noticeReceipt stores the receipt of notice key, added to in's slot; the
// last added (in's RequestID) keeps the whole slot text, and one continuing
// a legacy slot keeps its notices NULL.
func noticeReceipt(ctx context.Context, tx *sql.Tx, in SlotSend, key, res, now string, legacy bool) error {
	body, notices := any(nil), any("{}")
	if key == in.RequestID {
		body = in.Body
	}
	if legacy {
		notices = nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_send_receipts (agent_id, request_id, sender,
		result_json, created_at, body, notices) VALUES (?,?,?,?,?,?,?)`, in.AgentID, key, in.Sender, res, now, body, notices)
	return err
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_send_receipts (agent_id, request_id, sender, result_json, created_at, notices)
		VALUES (?,?,?,?,?,'{}')`, r.AgentID, r.RequestID, r.Sender, r.ResultJSON, r.CreatedAt)
	return r, err
}

// ErrHistoryPurged means the agent's history was purged under R29.
var ErrHistoryPurged = errors.New("loomstore: agent history purged")

// reopen starts the agent's next attempt when in.Reopen is set, bumping its
// revision, and returns the new revision.
func reopen(ctx context.Context, tx *sql.Tx, in SlotSend) (rev int64, err error) {
	if !in.Reopen {
		return 0, nil
	}
	agentID := in.AgentID
	res, err := tx.ExecContext(ctx, `UPDATE agents SET state = 'active', attempt = attempt + 1, outcome = NULL,
		finished_at = NULL, history_purge_failed_at = NULL, updated_at = ?, revision = revision + 1,
		attempt_after_seq = (SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = agents.agent_id)
		WHERE agent_id = ? AND state = 'finished' AND deleted_at IS NULL
		AND history_purged_at IS NULL`, Stamp(time.Now()), agentID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		err = tx.QueryRowContext(ctx, `SELECT revision FROM agents WHERE agent_id = ?`, agentID).Scan(&rev)
		return rev, err
	}
	var purged sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT history_purged_at FROM agents WHERE agent_id = ?`, agentID).Scan(&purged); err != nil {
		return 0, err
	}
	if purged.Valid {
		return 0, ErrHistoryPurged
	}
	return 0, ErrStateChanged
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
// nativeKey. When that slot holds task_completed records (and was saved
// with its notices), every other waiting slot that holds only records goes
// with it under the same key, as one input (OR4c): the records follow its
// text in that order, and its receipt keeps the input's whole text and
// every record it carried, for the delivery. Any other slot waits for a
// later hand-over. It returns ErrNotFound when nothing waits.
func (s *Store) HandNext(ctx context.Context, agentID string, nativeKey func(Slot) string) (Slot, error) {
	var sl Slot
	err := s.tx(ctx, func(tx *sql.Tx) error {
		batch, notes, err := nextInput(ctx, tx, agentID)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return ErrNotFound
		}
		sl = batch[0]
		k := nativeKey(sl)
		sl.State, sl.NativeKey, sl.UpdatedAt = SlotHanded, &k, Stamp(time.Now())
		bodies := make([]string, 0, len(batch))
		for i, w := range batch {
			bodies = append(bodies, w.Body)
			if err := handSlot(ctx, tx, w, k, sl.UpdatedAt, i == 0); err != nil {
				return err
			}
		}
		if len(batch) == 1 {
			return nil
		}
		sl.Body = strings.Join(bodies, "\n")
		b, err := json.Marshal(notes)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE agent_send_receipts SET body = ?, notices = ? WHERE agent_id = ? AND request_id = ?`,
			sl.Body, string(b), agentID, sl.RequestID)
		return err
	})
	return sl, err
}

// nextInput is the agent's waiting slots that HandNext hands over as one
// input, in order, with the records they carry. The input is the next slot
// in line; when that slot holds records (saved with notices), the first
// waiting slot whose records follow its sender's own message leads instead,
// and every other slot that holds only records follows it.
func nextInput(ctx context.Context, tx *sql.Tx, agentID string) ([]Slot, SlotNotices, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+slotCols+`, notices FROM agent_slots WHERE agent_id = ? AND state = ?
		ORDER BY first DESC, queued_at, sender`, agentID, SlotWaiting)
	if err != nil {
		return nil, SlotNotices{}, err
	}
	defer func() { _ = rows.Close() }()
	type waiting struct {
		slot  Slot
		notes SlotNotices
		saved bool // saved with its notices (not a migration-9 legacy row)
	}
	var all []waiting
	for rows.Next() {
		var w waiting
		var raw sql.NullString
		if err := rows.Scan(append(w.slot.fields(), &raw)...); err != nil {
			return nil, SlotNotices{}, err
		}
		if w.notes, err = readNotices(raw); err != nil {
			return nil, SlotNotices{}, err
		}
		w.saved = raw.Valid
		all = append(all, w)
	}
	if err := rows.Err(); err != nil || len(all) == 0 {
		return nil, SlotNotices{}, err
	}
	records := func(w waiting) bool { return w.saved && len(w.notes.Keys) > 0 }
	if !records(all[0]) {
		return []Slot{all[0].slot}, all[0].notes, nil
	}
	head := all[0]
	for _, w := range all {
		if records(w) && w.notes.At > 0 { // a message with records leads
			head = w
			break
		}
	}
	batch, notes := []Slot{head.slot}, head.notes
	for _, w := range all {
		if w.slot.Sender != head.slot.Sender && records(w) && w.notes.At == 0 {
			batch, notes.Keys = append(batch, w.slot), append(notes.Keys, w.notes.Keys...)
		}
	}
	return batch, notes, nil
}

// handSlot marks w handed under native key k. The Send's receipt now
// reports the hand-over, so a later retry of it returns state handed
// (design v2 §4.9); the input's own receipt (input) also records the key
// and keeps the slot's notices, which a delivery reports.
func handSlot(ctx context.Context, tx *sql.Tx, w Slot, k, at string, input bool) error {
	if _, err := tx.ExecContext(ctx, `UPDATE agent_slots SET state = ?, native_key = ?, updated_at = ?
		WHERE agent_id = ? AND sender = ?`, SlotHanded, k, at, w.AgentID, w.Sender); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE agent_send_receipts SET native_key = CASE WHEN ? THEN ? ELSE native_key END,
		result_json = CASE WHEN json_valid(result_json) THEN json_set(result_json, '$.state', ?) ELSE result_json END,
		notices = CASE WHEN ? THEN (SELECT notices FROM agent_slots WHERE agent_id = ? AND sender = ?) ELSE notices END
		WHERE agent_id = ? AND request_id = ?`, input, k, SlotHanded, input, w.AgentID, w.Sender, w.AgentID, w.RequestID)
	return err
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
		UNION SELECT u.agent_id FROM agent_update_requests u JOIN agents a USING (agent_id)
		WHERE u.status = ? AND a.deleted_at IS NULL AND a.workspace_id = ?
		ORDER BY 1`, SlotWaiting, SlotHanded, workspaceID, workspaceID, RequestSwitching, workspaceID)
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
// With records (Withdraw), completion records waiting after the message are
// not the sender's and stay waiting; a slot holding only records then has
// nothing to withdraw. Without, the whole slot is cleared.
func (s *Store) ClearSlot(ctx context.Context, agentID, sender string, records bool) (string, error) {
	out := NothingWaiting
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var cur Slot
		err := tx.QueryRowContext(ctx, `SELECT `+slotCols+` FROM agent_slots WHERE agent_id = ? AND sender = ?`,
			agentID, sender).Scan(cur.fields()...)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		case cur.State == SlotHanded:
			out = AlreadyHanded
			return nil
		case cur.State != SlotWaiting:
			return nil
		}
		notes, err := waitingNotices(ctx, tx, cur)
		if err != nil {
			return err
		}
		if records && notes.Legacy == "" && len(notes.Keys) > 0 {
			if notes.At > 0 {
				out = Withdrawn
				return dropMessage(ctx, tx, cur, notes)
			}
			return nil
		}
		out = Withdrawn
		_, err = tx.ExecContext(ctx, `UPDATE agent_slots SET state = ?, first = 0, updated_at = ?
			WHERE agent_id = ? AND sender = ?`, SlotWithdrawn, Stamp(time.Now()), agentID, sender)
		return err
	})
	return out, err
}

// dropMessage withdraws the sender's own text from cur, a waiting slot whose
// records (notes) follow it, leaving the records waiting in its place: the
// last record names the slot again, its receipt keeping the slot's text.
func dropMessage(ctx context.Context, tx *sql.Tx, cur Slot, notes SlotNotices) error {
	body, key := cur.Body[notes.At:], notes.Keys[len(notes.Keys)-1]
	notes.At = 0
	b, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_slots SET request_id = ?, body = ?, notices = ?, updated_at = ?
		WHERE agent_id = ? AND sender = ?`, key, body, string(b), Stamp(time.Now()), cur.AgentID, cur.Sender); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_send_receipts SET body = ? WHERE agent_id = ? AND request_id = ?`,
		body, cur.AgentID, key)
	return err
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
func (s *Store) HandedText(ctx context.Context, agentID, nativeKey string) (text, sender string, ok bool, err error) {
	var body sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT body, sender FROM agent_send_receipts WHERE agent_id = ? AND native_key = ?
		ORDER BY created_at DESC LIMIT 1`, agentID, nativeKey).Scan(&body, &sender)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return body.String, sender, err == nil && body.Valid, err
}

// WaitingNotices returns the notices of each of agentID's waiting slots that
// has any, or is legacy, by sender.
func (s *Store) WaitingNotices(ctx context.Context, agentID string) (map[string]SlotNotices, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sender, notices, request_id FROM agent_slots WHERE agent_id = ? AND state = ?`,
		agentID, SlotWaiting)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]SlotNotices{}
	for rows.Next() {
		var sender, requestID string
		var raw sql.NullString
		if err := rows.Scan(&sender, &raw, &requestID); err != nil {
			return nil, err
		}
		n, err := readNotices(raw)
		if err != nil {
			return nil, err
		}
		if legacyNotices(raw, requestID) {
			n.Legacy = requestID
		}
		if len(n.Keys) > 0 || n.Legacy != "" {
			out[sender] = n
		}
	}
	return out, rows.Err()
}

// HandedNotices returns the notices of the message agentID was handed with
// input key nativeKey, as its slot had them at hand-over (none for a legacy
// receipt or a message with none).
func (s *Store) HandedNotices(ctx context.Context, agentID, nativeKey string) (SlotNotices, error) {
	var raw sql.NullString
	var requestID string
	err := s.db.QueryRowContext(ctx, `SELECT notices, request_id FROM agent_send_receipts WHERE agent_id = ? AND native_key = ?
		ORDER BY created_at DESC LIMIT 1`, agentID, nativeKey).Scan(&raw, &requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return SlotNotices{}, nil
	}
	if err != nil {
		return SlotNotices{}, err
	}
	n, err := readNotices(raw)
	if legacyNotices(raw, requestID) {
		n.Legacy = requestID
	}
	return n, err
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

// SenderReceipt is one of a sender's receipts on an agent: its request id,
// the slot body it left (NULL for a Send with no message, a record other
// than its batch's last, or a row older than receipt bodies), and its stamp.
type SenderReceipt struct {
	RequestID string
	Body      *string
	CreatedAt string
}

// SenderReceipts returns sender's receipts on agentID, oldest first; the
// records one Notify added share a stamp.
func (s *Store) SenderReceipts(ctx context.Context, agentID, sender string) ([]SenderReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, body, created_at FROM agent_send_receipts
		WHERE agent_id = ? AND sender = ? ORDER BY created_at, rowid`, agentID, sender)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SenderReceipt
	for rows.Next() {
		var r SenderReceipt
		var body sql.NullString
		if err := rows.Scan(&r.RequestID, &body, &r.CreatedAt); err != nil {
			return nil, err
		}
		if body.Valid {
			r.Body = &body.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
