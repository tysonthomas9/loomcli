package bootstrap

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/pty"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// LocalTerminalRuntime is the process-owned PTY lifecycle needed by WebUI
// composition and workspace hooks.
type LocalTerminalRuntime interface {
	interaction.TerminalRuntime
	RegisterWorkspace(string, string) error
	DeregisterWorkspace(string) error
	SetLifecycleHook(interaction.TerminalLifecycleHook)
	SetGracePeriod(time.Duration)
	SetIdleTimeout(time.Duration)
	GracePeriod() time.Duration
	IdleTimeout() time.Duration
	Close() error
}

// LocalAgentTerminalRuntime is the machine-local view of CLI-owned agent tmux
// sessions. It attaches and cleans up only; it never creates agent sessions.
type LocalAgentTerminalRuntime interface {
	interaction.AgentTerminalRuntime
	KillWorkspaceSessions(string) error
	Shutdown() error
}

// ErrTmuxNotFound identifies a host where the optional agent terminal live
// view is unavailable.
var ErrTmuxNotFound = pty.ErrTmuxNotFound

// NewLocalTerminalRuntime creates Interaction's private process PTY adapter.
func NewLocalTerminalRuntime(command string, maxSessions int) LocalTerminalRuntime {
	return pty.NewRuntime(command, maxSessions)
}

// NewLocalAgentTerminalRuntime creates the attach-only agent tmux adapter.
func NewLocalAgentTerminalRuntime(maxSessions int) (LocalAgentTerminalRuntime, error) {
	return pty.NewAgentRuntime(maxSessions)
}
