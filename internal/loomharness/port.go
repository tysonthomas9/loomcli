// Package loomharness defines the port every harness adapter (OpenCode,
// codex, Claude) implements. The port stores nothing: waiting messages, open
// asks and the registry belong to loomagent and loomstore. It knows only
// provider-native session refs; the stable AgentID lives above it.
package loomharness

import (
	"context"
	"errors"
	"time"
)

// Harness is one harness runtime. OpenCode and codex run one shared server;
// Claude runs one process per session. The port does not assume either.
type Harness interface {
	Name() string // "opencode" | "codex" | "claude"
	Models(ctx context.Context) ([]Model, error)
	Health(ctx context.Context) (Health, error)
	// Open is idempotent by spec.Key. On failure it returns the zero ref if it
	// left nothing behind, or the ref of a native session it created and
	// could not remove; the caller records that ref as owned and
	// purge-pending, then Purges it (retried after a restart, R29).
	Open(ctx context.Context, spec OpenSpec) (NativeRef, error)
	Session(ref NativeRef) Session
	Feed(ctx context.Context) (Feed, error) // live events for all sessions; reconnects itself and emits feed.gap, ends only on ctx or Close
	// Purge deletes exactly the recorded refs it is given, never more, and
	// never resolves a root itself.
	Purge(ctx context.Context, owned []NativeRef) error
	Restart(ctx context.Context) error // a no-op for Claude
}

// Session is one native session, thread or Claude session.
type Session interface {
	// Resume recovers the same native session before hand-over. It first
	// installs rules as the session's whole permission policy, replacing what
	// it had, before any resumed turn or tool can run; if it cannot, it fails
	// and nothing runs.
	Resume(ctx context.Context, l Launch, rules []PermissionRule) (NativeRef, error)
	Prompt(ctx context.Context, in Input) error // only when idle; in.Key is the native key
	Interrupt(ctx context.Context) (interrupted bool, err error)
	Reply(ctx context.Context, askID string, r Reply) error
	HasInput(ctx context.Context, key string) (Landed, error)
	Messages(ctx context.Context, after string, limit int) (MessagePage, error) // catch-up read
	Status(ctx context.Context) (Status, error)
	SetModel(ctx context.Context, model string) error // from the next turn
	Move(ctx context.Context, dir string) error       // at a turn boundary
	Unload(ctx context.Context) error                 // free an idle session
	Close(ctx context.Context) error                  // stop this runtime; native history is kept
}

// NativeRef is a provider-native session or thread ID plus the root it lives
// under. Loom records every NativeRef an agent owns (loomstore ownership row:
// native_root, native_id) and passes those records back to Purge.
type NativeRef struct {
	Root     string
	NativeID string
}

// Launch is the opaque per-agent launch input (profile root and secret env).
// The port never interprets it; adapters return the Root they used.
type Launch struct {
	Root string
	Env  map[string]string
}

// OpenSpec describes a session to open.
type OpenSpec struct {
	Key      string // stable, derived from the AgentID
	Launch   Launch
	Preset   PresetConfig
	Dir      string // the agent's worktree
	Model    string
	Rules    []PermissionRule
	Metadata map[string]string // carries the AgentID for tracing
}

// PresetConfig is the preset as the adapter renders it.
type PresetConfig struct {
	Name    string
	Persona string
	Tools   []string
}

// PermissionRule is one {Action, Resource, Effect} rule.
type PermissionRule struct {
	Action   string
	Resource string
	Effect   string // allow | deny | ask
}

// Input is one prompt. Key is caller-chosen and checked by HasInput after a crash.
type Input struct {
	Key  string
	Text string
}

// Reply answers an ask.
type Reply struct {
	Allow bool
	// Always, with Allow, grants for the rest of this native session only: on
	// every harness the grant is gone after a Resume or a harness switch, and
	// it is never broader than the session (no project-wide or persistent
	// grant, such as OpenCode's "always"). An adapter that cannot honor it
	// fails Reply with an explicit error, never narrowing it; the ask stays open.
	Always bool
	Answer string
}

// Landed says whether an input reached the harness.
type Landed string

const (
	LandedFound    Landed = "found"
	LandedNotFound Landed = "not_found"
	LandedUnknown  Landed = "unknown"
)

// Status is the session's run state. TurnID is empty when the harness has no
// turn id outside its live feed (OpenCode); its Interrupt needs none.
type Status struct {
	Running           bool
	TurnID            string
	LastTurnInterrupt bool // the newest finished turn was interrupted
}

// Model is one model the harness offers.
type Model struct {
	ID   string
	Name string
}

// Health reports the installed version and the version check result.
type Health struct {
	OK      bool
	Version VersionCheck
	Warning string
}

// MessagePage is one page of a catch-up read.
type MessagePage struct {
	Events []Event
	Next   string // empty at the end
}

// Feed is the live event stream for all of a harness's sessions.
type Feed interface {
	Events() <-chan Event
	Close() error
}

// EventType is a harness-level event, mapped to Loom events by loomagent.
type EventType string

const (
	EventMessageDelivered EventType = "message.delivered"
	EventTurnStarted      EventType = "turn.started"
	EventDelta            EventType = "delta"
	EventItemStarted      EventType = "item.started"
	EventItemCompleted    EventType = "item.completed"
	EventUsage            EventType = "usage"
	EventTurnCompleted    EventType = "turn.completed"
	EventAskOpened        EventType = "ask.opened"
	EventAskResolved      EventType = "ask.resolved"
	EventAskLost          EventType = "ask.lost"
	EventTurnResumed      EventType = "turn.resumed"
	EventSubagentStarted  EventType = "harness.subagent.started"
	EventFeedGap          EventType = "feed.gap"
)

// Event is one harness event. ItemID is stable across the live feed and a
// catch-up read; Seq is the native sequence number when there is one.
type Event struct {
	Type       EventType
	Session    NativeRef
	TurnID     string
	ItemID     string
	ItemKind   string // message | reasoning | tool; for ask.opened: approval (or "") | question
	Seq        int64
	Time       time.Time
	InputKey   string // the input's key, for message.delivered and the turn.started it began
	AskID      string
	Text       string
	StopReason string // completed | cancelled | failed, for turn.completed
}

// Errors adapters return. Wrap them with fmt.Errorf("...: %w", err).
var (
	ErrUnavailable     = errors.New("loomharness: harness unavailable")
	ErrBusy            = errors.New("loomharness: session busy")
	ErrSessionNotFound = errors.New("loomharness: session not found")
	// ErrQuarantined: the session's policy is unconfirmed, so it refuses
	// Prompt and Reply until Open or Resume confirms one.
	ErrQuarantined = errors.New("loomharness: session quarantined: its policy is unconfirmed")
)
