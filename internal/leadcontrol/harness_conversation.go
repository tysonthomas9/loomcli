package leadcontrol

import (
	"context"
	"errors"
	"io"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

// harnessConversation is the slice of chat.Conversation (plus its underlying
// wrapper.Session) that lead delivery and the lead runtime need. It exists as
// an interface so tests can inject fakes, mirroring dialCodexAppServerClient.
type harnessConversation interface {
	AcquireControl(ctx context.Context) (release func(), err error)
	InputPending(ctx context.Context) (bool, error)
	Send(ctx context.Context, text string) (turnID string, err error)
	WriteStdin(p []byte) (int, error)
	AttachOutput(w io.Writer) func()
	Resize(cols, rows uint16) error
	Snapshot() wrapper.Snapshot
	PID() int
	ChatSessionID() string
	HarnessSessionID() string
	Events() <-chan chat.ConversationEvent
	Wait() (wrapper.Result, error)
	Close(ctx context.Context) error
}

// openHarnessConversation is the production factory; tests replace it.
var openHarnessConversation = func(ctx context.Context, opts chat.Options) (harnessConversation, error) {
	conv, err := chat.Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &chatHarnessConversation{conv: conv, store: opts.Store}, nil
}

type chatHarnessConversation struct {
	conv *chat.Conversation
	// store is retained because Conversation does not expose the harness
	// session ID (claude --resume UUID) directly; it is backfilled onto the
	// chat.Session record by the adapter when extracted.
	store chat.Store
}

func (c *chatHarnessConversation) AcquireControl(ctx context.Context) (func(), error) {
	return c.conv.AcquireControl(ctx)
}

// harnessInputStateProbeID cannot collide with the supported adapters' input
// IDs (fixed-width printable hashes). Conversation.Answer checks the request ID
// before translating an answer into keystrokes, so this is a side-effect-free,
// authoritative pending-input probe while the caller holds the control token.
const harnessInputStateProbeID = "\x00loom-input-state-probe\x00"

func (c *chatHarnessConversation) InputPending(ctx context.Context) (bool, error) {
	return inputPendingFromProbeError(c.conv.Answer(ctx, harnessInputStateProbeID, chat.InputAnswer{}))
}

func inputPendingFromProbeError(err error) (bool, error) {
	switch {
	case errors.Is(err, chat.ErrNoInputPending):
		return false, nil
	case errors.Is(err, chat.ErrStaleInputRequest):
		return true, nil
	case err != nil:
		return false, err
	default:
		// Reaching this would mean an adapter emitted the reserved probe ID and
		// accepted an empty answer. Fail closed at the Loom boundary.
		return true, nil
	}
}

func (c *chatHarnessConversation) Send(ctx context.Context, text string) (string, error) {
	return c.conv.Send(ctx, text)
}

func (c *chatHarnessConversation) WriteStdin(p []byte) (int, error) {
	return c.conv.Wrapper().WriteStdin(p)
}

func (c *chatHarnessConversation) AttachOutput(w io.Writer) func() {
	return c.conv.Wrapper().AttachOutput(w)
}

func (c *chatHarnessConversation) Resize(cols, rows uint16) error {
	return c.conv.Resize(cols, rows)
}

func (c *chatHarnessConversation) Snapshot() wrapper.Snapshot {
	return c.conv.Wrapper().Snapshot()
}

func (c *chatHarnessConversation) PID() int { return c.conv.Wrapper().PID() }

func (c *chatHarnessConversation) ChatSessionID() string { return c.conv.SessionID() }

func (c *chatHarnessConversation) HarnessSessionID() string {
	if c.store == nil {
		return ""
	}
	sess, err := c.store.GetSession(context.Background(), c.conv.SessionID())
	if err != nil {
		return ""
	}
	return sess.HarnessSessionID
}

func (c *chatHarnessConversation) Events() <-chan chat.ConversationEvent { return c.conv.Events() }

func (c *chatHarnessConversation) Wait() (wrapper.Result, error) { return c.conv.Wrapper().Wait() }

func (c *chatHarnessConversation) Close(ctx context.Context) error { return c.conv.Close(ctx) }
