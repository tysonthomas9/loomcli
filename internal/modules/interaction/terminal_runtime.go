package interaction

import (
	"context"
	"io"
)

// TerminalKey is the durable placement identity of one terminal runtime.
// A runtime may keep the child alive while browser attachments disconnect.
type TerminalKey struct {
	WorkspaceKey string
	TerminalID   string
}

// String returns a diagnostic identity. It is never used as persistence or
// authorization input.
func (key TerminalKey) String() string {
	if key.WorkspaceKey == "" {
		return key.TerminalID
	}
	return key.WorkspaceKey + "/" + key.TerminalID
}

// TerminalAttachment is one live browser attachment to a terminal runtime.
// The runtime owns replay buffering and process mechanics; transports own only
// framing, ping/pong, and disconnect translation.
type TerminalAttachment interface {
	ConnID() string
	Output() <-chan []byte
	WriteInput([]byte) (int, error)
	Replay() []TerminalReplayEvent
	Resize(connID string, cols, rows uint16) error
	ExitReason() string
}

// TerminalReplayEvent preserves the ordering between PTY output and the
// terminal geometry under which that output was rendered. A raw byte log is
// not sufficient after SIGWINCH-driven redraws: replaying it at only the final
// size can erase or fragment earlier output.
type TerminalReplayEvent struct {
	Output  []byte
	Columns uint16
	Rows    uint16
}

// IsResize reports whether the event changes replay renderer geometry.
func (event TerminalReplayEvent) IsResize() bool {
	return event.Columns > 0 && event.Rows > 0
}

// TerminalRuntime is Interaction's outbound process-control port. The private
// PTY adapter implements attach fencing, replay, resize, buffering, detach,
// and shutdown without exposing process, PTY, or tmux types.
type TerminalRuntime interface {
	Attach(key TerminalKey, cols, rows uint16, launch *LaunchSpec) (TerminalAttachment, bool, error)
	Detach(key TerminalKey, attachmentID string)
	Kill(key TerminalKey) error
	IsLive(key TerminalKey) bool
	IsClosed(key TerminalKey) bool
	AttachmentCount(key TerminalKey) int
	SessionCount() int
	SessionCountFor(workspaceKey string) int
	MaxSessions() int
	Ensure(key TerminalKey, cols, rows uint16, launch *LaunchSpec) (bool, error)
	WriteInput(key TerminalKey, input []byte) error
}

// TerminalLifecycleHook converges durable Interaction state before an adapter
// gives up ownership of a live child.
type TerminalLifecycleHook func(context.Context, TerminalKey, string) error

// AgentTerminalConnection is a read/write attachment to a tmux-hosted worker
// process. The private adapter keeps tmux names, PTY file descriptors, and
// child processes behind this interface.
type AgentTerminalConnection interface {
	io.Reader
	io.Writer
	ConnectionID() string
	SessionName() string
	Killed() <-chan struct{}
	Resize(connectionID string, cols, rows uint16) error
}

// AgentTerminalRuntime is the read-only live-view port for worker terminals
// created outside the web process.
type AgentTerminalRuntime interface {
	HasSession(string) bool
	PaneDead(string) bool
	CapturePane(string, int) string
	FindLatestAgentSession(workspaceKey, agentID string) (string, bool, error)
	SessionCount() int
	MaxSessions() int
	Attach(sessionName string, cols, rows uint16) (AgentTerminalConnection, error)
	Detach(connectionID string) error
}

// AgentTerminalMonitor is the read-only liveness surface needed by WebSocket
// delivery while relaying one private tmux-backed attachment.
type AgentTerminalMonitor interface {
	HasSession(string) bool
	PaneDead(string) bool
	CapturePane(string, int) string
}
