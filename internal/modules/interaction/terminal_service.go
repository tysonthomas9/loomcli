package interaction

import (
	"time"
)

// TerminalTabService is Interaction's terminal owner. It converges tab
// identity, AgentSession and TerminalSession generations, launch policy,
// private PTY/tmux attachment, reconnect, replay, resize, and shutdown while
// exposing only process-neutral attachments to delivery adapters.
type TerminalTabService struct {
	tabStore      TabMetadataStore
	runtime       TerminalRuntime
	startedAt     time.Time
	agentTerminal TerminalDependencies
}

// NewTerminalTabs composes Interaction's terminal-tab policy.
func NewTerminalTabs(
	tabStore TabMetadataStore,
	runtime TerminalRuntime,
	startedAt time.Time,
	agentTerminal TerminalDependencies,
) TerminalTabs {
	return &TerminalTabService{
		tabStore:      tabStore,
		runtime:       runtime,
		startedAt:     startedAt,
		agentTerminal: agentTerminal,
	}
}

// ptyAlive reports whether the named session has a live PTY in this server
// process. Returns false when no PTY backend is wired (e.g. auth-only tests).
func (s *TerminalTabService) ptyAlive(wsID, session string) bool {
	if s.runtime == nil {
		return false
	}
	return s.runtime.IsLive(TerminalKey{WorkspaceKey: wsID, TerminalID: session})
}

// ptyAttachable reports the value exposed as pty_alive to the UI. A live
// PTY is attachable. Metadata created during this server process is also
// attachable because the PTY may not exist until the first WebSocket connects.
// Metadata from before this server started and without a PTY remains false,
// which preserves stale-session protection after a server restart.
func (s *TerminalTabService) ptyAttachable(wsID string, meta *TabMetadata) bool {
	if meta == nil {
		return false
	}
	key := TerminalKey{WorkspaceKey: wsID, TerminalID: meta.SessionName}
	if s.runtime != nil && s.runtime.IsLive(key) {
		return true
	}
	if s.runtime != nil && s.runtime.IsClosed(key) {
		return false
	}
	if meta.Launch == nil || len(meta.Launch.Argv) == 0 {
		return false
	}
	if s.startedAt.IsZero() || meta.CreatedAt.IsZero() {
		return false
	}
	return !meta.CreatedAt.Before(s.startedAt)
}

// attachedClients reports the number of WebSocket clients currently
// attached to the named session. Zero when no PTY backend is wired or the
// session has no live PTY.
func (s *TerminalTabService) attachedClients(wsID, session string) int {
	if s.runtime == nil {
		return 0
	}
	return s.runtime.AttachmentCount(TerminalKey{WorkspaceKey: wsID, TerminalID: session})
}
