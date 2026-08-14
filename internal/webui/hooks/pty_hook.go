package hooks

import (
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

type WorkspaceTerminalRuntime interface {
	RegisterWorkspace(workspaceKey, path string) error
	DeregisterWorkspace(workspaceKey string) error
}

// PTYHook implements coordinator.LifecycleHook for per-workspace web-terminal
// PTY manager lifecycle. On workspace registration it registers the workspace
// with MultiPTYManager so subsequent AttachSession calls dispatch to a
// per-workspace *PTYManager whose shell cwd == workspace.Path. On
// deregistration or rollback it tears the entry (and any live sessions) down.
//
// Non-critical by design (decision F-1): a bad workspace path downgrades the
// workspace to "no terminal available" rather than failing registration.
type PTYHook struct {
	multi  WorkspaceTerminalRuntime
	logger *slog.Logger
}

// NewPTYHook creates a PTYHook. multi must not be nil (panics).
// A nil logger defaults to slog.Default().
func NewPTYHook(multi WorkspaceTerminalRuntime, logger *slog.Logger) *PTYHook {
	if multi == nil {
		panic("NewPTYHook: multi must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PTYHook{multi: multi, logger: logger}
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
	if err := h.multi.RegisterWorkspace(wsID, path); err != nil {
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
	_ = h.multi.DeregisterWorkspace(ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *PTYHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}
