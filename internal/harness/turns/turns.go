// Package turns translates low-level harness signals — emulated screen
// state changes and wrapper-level status events — into a small
// vocabulary of chat-oriented turn events: turn complete, tool call,
// blocked, errored.
//
// Adapters implement the per-harness logic. A generic fallback adapter
// (pkg/turns/generic) maps wrapper.Status to turn events without
// looking at the screen at all; per-harness adapters live in
// pkg/turns/harness/<name>/ and add screen-derived signals such as
// prompt-region detection and tool-call markers.
//
// Watcher composes a wrapper.Session, a screen.Screen, and an Adapter
// into a single <-chan Event stream.
package turns

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/transcript"
	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

// Kind is the categorical type of a turn event.
//
// The vocabulary is intentionally small. Adapters with richer signals
// should attach details via Event.Reason and Event.Snap rather than
// growing the kind set.
type Kind string

const (
	// TurnComplete means the assistant has finished its turn and the
	// caller may send the next user message. This is the primary "ready
	// for next message" signal a chat client cares about.
	TurnComplete Kind = "turn_complete"

	// ToolCall means the harness is invoking a tool (shell, file edit,
	// HTTP, …). Informational; turn is still in progress.
	ToolCall Kind = "tool_call"

	// Blocked means the harness reported a transient block — cost,
	// quota, rate limit, or a "retry later" hint. Callers should back
	// off and retry; the turn did not complete.
	Blocked Kind = "blocked"

	// Errored means the harness reported a terminal failure: non-zero
	// exit, signal termination, classifier saw a fatal error. The turn
	// did not complete and is unlikely to recover without intervention.
	Errored Kind = "errored"
)

// Event is one observation about the conversation flow.
type Event struct {
	// Kind categorizes the event.
	Kind Kind

	// At is when the event was observed. Watcher backfills this from
	// the originating wrapper.SessionEvent or time.Now() if the adapter
	// left it zero.
	At time.Time

	// Reason is a short human-readable description, surfacing whatever
	// the adapter or wrapper classifier wrote. Not stable for parsing.
	Reason string

	// Snap is the screen snapshot at the moment the event fired, if
	// the event originated from a screen change. nil for events that
	// came from wrapper status transitions only.
	Snap *screen.Snapshot

	// HTTPCode is the upstream API status code when the originating
	// wrapper event carried one (e.g. StatusAPIError with HTTPCode=529).
	// Zero for non-API-error events and for adapter-synthesized events
	// without an upstream code. The Watcher copies this from the
	// SessionEvent automatically; adapters do not need to populate it.
	HTTPCode int

	// RetryAfter is the wait duration the wrapper parsed from the
	// harness's error message. Zero when no hint was available.
	// Watcher-populated like HTTPCode.
	RetryAfter time.Duration
}

// Adapter is the per-harness contract that translates raw signals
// (screen state + wrapper status) into turn events.
//
// Implementations must be safe for concurrent calls to OnScreen and
// OnWrapperStatus — Watcher calls them from independent goroutines.
//
// Implementations should be stateless or guard internal state with a
// mutex; the Watcher does not serialize calls.
type Adapter interface {
	// Name identifies the adapter ("generic", "codex", "claude-code", …).
	Name() string

	// OnScreen is called after every successful screen.Write. Returns
	// any turn events the adapter wishes to emit. nil/empty slice means
	// "no events for this snapshot."
	OnScreen(snap screen.Snapshot) []Event

	// OnWrapperStatus is called on every wrapper.SessionEvent. Returns
	// any turn events the adapter wishes to emit.
	OnWrapperStatus(status wrapper.Status, reason string) []Event
}

// SessionIDExtractor is an optional capability adapters may implement
// to surface the harness's own session ID by scraping the rendered
// screen. The chat layer calls this opportunistically; once a non-empty
// ID is returned it is persisted and no longer queried.
type SessionIDExtractor interface {
	// ExtractSessionID returns the harness-assigned session UUID if it
	// appears in the snapshot. Returns ("", false) when no ID is yet
	// visible.
	ExtractSessionID(snap screen.Snapshot) (string, bool)
}

// TranscriptReader is an optional capability adapters may implement to
// provide access to the harness's persisted conversation log. The chat
// layer uses this to hydrate Conversation.History() once a harness
// session ID is known.
type TranscriptReader interface {
	// ReadTranscript locates and parses the harness's session log for
	// the given session ID + working directory and returns the ordered
	// turns. Different harnesses index transcripts differently; some
	// ignore workingDir.
	ReadTranscript(harnessSessionID, workingDir string) ([]transcript.Turn, error)
}
