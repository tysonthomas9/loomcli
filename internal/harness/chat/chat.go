// Package chat is the Go-level chat-conversation API on top of
// harness-wrapper. A Conversation owns one PTY-supervised harness
// process (Codex, Claude Code, …) and exposes a small interface:
// acquire exclusive control, send a user message, observe turn-level
// state transitions.
//
// The package is the substrate that transport layers (HTTP, gRPC, …)
// import. Transport concerns — framing, streaming protocol, auth — are
// not part of this package and live in separate cmd/ binaries.
//
// Lifecycle:
//
//	conv, err := chat.Open(ctx, chat.Options{
//	    Harness:    "codex",
//	    BinaryPath: "/usr/local/bin/codex",
//	})
//	defer conv.Close(context.Background())
//
//	release, err := conv.AcquireControl(ctx)
//	defer release()
//
//	turnID, err := conv.Send(ctx, "hello")
//	for ev := range conv.Events() {
//	    if ev.Turn.ID == turnID && ev.Turn.State == chat.TurnStateComplete {
//	        break
//	    }
//	}
//
// Concurrency: all Conversation methods are safe for concurrent use.
// Send specifically requires that the caller's goroutine has previously
// acquired control via AcquireControl; otherwise it returns
// ErrNoControl.
package chat

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// Role identifies who produced a turn.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// TurnState is the lifecycle stage of a Turn.
type TurnState string

const (
	// TurnStatePending means the turn has been recorded but no output
	// has streamed yet. Applies to assistant turns from the moment
	// Send returns until the first byte is observed.
	TurnStatePending TurnState = "pending"

	// TurnStateStreaming means output is actively arriving. v1 does not
	// surface per-delta events, so most assistant turns transition
	// directly Pending → Complete.
	TurnStateStreaming TurnState = "streaming"

	// TurnStateComplete means the turn finished cleanly — the adapter
	// observed a turn-complete signal (Codex token footer, Claude Code
	// thinking summary, or wrapper waiting_for_input).
	TurnStateComplete TurnState = "complete"

	// TurnStateErrored means the turn ended in failure: harness exited,
	// the user interrupted, or the adapter reported an unrecoverable
	// error. Reason carries the detail.
	TurnStateErrored TurnState = "errored"
)

// Turn is one message in the conversation.
type Turn struct {
	ID          string
	SessionID   string
	Role        Role
	State       TurnState
	Text        string // populated for user turns at send time, and for assistant turns from the screen extract once Complete
	Reason      string // non-empty for Errored turns; mirrors adapter event Reason
	StartedAt   time.Time
	CompletedAt time.Time

	// HTTPCode is the upstream API status code carried with a Blocked
	// transition when the wrapper recognized an api_error event
	// (claudecode "API Error: 529", Gemini "(Status: 429)", Codex
	// "exceeded retry limit, last status: 503"). Zero for non-api
	// blocks and for transport-level errors with no numeric code.
	HTTPCode int

	// RetryAfter is the wait duration parsed from the harness's error
	// message (e.g. "Retry after 30 seconds"). Zero when no hint was
	// parseable. Consumers can read this to schedule their retry.
	RetryAfter time.Duration
}

// TurnEvent is a state transition the caller can observe on
// Conversation.Events().
type TurnEvent struct {
	Turn Turn
	// Err is non-nil if the event represents an out-of-band error, e.g.
	// Store failures. It is independent of Turn.State == TurnStateErrored
	// (which represents harness-side failures).
	Err error
}

// Session is the chat-level session record. Distinct from
// wrapper.Session: this is the persistence/metadata view, owned by Store.
type Session struct {
	ID         string
	Harness    string
	WorkingDir string
	CreatedAt  time.Time
	// HarnessSessionID is the ID the underlying harness assigned to its
	// own session (Codex's resume UUID, Claude Code's session UUID).
	// Empty until detected; v1 leaves this empty — extraction is
	// scheduled for a follow-up.
	HarnessSessionID string
}

// Sentinel errors.
var (
	// ErrInvalidOptions is returned by Open when Options is incomplete
	// or inconsistent.
	ErrInvalidOptions = errors.New("chat: invalid options")

	// ErrUnknownHarness is returned by Open when Options.Harness names
	// no registered adapter.
	ErrUnknownHarness = errors.New("chat: unknown harness")

	// ErrNoControl is returned by Send when no caller has acquired
	// control. Acquire via AcquireControl first.
	ErrNoControl = errors.New("chat: control token not held")

	// ErrTurnInFlight is returned by Send when a previous assistant
	// turn is still Pending or Streaming. Wait for it to complete (or
	// error) before sending the next message.
	ErrTurnInFlight = errors.New("chat: previous turn still in flight")

	// ErrClosed is returned by methods called after Close.
	ErrClosed = errors.New("chat: conversation closed")
)

// newID returns a fresh 16-byte hex ID. Used for chat-level Session
// and Turn IDs.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
