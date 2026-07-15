package hooks

import (
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// PTYHook implements coordinator.LifecycleHook for per-workspace web-terminal
// PTY manager lifecycle. On workspace registration it ensures the workspace
// is known to the configured PTY source so subsequent AttachSession calls
// dispatch to a manager whose shell cwd == workspace.Path. Same-path
// re-registration is non-destructive so serve restarts do not kill sessions
// owned by a persistent terminal host.
//
// Non-critical by design (decision F-1): a bad workspace path downgrades the
// workspace to "no terminal available" rather than failing registration.
type PTYHook struct {
	mgr    terminal.WorkspaceRegistrar
	logger *slog.Logger
}

// NewPTYHook creates a PTYHook. mgr must not be nil (panics).
// A nil logger defaults to slog.Default().
func NewPTYHook(mgr terminal.WorkspaceRegistrar, logger *slog.Logger) *PTYHook {
	if mgr == nil {
		panic("NewPTYHook: manager must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PTYHook{mgr: mgr, logger: logger}
}

// Name returns "pty-manager".
func (h *PTYHook) Name() string { return "pty-manager" }

// Critical returns false — a bad workspace path must not block registration
// of the other subsystems (pool, subscriber, fleet). The workspace is still
// valid; only the web terminal for it is unavailable.
func (h *PTYHook) Critical() bool { return false }

// OnRegister associates the workspace with MultiPTYManager. Errors (e.g.
// ErrInvalidWorkspacePath, ErrPTYManagerClosed) are swallowed after logging:
// non-critical hooks must return nil so the registry keeps the hook in the
// "succeeded" set and still calls OnDeregister on teardown.
func (h *PTYHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	wsID := ctx.WorkspaceID
	path := ctx.WorkspacePath
	if err := h.mgr.EnsureRegistered(wsID, path); err != nil {
		h.logger.Warn("pty manager register failed; terminal disabled for workspace",
			"workspace", wsID, "path", path, "err", err)
		return nil
	}
	h.logger.Info("registered pty manager for workspace", "workspace", wsID)
	return nil
}

// OnDeregister removes the workspace entry and kills any live PTY sessions.
// MultiPTYManager.Deregister is idempotent on unknown IDs.
func (h *PTYHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.mgr.Deregister(ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *PTYHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}
