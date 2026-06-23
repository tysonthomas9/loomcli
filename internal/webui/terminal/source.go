package terminal

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
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
	// existing session was joined (scrollback replay expected).
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

// PTYLifetime is implemented by PTY sources that expose their detached-session
// grace and idle windows. The app surfaces these to the UI as the terminal
// reconnect/idle timeouts. Sources without configurable lifetimes (and the
// remote terminal host, which reports 0) still satisfy it.
type PTYLifetime interface {
	GracePeriod() time.Duration
	IdleTimeout() time.Duration
}

// WorkspaceRegistrar is implemented by PTY sources that need workspace ID →
// filesystem path registration before sessions can be created. The web app
// lifecycle hook uses EnsureRegistered so a restarted serve process does not
// tear down already-running sessions owned by an external terminal host.
type WorkspaceRegistrar interface {
	EnsureRegistered(wsID, path string) error
	Deregister(wsID string)
}

// Attachment is the handle returned by PTYSource.AttachSession. The WS
// handler reads output frames from Output() and writes user input via
// WriteInput(). Callers release the attachment by invoking
// PTYSource.Detach(key, ConnID()).
type Attachment interface {
	// ConnID is an opaque identifier unique to this attachment.
	ConnID() string

	// Output is the channel the handler reads live output from. Closed when
	// the attachment ends (session killed or replaced by a newer WS).
	Output() <-chan []byte

	// WriteInput sends user keystrokes (or other raw bytes) toward the
	// session's PTY / shell.
	WriteInput(p []byte) (int, error)

	// Scrollback returns the reset escape + ring-buffer snapshot to emit
	// before live output on a reattach. nil on the first attach to a fresh
	// session.
	Scrollback() []byte

	// Resize satisfies realtime.Resizer so the WS handler can pass the
	// Attachment directly to realtime.WSToPTY. Implementations may ignore connID.
	Resize(connID string, cols, rows uint16) error

	// ExitReason returns the reason the owning session closed. Values are
	// drawn from the ExitReason* constants in pty_session.go
	// (ExitReasonKilled, ExitReasonExited, ExitReasonShutdown). Returns
	// the empty string when the session is still live or no reason was
	// recorded. Only meaningful after Output() has been observed closed.
	ExitReason() string
}

// Compile-time assertions.
var (
	_ PTYSource          = (*PTYManager)(nil)
	_ PTYSource          = (*MultiPTYManager)(nil)
	_ PTYLifetime        = (*PTYManager)(nil)
	_ PTYLifetime        = (*MultiPTYManager)(nil)
	_ PTYCommandRunner   = (*PTYManager)(nil)
	_ PTYCommandRunner   = (*MultiPTYManager)(nil)
	_ WorkspaceRegistrar = (*MultiPTYManager)(nil)
	_ Attachment         = (*localAttachment)(nil)
	_ realtime.Resizer   = (*localAttachment)(nil)
)
