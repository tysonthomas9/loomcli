package terminal

import (
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

type LaunchSpec = tabmeta.LaunchSpec

// PTYSource is the backend-agnostic contract the web terminal handler uses
// to talk to a terminal backend. Two implementations exist / will exist:
//
//   - *PTYManager — local, in-process PTY (ephemeral agents, same-pod shells).
//   - *agentd.Client (in a sibling package / repo) — gRPC to loom-agentd
//     running inside a persistent-agent Firecracker microVM.
//
// Keeping this interface narrow means `handlers/terminal/ws.go` never grows
// knowledge of how the backend is realized — it just Attaches, Detaches, and
// Kills sessions.
type PTYSource interface {
	// AttachSession opens or re-opens a session. reattached is true when an
	// existing session was joined (initial state contains retained output).
	AttachSession(key SessionKey, cols, rows uint16, launch *tabmeta.LaunchSpec) (att Attachment, reattached bool, err error)

	// Detach releases the attachment identified by connID for the given
	// session. Does not kill the session — a grace period begins.
	Detach(key SessionKey, connID string)

	// Kill immediately terminates the session. Idempotent.
	Kill(key SessionKey) error

	// HasSession reports whether a live (possibly detached) session exists for
	// key. Used by callers that need to distinguish "tab metadata is stale
	// from a prior server process" from "session is still running and the
	// client is reconnecting".
	HasSession(key SessionKey) bool

	// SessionClosed reports whether a session with this key existed in the
	// current process and has since exited or been killed. This lets callers
	// distinguish "fresh metadata that may spawn on first attach" from
	// "metadata for a completed command tab that must not auto-respawn".
	SessionClosed(key SessionKey) bool

	// AttachmentCount reports the number of concurrent clients currently
	// attached to the session identified by key. Returns 0 for unknown
	// sessions and for sessions that exist but have no live WebSockets
	// (within the grace window). Used to surface "N viewers" on the tab
	// DTO so the UI can warn before destructive tab-close actions.
	AttachmentCount(key SessionKey) int

	// SessionCount returns the number of live sessions, including detached
	// ones still within the grace/idle windows. For per-workspace
	// implementations this is the sum across workspaces and is intended for
	// diagnostics only — gate decisions should use SessionCountFor.
	SessionCount() int

	// SessionCountFor returns the live-session count scoped to wsID. This
	// is the value a per-workspace cap should be measured against; for
	// non-scoped implementations it is equivalent to SessionCount.
	SessionCountFor(wsID string) int

	// MaxSessions returns the configured concurrent-session cap. For
	// per-workspace implementations this is the per-workspace cap, not a
	// sum across workspaces.
	MaxSessions() int
}

// PTYCommandRunner is implemented by PTY sources that can start a session
// without a browser attachment and write backend-owned input into it. Setup
// flows use this to run a typed command inside the same TTY the user later
// controls from the web terminal.
type PTYCommandRunner interface {
	EnsureSession(key SessionKey, cols, rows uint16, argv []string) (created bool, err error)
	WriteToSession(key SessionKey, p []byte) error
}

// OutputInjector appends display-only backend text to the owner-sequenced
// terminal stream and ring without writing it to the child process.
type OutputInjector interface {
	InjectOutput(key SessionKey, p []byte) error
}

// Generation identifies one PTY process. It remains stable across attaches.
type Generation [16]byte

type EventKind uint8

const (
	EventOutput EventKind = 1
	EventResize EventKind = 2
	EventNotice EventKind = 3
	EventClose  EventKind = 4
)

// TerminalEvent is one owner-sequenced server-to-viewer event.
type TerminalEvent struct {
	Sequence uint64
	Kind     EventKind
	Data     []byte
	Cols     uint16
	Rows     uint16
}

// TerminalInitialState is the atomic state cut returned by AttachSession.
// Output begins strictly after Sequence and is contiguous from Sequence+1.
type TerminalInitialState struct {
	Generation    Generation
	Sequence      uint64
	Cols          uint16
	Rows          uint16
	RetainedLines uint32
	Encoding      string
	Data          []byte
}

type CloseReason string

const (
	CloseExited       CloseReason = "exited"
	CloseKilled       CloseReason = "killed"
	CloseShutdown     CloseReason = "shutdown"
	CloseSlowConsumer CloseReason = "slow_consumer"
	CloseReplaced     CloseReason = "replaced"
	CloseStateRebuild CloseReason = "state_rebuilding"
)

// Keep the service-facing names until the v1 handler slice switches close
// policy to CloseReason directly.
const (
	ExitReasonExited   = string(CloseExited)
	ExitReasonKilled   = string(CloseKilled)
	ExitReasonShutdown = string(CloseShutdown)
)

// Attachment is the handle returned by PTYSource.AttachSession. InitialState
// and subscriber registration are produced by the same owner operation: if
// InitialState.Sequence is N, Output is a contiguous stream beginning at N+1.
// Callers release it through PTYSource.Detach(key, ConnID()).
type Attachment interface {
	ConnID() string
	InitialState() TerminalInitialState
	Output() <-chan TerminalEvent
	WriteInput(p []byte) (int, error)
	RequestResize(cols, rows uint16) error
	Focus() error
	CloseReason() CloseReason
}

// Compile-time assertions.
var (
	_ PTYSource        = (*PTYManager)(nil)
	_ PTYSource        = (*MultiPTYManager)(nil)
	_ PTYCommandRunner = (*PTYManager)(nil)
	_ PTYCommandRunner = (*MultiPTYManager)(nil)
	_ OutputInjector   = (*PTYManager)(nil)
	_ OutputInjector   = (*MultiPTYManager)(nil)
	_ Attachment       = (*localAttachment)(nil)
)
