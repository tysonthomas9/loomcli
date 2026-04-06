package hooks

import (
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// TerminalHook implements coordinator.LifecycleHook for per-workspace terminal
// session lifecycle. On workspace registration, it provides the TerminalManager
// to the resource bag. On workspace deregistration, it kills all terminal
// sessions owned by that workspace.
type TerminalHook struct {
	termMgr *terminal.TerminalManager
	logger  *slog.Logger
}

// NewTerminalHook creates a TerminalHook. termMgr must not be nil (panics).
// A nil logger defaults to slog.Default().
func NewTerminalHook(termMgr *terminal.TerminalManager, logger *slog.Logger) *TerminalHook {
	if termMgr == nil {
		panic("NewTerminalHook: termMgr must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TerminalHook{
		termMgr: termMgr,
		logger:  logger,
	}
}

// Name returns "terminal".
func (h *TerminalHook) Name() string { return "terminal" }

// Critical returns false — terminal support is not required for workspace
// registration. The workspace is fully functional without it.
func (h *TerminalHook) Critical() bool { return false }

// OnRegister provides the TerminalManager to the resource bag so downstream
// hooks and handlers can discover it via the WorkspaceHandle.
func (h *TerminalHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	ctx.Provide(coordinator.ResourceKeyTerminal, h.termMgr)
	h.logger.Debug("provided terminal manager for workspace", "workspace", ctx.WorkspaceID)
	return nil
}

// OnDeregister kills all terminal sessions owned by the workspace being removed.
// Sessions with no recorded owner are skipped (they may belong to another workspace
// that predates ownership tracking). Errors are logged but never propagated.
func (h *TerminalHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.cleanupWorkspaceSessions(ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *TerminalHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.cleanupWorkspaceSessions(ctx.WorkspaceID)
}

// cleanupWorkspaceSessions kills all terminal sessions confirmed owned by the
// given workspace. Unowned sessions are skipped. Errors on individual session
// kills are logged but do not abort the loop.
func (h *TerminalHook) cleanupWorkspaceSessions(wsID string) {
	sessions, err := h.termMgr.ListActiveSessionsForWorkspace(wsID)
	if err != nil {
		h.logger.Warn("failed to list terminal sessions for workspace cleanup",
			"workspace", wsID, "err", err)
		return
	}

	killed := 0
	for _, s := range sessions {
		// Only kill sessions confirmed owned by this workspace.
		// ListActiveSessionsForWorkspace includes unowned sessions for backward
		// compatibility, but we must not kill them during workspace cleanup —
		// they may belong to another workspace.
		owner, hasOwner := h.termMgr.SessionOwner(s.Name)
		if !hasOwner || owner != wsID {
			continue
		}

		if err := h.termMgr.KillSessionByName(s.Name); err != nil {
			h.logger.Warn("failed to kill terminal session during workspace cleanup",
				"workspace", wsID, "session", s.Name, "err", err)
			continue
		}
		killed++
	}

	if killed > 0 {
		h.logger.Info("killed terminal sessions for workspace",
			"workspace", wsID, "count", killed)
	} else {
		h.logger.Debug("no terminal sessions to clean up for workspace",
			"workspace", wsID)
	}
}
