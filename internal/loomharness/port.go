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
	// SetModel sets the model and its options from the next turn; opts is
	// the session's whole option selection, replacing what it had.
	SetModel(ctx context.Context, model string, opts []Option) error
	Move(ctx context.Context, dir string) error // at a turn boundary
	Unload(ctx context.Context) error           // free an idle session
	Close(ctx context.Context) error            // stop this runtime; native history is kept
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
	// Answer answers a question ask's first question. Answers, when set,
	// answers each question by its Question.ID instead: one value, or one
	// per chosen option of a MultiSelect question.
	Answer  string
	Answers map[string][]string
}

// Question is one question of a question ask, in T3 Code's shape: a short
// Header, the Question text and the Options to choose from; with none, or
// besides them, the answer may be free text.
type Question struct {
	ID, Header, Question string
	Options              []Choice
	MultiSelect          bool
}

// Choice is one option a Question offers.
type Choice struct{ Label, Description string }

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

// Model is one model the harness offers, with its capabilities in T3 Code's
// generic shape: each option the model takes is a descriptor, and a selection
// is a list of {ID, Value}.
type Model struct {
	ID           string
	Name         string
	Provider     string // the provider id, e.g. "openai"; the harness name when it has one provider
	ProviderName string
	ContextLimit int64    // tokens; 0 when unknown
	Input        []string // text | image | pdf
	Default      bool     // the model a session gets with none chosen
	Options      []OptionDescriptor
	Custom       bool // a workspace custom model id the harness does not list (MCS3)
}

// Option types.
const (
	OptionSelect  = "select"
	OptionBoolean = "boolean"
)

// OptionEffort is the reasoning-effort option every harness names the same way.
const OptionEffort = "effort"

// OptionDescriptor is one option a model takes. A select lists its Choices;
// a boolean takes "true" or "false". Current is the value used when none is set.
type OptionDescriptor struct {
	ID, Label, Description string
	Type                   string // select | boolean
	Choices                []OptionChoice
	Current                string
}

// OptionChoice is one value of a select option.
type OptionChoice struct {
	ID, Label, Description string
	Default                bool
}

// Option is one chosen option value; a boolean's Value is "true" or "false".
type Option struct {
	ID    string
	Value string
}

// OptionValue returns the value of option id in opts, or "".
func OptionValue(opts []Option, id string) string {
	for _, o := range opts {
		if o.ID == id {
			return o.Value
		}
	}
	return ""
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
	Text       string // for ask.opened: what it asks about (the command, file or diff, or the question)
	Sender     string // the Loom slot sender of a message.delivered; loomagent sets it
	StopReason string // completed | cancelled | declined (OpenCode, a rejected permission) | failed, for turn.completed
	Error      string // for a failed turn.completed: the harness's reason, when it gives one
	// Failure, for a failed turn.completed, is the failure's class, when the
	// harness says why the turn failed; nil when it does not.
	Failure *Failure
	Usage   Usage // for usage: this step's own counts, never a running total
	Tool    *Tool // for a tool item's item.started and item.completed: what the chat shows
	// Questions, for a question's ask.opened, are what it asks when the
	// harness says; Text is then the first question.
	Questions []Question
}

// Failure is a failed turn's terminal failure, the same for every harness.
// Retryable: the same input may succeed later unchanged (a usage limit after
// its window, an overloaded or dropped provider), never after an auth or
// request error.
//
// No harness reports when a usage limit resets: Claude's rate_limit_event
// (rate_limit_info.resetsAt) and codex's account/rateLimits/updated
// (rateLimits.primary|secondary.resetsAt) are not mapped until a recorded
// native frame shows a non-null reset on a rejected limit, and OpenCode has
// no reset field. A reset time, when one is mapped, is its own event.
type Failure struct {
	Class     string `json:"class"` // FailureUsageLimit | FailureAuth | FailureProvider
	Retryable bool   `json:"retryable,omitempty"`
}

// Failure classes.
const (
	FailureUsageLimit = "usage_limit"    // a plan, quota or rate limit
	FailureAuth       = "auth"           // the credential was refused
	FailureProvider   = "provider_error" // any other failure the harness names
)

// Tool is a tool call as the chat shows it, the same for every harness: the
// harness's tool name, its input as text (JSON when the input is structured)
// and, once it completed, its output text and whether it failed. An
// item.started carries what is known when the call starts.
type Tool struct {
	Name   string `json:"name,omitempty"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	Failed bool   `json:"failed,omitempty"`
}

// Usage is one step's token counts, and its cost where the harness reports one.
// Input excludes cached input; Output includes reasoning. A harness that
// reports only the session's running cost (Claude) sets CostTotalUSD instead
// of CostUSD; loomagent saves its rise since the session's last saved total.
type Usage struct {
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	CostUSD, CostTotalUSD                                        float64
}

// Errors adapters return. Wrap them with fmt.Errorf("...: %w", err).
var (
	ErrUnavailable     = errors.New("loomharness: harness unavailable")
	ErrBusy            = errors.New("loomharness: session busy")
	ErrSessionNotFound = errors.New("loomharness: session not found")
	// ErrBadRequest: the harness refused the request as invalid; repeating
	// it fails the same way.
	ErrBadRequest = errors.New("loomharness: bad request")
	// ErrQuarantined: the session's policy is unconfirmed, so it refuses
	// Prompt and Reply until Open or Resume confirms one.
	ErrQuarantined = errors.New("loomharness: session quarantined: its policy is unconfirmed")
	// ErrNotSent: the call failed before any of its request reached the
	// harness (no connection, or refused before writing), so it had no
	// effect there and may be made again.
	ErrNotSent = errors.New("loomharness: request not sent")
)
