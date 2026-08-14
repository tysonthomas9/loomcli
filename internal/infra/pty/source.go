package pty

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type LaunchSpec = interaction.LaunchSpec

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
	AttachSession(key SessionKey, cols, rows uint16, launch *LaunchSpec) (att Attachment, reattached bool, err error)

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

	// Replay returns the bounded ordered output/resize timeline to apply
	// before live output. A fresh session includes its initial geometry;
	// reattachments also include the retained output history.
	Replay() []interaction.TerminalReplayEvent

	// Resize changes the terminal dimensions. Implementations may ignore connID.
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
	_ PTYSource                   = (*PTYManager)(nil)
	_ PTYSource                   = (*MultiPTYManager)(nil)
	_ PTYCommandRunner            = (*PTYManager)(nil)
	_ PTYCommandRunner            = (*MultiPTYManager)(nil)
	_ Attachment                  = (*localAttachment)(nil)
	_ interaction.TerminalRuntime = (*Runtime)(nil)
)

// Runtime is the private adapter that exposes PTYManager mechanics only
// through Interaction's TerminalRuntime port.
type Runtime struct{ manager *MultiPTYManager }

func NewRuntime(command string, maxPerWorkspace int) *Runtime {
	return &Runtime{manager: NewMultiPTYManager(command, maxPerWorkspace)}
}

func (runtime *Runtime) Attach(key interaction.TerminalKey, cols, rows uint16, launch *interaction.LaunchSpec) (interaction.TerminalAttachment, bool, error) {
	attachment, reattached, err := runtime.manager.AttachSession(sessionKey(key), cols, rows, launch)
	return attachment, reattached, publicRuntimeError(err)
}

func (runtime *Runtime) Detach(key interaction.TerminalKey, attachmentID string) {
	runtime.manager.Detach(sessionKey(key), attachmentID)
}

func (runtime *Runtime) Kill(key interaction.TerminalKey) error {
	return publicRuntimeError(runtime.manager.Kill(sessionKey(key)))
}

func (runtime *Runtime) IsLive(key interaction.TerminalKey) bool {
	return runtime.manager.HasSession(sessionKey(key))
}

func (runtime *Runtime) IsClosed(key interaction.TerminalKey) bool {
	return runtime.manager.SessionClosed(sessionKey(key))
}

func (runtime *Runtime) AttachmentCount(key interaction.TerminalKey) int {
	return runtime.manager.AttachmentCount(sessionKey(key))
}

func (runtime *Runtime) SessionCount() int { return runtime.manager.SessionCount() }
func (runtime *Runtime) SessionCountFor(workspaceKey string) int {
	return runtime.manager.SessionCountFor(workspaceKey)
}
func (runtime *Runtime) MaxSessions() int { return runtime.manager.MaxSessions() }

func (runtime *Runtime) Ensure(key interaction.TerminalKey, cols, rows uint16, launch *interaction.LaunchSpec) (bool, error) {
	if launch == nil || len(launch.Argv) == 0 {
		return false, ErrPTYSessionNotFound
	}
	created, err := runtime.manager.EnsureSession(sessionKey(key), cols, rows, launch.Argv)
	return created, publicRuntimeError(err)
}

func (runtime *Runtime) WriteInput(key interaction.TerminalKey, input []byte) error {
	return publicRuntimeError(runtime.manager.WriteToSession(sessionKey(key), input))
}

func (runtime *Runtime) RegisterWorkspace(workspaceKey, path string) error {
	return runtime.manager.Register(workspaceKey, path)
}

func (runtime *Runtime) EnsureRegistered(workspaceKey, path string) error {
	return runtime.manager.EnsureRegistered(workspaceKey, path)
}

func (runtime *Runtime) DeregisterWorkspace(workspaceKey string) error {
	runtime.manager.Deregister(workspaceKey)
	return nil
}

func (runtime *Runtime) SetLifecycleHook(hook interaction.TerminalLifecycleHook) {
	if hook == nil {
		runtime.manager.SetBeforeKill(nil)
		return
	}
	runtime.manager.SetBeforeKill(func(ctx context.Context, key SessionKey, reason string) error {
		return hook(ctx, interaction.TerminalKey{WorkspaceKey: key.Workspace, TerminalID: key.Name}, reason)
	})
}
func (runtime *Runtime) SetGracePeriod(value time.Duration) { runtime.manager.SetGracePeriod(value) }
func (runtime *Runtime) SetIdleTimeout(value time.Duration) { runtime.manager.SetIdleTimeout(value) }
func (runtime *Runtime) GracePeriod() time.Duration         { return runtime.manager.GracePeriod() }
func (runtime *Runtime) IdleTimeout() time.Duration         { return runtime.manager.IdleTimeout() }
func (runtime *Runtime) Close() error                       { return runtime.manager.Close() }

func sessionKey(key interaction.TerminalKey) SessionKey {
	return SessionKey{Workspace: key.WorkspaceKey, Name: key.TerminalID}
}

func publicRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrPTYMaxSessionsReached), errors.Is(err, ErrMaxSessionsReached):
		return fmt.Errorf("%v: %w", err, interaction.ErrTerminalCapacity)
	case errors.Is(err, ErrPTYManagerClosed):
		return fmt.Errorf("%v: %w", err, interaction.ErrTerminalClosed)
	case errors.Is(err, ErrWorkspaceNotRegistered):
		return fmt.Errorf("%v: %w", err, interaction.ErrTerminalPlacement)
	default:
		return err
	}
}
