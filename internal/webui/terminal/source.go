package terminal

import (
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// PTYSource is the backend-agnostic contract the web terminal handler uses
// to talk to a terminal backend. Two implementations exist / will exist:
//
//   - *PTYManager — local, in-process PTY (ephemeral agents, same-pod shells).
//   - *agentd.Client (in a sibling package / repo) — gRPC to loom-agentd
//     running inside a persistent-agent Firecracker microVM.
//
// Keeping this interface narrow means `handlers/terminal/ws.go` never grows
// knowledge of how the backend is realised — it just Attaches, Detaches, and
// Kills sessions.
type PTYSource interface {
	// AttachSession opens or re-opens a session. reattached is true when an
	// existing session was joined (scrollback replay expected).
	AttachSession(key SessionKey, cols, rows uint16, argv []string) (att Attachment, reattached bool, err error)

	// Detach releases the attachment identified by connID for the given
	// session. Does not kill the session — a grace period begins.
	Detach(key SessionKey, connID string)

	// Kill immediately terminates the session. Idempotent.
	Kill(key SessionKey) error

	// SessionCount returns the number of live sessions, including detached
	// ones still within the grace/idle windows.
	SessionCount() int

	// MaxSessions returns the configured concurrent-session cap.
	MaxSessions() int
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
	// Attachment directly to realtime.WSToPTY. connID is accepted for
	// interface compatibility; implementations may ignore it.
	Resize(connID string, cols, rows uint16) error
}

// Compile-time assertions.
var (
	_ PTYSource        = (*PTYManager)(nil)
	_ Attachment       = (*localAttachment)(nil)
	_ realtime.Resizer = (*localAttachment)(nil)
)
