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
)

// AskClaim is the one claim on an agent's ask, saved before its Reply.
type AskClaim struct {
	AgentID, AskID, RequestID, PayloadHash, State, CreatedAt string
}

const claimCols = `agent_id, ask_id, request_id, payload_hash, state, created_at`

func (c *AskClaim) fields() []any {
	return []any{&c.AgentID, &c.AskID, &c.RequestID, &c.PayloadHash, &c.State, &c.CreatedAt}
}

// ClaimAsk saves c, pending, unless the ask already has a claim, and returns
// the claim that holds the ask: c, or the earlier one.
func (s *Store) ClaimAsk(ctx context.Context, c AskClaim) (AskClaim, error) {
	c.State, c.CreatedAt = ClaimPending, Stamp(time.Now())
	if _, err := s.db.ExecContext(ctx, `INSERT INTO agent_ask_claims (`+claimCols+`) VALUES (?,?,?,?,?,?)
		ON CONFLICT (agent_id, ask_id) DO NOTHING`, c.AgentID, c.AskID, c.RequestID, c.PayloadHash, c.State, c.CreatedAt); err != nil {
		return AskClaim{}, err
	}
	return s.AskClaim(ctx, c.AgentID, c.AskID)
}

// AskClaim returns the claim on agentID's ask askID, or ErrNotFound.
func (s *Store) AskClaim(ctx context.Context, agentID, askID string) (AskClaim, error) {
	var c AskClaim
	err := s.db.QueryRowContext(ctx, `SELECT `+claimCols+` FROM agent_ask_claims WHERE agent_id = ? AND ask_id = ?`,
		agentID, askID).Scan(c.fields()...)
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

// SettleAskClaim records the outcome of agentID's pending claim on askID:
// replied or unknown, or "" to release it (its Reply was never sent), so the
// ask can be answered again.
func (s *Store) SettleAskClaim(ctx context.Context, agentID, askID, state string) error {
	q := `UPDATE agent_ask_claims SET state = ? WHERE agent_id = ? AND ask_id = ? AND state = ?`
	args := []any{state, agentID, askID, ClaimPending}
	if state == "" {
		q, args = `DELETE FROM agent_ask_claims WHERE agent_id = ? AND ask_id = ? AND state = ?`, args[1:]
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}
