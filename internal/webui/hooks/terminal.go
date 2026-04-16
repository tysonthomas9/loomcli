package hooks

import (
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// TerminalHook implements coordinator.LifecycleHook for per-workspace terminal
// session lifecycle.
//
// On workspace registration it publishes the AgentTmuxManager to the resource
// bag (so other hooks and handlers can reach it through the WorkspaceHandle).
// On workspace deregistration it kills every tmux session whose name is
// prefixed by the workspace — this reaps long-lived auto-mode agent
// processes that outlive their workspace. The main web terminal path
// doesn't need cleanup here: PTY shells are WebSocket-scoped and die with
// their connection.
//
// When tmux is not installed, agentTmuxMgr will be nil; the hook then simply
// provides nil to the bag and no-ops on cleanup.
type TerminalHook struct {
	agentTmuxMgr *terminal.AgentTmuxManager
	logger       *slog.Logger
}

// NewTerminalHook creates a TerminalHook. agentTmuxMgr may be nil (tmux not
// installed). A nil logger defaults to slog.Default().
func NewTerminalHook(agentTmuxMgr *terminal.AgentTmuxManager, logger *slog.Logger) *TerminalHook {
	if logger == nil {
		logger = slog.Default()
	}
	return &TerminalHook{
		agentTmuxMgr: agentTmuxMgr,
		logger:       logger,
	}
}

// Name returns "terminal".
func (h *TerminalHook) Name() string { return "terminal" }

// Critical returns false — terminal support is not required for workspace
// registration. The workspace is fully functional without it.
func (h *TerminalHook) Critical() bool { return false }

// OnRegister provides the AgentTmuxManager to the resource bag.
func (h *TerminalHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	ctx.Provide(coordinator.ResourceKeyTerminal, h.agentTmuxMgr)
	h.logger.Debug("provided agent tmux manager for workspace", "workspace", ctx.WorkspaceID)
	return nil
}

// OnDeregister kills all tmux sessions owned by the workspace being removed.
func (h *TerminalHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.cleanupWorkspaceSessions(ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *TerminalHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.cleanupWorkspaceSessions(ctx.WorkspaceID)
}

func (h *TerminalHook) cleanupWorkspaceSessions(wsID string) {
	if wsID == "" || h.agentTmuxMgr == nil {
		return
	}
	if err := h.agentTmuxMgr.KillWorkspaceSessions(wsID); err != nil {
		h.logger.Warn("failed to kill tmux sessions for workspace cleanup",
			"workspace", wsID, "err", err)
		return
	}
	h.logger.Info("killed tmux sessions for workspace", "workspace", wsID)
}
