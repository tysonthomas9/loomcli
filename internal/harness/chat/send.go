package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

// Send transmits a user message to the harness and records two turns:
// the user turn (immediately complete) and a placeholder assistant
// turn (TurnStatePending). The returned turnID identifies the
// assistant turn — observe Events() to learn when it transitions to
// Complete or Errored.
//
// Preconditions:
//   - The caller must currently hold the control token from
//     AcquireControl. Send returns ErrNoControl otherwise.
//   - No prior assistant turn may be in flight. Send returns
//     ErrTurnInFlight otherwise.
//
// The text is sent verbatim followed by a single carriage return; both
// Codex and Claude Code accept this as the "submit" keystroke. Senders
// that need richer input (multi-line, control characters) should use
// Conversation.Wrapper().WriteStdin directly after acquiring control.
//
//nolint:funlen // Linear send protocol (claim → write → record → return turn ID); extraction would obscure the back-and-forth. Mirrors upstream harness-wrapper.
func (c *Conversation) Send(ctx context.Context, text string) (turnID string, err error) {
	select {
	case <-c.closed:
		return "", ErrClosed
	default:
	}

	if !c.queue.Held() {
		return "", ErrNoControl
	}

	c.mu.Lock()
	if c.currentTurn != nil {
		c.mu.Unlock()
		return "", ErrTurnInFlight
	}
	c.mu.Unlock()

	now := time.Now()

	userTurn := Turn{
		ID:          newID(),
		SessionID:   c.session.ID,
		Role:        RoleUser,
		State:       TurnStateComplete,
		Text:        text,
		StartedAt:   now,
		CompletedAt: now,
	}
	if err := c.store.AppendTurn(ctx, &userTurn); err != nil {
		return "", fmt.Errorf("chat: append user turn: %w", err)
	}
	c.emit(TurnEvent{Turn: userTurn})

	assistantTurn := Turn{
		ID:        newID(),
		SessionID: c.session.ID,
		Role:      RoleAssistant,
		State:     TurnStatePending,
		StartedAt: now,
	}
	if err := c.store.AppendTurn(ctx, &assistantTurn); err != nil {
		return "", fmt.Errorf("chat: append assistant turn: %w", err)
	}

	c.mu.Lock()
	turnCopy := assistantTurn
	c.currentTurn = &turnCopy
	c.mu.Unlock()

	if _, err := c.sess.WriteStdin([]byte(text + "\r")); err != nil {
		// Roll back the in-flight pointer and mark the turn errored.
		c.mu.Lock()
		c.currentTurn = nil
		c.mu.Unlock()
		assistantTurn.State = TurnStateErrored
		assistantTurn.Reason = "WriteStdin: " + err.Error()
		assistantTurn.CompletedAt = time.Now()
		if uerr := c.store.UpdateTurn(ctx, &assistantTurn); uerr != nil {
			return "", fmt.Errorf("chat: write stdin + update turn: write=%v update=%w", err, uerr)
		}
		c.emit(TurnEvent{Turn: assistantTurn, Err: err})
		return assistantTurn.ID, fmt.Errorf("chat: write stdin: %w", err)
	}

	c.emit(TurnEvent{Turn: assistantTurn})
	return assistantTurn.ID, nil
}

// Wrapper returns the underlying wrapper.Session for callers that need
// to reach past the chat API — e.g. to Resize, AttachOutput, or read
// the raw RecentOutput buffer. Use with care: writing directly to
// stdin bypasses the control-token guard.
func (c *Conversation) Wrapper() *wrapper.Session { return c.sess }
