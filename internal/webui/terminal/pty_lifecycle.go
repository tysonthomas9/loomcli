package terminal

const (
	// PTYLifecycleStarted means a new child process was successfully spawned.
	PTYLifecycleStarted = "terminal.pty_started"
	// PTYLifecycleExited means the child process exited without a manager kill.
	PTYLifecycleExited = "terminal.pty_exited"
	// PTYLifecycleKilled means the manager terminated the child process.
	PTYLifecycleKilled = "terminal.pty_killed"

	// PTYKind identifies the main web-terminal PTY runtime. Agent tmux sessions
	// are externally owned and use a separate lifecycle source.
	PTYKind = "pty"
)

// PTYLifecycleEvent describes one process-liveness transition. Key carries
// both the workspace routing scope and the workspace-local session name.
type PTYLifecycleEvent struct {
	Key        SessionKey
	Action     string
	PTYAlive   bool
	ExitReason string
	Kind       string
	Agent      bool
}

// PTYLifecycleObserver receives process-liveness transitions after the
// manager's authoritative state has changed. Implementations must return
// quickly and must not retain mutable manager state.
type PTYLifecycleObserver interface {
	OnPTYLifecycle(PTYLifecycleEvent)
}
