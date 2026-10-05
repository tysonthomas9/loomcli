package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Ask claim states.
const (
	ClaimPending = "claimed"
	ClaimReplied = "replied"
	ClaimUnknown = "unknown"
	// ClaimReleased: the Reply was never sent; the ask can be claimed again.
	ClaimReleased = "released"
)

// AskClaim is the one claim on an agent's ask (its ID on its turn), saved
// before its Reply.
type AskClaim struct {
	AgentID, AskID, TurnID, RequestID, PayloadHash, State, CreatedAt string
}

const claimCols = `agent_id, ask_id, turn_id, request_id, payload_hash, state, created_at`

func (c *AskClaim) fields() []any {
	return []any{&c.AgentID, &c.AskID, &c.TurnID, &c.RequestID, &c.PayloadHash, &c.State, &c.CreatedAt}
}

// ClaimAsk saves c, pending, unless the ask already has a claim not
// released (c's own released claim is taken again). It returns the claim
// that holds the ask, and won when that is c, saved by this call.
func (s *Store) ClaimAsk(ctx context.Context, c AskClaim) (held AskClaim, won bool, err error) {
	c.State, c.CreatedAt = ClaimPending, Stamp(time.Now())
	err = s.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT `+claimCols+` FROM agent_ask_claims WHERE agent_id = ? AND ask_id = ? AND turn_id = ?
			AND state != ?`, c.AgentID, c.AskID, c.TurnID, ClaimReleased).Scan(held.fields()...)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		held, won = c, true
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_ask_claims (`+claimCols+`) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT (agent_id, ask_id, turn_id, request_id) DO UPDATE SET payload_hash = excluded.payload_hash,
			state = excluded.state, created_at = excluded.created_at`,
			c.AgentID, c.AskID, c.TurnID, c.RequestID, c.PayloadHash, c.State, c.CreatedAt)
		return err
	})
	return held, won && err == nil, err
}

// GetAskClaim reads c's claim again, by its ask, turn and request.
func (s *Store) GetAskClaim(ctx context.Context, c AskClaim) (AskClaim, error) {
	err := s.db.QueryRowContext(ctx, `SELECT `+claimCols+` FROM agent_ask_claims WHERE agent_id = ? AND ask_id = ? AND turn_id = ?
		AND request_id = ?`, c.AgentID, c.AskID, c.TurnID, c.RequestID).Scan(c.fields()...)
	return c, err
}

// AskClaim returns the latest claim not released on agentID's ask ID
// askID, or with a requestID the latest that request made, released or
// not; else ErrNotFound.
func (s *Store) AskClaim(ctx context.Context, agentID, askID string, requestID ...string) (AskClaim, error) {
	var c AskClaim
	q, args := ` AND state != ?`, []any{agentID, askID, ClaimReleased}
	if len(requestID) > 0 {
		q, args = ` AND request_id = ?`, []any{agentID, askID, requestID[0]}
	}
	err := s.db.QueryRowContext(ctx, `SELECT `+claimCols+` FROM agent_ask_claims WHERE agent_id = ? AND ask_id = ?`+q+`
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, args...).Scan(c.fields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// PendingAskClaims lists agentID's claims whose outcome is not yet known.
func (s *Store) PendingAskClaims(ctx context.Context, agentID string) ([]AskClaim, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+claimCols+` FROM agent_ask_claims WHERE agent_id = ? AND state = ?
		ORDER BY created_at, ask_id`, agentID, ClaimPending)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AskClaim
	for rows.Next() {
		var c AskClaim
		if err := rows.Scan(c.fields()...); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SettleAskClaim records the outcome of c, a pending claim: replied,
// unknown, or released (its Reply was never sent, so the ask can be
// answered again).
func (s *Store) SettleAskClaim(ctx context.Context, c AskClaim, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_ask_claims SET state = ? WHERE agent_id = ? AND ask_id = ? AND turn_id = ?
		AND request_id = ? AND state = ?`, state, c.AgentID, c.AskID, c.TurnID, c.RequestID, ClaimPending)
	return err
}
