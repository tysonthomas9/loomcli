package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Update request kinds and statuses.
const (
	RequestUpdate    = "update"
	RequestSwitch    = "switch"
	RequestSwitching = "switching" // saved before the switch stops the turn
	RequestDone      = "done"
)

// UpdateRecord is one Update or harness switch request an agent applied:
// its payload and, once done, its result.
type UpdateRecord struct {
	AgentID, RequestID, Kind, PayloadHash, Status, Payload string
	FromHarness, ToHarness, OpenKey                        string
	TargetSpecVersion                                      int64
	Result, CreatedAt                                      string
}

const updateCols = `agent_id, request_id, kind, payload_hash, status, payload, from_harness, to_harness, open_key,
	target_spec_version, result, created_at`

func (r *UpdateRecord) fields() []any {
	return []any{&r.AgentID, &r.RequestID, &r.Kind, &r.PayloadHash, &r.Status, &r.Payload, &r.FromHarness,
		&r.ToHarness, &r.OpenKey, &r.TargetSpecVersion, &r.Result, &r.CreatedAt}
}

// UpdateRecord returns agentID's request requestID, else ErrNotFound.
func (s *Store) UpdateRecord(ctx context.Context, agentID, requestID string) (UpdateRecord, error) {
	return s.updateRecord(ctx, `request_id = ?`, agentID, requestID)
}

// PendingSwitch returns agentID's switching request, else ErrNotFound.
func (s *Store) PendingSwitch(ctx context.Context, agentID string) (UpdateRecord, error) {
	return s.updateRecord(ctx, `status = ?`, agentID, RequestSwitching)
}

func (s *Store) updateRecord(ctx context.Context, where string, agentID, arg string) (UpdateRecord, error) {
	var r UpdateRecord
	err := s.db.QueryRowContext(ctx, `SELECT `+updateCols+` FROM agent_update_requests WHERE agent_id = ? AND `+where,
		agentID, arg).Scan(r.fields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// SaveUpdateRecord saves r, replacing the row of its request.
func (s *Store) SaveUpdateRecord(ctx context.Context, r UpdateRecord) error {
	return saveUpdateRecord(ctx, s.db, r)
}

// FailSwitch saves e, as AppendEvent does, and drops e's agent's request
// requestID if it is switching, in one transaction. It returns the saved e.
func (s *Store) FailSwitch(ctx context.Context, e Event, requestID string) (got Event, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if got, err = appendEvent(ctx, tx, e); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM agent_update_requests WHERE agent_id = ? AND request_id = ? AND status = ?`,
			e.AgentID, requestID, RequestSwitching)
		return err
	})
	return got, err
}

func saveUpdateRecord(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, r UpdateRecord) error {
	_, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO agent_update_requests (`+updateCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.AgentID, r.RequestID, r.Kind, r.PayloadHash, r.Status, r.Payload, r.FromHarness, r.ToHarness, r.OpenKey,
		r.TargetSpecVersion, r.Result, Stamp(time.Now()))
	return err
}
